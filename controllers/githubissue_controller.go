package controllers

import (
	"context"
	"github.com/go-logr/logr"
	"github.com/leejoebarak/githubissue-operator/api/v1alpha1"
	githubinterface "github.com/leejoebarak/githubissue-operator/pkg/github"
	pkgerr "github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"log"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil" //finalizer related
	"strings"
	// "github.com/google/go-github/github" // with go modules disabled
)

// GithubIssueReconciler reconciles a GithubIssue object
type GithubIssueReconciler struct {
	client.Client //type embedding
	Log           logr.Logger
	Scheme        *runtime.Scheme
	GithubClient  githubinterface.Client
}

const finalizerName = "training.redhat.com/finalizer" // domain/name-of-custom-finalizer

func (r *GithubIssueReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("githubissue_name", req.NamespacedName)
	logger.Info("************** START LOGIC **************")

	ghissue := v1alpha1.GithubIssue{}
	err := r.Client.Get(ctx, req.NamespacedName, &ghissue) //get the githubIssue *k8s* object VALUES, if exists (from k8s api server)
	if err != nil {
		if errors.IsNotFound(err) { //err status is 404 -> return with nil error (don't requeue)
			logger.Info("Returned status is 404 -> Request object Not Found (could have been deleted after reconcile request)")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Error reading the object -> Requeue the request.")
		return ctrl.Result{}, err
	}

	err = r.AddFinalizer(&ghissue, ctx) // Add finalizer for this CR
	if err != nil {
		logger.Error(err, "")
	}

	owner, repo := splitOwnerRepo(ghissue.Spec.Repo)                   // check if issue already exists on github
	allRepoIssues, err := r.GithubClient.ListIssuesByRepo(owner, repo) //[V]
	if err != nil {
		logger.Error(err, "couldn't retrieve list of issues from Github")
		return ctrl.Result{}, err
	}

	issue, err := r.GithubClient.SearchIssueByTitle(allRepoIssues, ghissue.Spec.Title) //[V]
	if err != nil {
		// issue not found
		if !ghissue.ObjectMeta.DeletionTimestamp.IsZero() { //DeletionTimestamp Not Zero && No issue on Github
			controllerutil.RemoveFinalizer(&ghissue, finalizerName)
			err = r.Update(ctx, &ghissue)
			if err != nil {
				logger.Error(err, "r.Update() failed")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		issue, err = r.GithubClient.Create(owner, repo, &ghissue) // k8s object is not being deleted
		if err != nil {
			logger.Error(err, "couldn't create new issue on Github")
			return ctrl.Result{}, err
		}
	} else {
		// issue was found
		if !ghissue.ObjectMeta.DeletionTimestamp.IsZero() { // DeletionTimestamp Not Zero
			err = r.handleDeletionIfIssueFound(owner, repo, &ghissue, issue) //[V]
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
		if !isDescriptionEqual(issue, &ghissue) { // k8s object is not being deleted
			_, err = r.GithubClient.Update(owner, repo, issue.Number, &ghissue) //[V]
			if err != nil {
				logger.Error(err, "couldn't update issue on Github")
				return ctrl.Result{}, err
			}
		}
	}
	err = r.updateStatus(ctx, issue, &ghissue) //[V]
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil //no error
}

// SetupWithManager sets up the controller with the Manager.
func (r *GithubIssueReconciler) SetupWithManager(mgr ctrl.Manager) error {
	/* this method tells the controller "you are tracking resources of type GitHubIssue" */
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.GithubIssue{}).
		Complete(r)
}

func (r *GithubIssueReconciler) AddFinalizer(ghissue *v1alpha1.GithubIssue, ctx context.Context) error {
	// Add finalizer for this CR
	if !controllerutil.ContainsFinalizer(ghissue, finalizerName) {
		controllerutil.AddFinalizer(ghissue, finalizerName)
		err := r.Update(ctx, ghissue)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *GithubIssueReconciler) handleDeletionIfIssueFound(owner, repo string, ghissue *v1alpha1.GithubIssue, issue *githubinterface.IssueData) error {
	if issue == nil {
		return pkgerr.New("handleDeletionIfIssueFound(..) received a nil 'issue' argument (you can't close an issue that never existed) ")
	}

	if stateClosed(issue) { // issue already closed on github
		controllerutil.RemoveFinalizer(ghissue, finalizerName)
	} else {
		err := r.GithubClient.CloseIssue(owner, repo, ghissue, issue)
		if err != nil {
			return pkgerr.Wrap(err, "couldn't close issue on Github")
		}
		controllerutil.RemoveFinalizer(ghissue, finalizerName) // successful deletion of external resources -> remove our finalizer from the list
	}
	return nil
}

func stateClosed(issue *githubinterface.IssueData) bool {
	return issue != nil && issue.State == "closed"
}

func (r *GithubIssueReconciler) updateStatus(ctx context.Context, issue *githubinterface.IssueData, ghissue *v1alpha1.GithubIssue) error {
	ghissue.Status.State = issue.State
	ghissue.Status.LastUpdateTimestamp = issue.UpdatedAt
	err := r.Status().Update(ctx, ghissue)
	if err != nil {
		r.Log.Error(err, "((GithubIssueReconciler)r).Status().Update() failed ")
		return err
	}
	return nil
}

func isDescriptionEqual(issue *githubinterface.IssueData, ghissue *v1alpha1.GithubIssue) bool {
	return issue != nil && issue.Body == ghissue.Spec.Desc
}

func splitOwnerRepo(githubIssueRepo string) (owner string, repo string) {
	// LeeJoeBarak/githubissue-operator
	split := strings.Split(githubIssueRepo, "/")
	owner = split[0]
	repo = split[1]
	return owner, repo
}

/*=====================================================================================
======================================= UTILS =========================================
=======================================================================================*/
func getTitle(ghissue *v1alpha1.GithubIssue) string {
	return ghissue.Spec.Title
}
func getOwnerRepo(ghissue *v1alpha1.GithubIssue) string {
	return ghissue.Spec.Repo
}
func getDesc(ghissue *v1alpha1.GithubIssue) string {
	return ghissue.Spec.Desc
}
func getState(ghissue *v1alpha1.GithubIssue) string {
	return ghissue.Status.State
}
func getupdateAt(ghissue *v1alpha1.GithubIssue) string {
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
