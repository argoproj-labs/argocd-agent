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
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	PrincipalName    = "principal"
	AgentManagedName = "agent-managed"
)

type CacheTestSuite struct {
	fixture.BaseSuite
}

func TestCacheTestSuite(t *testing.T) {
	suite.Run(t, new(CacheTestSuite))
}

func (suite *CacheTestSuite) TearDownTest() {
	suite.BaseSuite.TearDownTest()
}

func (suite *CacheTestSuite) SetupTest() {
	if !fixture.IsProcessRunning(PrincipalName) {
		// Start the principal if it is not running and wait for it to be ready
		suite.Require().NoError(fixture.StartProcess(PrincipalName))
		fixture.CheckReadiness(suite.T(), PrincipalName)
	} else {
		// If principal is already running, verify that it is ready
		fixture.CheckReadiness(suite.T(), PrincipalName)
	}

	if !fixture.IsProcessRunning(AgentManagedName) {
		// Start the agent if it is not running and wait for it to be ready
		suite.Require().NoError(fixture.StartProcess(AgentManagedName))
		fixture.CheckReadiness(suite.T(), AgentManagedName)
	} else {
		// If agent is already running, verify that it is ready
		fixture.CheckReadiness(suite.T(), AgentManagedName)
	}

	suite.BaseSuite.SetupTest()
}

// This test validates the scenario when a user creates an application in principal and it is deployed in managed-cluster by agent.
// Now if user tries to directly change the application in managed-cluster, then agent should revert those changes.
// But when user makes changes in principal, then they are successfully propagated to managed-cluster.
func (suite *CacheTestSuite) Test_RevertManagedClusterChanges() {
	requires := suite.Require()

	// Create a managed application in the principal-cluster and ensure it is deployed into managed-cluster
	app := createApp(suite.Ctx, suite.PrincipalClient, requires)
	principalKey := fixture.ToNamespacedName(&app)
	agentKey := types.NamespacedName{Name: app.Name, Namespace: "argocd"}
	app = validateManagedAppCreated(suite.Ctx, suite.ManagedAgentClient, suite.PrincipalClient, principalKey, agentKey, requires)

	// Case 1: Modify the application directly in the managed-cluster,
	// but changes are reverted to be in sync with principal
	updateAppInfo(suite.Ctx, suite.ManagedAgentClient, agentKey, []string{"a", "b"}, requires)
	validateAppReverted(suite.Ctx, suite.ManagedAgentClient, &app, agentKey, requires, suite.T())

	// Case 2:  Modify the application on the principal-cluster and
	// ensure new updates are propagated to the managed-cluster
	updateAppInfo(suite.Ctx, suite.PrincipalClient, principalKey, []string{"a", "b"}, requires)
	validateAppInfoUpdated(suite.Ctx, suite.ManagedAgentClient, agentKey, []string{"a", "b"}, requires)

	// Case 3: Again modify the application directly in the managed-agent,
	// but this time application is reverted to state of Case 2.
	updateAppInfo(suite.Ctx, suite.ManagedAgentClient, agentKey, []string{"x", "y"}, requires)
	requires.NoError(suite.PrincipalClient.Get(suite.Ctx, principalKey, &app, metav1.GetOptions{}))
	app.Spec.Destination.Name = "in-cluster"
	app.Spec.Destination.Server = ""
	validateAppReverted(suite.Ctx, suite.ManagedAgentClient, &app, agentKey, requires, suite.T())
}

// This test validates the scenario when agent is disconnected with principal and then user tries
// to directly changes the application in managed-cluster, in this case agent should revert those changes.
func (suite *CacheTestSuite) Test_RevertDisconnectedManagedClusterChanges() {
	requires := suite.Require()

	// Create a managed application in the principal-cluster and ensure it is deployed into managed-cluster
	app := createApp(suite.Ctx, suite.PrincipalClient, requires)
	principalKey := fixture.ToNamespacedName(&app)
	agentKey := types.NamespacedName{Name: app.Name, Namespace: "argocd"}
	app = validateManagedAppCreated(suite.Ctx, suite.ManagedAgentClient, suite.PrincipalClient, principalKey, agentKey, requires)

	// Case 1: Agent is disconnected with principal, now modify the application directly in the managed-cluster,
	// but changes should be reverted, to be in sync with last known state of principal application
	requires.NoError(fixture.StopProcess(PrincipalName))
	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning(PrincipalName)
	}, 30*time.Second, 1*time.Second)

	updateAppInfo(suite.Ctx, suite.ManagedAgentClient, agentKey, []string{"a", "b"}, requires)
	validateAppReverted(suite.Ctx, suite.ManagedAgentClient, &app, agentKey, requires, suite.T())

	// Case 2: Agent is reconnected with principal and now changes done in principal should reflect in managed-cluster
	requires.NoError(fixture.StartProcess(PrincipalName))
	fixture.CheckReadiness(suite.T(), PrincipalName)

	updateAppInfo(suite.Ctx, suite.PrincipalClient, principalKey, []string{"a", "b"}, requires)
	validateAppInfoUpdated(suite.Ctx, suite.ManagedAgentClient, agentKey, []string{"a", "b"}, requires)
}

// This test is to validate the case when agent is restarted and managed-cluster app is still in sync with principal,
// hence no update events are sent from principal to agent. In this case agent internally recreates application cache
// and still reverts any direct changes done in managed-cluster.
func (suite *CacheTestSuite) Test_CacheRecreatedOnRestart() {
	requires := suite.Require()

	// Create a managed application in the principal-cluster and ensure it is deployed into managed-cluster
	app := createApp(suite.Ctx, suite.PrincipalClient, requires)
	principalKey := fixture.ToNamespacedName(&app)
	agentKey := types.NamespacedName{Name: app.Name, Namespace: "argocd"}
	app = validateManagedAppCreated(suite.Ctx, suite.ManagedAgentClient, suite.PrincipalClient, principalKey, agentKey, requires)

	// Case 1: Agent is restarted, now make direct changes in the managed-cluster,
	// but they should be reverted to be in sync with last known state of principal app
	fixture.RestartAgent(suite.T(), AgentManagedName)
	fixture.CheckReadiness(suite.T(), AgentManagedName)

	updateAppInfo(suite.Ctx, suite.ManagedAgentClient, agentKey, []string{"a", "b"}, requires)
	validateAppReverted(suite.Ctx, suite.ManagedAgentClient, &app, agentKey, requires, suite.T())

	// Case 2:  Modify the application on the principal-cluster and
	// ensure new updates are propagated to the managed-cluster
	updateAppInfo(suite.Ctx, suite.PrincipalClient, principalKey, []string{"a", "b"}, requires)
	validateAppInfoUpdated(suite.Ctx, suite.ManagedAgentClient, agentKey, []string{"a", "b"}, requires)
}

// This test validates the scenario when agent is down, but user still makes changes in application manifest
// directly on managed-cluster, but when agent is up again, it should revert those changes.
func (suite *CacheTestSuite) Test_RevertManagedClusterOfflineChanges() {
	requires := suite.Require()

	// Create a managed application in the principal-cluster and ensure it is deployed into managed-cluster
	app := createApp(suite.Ctx, suite.PrincipalClient, requires)
	principalKey := fixture.ToNamespacedName(&app)
	agentKey := types.NamespacedName{Name: app.Name, Namespace: "argocd"}
	app = validateManagedAppCreated(suite.Ctx, suite.ManagedAgentClient, suite.PrincipalClient, principalKey, agentKey, requires)

	// Agent in not running, but still make changes in the managed-cluster application manifest.
	// When agent is restarted, these changes should be reverted to be in sync with principal.
	requires.Eventually(func() bool {
		return fixture.IsProcessRunning(AgentManagedName)
	}, 60*time.Second, 1*time.Second)

	requires.NoError(fixture.StopProcess(AgentManagedName))
	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning(AgentManagedName)
	}, 60*time.Second, 1*time.Second)

	updateAppInfo(suite.Ctx, suite.ManagedAgentClient, agentKey, []string{"a", "b"}, requires)

	requires.NoError(fixture.StartProcess(AgentManagedName))
	fixture.CheckReadiness(suite.T(), AgentManagedName)
	validateAppReverted(suite.Ctx, suite.ManagedAgentClient, &app, agentKey, requires, suite.T())
}

func createApp(ctx context.Context, client fixture.KubeClient, requires *require.Assertions) argoapp.Application {
	app := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "guestbook",
			Namespace: AgentManagedName,
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
	requires.NoError(client.Create(ctx, &app, metav1.CreateOptions{}))
	return app
}

func validateManagedAppCreated(ctx context.Context, mClient fixture.KubeClient, pClient fixture.KubeClient, principalKey types.NamespacedName, agentKey types.NamespacedName, requires *require.Assertions) argoapp.Application {
	app := argoapp.Application{}
	requires.NoError(pClient.Get(ctx, principalKey, &app, metav1.GetOptions{}))
	// The destination on the agent will be set to "in-cluster"
	app.Spec.Destination.Name = "in-cluster"
	app.Spec.Destination.Server = ""

	// Check that the spec field of agent app matches with principal app
	mapp := argoapp.Application{}
	requires.Eventually(func() bool {
		err := mClient.Get(ctx, agentKey, &mapp, metav1.GetOptions{})
		return err == nil && reflect.DeepEqual(&app.Spec, &mapp.Spec)
	}, 90*time.Second, 2*time.Second)

	return app
}

func updateAppInfo(ctx context.Context, client fixture.KubeClient, key types.NamespacedName, info []string, requires *require.Assertions) {
	requires.Eventually(func() bool {
		err := client.EnsureApplicationUpdate(ctx, key, func(app *argoapp.Application) error {
			app.Spec.Info = []argoapp.Info{
				{
					Name:  info[0],
					Value: info[1],
				},
			}
			return nil
		}, metav1.UpdateOptions{})
		if err != nil {
			fmt.Printf("Error updating app info for %v: %v\n", key, err)
		}
		return err == nil
	}, 30*time.Second, 2*time.Second)
}

func validateAppInfoUpdated(ctx context.Context, client fixture.KubeClient, key types.NamespacedName, info []string, requires *require.Assertions) {
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := client.Get(ctx, key, &app, metav1.GetOptions{})
		return err == nil &&
			len(app.Spec.Info) == 1 &&
			app.Spec.Info[0].Name == info[0] &&
			app.Spec.Info[0].Value == info[1]
	}, 90*time.Second, 2*time.Second)
}

func validateAppReverted(ctx context.Context, client fixture.KubeClient, app *argoapp.Application, key types.NamespacedName, requires *require.Assertions, t *testing.T) {
	requires.Eventually(func() bool {
		mapp := argoapp.Application{}
		err := client.Get(ctx, key, &mapp, metav1.GetOptions{})
		if err != nil {
			t.Logf("Get error in validateAppReverted: %v", err)
		}
		return err == nil &&
			reflect.DeepEqual(&app.Spec, &mapp.Spec)
	}, 90*time.Second, 2*time.Second)
}
