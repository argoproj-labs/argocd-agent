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

package e2e

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	"github.com/argoproj/argo-cd/v3/common"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
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

	if _, err := os.Stat(fixture.EnvVariablesFromE2EFile); err == nil {
		requires.NoError(os.Remove(fixture.EnvVariablesFromE2EFile))
		fixture.RestartAgent(suite.T(), "agent-managed")
		fixture.RestartAgent(suite.T(), "agent-autonomous")
	}

	// Ensure that all the components are running after runnings the tests
	if !fixture.IsProcessRunning("principal") {
		err := fixture.StartProcess("principal")
		requires.NoError(err)
	}

	fixture.CheckReadiness(suite.T(), "principal")

	if !fixture.IsProcessRunning("agent-managed") {
		err := fixture.StartProcess("agent-managed")
		requires.NoError(err)
	}

	fixture.CheckReadiness(suite.T(), "agent-managed")

	if !fixture.IsProcessRunning("agent-autonomous") {
		err := fixture.StartProcess("agent-autonomous")
		requires.NoError(err)
	}

	fixture.CheckReadiness(suite.T(), "agent-autonomous")
}

// Managed Mode: delete the app from the control-plane when the principal process is
// down and ensure that the app is deleted from the workload cluster when the principal process restarts
func (suite *ResyncTestSuite) Test_ResyncDeletionOnPrincipalStartupManaged() {
	requires := suite.Require()

	app := suite.createManagedApp()
	key := types.NamespacedName{Name: app.Name, Namespace: "argocd"}

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

	fixture.CheckReadiness(suite.T(), "principal")

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
	principalKey := fixture.ToNamespacedName(app)
	agentKey := types.NamespacedName{Name: app.Name, Namespace: "argocd"}

	// Stop the principal and update the app on the control-plane
	err := fixture.StopProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	err = suite.PrincipalClient.EnsureApplicationUpdate(suite.Ctx, principalKey, func(a *argoapp.Application) error {
		a.Spec.Source.Path = "guestbook"
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// App should not be updated on the workload cluster
	err = suite.ManagedAgentClient.Get(suite.Ctx, agentKey, app, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal("kustomize-guestbook", app.Spec.Source.Path)

	// Start the principal and ensure that the app is updated on the workload cluster
	err = fixture.StartProcess("principal")
	requires.NoError(err)

	fixture.CheckReadiness(suite.T(), "principal")

	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, agentKey, &app, metav1.GetOptions{})
		return err == nil &&
			app.Spec.Source.Path == "guestbook"
	}, 60*time.Second, 1*time.Second)
}

// Managed Mode: delete the app from the workload cluster when the agent process is down and
// ensure that the app gets recreated on the workload cluster when the agent process is restarted
func (suite *ResyncTestSuite) Test_ResyncDeletionOnAgentStartupManaged() {
	requires := suite.Require()

	app := suite.createManagedApp()
	key := types.NamespacedName{Name: app.Name, Namespace: "argocd"}

	// Stop the agent and delete the app
	err := fixture.StopProcess("agent-managed")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("agent-managed")
	}, 30*time.Second, 1*time.Second)

	err = suite.ManagedAgentClient.Delete(suite.Ctx, &argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: "argocd",
		},
	}, metav1.DeleteOptions{})
	requires.NoError(err)

	// Start the agent and ensure that the app is recreated on the agent side
	err = fixture.StartProcess("agent-managed")
	requires.NoError(err)

	fixture.CheckReadiness(suite.T(), "agent-managed")

	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second)
}

// Managed Mode: update the app on the workload cluster when the agent process is down and
// ensure that the app is resynced when the agent process is restarted
func (suite *ResyncTestSuite) Test_ResyncUpdatesOnAgentStartupManaged() {
	requires := suite.Require()

	app := suite.createManagedApp()
	key := types.NamespacedName{Name: app.Name, Namespace: "argocd"}

	// Stop the agent and update the app on the workload cluster
	err := fixture.StopProcess("agent-managed")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("agent-managed")
	}, 30*time.Second, 1*time.Second)

	err = suite.ManagedAgentClient.EnsureApplicationUpdate(suite.Ctx, key, func(a *argoapp.Application) error {
		a.Spec.Source.Path = "guestbook"
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Start the agent and ensure that the app is updated back on the agent side
	err = fixture.StartProcess("agent-managed")
	requires.NoError(err)

	fixture.CheckReadiness(suite.T(), "agent-managed")

	// updates to the app on the agent side should be reverted since principal is the source of truth
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
		return err == nil && app.Spec.Source.Path == "kustomize-guestbook"
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

	fixture.CheckReadiness(suite.T(), "agent-autonomous")

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

	fixture.CheckReadiness(suite.T(), "agent-autonomous")

	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &app, metav1.GetOptions{})
		return err == nil &&
			app.Spec.Source.Path == "guestbook"
	}, 80*time.Second, 1*time.Second)
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

	fixture.CheckReadiness(suite.T(), "principal")

	requires.Eventually(func() bool {
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, principalApp, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second)
}

// Autonomous Mode: update the app on the control-plane when the principal process is down and
// ensure that the app is resynced back on the control-plane when the principal process is restarted
func (suite *ResyncTestSuite) Test_ResyncUpdatesOnPrincipalStartupAutonomous() {
	requires := suite.Require()

	app := suite.createAutonomousApp()
	principalKey := types.NamespacedName{Name: app.Name, Namespace: "agent-autonomous"}

	// Stop the principal process and update the app on the principal side
	err := fixture.StopProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	principalApp := app.DeepCopy()
	principalApp.Namespace = "agent-autonomous"

	err = suite.PrincipalClient.EnsureApplicationUpdate(suite.Ctx, principalKey, func(a *argoapp.Application) error {
		a.Spec.Source.Path = "guestbook"
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Start the principal process and ensure that the app is updated back on the principal
	err = fixture.StartProcess("principal")
	requires.NoError(err)

	fixture.CheckReadiness(suite.T(), "principal")

	// updates to the app on the principal side should be reverted since agent is the source of truth in autonomous mode
	requires.Eventually(func() bool {
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, principalApp, metav1.GetOptions{})
		return err == nil &&
			principalApp.Spec.Source.Path == "kustomize-guestbook"
	}, 60*time.Second, 1*time.Second)
}

func (suite *ResyncTestSuite) Test_ResyncOnConnectionLostManagedMode() {
	requires := suite.Require()

	// Setup Toxiproxy
	proxy, cleanup, err := fixture.SetupToxiproxy(suite.T(), "agent-managed", "127.0.0.1:8475")
	requires.NoError(err)
	defer cleanup()

	// Restart the agent process so that the agent talks to the principal via Toxiproxy
	fixture.RestartAgent(suite.T(), "agent-managed")

	fixture.CheckReadiness(suite.T(), "agent-managed")

	// Create a managed app
	app := suite.createManagedApp()
	requires.NotNil(app)
	key := types.NamespacedName{Name: app.Name, Namespace: "argocd"}

	// Disable the connection between the agent and the principal
	requires.NoError(proxy.Disable())

	// Delete the app on the principal side
	err = suite.PrincipalClient.Delete(suite.Ctx, app, metav1.DeleteOptions{})
	requires.NoError(err)

	// App should still exist on the workload cluster since the agent is unaware of the deletion on the control plane
	err = suite.ManagedAgentClient.Get(suite.Ctx, key, app, metav1.GetOptions{})
	requires.NoError(err)

	// Enable the connection and ensure that the app is deleted from the workload cluster
	requires.NoError(proxy.Enable())

	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)
}

func (suite *ResyncTestSuite) Test_ResyncOnConnectionLostAutonomousMode() {
	requires := suite.Require()

	// Setup Toxiproxy
	proxy, cleanup, err := fixture.SetupToxiproxy(suite.T(), "agent-autonomous", "127.0.0.1:8475")
	requires.NoError(err)
	defer cleanup()

	// Restart the agent process so that the agent talks to the principal via Toxiproxy
	fixture.RestartAgent(suite.T(), "agent-autonomous")

	fixture.CheckReadiness(suite.T(), "agent-autonomous")

	// Create an autonomous app
	app := suite.createAutonomousApp()
	requires.NotNil(app)
	principalKey := types.NamespacedName{Name: app.Name, Namespace: "agent-autonomous"}

	// Disable the connection between the agent and the principal
	requires.NoError(proxy.Disable())

	// Delete the app on the agent side
	err = suite.AutonomousAgentClient.Delete(suite.Ctx, app, metav1.DeleteOptions{})
	requires.NoError(err)

	// App should still exist on the control plane since the principal is unaware of the deletion on the workload cluster
	err = suite.PrincipalClient.Get(suite.Ctx, principalKey, app, metav1.GetOptions{})
	requires.NoError(err)

	// Enable the connection and ensure that the app is deleted from the control plane
	requires.NoError(proxy.Enable())

	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &app, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)
}

// Update the AppProject rules when the principal is down and ensure that the
// repository is deleted from the workload cluster when the principal is restarted
func (suite *ResyncTestSuite) Test_RepositoryResync_OnAppProjectUpdate() {
	requires := suite.Require()

	// Create an appProject on the principal's cluster
	appProject := suite.createAppProject()
	projKey := fixture.ToNamespacedName(appProject)

	// Create a repository on the principal's cluster
	repo := suite.createRepository()
	key := fixture.ToNamespacedName(repo)

	// Stop the principal and update the AppProject rules
	err := fixture.StopProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	// Update the AppProject rules to not match any agent
	err = suite.PrincipalClient.EnsureAppProjectUpdate(suite.Ctx, projKey, func(a *argoapp.AppProject) error {
		a.Spec.Destinations = []argoapp.ApplicationDestination{}
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Start the principal and ensure that the repository and the appProject are deleted from the managed-agent
	err = fixture.StartProcess("principal")
	requires.NoError(err)

	fixture.CheckReadiness(suite.T(), "principal")

	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)

	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)

}

// Create a Repository when the principal is down and ensure that the
// repository is created on the workload cluster when the principal is restarted
func (suite *ResyncTestSuite) Test_RepositoryResync_OnCreation() {
	requires := suite.Require()

	// Stop the principal process
	err := fixture.StopProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	// Create an appProject on the control plane cluster
	appProject := sampleAppProject()

	err = suite.PrincipalClient.Create(suite.Ctx, appProject, metav1.CreateOptions{})
	requires.NoError(err)

	projKey := fixture.ToNamespacedName(appProject)

	// Create a repository on the control plane cluster
	sourceRepo := sampleRepository()

	err = suite.PrincipalClient.Create(suite.Ctx, sourceRepo, metav1.CreateOptions{})
	requires.NoError(err)

	// Start the principal and ensure that the repository is created on the workload cluster
	err = fixture.StartProcess("principal")
	requires.NoError(err)

	fixture.CheckReadiness(suite.T(), "principal")

	// Ensure the appProject has been created on the workload cluster
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET appProject from managed-agent")

	key := fixture.ToNamespacedName(sourceRepo)
	// Ensure the repository has been created on the workload cluster
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")
}

// Update the Repository when the principal is down and ensure that the
// repository is updated on the workload cluster when the principal is restarted
func (suite *ResyncTestSuite) Test_RepositoryResync_OnUpdate() {
	requires := suite.Require()

	// Create an appProject on the control plane cluster
	_ = suite.createAppProject()

	// Create a repository on the control plane cluster
	sourceRepo := suite.createRepository()
	key := fixture.ToNamespacedName(sourceRepo)

	// Stop the principal and update the AppProject rules
	err := fixture.StopProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	// Update the repository secret
	newURL := "https://github.com/example/repo2.git"
	err = suite.PrincipalClient.EnsureRepositoryUpdate(suite.Ctx, key, func(s *corev1.Secret) error {
		s.Data["url"] = []byte(newURL)
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Start the principal and ensure that the repository is updated on the workload cluster
	err = fixture.StartProcess("principal")
	requires.NoError(err)

	// Wait for the principal to be ready
	fixture.CheckReadiness(suite.T(), "principal")

	// Ensure the repository is updated on the workload cluster
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		if err != nil {
			fmt.Println("error getting repository", err)
			return false
		}
		if string(repository.Data["url"]) != newURL {
			fmt.Println("repository url does not match", string(repository.Data["url"]), newURL)
			return false
		}
		return true
	}, 30*time.Second, 1*time.Second)
}

// Delete the Repository when the principal is down and ensure that the
// repository is deleted from the workload cluster when the principal is restarted
func (suite *ResyncTestSuite) Test_RepositoryResync_OnDeletion() {
	requires := suite.Require()

	// Create an appProject on the control plane cluster
	appProject := suite.createAppProject()
	projKey := fixture.ToNamespacedName(appProject)

	// Create a repository on the control plane cluster
	sourceRepo := suite.createRepository()

	key := fixture.ToNamespacedName(sourceRepo)
	// Ensure the repository has been pushed to the workload cluster
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")

	// Stop the principal and update the AppProject rules
	err := fixture.StopProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	// Delete the repository
	err = suite.PrincipalClient.Delete(suite.Ctx, sourceRepo, metav1.DeleteOptions{})
	requires.NoError(err)

	// Start the principal and ensure that the repository is deleted from the workload cluster
	err = fixture.StartProcess("principal")
	requires.NoError(err)

	fixture.CheckReadiness(suite.T(), "principal")

	// Ensure the appProject is still present on the workload cluster
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)
}

// Create an AppProject when the principal is down and ensure that the
// AppProject is created on the workload cluster when the principal is restarted
func (suite *ResyncTestSuite) Test_AppProjectResync_OnCreate() {
	requires := suite.Require()

	// Stop the principal
	err := fixture.StopProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	// Create an appProject on the control plane cluster
	appProject := sampleAppProject()

	err = suite.PrincipalClient.Create(suite.Ctx, appProject, metav1.CreateOptions{})
	requires.NoError(err)

	// Start the principal and ensure that the appProject is created on the workload cluster
	err = fixture.StartProcess("principal")
	requires.NoError(err)

	fixture.CheckReadiness(suite.T(), "principal")

	projKey := fixture.ToNamespacedName(appProject)

	// Ensure the appProject has been created on the workload cluster
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET appProject from managed-agent")

}

// Update the AppProject when the principal is down and ensure that the
// AppProject is updated on the workload cluster when the principal is restarted
func (suite *ResyncTestSuite) Test_AppProjectResync_OnUpdate() {
	requires := suite.Require()

	// Create an appProject on the control plane cluster
	appProject := suite.createAppProject()
	projKey := fixture.ToNamespacedName(appProject)

	// Stop the principal
	err := fixture.StopProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	// Update the appProject spec
	des := "updated from e2e test"
	err = suite.PrincipalClient.EnsureAppProjectUpdate(suite.Ctx, projKey, func(a *argoapp.AppProject) error {
		a.Spec.Description = des
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Start the principal and ensure that the appProject is updated on the workload cluster
	err = fixture.StartProcess("principal")
	requires.NoError(err)

	fixture.CheckReadiness(suite.T(), "principal")

	// Ensure the appProject is updated on the workload cluster
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return err == nil && appProject.Spec.Description == des
	}, 30*time.Second, 1*time.Second)
}

// Delete the AppProject when the principal is down and ensure that the
// AppProject is deleted from the workload cluster when the principal is restarted
func (suite *ResyncTestSuite) Test_AppProjectResync_OnDeletion() {
	requires := suite.Require()

	// Create an appProject on the control plane cluster
	appProject := suite.createAppProject()
	projKey := fixture.ToNamespacedName(appProject)

	// Stop the principal
	err := fixture.StopProcess("principal")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("principal")
	}, 30*time.Second, 1*time.Second)

	// Delete the appProject
	err = suite.PrincipalClient.Delete(suite.Ctx, appProject, metav1.DeleteOptions{})
	requires.NoError(err)

	// Start the principal and ensure that the appProject is deleted from the workload cluster
	err = fixture.StartProcess("principal")
	requires.NoError(err)

	fixture.CheckReadiness(suite.T(), "principal")

	// Ensure the appProject is deleted from the workload cluster
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)
}

// Delete the AppProject from the control plane when the agent is down
// and ensure that the AppProject is deleted from the workload cluster when the agent is restarted
func (suite *ResyncTestSuite) Test_AppProjectResync_DeleteOnAgentDelete() {
	requires := suite.Require()

	appProject := suite.createAppProject()
	projKey := fixture.ToNamespacedName(appProject)

	// Stop the agent
	err := fixture.StopProcess("agent-managed")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("agent-managed")
	}, 30*time.Second, 1*time.Second)

	// Delete the appProject from the control plane
	err = suite.PrincipalClient.Delete(suite.Ctx, appProject, metav1.DeleteOptions{})
	requires.NoError(err)

	// AppProject should still exist on the workload cluster
	err = suite.ManagedAgentClient.Get(suite.Ctx, projKey, appProject, metav1.GetOptions{})
	requires.NoError(err)

	// Start the agent and ensure that the appProject is deleted from the workload cluster
	err = fixture.StartProcess("agent-managed")
	requires.NoError(err)

	fixture.CheckReadiness(suite.T(), "agent-managed")

	// Ensure the appProject has been created on the workload cluster
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)
}

// Create an AppProject on the workload cluster when the agent is down
// and ensure that the AppProject is deleted when the agent is restarted
func (suite *ResyncTestSuite) Test_AppProjectResync_CreateOnAgentDelete() {
	requires := suite.Require()

	// Stop the agent
	err := fixture.StopProcess("agent-managed")
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning("agent-managed")
	}, 30*time.Second, 1*time.Second)

	// Create an appProject on the control plane cluster
	appProject := sampleAppProject()
	err = suite.PrincipalClient.Create(suite.Ctx, appProject, metav1.CreateOptions{})
	requires.NoError(err)

	projKey := fixture.ToNamespacedName(appProject)

	// Start the agent and ensure that the appProject is created on the workload cluster
	err = fixture.StartProcess("agent-managed")
	requires.NoError(err)

	fixture.CheckReadiness(suite.T(), "agent-managed")

	// Ensure the appProject has been created on the workload cluster
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)
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
	app.Spec.Destination.Name = "agent-autonomous"
	app.Spec.Destination.Server = ""
	app.Spec.Project = "agent-autonomous-default"
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

	principalKey := fixture.ToNamespacedName(&app)
	agentKey := types.NamespacedName{Name: app.Name, Namespace: "argocd"}

	// Ensure the app has been pushed to the workload cluster
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, agentKey, &app, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)

	// Check that the .spec field of the workload cluster matches that of the control-plane
	app = argoapp.Application{}
	err = suite.PrincipalClient.Get(suite.Ctx, principalKey, &app, metav1.GetOptions{})
	// The destination on the agent will be set to in-cluster
	app.Spec.Destination.Name = "in-cluster"
	app.Spec.Destination.Server = ""
	requires.NoError(err)
	mapp := argoapp.Application{}
	err = suite.ManagedAgentClient.Get(suite.Ctx, agentKey, &mapp, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal(&app.Spec, &mapp.Spec)

	return &app
}

func (suite *ResyncTestSuite) createAppProject() *argoapp.AppProject {
	requires := suite.Require()

	// Create an AppProject on the control plane cluster
	appProject := sampleAppProject()

	err := suite.PrincipalClient.Create(suite.Ctx, appProject, metav1.CreateOptions{})
	requires.NoError(err)

	projKey := fixture.ToNamespacedName(appProject)

	// Ensure the AppProject has been pushed to the workload cluster
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		if err != nil {
			fmt.Println("error getting appProject", err)
			return false
		}
		return true
	}, 30*time.Second, 1*time.Second)

	return appProject
}

func (suite *ResyncTestSuite) createRepository() *corev1.Secret {
	requires := suite.Require()

	// Create a repository on the principal's cluster
	sourceRepo := sampleRepository()

	err := suite.PrincipalClient.Create(suite.Ctx, sourceRepo, metav1.CreateOptions{})
	requires.NoError(err)

	key := fixture.ToNamespacedName(sourceRepo)
	// Ensure the repository has been pushed to the managed-agent
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")

	return sourceRepo
}

func sampleAppProject() *argoapp.AppProject {
	return &argoapp.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample",
			Namespace: "argocd",
		},
		Spec: argoapp.AppProjectSpec{
			Destinations: []argoapp.ApplicationDestination{
				{
					Namespace: "*",
					Name:      "agent-*",
				},
			},
			SourceNamespaces: []string{"agent-*"},
		},
	}
}

func sampleRepository() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample",
			Namespace: "argocd",
			Labels: map[string]string{
				common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
			},
		},
		Data: map[string][]byte{
			"project": []byte("sample"),
			"url":     []byte("https://github.com/example/repo.git"),
		},
	}
}

// Test_ManagedAppRecreationOnDirectDeletion tests the scenario where:
// 1. A managed application exists on both principal and managed agent
// 2. The application is directly deleted from the managed agent (while agent is running)
// 3. The principal should detect this and recreate the application automatically
func (suite *ResyncTestSuite) Test_ManagedAppRecreationOnDirectDeletion() {
	requires := suite.Require()
	t := suite.T()

	t.Log("Create a managed application")
	app := suite.createManagedApp()
	key := types.NamespacedName{Name: app.Name, Namespace: "argocd"}

	t.Log("Verify application exists on both principal and managed agent")
	// Verify on principal
	principalApp := argoapp.Application{}
	err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{Name: app.Name, Namespace: "agent-managed"}, &principalApp, metav1.GetOptions{})
	requires.NoError(err, "Application should exist on principal")

	// Verify on managed agent
	managedApp := argoapp.Application{}
	err = suite.ManagedAgentClient.Get(suite.Ctx, key, &managedApp, metav1.GetOptions{})
	requires.NoError(err, "Application should exist on managed agent")

	t.Log("Delete application directly from managed agent")
	err = suite.ManagedAgentClient.Delete(suite.Ctx, &argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: "argocd",
		},
	}, metav1.DeleteOptions{})
	requires.NoError(err)

	t.Log("Verify application is deleted from managed agent")
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
		return err != nil
	}, 10*time.Second, 1*time.Second, "Application should be deleted from managed agent")

	t.Log("Verify application is automatically recreated on managed agent")
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &app, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 2*time.Second, "Application should be recreated on managed agent")

	t.Log("Verify recreated application has correct spec")
	recreatedApp := argoapp.Application{}
	err = suite.ManagedAgentClient.Get(suite.Ctx, key, &recreatedApp, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal(principalApp.Spec.Source.RepoURL, recreatedApp.Spec.Source.RepoURL, "Recreated app should have same source")
	requires.Equal(principalApp.Spec.Destination.Namespace, recreatedApp.Spec.Destination.Namespace, "Recreated app should have same namespace")
}

func TestResyncTestSuite(t *testing.T) {
	suite.Run(t, new(ResyncTestSuite))
}
