package github

import (
	"context"
	"github.com/google/go-github/v35/github"
	"github.com/leejoebarak/githubissue-operator/api/v1alpha1"
)

type fakeClient struct {
}

func (f fakeClient) Create(ctx context.Context, owner string, repo string, g2 *v1alpha1.GithubIssue) (*github.Issue, error) {
	panic("implement me")
}

func NewFakeClient() fakeClient {
	return fakeClient{}
}
