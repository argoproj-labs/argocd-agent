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
	"fmt"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type SpireTestSuite struct {
	fixture.BaseSuite

	spireEnv   *fixture.FakeSpireEnvironment
	tlsBackups *fixture.StaticTLSSecretBackups
}

var (
	spirePrincipalLogs = []string{
		fmt.Sprintf("Using SPIRE for TLS credentials (socket: unix://%s)", fixture.SpirePrincipalSocket),
		fmt.Sprintf("Connecting to SPIRE Agent at unix://%s", fixture.SpirePrincipalSocket),
		"Connected to SPIRE Agent, SVID obtained",
		"Using SPIRE for TLS credentials",
		"Now listening on [::]:8443",
	}
	spireManagedAgentLogs = []string{
		fmt.Sprintf("Connecting to SPIRE Agent at unix://%s", fixture.SpireManagedAgentSocket),
		"Connected to SPIRE Agent, SVID obtained",
		"Authentication successful",
	}
	spireAutonomousAgentLogs = []string{
		fmt.Sprintf("Connecting to SPIRE Agent at unix://%s", fixture.SpireAutonomousAgentSocket),
		"Connected to SPIRE Agent, SVID obtained",
		"Authentication successful",
	}
)

func (suite *SpireTestSuite) SetupSuite() {
	suite.BaseSuite.SetupSuite()

	requires := suite.Require()
	fixture.SkipIfAgentInClusterEnvVarIsSet(suite.T())

	// Backup static TLS secrets
	var err error
	suite.tlsBackups, err = fixture.BackupAndDeleteStaticTLSSecrets(
		suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient, suite.AutonomousAgentClient)
	requires.NoError(err)

	// Create fake SPIRE environment
	suite.spireEnv, err = fixture.NewFakeSpireEnvironment()
	requires.NoError(err)
	requires.NoError(suite.spireEnv.Start())
	requires.NoError(suite.spireEnv.WriteEnvVars())

	// Restart agents to pick up new SPIRE environment variables
	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	fixture.RestartAgent(suite.T(), fixture.AgentManagedName)
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)

	fixture.RestartAgent(suite.T(), fixture.AgentAutonomousName)
	fixture.CheckReadiness(suite.T(), fixture.AgentAutonomousName)
}

func (suite *SpireTestSuite) Test_AgentsConnectWhenSPIREIsEnabled() {
	requires := suite.Require()

	suite.T().Log("Verifying agents connected via SPIRE")

	fixture.VerifyAgentConnected(suite.T(), fixture.AgentManagedName, suite.ClusterDetails)
	fixture.VerifyAgentConnected(suite.T(), fixture.AgentAutonomousName, suite.ClusterDetails)

	suite.T().Log("Verifying principal and agent logs to ensure SPIRE is used for authentication")

	fixture.VerifyProcessLogs(suite.T(), fixture.PrincipalName, spirePrincipalLogs)
	fixture.VerifyProcessLogs(suite.T(), fixture.AgentManagedName, spireManagedAgentLogs)
	fixture.VerifyProcessLogs(suite.T(), fixture.AgentAutonomousName, spireAutonomousAgentLogs)

	suite.T().Log("Creating managed application")

	managedApp := newSpireGuestbookApplication(
		fixture.ManagedPrincipalAppNamespace(),
		"managed",
	)
	requires.NoError(suite.PrincipalClient.Create(suite.Ctx, &managedApp, metav1.CreateOptions{}))

	suite.T().Log("Verifying managed app is propagated to managed agent")

	managedAgentKey := types.NamespacedName{Name: managedApp.Name, Namespace: fixture.ManagedAgentAppNamespace()}
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, managedAgentKey, &app, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "managed application should replicate to agent via SPIRE")

	suite.T().Log("Verifying managed app is Synced and healthy")

	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, managedAgentKey, &app, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return app.Status.Sync.Status == "Synced" && app.Status.Health.Status == "Healthy"
	}, 60*time.Second, 2*time.Second, "app should become Synced and Healthy")

	suite.T().Log("Creating autonomous application")

	autonomousApp := newSpireGuestbookApplication(
		fixture.AutonomousAgentNamespace,
		"autonomous",
	)
	requires.NoError(suite.AutonomousAgentClient.Create(suite.Ctx, &autonomousApp, metav1.CreateOptions{}))

	suite.T().Log("Verifying autonomous app is propagated to principal")

	principalKey := types.NamespacedName{Name: autonomousApp.Name, Namespace: "agent-autonomous"}
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &app, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "autonomous application should replicate to principal via SPIRE")

	suite.T().Log("Verifying autonomous app is Synced and healthy")

	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &app, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return app.Status.Sync.Status == "Synced" && app.Status.Health.Status == "Healthy"
	}, 60*time.Second, 2*time.Second, "app should become Synced and Healthy")
}

func TestSpireTestSuite(t *testing.T) {
	suite.Run(t, new(SpireTestSuite))
}

func (suite *SpireTestSuite) TearDownSuite() {
	if suite.spireEnv != nil {
		suite.spireEnv.Stop()
	}

	fixture.ClearEnvVarsFile()
	suite.Require().NoError(fixture.RestoreStaticTLSSecrets(
		suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient, suite.AutonomousAgentClient, suite.tlsBackups))

	// Restart agents
	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	fixture.RestartAgent(suite.T(), fixture.AgentManagedName)
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)

	fixture.RestartAgent(suite.T(), fixture.AgentAutonomousName)
	fixture.CheckReadiness(suite.T(), fixture.AgentAutonomousName)
}

func newSpireGuestbookApplication(namespace string, mode string) argoapp.Application {
	app := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spire-guestbook",
			Namespace: namespace,
		},
		Spec: argoapp.ApplicationSpec{
			Project: "default",
			Source: &argoapp.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
			},
			SyncPolicy: &argoapp.SyncPolicy{
				SyncOptions: argoapp.SyncOptions{
					"CreateNamespace=true",
				},
				Automated: &argoapp.SyncPolicyAutomated{},
			},
		},
	}

	if mode == "managed" {
		app.Spec.Destination = fixture.ManagedDestination("guestbook")
	} else {
		app.Spec.Destination = argoapp.ApplicationDestination{
			Server:    "https://kubernetes.default.svc",
			Namespace: "guestbook",
		}
	}

	return app
}
