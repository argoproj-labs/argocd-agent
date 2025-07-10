// Copyright 2024 The argocd-agent Authors
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
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type BasicTestSuite struct {
	fixture.BaseSuite
}

func (suite *BasicTestSuite) Test_AgentManaged() {
	requires := suite.Require()

	// Create a managed application in the principal's cluster
	app := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "guestbook",
			Namespace: "agent-managed",
		},
		Spec: argoapp.ApplicationSpec{
			Project: "default",
			Source: &argoapp.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
			},
			Destination: argoapp.ApplicationDestination{
				Server:    "https://kubernetes.default.svc",
				Namespace: "guestbook",
			},
			SyncPolicy: &argoapp.SyncPolicy{
				SyncOptions: argoapp.SyncOptions{
					"CreateNamespace=true",
				},
			},
		},
	}
	err := suite.PrincipalClient.Create(suite.Ctx, &app, metav1.CreateOptions{})
	requires.NoError(err)

	key := fixture.ToNamespacedName(&app)

	// Ensure the app has been pushed to the managed-agent
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)

	// Check that the .spec field of the managed-agent matches that of the
	// principal
	app = argoapp.Application{}
	err = suite.PrincipalClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
	// The destination on the agent will be set to in-cluster
	app.Spec.Destination.Name = "in-cluster"
	app.Spec.Destination.Server = ""
	requires.NoError(err)
	mapp := argoapp.Application{}
	err = suite.ManagedAgentClient.Get(suite.Ctx, key, &mapp, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal(&app.Spec, &mapp.Spec)

	// Modify the application on the principal and ensure the change is
	// propagated to the managed-agent
	err = suite.PrincipalClient.EnsureApplicationUpdate(suite.Ctx, key, func(app *argoapp.Application) error {
		app.Spec.Info = []argoapp.Info{
			{
				Name:  "e2e",
				Value: "test",
			},
		}
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
		return err == nil &&
			len(app.Spec.Info) == 1 &&
			app.Spec.Info[0].Name == "e2e" &&
			app.Spec.Info[0].Value == "test"
	}, 30*time.Second, 1*time.Second)

	// Delete the app from the principal
	err = suite.PrincipalClient.Delete(suite.Ctx, &app, metav1.DeleteOptions{})
	requires.NoError(err)

	// Ensure the app has been deleted from the managed-agent
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)
}

func (suite *BasicTestSuite) Test_AgentAutonomous() {
	requires := suite.Require()

	// Create an autonomous application on the autonomous-agent's cluster
	app := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "guestbook",
			Namespace: "argocd",
			Finalizers: []string{
				"resources-finalizer.argocd.argoproj.io",
			},
		},
		Spec: argoapp.ApplicationSpec{
			Project: "default",
			Source: &argoapp.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
			},
			Destination: argoapp.ApplicationDestination{
				Server:    "https://kubernetes.default.svc",
				Namespace: "guestbook",
			},
			SyncPolicy: &argoapp.SyncPolicy{
				SyncOptions: argoapp.SyncOptions{
					"CreateNamespace=true",
				},
			},
		},
	}
	err := suite.AutonomousAgentClient.Create(suite.Ctx, &app, metav1.CreateOptions{})
	requires.NoError(err)

	principalKey := types.NamespacedName{Name: app.Name, Namespace: "agent-autonomous"}
	agentKey := fixture.ToNamespacedName(&app)

	// Ensure the app has been pushed to the principal
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &app, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)

	// Check that the .spec field of the principal matches that of the
	// autonomous-agent
	app = argoapp.Application{}
	err = suite.AutonomousAgentClient.Get(suite.Ctx, agentKey, &app, metav1.GetOptions{})
	requires.NoError(err)
	papp := argoapp.Application{}
	err = suite.PrincipalClient.Get(suite.Ctx, principalKey, &papp, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal(&app.Spec, &papp.Spec)

	// Modify the application on the autonomous-agent and ensure the change is
	// propagated to the principal
	err = suite.AutonomousAgentClient.EnsureApplicationUpdate(suite.Ctx, agentKey, func(app *argoapp.Application) error {
		app.Spec.Info = []argoapp.Info{
			{
				Name:  "e2e",
				Value: "test",
			},
		}
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &app, metav1.GetOptions{})
		return err == nil &&
			len(app.Spec.Info) == 1 &&
			app.Spec.Info[0].Name == "e2e" &&
			app.Spec.Info[0].Value == "test"
	}, 30*time.Second, 1*time.Second)

	// Delete the app from the autonomous-agent
	err = suite.AutonomousAgentClient.Delete(suite.Ctx, &app, metav1.DeleteOptions{})
	requires.NoError(err)

	// Ensure the app has been deleted from the principal
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &app, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)
}

func TestBasicTestSuite(t *testing.T) {
	suite.Run(t, new(BasicTestSuite))
}
