package github

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/google/go-github/v35/github"
	"github.com/leejoebarak/githubissue-operator/api/v1alpha1"
	"golang.org/x/oauth2"
	"net/http"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
)

type IssueData struct {
	Number    int    `json:"number,omitempty"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
	State     string `json:"state,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type Client interface {
	Create(owner, repo string, ghissue *v1alpha1.GithubIssue) (*IssueData, error)
	Update(owner, repo string, number int, ghissue *v1alpha1.GithubIssue) (*IssueData, error)
	ListIssuesByRepo(owner, repo string) ([]*IssueData, error)
	CloseIssue(owner, repo string, ghissue *v1alpha1.GithubIssue, issue *IssueData) error
	SearchIssueByTitle(issues []*IssueData, title string) (*IssueData, error)
}

/*----------------- IMPLEMENTATION -----------------------*/

type ClientImpl struct {
	Log            logr.Logger
	GoGithubClient *github.Client
	ctx            context.Context
}

func (c ClientImpl) SearchIssueByTitle(issues []*IssueData, title string) (*IssueData, error) {
	for _, issue := range issues {
		// i is the index where we are, title is the element from titles slice for where we are
		if title == issue.Title {
			return issue, nil
		}
	}
	return nil, fmt.Errorf("issue %s not found", title)
}

func (c ClientImpl) CloseIssue(owner, repo string, ghissue *v1alpha1.GithubIssue, issue *IssueData) error {
	/*
	* delete any external resources associated with the ghissue
	* Ensure that delete implementation is idempotent and safe to invoke multiple times for same object.*/
	issueReq := &github.IssueRequest{
		Title: github.String(ghissue.Spec.Title),
		Body:  github.String(ghissue.Spec.Desc),
		State: github.String("closed"),
	}
	_, resp, err := c.GoGithubClient.Issues.Edit(c.ctx, owner, repo, issue.Number, issueReq)
	if err != nil || (resp != nil && resp.StatusCode != http.StatusOK) {
		c.Log.WithName("CloseIssue()").Error(err, "Deleting github issue from Github failed") //print body as it may contain hints in case of errors
		return err
	}
	return nil
}

func (c ClientImpl) ListIssuesByRepo(owner, repo string) ([]*IssueData, error) {
	opts := github.IssueListByRepoOptions{
		State: "all",
	}
	issues, resp, err := c.GoGithubClient.Issues.ListByRepo(c.ctx, owner, repo, &opts)
	if err != nil || (resp != nil && resp.StatusCode != http.StatusOK) {
		c.Log.WithName("ListIssuesByRepo()").Error(err, "Reading the list of issues from github repo failed")
		return nil, err
	}
	issueList := createListOfIssues(issues)
	return issueList, nil
}

func createListOfIssues(issues []*github.Issue) []*IssueData {
	var issueList []*IssueData
	for _, issue := range issues {
		if issue != nil {
			auxIssue := &IssueData{
				Number:    *issue.Number,
				Title:     *issue.Title,
				Body:      *issue.Body,
				State:     *issue.State,
				UpdatedAt: (*issue.UpdatedAt).String(),
			}
			issueList = append(issueList, auxIssue)
		}
	}
	return issueList
}

func (c ClientImpl) Update(owner string, repo string, number int, ghissue *v1alpha1.GithubIssue) (*IssueData, error) {
	issueReq := &github.IssueRequest{
		Title: github.String(ghissue.Spec.Title),
		Body:  github.String(ghissue.Spec.Desc),
	}
	goGithubIssue, resp, err := c.GoGithubClient.Issues.Edit(c.ctx, owner, repo, number, issueReq)
	if err != nil || (resp != nil && resp.StatusCode != http.StatusOK) {
		c.Log.WithName("Update()").Error(err, "Updating github issue on Github failed")
		return nil, err
	}
	issue := &IssueData{
		Number:    *goGithubIssue.Number,
		Title:     *goGithubIssue.Title,
		Body:      *goGithubIssue.Body,
		State:     *goGithubIssue.State,
		UpdatedAt: (*goGithubIssue.UpdatedAt).String(),
	}
	return issue, nil
}

func (c ClientImpl) Create(owner string, repo string, ghissue *v1alpha1.GithubIssue) (*IssueData, error) {
	issueReq := &github.IssueRequest{
		Title: github.String(ghissue.Spec.Title),
		Body:  github.String(ghissue.Spec.Desc),
		State: github.String("open"),
	}
	goGithubIssue, resp, err := c.GoGithubClient.Issues.Create(c.ctx, owner, repo, issueReq)
	if err != nil || (resp != nil && resp.StatusCode != http.StatusCreated) {
		c.Log.WithName("Create()").Error(err, "Creation of github issue on Github  failed")
		return nil, err
	}
	issue := &IssueData{
		Number:    *goGithubIssue.Number,
		Title:     *goGithubIssue.Title,
		Body:      *goGithubIssue.Body,
		State:     *goGithubIssue.State,
		UpdatedAt: (*goGithubIssue.UpdatedAt).String(),
	}
	return issue, nil
}

func NewClientImpl() ClientImpl {
	tkn := os.Getenv("TOKEN")
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: tkn},
	)
	tc := oauth2.NewClient(ctx, ts)
	githubClient := github.NewClient(tc)
	return ClientImpl{
		Log:            ctrl.Log.WithName("client").WithName("GoGithubClient"),
		GoGithubClient: githubClient,
	}
}
