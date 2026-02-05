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
	"strings"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	v1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type TerminalStreamingTestSuite struct {
	fixture.BaseSuite
}

func (suite *TerminalStreamingTestSuite) validateTerminalStreaming() {
	requires := suite.Require()

	app := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "guestbook",
			Namespace: "agent-managed",
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				Path:           "kustomize-guestbook",
				TargetRevision: "HEAD",
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

	err := suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)

	// Create ArgoCD client using the ArgoCD server endpoint and admin password
	// ArgoCD Client is acting as a browser and trying to open a terminal session to the application in the managed-cluster.
	argoEndpoint, err := fixture.GetArgoCDServerEndpoint(suite.PrincipalClient)
	requires.NoError(err)
	password, err := fixture.GetInitialAdminSecret(suite.PrincipalClient)
	requires.NoError(err)
	argoClient := fixture.NewArgoClient(argoEndpoint, "admin", password)
	err = argoClient.Login()
	requires.NoError(err)

	// Wait until the app is synced and healthy
	fixture.WaitForAppSyncedAndHealthy(suite.T(), suite.Ctx, suite.PrincipalClient, argoClient, app)

	// Find the application pod which we want to open a terminal session for
	var podName, containerName string
	pods := &corev1.PodList{}
	err = suite.ManagedAgentClient.List(suite.Ctx, "guestbook", pods, metav1.ListOptions{})
	requires.NoError(err)
	requires.True(len(pods.Items) > 0, "expected at least one pod in guestbook namespace")
	for _, p := range pods.Items {
		if strings.Contains(p.Name, "kustomize-guestbook-ui") && p.Status.Phase == corev1.PodRunning {
			podName = p.Name
			if len(p.Spec.Containers) > 0 {
				containerName = p.Spec.Containers[0].Name
			}
			break
		}
	}
	requires.NotEmpty(podName, "could not find guestbook pod")
	requires.NotEmpty(containerName, "could not determine container name")

	// Open a terminal session with ArgoCD Server API.
	// This replicates the behavior of the ArgoCD UI when a user opens a terminal session to an application.
	// This is done by sending a resize message to the shell and then sending the command to the shell.
	// The shell will then execute the command and stream the output back to the principal.
	// The principal will then stream the output back to the UI.
	suite.T().Logf("Opening terminal session to pod %s, container %s", podName, containerName)
	session, err := argoClient.ExecTerminal(app, "guestbook", podName, containerName)
	requires.NoError(err, "failed to open terminal session")
	defer session.Close()

	// Send a resize message first, this is required by the shell to render the output content accordingly.
	err = session.SendResize(80, 24)
	requires.NoError(err, "failed to send resize")

	// Wait for shell to initialize by checking for any output
	requires.Eventually(func() bool {
		return len(session.GetOutput()) > 0
	}, 10*time.Second, 1*time.Second, "shell did not initialize")

	// Test 1: Run 'pwd' command - should return /var/www/html
	err = session.SendInput("pwd\n")
	requires.NoError(err, "failed to send pwd command")
	found := session.WaitForOutput("/var/www/html", 10*time.Second)
	requires.True(found, "expected to find /var/www/html in pwd output, got: %s", session.GetOutput())
	suite.T().Log("Test 1 passed: pwd command executed successfully")

	// Test 2: Run 'whoami' command - should return root
	err = session.SendInput("whoami\n")
	requires.NoError(err, "failed to send whoami command")
	found = session.WaitForOutput("root", 10*time.Second)
	requires.True(found, "expected to find 'root' in whoami output, got: %s", session.GetOutput())
	suite.T().Log("Test 2 passed: whoami command executed successfully")

	// Test 3: Run 'ls' command - should list files including index.html
	err = session.SendInput("ls\n")
	requires.NoError(err, "failed to send ls command")
	found = session.WaitForOutput("index.html", 10*time.Second)
	requires.True(found, "expected to find 'index.html' in ls output, got: %s", session.GetOutput())
	suite.T().Log("Test 3 passed: ls command executed successfully")

	suite.T().Logf("All terminal commands executed successfully.")
}

// Test_terminal_streaming_managed tests terminal streaming with manually created cluster secret
func (suite *TerminalStreamingTestSuite) Test_terminal_streaming_managed() {
	suite.validateTerminalStreaming()
}

// Test_SelfRegisteredSecret_Terminal tests terminal streaming with self-registered cluster secret
func (suite *TerminalStreamingTestSuite) Test_SelfRegisteredSecret_Terminal() {
	requires := suite.Require()

	// Get original secret before any modifications for restoration later
	originalSecret, err := fixture.GetClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)

	// Enable self-registration
	requires.NoError(fixture.EnableSelfAgentRegistration(suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient))
	defer func() {
		// Disable self-registration
		_ = fixture.DisableSelfAgentRegistration(suite.Ctx, suite.PrincipalClient)

		// Delete the self-registered secret
		_ = fixture.DeleteClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)

		// Restore the original secret
		originalSecret.ResourceVersion = "" // Clear for recreation
		originalSecret.UID = ""
		_ = suite.PrincipalClient.Create(suite.Ctx, originalSecret, metav1.CreateOptions{})

		// Restart to use restored secret
		fixture.RestartAgent(suite.T(), fixture.PrincipalName)
		fixture.CheckReadiness(suite.T(), fixture.PrincipalName)
		fixture.RestartAgent(suite.T(), fixture.AgentManagedName)
		fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)
	}()

	// Stop agent
	err = fixture.StopProcess(fixture.AgentManagedName)
	requires.NoError(err)
	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning(fixture.AgentManagedName)
	}, 30*time.Second, 1*time.Second)

	// Delete manual secret
	err = fixture.DeleteClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)

	// Wait for secret to be deleted
	requires.Eventually(func() bool {
		return !fixture.ClusterSecretExists(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	}, 10*time.Second, 1*time.Second)

	// Restart principal with self-registration enabled
	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	// Restart agent to trigger self-registration
	fixture.RestartAgent(suite.T(), fixture.AgentManagedName)
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)

	// Wait for self-registered secret to be created
	requires.Eventually(func() bool {
		return fixture.ClusterSecretExists(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	}, 30*time.Second, 1*time.Second, "Self-registered cluster secret should be created")

	suite.T().Log("Self-registered secret created, now testing terminal streaming")

	// Run the same terminal validation
	suite.validateTerminalStreaming()
}

func TestTerminalStreamingTestSuite(t *testing.T) {
	t.Run("terminal_streaming", func(t *testing.T) {
		suite.Run(t, new(TerminalStreamingTestSuite))
	})
}
