package controllers

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/google/go-github/v35/github"
	g "github.com/leejoebarak/githubissue-operator/api/v1alpha1"
	"golang.org/x/oauth2"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"log"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil" //finalizer related
	"strings"

	"io/ioutil"
	"net/http"
	// "github.com/google/go-github/github" // with go modules disabled
)

// GithubIssueReconciler reconciles a GithubIssue object
type GithubIssueReconciler struct {
	client.Client //type embedding
	Log           logr.Logger
	Scheme        *runtime.Scheme
}

const finalizerName = "training.redhat.com/finalizer" // domain/name-of-custom-finalizer

func (r *GithubIssueReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("githubissue_name", req.NamespacedName)
	logger.Info("**************START LOGIC**************")
	/* AUTHENTICATION */
	githubClient, ctx1 := getGithubClient()

	/* Get object from k8s cluster */
	ghissue := g.GithubIssue{}
	err := r.Client.Get(ctx, req.NamespacedName, &ghissue) //get the githubIssue *k8s* object VALUES, if exists (from k8s api server)
	if err != nil {
		if errors.IsNotFound(err) { //err status is 404 -> return with nil error (don't requeue)
			log404(logger)
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Error reading the object -> Requeue the request.")
		return ctrl.Result{}, err
	}
	// Add finalizer for this CR
	if !controllerutil.ContainsFinalizer(&ghissue, finalizerName) {
		controllerutil.AddFinalizer(&ghissue, finalizerName)
		err = r.Update(ctx, &ghissue)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	owner, repo := splitOwnerRepo(ghissue.Spec.Repo)
	allRepoIssues, err := getListOfIssues(githubClient, ctx1, owner, repo, logger)
	if err != nil {
		logger.Error(err, "While trying to get repo's list of issues")
		return ctrl.Result{}, err
	}

	/* check if issue exists in github repo */
	issue, err := searchIssueByTitle(allRepoIssues, ghissue.Spec.Title)
	if err != nil {
		/*issue not found*/
		if !ghissue.ObjectMeta.DeletionTimestamp.IsZero() {
			/* DeletionTimestamp Not Zero && No issue on Github */
			controllerutil.RemoveFinalizer(&ghissue, finalizerName)
			err = r.Update(ctx, &ghissue) // Update the cluster
			if err != nil {
				logger.Error(err, "r.Update() failed")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		/* k8s object is not being deleted */
		issue, err = createIssueOnGithub(githubClient, ctx1, owner, repo, &ghissue, logger)
		if err != nil {
			logger.Error(err, "While trying to create issue on Github")
			return ctrl.Result{}, err
		}
		/*************************************************************************************************/
	} else {
		/*issue was found*/
		if !ghissue.ObjectMeta.DeletionTimestamp.IsZero() {
			/* DeletionTimestamp Not Zero -> delete */
			err = handleDeletionIfIssueFound(githubClient, ctx1, owner, repo, issue, &ghissue, logger)
			if err != nil {
				logger.Error(err, "While trying to delete issue on Github")
				return ctrl.Result{}, err
			}
			err = r.Update(ctx, &ghissue)
			if err != nil {
				logger.Error(err, " r.Update() failed ")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		/* k8s object is not being deleted */
		if !isDescriptionEqual(issue, &ghissue) {
			_, err = updateDescriptionOnGithub(githubClient, ctx1, owner, repo, *issue.Number, &ghissue, logger)
			if err != nil {
				logger.Error(err, "While trying to update issue on Github")
				return ctrl.Result{}, err
			}
		}
	}
	/*important! call the below 3 lines of code only ONCE in entire reconcile. Avoid redundant calls!*/
	err = r.updateStatus(ctx, issue, &ghissue)
	if err != nil{
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil //no error
}

// SetupWithManager sets up the controller with the Manager.
func (r *GithubIssueReconciler) SetupWithManager(mgr ctrl.Manager) error {
	/* this method tells the controller "you are tracking resources of type GitHubIssue" */
	return ctrl.NewControllerManagedBy(mgr).
		For(&g.GithubIssue{}).
		Complete(r)
}

func (r *GithubIssueReconciler)  updateStatus(ctx context.Context, issue *github.Issue, ghissue *g.GithubIssue)  error{
	ghissue.Status.State = *issue.State
	ghissue.Status.LastUpdateTimestamp = issue.UpdatedAt.String()
	err := r.Status().Update(ctx, ghissue)
	if err != nil {
		r.Log.Error(err, "((GithubIssueReconciler)r).Status().Update() failed ")
		return err
	}
	return nil
}

func closeIssueOnGithub(githubClient *github.Client, ctx context.Context, owner, repo string, issue *github.Issue, ghissue *g.GithubIssue, logger logr.Logger) error {
	/*
	* delete any external resources associated with the ghissue
	* Ensure that delete implementation is idempotent and safe to invoke multiple times for same object.*/
	if issue == nil {
		return fmt.Errorf("closeIssueOnGithub() was passed nil issue param (you can't close an issue that never existed)")
	}
	issueReq := &github.IssueRequest{
		Title: github.String(ghissue.Spec.Title),
		Body:  github.String(ghissue.Spec.Desc),
		State: github.String("closed"),
	}
	issue, resp, err := githubClient.Issues.Edit(ctx, owner, repo, *issue.Number, issueReq)
	if err != nil || (resp != nil && resp.StatusCode != http.StatusOK) {
		body, _ := ioutil.ReadAll(resp.Body)
		logger = logger.WithName("closeIssueOnGithub()")
		logger.Error(err, "Deleting github issue failed", "Github api response code is", resp.StatusCode, "The response body is", string(body)) //print body as it may contain hints in case of errors
		return err
	}
	return nil
}

func getListOfIssues(githubClient *github.Client, ctx context.Context, owner, repo string, logger logr.Logger) ([]*github.Issue, error) {
	//opts := githubClient.Issues.IssueListByRepoOptions
	opts := github.IssueListByRepoOptions{
		State: "all",
	}
	issues, resp, err := githubClient.Issues.ListByRepo(ctx, owner, repo, &opts)
	if err != nil || (resp != nil && resp.StatusCode != http.StatusOK) {
		//log body as it may contain hints in case of errors
		body, _ := ioutil.ReadAll(resp.Body)
		logger = logger.WithName("getListOfIssues()")
		logger.Error(err, "Reading the list of issues from github repo failed", "Github api response code is", resp.StatusCode, "The response body is", string(body))
		return nil, err
	}
	return issues, nil
}

func createIssueOnGithub(githubClient *github.Client, ctx context.Context, owner, repo string, githubIssueObj *g.GithubIssue, logger logr.Logger) (*github.Issue, error) {
	issueReq := &github.IssueRequest{
		Title: github.String(githubIssueObj.Spec.Title),
		Body:  github.String(githubIssueObj.Spec.Desc),
		State: github.String("open"),
	}
	issue, resp, err := githubClient.Issues.Create(ctx, owner, repo, issueReq)
	if err != nil || (resp != nil && resp.StatusCode != http.StatusCreated) {
		body, _ := ioutil.ReadAll(resp.Body)
		logger = logger.WithName("createIssueOnGithub()")
		logger.Error(err, "Creation of github issue failed", "Github api response code is", resp.StatusCode, "The response body is", string(body)) //print body as it may contain hints in case of errors
		return nil, err
	}
	return issue, nil
}

/*
update the real world Description (aka Body) */
func updateDescriptionOnGithub(githubClient *github.Client, ctx context.Context, owner, repo string, number int, githubIssueObj *g.GithubIssue, logger logr.Logger) (*github.Issue, error) {
	issueReq := &github.IssueRequest{
		Title: github.String(githubIssueObj.Spec.Title),
		Body:  github.String(githubIssueObj.Spec.Desc),
	}
	issue, resp, err := githubClient.Issues.Edit(ctx, owner, repo, number, issueReq)
	if err != nil || (resp != nil && resp.StatusCode != http.StatusOK) {
		body, _ := ioutil.ReadAll(resp.Body)
		logger = logger.WithName("updateDescriptionOnGithub()")
		logger.Error(err, "Updating github issue failed", "Github api response code is", resp.StatusCode, "The response body is", string(body)) //print body as it may contain hints in case of errors
		return nil, err
	}
	return issue, nil
}

func handleDeletionIfIssueFound(githubClient *github.Client, ctx1 context.Context, owner, repo string, issue *github.Issue, ghissue *g.GithubIssue, logger logr.Logger) error {
	if stateClosed(issue) { // issue already closed on github
		controllerutil.RemoveFinalizer(ghissue, finalizerName)
	} else {
		err := closeIssueOnGithub(githubClient, ctx1, owner, repo, issue, ghissue, logger) //handle external dependency
		if err != nil {
			logger.Error(err, "While trying to close issue on Github")
			return err // if fail to delete the external dependency, return with error so that it can be retried
		}
		controllerutil.RemoveFinalizer(ghissue, finalizerName) // successful deletion of external resources -> remove our finalizer from the list
	}
	return nil
}

/**** HELPERS ****/
func searchIssueByTitle(issues []*github.Issue, title string) (*github.Issue, error) {
	for _, issue := range issues {
		// i is the index where we are, title is the element from titles slice for where we are
		if title == *issue.Title {
			return issue, nil
		}
	}
	return nil, fmt.Errorf("issue %s not found", title)
}

func getGithubClient() (*github.Client, context.Context) {
	tkn := os.Getenv("TOKEN")
	ctx1 := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: tkn},
	)
	tc := oauth2.NewClient(ctx1, ts)
	githubClient := github.NewClient(tc)
	return githubClient, ctx1
}

func log404(logger logr.Logger) {
	logger.Info("Returned status is 404 -> Request object Not Found (could have been deleted after reconcile request)")
}

func splitOwnerRepo(githubIssueRepo string) (owner string, repo string) {
	// LeeJoeBarak/githubissue-operator
	split := strings.Split(githubIssueRepo, "/")
	owner = split[0]
	repo = split[1]
	return owner, repo
}

func stateClosed(issue *github.Issue) bool {
	return issue != nil && *issue.State == "closed"
}

func isDescriptionEqual(issue *github.Issue, ghissue *g.GithubIssue) bool {
	return *issue.Body == ghissue.Spec.Desc
}

/**** UTILS ****/
func getTitle(ghissue *g.GithubIssue) string {
	return ghissue.Spec.Title
}
func getOwnerRepo(ghissue *g.GithubIssue) string {
	return ghissue.Spec.Repo
}
func getDesc(ghissue *g.GithubIssue) string {
	return ghissue.Spec.Desc
}
func getState(ghissue *g.GithubIssue) string {
	return ghissue.Status.State
}
func getupdateAt(ghissue *g.GithubIssue) string {
	return ghissue.Status.LastUpdateTimestamp
}

func setLogger(filemane string) {
	f, err := os.OpenFile(filemane, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("error opening file: %v", err)
	}
	defer f.Close()

	log.SetOutput(f)
	log.Println(" ===> This is a test log entry")
}
