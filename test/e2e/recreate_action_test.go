// Copyright 2026 The argocd-agent Authors
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
	"os"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/gitops-engine/pkg/health"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type RecreateActionTestSuite struct {
	fixture.BaseSuite
}

func (suite *RecreateActionTestSuite) TearDownTest() {
	suite.BaseSuite.TearDownTest()
	requires := suite.Require()

	if _, err := os.Stat(fixture.EnvVariablesFromE2EFile); err == nil {
		requires.NoError(os.Remove(fixture.EnvVariablesFromE2EFile))
		fixture.RestartAgent(suite.T(), "agent-managed")
	}

	if !fixture.IsProcessRunning("agent-managed") {
		err := fixture.StartProcess("agent-managed")
		requires.NoError(err)
	}

	fixture.CheckReadiness(suite.T(), "agent-managed")
}

func (suite *RecreateActionTestSuite) createAndDeleteApp(action, appName string) *argoapp.Application {
	requires := suite.Require()
	t := suite.T()

	t.Logf("Configure agent with --on-application-recreate=%s", action)
	envVar := "ARGOCD_AGENT_ON_APPLICATION_RECREATE=" + action
	err := os.WriteFile(fixture.EnvVariablesFromE2EFile, []byte(envVar+"\n"), 0644)
	requires.NoError(err)

	fixture.RestartAgent(t, "agent-managed")
	fixture.CheckReadiness(t, "agent-managed")

	t.Log("Create a managed application with resources-finalizer and automated sync")
	app := &argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appName,
			Namespace: fixture.AgentManagedName,
			// add resources-finalizer to produce the cascade delete scenario
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
				Name:      fixture.AgentManagedName,
				Namespace: "guestbook",
			},
			SyncPolicy: &argoapp.SyncPolicy{
				Automated: &argoapp.SyncPolicyAutomated{},
				SyncOptions: argoapp.SyncOptions{
					"CreateNamespace=true",
				},
			},
		},
	}

	err = suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)

	fixture.WaitForAppSyncedAndHealthy(t, suite.Ctx, suite.PrincipalClient, nil, app)

	t.Log("Delete the application directly from the agent cluster (unauthorized)")
	agentApp := &argoapp.Application{ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: fixture.ManagedAgentNamespace}}
	err = suite.ManagedAgentClient.Delete(suite.Ctx, agentApp, metav1.DeleteOptions{})
	requires.NoError(err)

	t.Log("Wait for status (OutOfSync and Unhealthy) to be updated on the principal")
	appKey := fixture.ToNamespacedName(app)
	requires.Eventually(func() bool {
		principalApp := argoapp.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, appKey, &principalApp, metav1.GetOptions{})
		if err != nil {
			t.Logf("waiting for principal app status update: %v", err)
			return false
		}
		return principalApp.Status.Sync.Status != argoapp.SyncStatusCodeSynced ||
			principalApp.Status.Health.Status != health.HealthStatusHealthy
	}, 120*time.Second, 2*time.Second)

	return app
}

// Test_UnauthorizedDeleteWithClearStatus verifies that clearing operationState
// after recreation allows auto-sync to re-trigger and bring the app back.
func (suite *RecreateActionTestSuite) Test_UnauthorizedDeleteWithClearStatus() {
	app := suite.createAndDeleteApp("clear-status", "recreate-clear-test")

	suite.T().Log("Verify the application is recreated and returns to Synced and Healthy on the agent")
	agentAppKey := types.NamespacedName{Name: app.Name, Namespace: fixture.ManagedAgentNamespace}
	suite.Require().Eventually(func() bool {
		agentApp := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, agentAppKey, &agentApp, metav1.GetOptions{})
		return err == nil &&
			agentApp.Status.Sync.Status == argoapp.SyncStatusCodeSynced &&
			agentApp.Status.Health.Status == health.HealthStatusHealthy
	}, 300*time.Second, 2*time.Second)
}

// Test_UnauthorizedDeleteWithResync verifies that setting a sync operation
// after recreation forces an immediate re-sync back to healthy.
func (suite *RecreateActionTestSuite) Test_UnauthorizedDeleteWithResync() {
	app := suite.createAndDeleteApp("resync", "recreate-resync-test")

	suite.T().Log("Verify the application is recreated and returns to Synced and Healthy")
	fixture.WaitForAppSyncedAndHealthy(suite.T(), suite.Ctx, suite.PrincipalClient, nil, app)
}

// Test_UnauthorizedDeleteWithIgnore verifies that with "ignore", the agent does
// not take any corrective action after recreating the application: no Operation
// is set and operationState is not cleared. We verify this on the principal side
// because the agent-side app may be short-lived due to informer relist behavior.
func (suite *RecreateActionTestSuite) Test_UnauthorizedDeleteWithIgnore() {
	requires := suite.Require()
	app := suite.createAndDeleteApp("ignore", "recreate-ignore-test")

	suite.T().Log("Verify the agent does not trigger any recovery action on the principal app")
	appKey := fixture.ToNamespacedName(app)
	requires.Never(func() bool {
		var principalApp argoapp.Application
		err := suite.PrincipalClient.Get(suite.Ctx, appKey, &principalApp, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return principalApp.Operation != nil || principalApp.Status.OperationState == nil
	}, 30*time.Second, 2*time.Second)
}

func TestRecreateActionTestSuite(t *testing.T) {
	suite.Run(t, new(RecreateActionTestSuite))
}
