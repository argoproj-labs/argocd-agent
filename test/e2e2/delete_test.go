// Copyright 2025 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e2

import (
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e2/fixture"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type DeleteTestSuite struct {
	fixture.BaseSuite
}

func (suite *DeleteTestSuite) Test_CascadeDeleteOnManagedAgent() {
	requires := suite.Require()

	t := suite.T()

	t.Log("Create a managed application in the principal's cluster")
	appOnPrincipal := v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: "agent-managed",
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
			},
			Destination: v1alpha1.ApplicationDestination{
				Name:      "agent-managed",
				Namespace: "guestbook",
			},
			SyncPolicy: &v1alpha1.SyncPolicy{
				Automated: &v1alpha1.SyncPolicyAutomated{},
				SyncOptions: v1alpha1.SyncOptions{
					"CreateNamespace=true",
				},
			},
		},
	}

	err := ensureAppExistsAndIsSyncedAndHealthy(&appOnPrincipal, suite.PrincipalClient, &suite.BaseSuite)
	requires.NoError(err)

	t.Log("'kustomize-guestbook-ui' Deployment should exist on managed-agent")
	depl := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "kustomize-guestbook-ui", Namespace: "guestbook"}}
	requires.Eventually(func() bool {
		err := suite.ManagedAgentClient.Get(suite.Ctx, fixture.ToNamespacedName(depl), depl, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second)

	t.Log("Add a finalizer to the Deployment to keep it from being deleted")
	depl.Finalizers = append(depl.Finalizers, "test-e2e/my-test-finalizer")
	err = suite.ManagedAgentClient.Update(suite.Ctx, depl, metav1.UpdateOptions{})
	requires.NoError(err)

	// Ensure that finalizer is removed from Deployment if the test ends prematurely
	defer func() {
		err = suite.ManagedAgentClient.Get(suite.Ctx, fixture.ToNamespacedName(depl), depl, metav1.GetOptions{})
		if err == nil { // If the Deployment still exists
			depl.Finalizers = nil // Remove the finalizer
			err = suite.ManagedAgentClient.Update(suite.Ctx, depl, metav1.UpdateOptions{})
			requires.NoError(err)
		}
	}()

	t.Log("Simulate cascade deletion from Argo CD: Add a resources-finalizer to principal, then delete it (which sets deletion timestamp)")
	err = suite.PrincipalClient.EnsureApplicationUpdate(suite.Ctx, fixture.ToNamespacedName(&appOnPrincipal), func(a *v1alpha1.Application) error {
		a.Finalizers = append(appOnPrincipal.Finalizers, "resources-finalizer.argocd.argoproj.io")
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	err = suite.PrincipalClient.Delete(suite.Ctx, &appOnPrincipal, metav1.DeleteOptions{})
	requires.NoError(err)

	t.Log("Ensure that finalizers and deletion timestamp are synced to managed-agent")
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, fixture.ToNamespacedName(&appOnPrincipal), &app, metav1.GetOptions{})
		return err == nil && app.DeletionTimestamp != nil && len(app.Finalizers) > 0
	}, 60*time.Second, 1*time.Second)

	t.Log("Remove finalizer from the Deployment, so that it may proceed with being deleted")
	err = suite.ManagedAgentClient.Get(suite.Ctx, fixture.ToNamespacedName(depl), depl, metav1.GetOptions{})
	requires.NoError(err)

	depl.Finalizers = nil
	err = suite.ManagedAgentClient.Update(suite.Ctx, depl, metav1.UpdateOptions{})
	requires.NoError(err)

	t.Log("Verify managed agent application is eventually deleted")
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, fixture.ToNamespacedName(&appOnPrincipal), &app, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 60*time.Second, 1*time.Second)

	t.Log("Verify guestbook Deployment is deleted from managed-agent cluster, which proves it is a cascaded delete")
	requires.Eventually(func() bool {
		err := suite.ManagedAgentClient.Get(suite.Ctx, fixture.ToNamespacedName(depl), depl, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 60*time.Second, 1*time.Second)

	t.Log("Verify principal application is deleted")
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, fixture.ToNamespacedName(&appOnPrincipal), &app, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 60*time.Second, 1*time.Second)

}

func TestDeleteTestSuite(t *testing.T) {
	suite.Run(t, new(DeleteTestSuite))
}
