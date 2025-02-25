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
	argoapp "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type ResyncTestSuite struct {
	fixture.BaseSuite
}

func (suite *ResyncTestSuite) TearDownTest() {
	suite.BaseSuite.TearDownTest()
	requires := suite.Require()

	// Ensure that all the components are running after runnings the tests
	if !fixture.IsProcessRunning("process") {
		err := fixture.StartProcess("principal")
		requires.NoError(err)
	}

	if !fixture.IsProcessRunning("agent-managed") {
		err := fixture.StartProcess("agent-managed")
		requires.NoError(err)
	}

	if !fixture.IsProcessRunning("agent-autonomous") {
		err := fixture.StartProcess("agent-autonomous")
		requires.NoError(err)
	}
}

// Managed Mode: delete the app from the control-plane when the principal process is
// down and ensure that the app is deleted from the workload cluster when the principal process restarts
func (suite *ResyncTestSuite) Test_ResyncDeletionOnPrincipalStartupManaged() {
	requires := suite.Require()

	app := suite.createManagedApp()
	key := fixture.ToNamespacedName(app)

	// Stop the principal and delete the app from the control-plane
	err := fixture.StopProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	err = suite.PrincipalClient.Delete(suite.Ctx, app, metav1.DeleteOptions{})
	requires.NoError(err)

	// App should still exist on the workload cluster
	err = suite.ManagedAgentClient.Get(suite.Ctx, key, app, metav1.GetOptions{})
	requires.NoError(err)

	// Start the principal and ensure that the app is deleted from the workload cluster
	err = fixture.StartProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 60*time.Second, 1*time.Second)
}

// Managed Mode: update the app on the control-plane when the principal process is down and ensure
// that the app gets updated on the workload when the principal process restarts
func (suite *ResyncTestSuite) Test_ResyncUpdatesOnPrincipalStartupManaged() {
	requires := suite.Require()

	app := suite.createManagedApp()
	key := fixture.ToNamespacedName(app)

	// Stop the principal and update the app on the control-plane
	err := fixture.StopProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	err = suite.PrincipalClient.EnsureApplicationUpdate(suite.Ctx, key, func(a *argoapp.Application) error {
		a.Spec.Source.Path = "guestbook"
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// App should not be updated on the workload cluster
	err = suite.ManagedAgentClient.Get(suite.Ctx, key, app, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal("kustomize-guestbook", app.Spec.Source.Path)

	// Start the principal and ensure that the app is updated on the workload cluster
	err = fixture.StartProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
		return err == nil &&
			app.Spec.Source.Path == "guestbook"
	}, 60*time.Second, 1*time.Second)
}

// Managed Mode: delete the app from the workload cluster when the agent process is down and
// ensure that the app gets recreated on the workload cluster when the agent process is restarted
func (suite *ResyncTestSuite) Test_ResyncDeletionOnAgentStartupManaged() {
	requires := suite.Require()

	app := suite.createManagedApp()
	key := fixture.ToNamespacedName(app)

	// Stop the agent and delete the app
	err := fixture.StopProcess("agent-managed")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("agent-managed")
	}, 30*time.Second, 1*time.Second)

	err = suite.ManagedAgentClient.Delete(suite.Ctx, app, metav1.DeleteOptions{})
	requires.NoError(err)

	// Start the agent and ensure that the app is recreated on the agent side
	err = fixture.StartProcess("agent-managed")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return fixture.IsProcessRunning("agent-managed")
	}, 30*time.Second, 1*time.Second)

	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second)
}

// Autonomous Mode: delete the app when the agent process is down and ensure that the app is
// deleted on the principal side when the agent process is restarted
func (suite *ResyncTestSuite) Test_ResyncDeletionOnAgentStartupAutonomous() {
	requires := suite.Require()

	app := suite.createAutonomousApp()
	principalKey := types.NamespacedName{Name: app.Name, Namespace: "agent-autonomous"}

	// Stop the agent process and delete the app on the agent side
	err := fixture.StopProcess("agent-autonomous")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("agent-autonomous")
	}, 30*time.Second, 1*time.Second)

	err = suite.AutonomousAgentClient.Delete(suite.Ctx, app, metav1.DeleteOptions{})
	requires.NoError(err)

	// App should still exist on the principal
	err = suite.PrincipalClient.Get(suite.Ctx, principalKey, app, metav1.GetOptions{})
	requires.NoError(err)

	// Start the agent process and ensure that the app is deleted on the principal
	err = fixture.StartProcess("agent-autonomous")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return fixture.IsProcessRunning("agent-autonomous")
	}, 30*time.Second, 1*time.Second)

	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &app, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 60*time.Second, 1*time.Second)
}

// Autonomous Mode: update the app on the workload when the agent process is down and ensure
// that the app is updated on the principal side when the agent process is restarted
func (suite *ResyncTestSuite) Test_ResyncUpdatesOnAgentStartupAutonomous() {
	requires := suite.Require()

	app := suite.createAutonomousApp()
	principalKey := types.NamespacedName{Name: app.Name, Namespace: "agent-autonomous"}
	agentKey := fixture.ToNamespacedName(app)

	// Stop the agent process and update the app on the agent side
	err := fixture.StopProcess("agent-autonomous")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("agent-autonomous")
	}, 30*time.Second, 1*time.Second)

	app.Spec.Source.Path = "guestbook"
	err = suite.AutonomousAgentClient.EnsureApplicationUpdate(suite.Ctx, agentKey, func(a *argoapp.Application) error {
		a.Spec.Source.Path = "guestbook"
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// App should not be updated on the principal
	err = suite.PrincipalClient.Get(suite.Ctx, principalKey, app, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal("kustomize-guestbook", app.Spec.Source.Path)

	// Start the agent process and ensure that the app is updated on the principal
	err = fixture.StartProcess("agent-autonomous")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return fixture.IsProcessRunning("agent-autonomous")
	}, 30*time.Second, 1*time.Second)

	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &app, metav1.GetOptions{})
		return err == nil &&
			app.Spec.Source.Path == "guestbook"
	}, 60*time.Second, 1*time.Second)
}

// Autonomous Mode: delete the app from the control-plane when the principal process is down and
// ensure that the app is recreated on the control-plane when the principal process is restarted
func (suite *ResyncTestSuite) Test_ResyncDeletionOnPrincipalStartupAutonomous() {
	requires := suite.Require()

	app := suite.createAutonomousApp()
	principalKey := types.NamespacedName{Name: app.Name, Namespace: "agent-autonomous"}
	agentKey := fixture.ToNamespacedName(app)

	// Stop the principal process and delete the app on the principal side
	err := fixture.StopProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	principalApp := app.DeepCopy()
	principalApp.Namespace = "agent-autonomous"
	err = suite.PrincipalClient.Delete(suite.Ctx, principalApp, metav1.DeleteOptions{})
	requires.NoError(err)

	// App should still exist on the agent
	err = suite.AutonomousAgentClient.Get(suite.Ctx, agentKey, app, metav1.GetOptions{})
	requires.NoError(err)

	// Start the principal process and ensure that the app is created on the principal
	err = fixture.StartProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	requires.Eventually(func() bool {
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, principalApp, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second)
}

func (suite *ResyncTestSuite) createAutonomousApp() *argoapp.Application {
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

	return &app
}

func (suite *ResyncTestSuite) createManagedApp() *argoapp.Application {
	requires := suite.Require()
	// Create a managed application on the control-plane cluster
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

	// Ensure the app has been pushed to the workload cluster
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)

	// Check that the .spec field of the workload cluster matches that of the control-plane
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

	return &app
}

func TestResyncTestSuite(t *testing.T) {
	suite.Run(t, new(ResyncTestSuite))
}
