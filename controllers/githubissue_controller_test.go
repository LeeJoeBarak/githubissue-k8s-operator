package controllers

import (
	"context"
	"github.com/leejoebarak/githubissue-operator/api/v1alpha1"
	fakegithub "github.com/leejoebarak/githubissue-operator/pkg/github"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"testing"
)

func TestGithubIssueControllerDeploymentCreate(t *testing.T) {

	// Create array of tester GithubIssue objects with metadata and spec & save to 'objs'
	// Register operator types with the runtime scheme. - not working, maybe redundant?
	// Create a fake client to mock API calls.
	// Create a Reconciler object with the scheme and fake k8s client + fake Github client.
	// Create Array of Mock requests to simulate Reconcile() being called on an event for a
	// watched resource .
	testGI := v1alpha1.GithubIssue{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ghTest",
			Namespace: "default",
		},
		Spec: v1alpha1.GithubIssueSpec{
			Title: "testTitle",
			Repo:  "repoTest",
			Desc:  "testDesc",
		},
	}

	// Objects to track in the fake client.
	objs := []runtime.Object{testGI.DeepCopyObject()}

	// Register operator types with the runtime scheme.
	/*s := scheme.Scheme
	s.AddKnownTypes(v1alpha1.SchemeGroupVersion, testGI)
	*/
	// Create a fake client to mock API calls.
	cl := fake.NewClientBuilder().WithRuntimeObjects(objs...).Build()

	// Create a Reconciler object with the scheme and fake client.
	r := &GithubIssueReconciler{
		Client:       cl,
		Log:          ctrl.Log.WithName("controllers").WithName("GithubIssue"),
		Scheme:       scheme.Scheme,
		GithubClient: fakegithub.NewFakeClient(),
	}

	// Mock request to simulate Reconcile() being called on an event for a
	// watched resource .
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "ghTest",
			Namespace: "default",
		},
	}
	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}
	/*TODO implement reconcile test-cases loop */
	res.IsZero()
	/*	// Check the result of reconciliation to make sure it has the desired state.
		if !res.Requeue {
			t.Error("reconcile did not requeue request as expected")
		}
		// Check if deployment has been created and has the correct size.
		dep := &appsv1.Deployment{}
		err = r.Get(context.T ODO(), req.NamespacedName, dep)
		if err != nil {
			t.Fatalf("get deployment: (%v)", err)
		}
		// Check if the quantity of Replicas for this deployment is equals the specification
		dsize := *dep.Spec.Replicas
		if dsize != replicas {
			t.Errorf("dep size (%d) is not the expected size (%d)", dsize, replicas)
		}
	*/
}
