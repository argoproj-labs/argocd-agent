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

	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	"github.com/argoproj/argo-cd/v3/common"
	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SelfAgentRegistrationTestSuite struct {
	fixture.BaseSuite
	// originalSecret stores the default manually created cluster secret for restoration after tests
	originalSecret *corev1.Secret
}

func TestSelfAgentRegistrationTestSuite(t *testing.T) {
	suite.Run(t, new(SelfAgentRegistrationTestSuite))
}

func (suite *SelfAgentRegistrationTestSuite) SetupSuite() {
	suite.BaseSuite.SetupSuite()

	// Store the original manually-created cluster secret which is created by e2e setup
	// This will be used to restore the secret after tests that modify/delete it
	secret, err := fixture.GetClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	if err != nil {
		suite.T().Fatalf("Failed to get original cluster secret: %v - tests cannot run without a restorable secret", err)
	}
	suite.originalSecret = secret.DeepCopy()
	suite.T().Logf("Stored original cluster secret: %s, UID: %s", secret.Name, secret.UID)
}

func (suite *SelfAgentRegistrationTestSuite) SetupTest() {
	suite.BaseSuite.SetupTest()

	if !fixture.IsProcessRunning(fixture.PrincipalName) {
		suite.Require().NoError(fixture.StartProcess(fixture.PrincipalName))
		fixture.CheckReadiness(suite.T(), fixture.PrincipalName)
	} else {
		fixture.CheckReadiness(suite.T(), fixture.PrincipalName)
	}

	if !fixture.IsProcessRunning(fixture.AgentManagedName) {
		suite.Require().NoError(fixture.StartProcess(fixture.AgentManagedName))
		fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)
	} else {
		fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)
	}

	// Ensure the original manually-created secret exists before each test
	suite.ensureOriginalSecretExists()
}

func (suite *SelfAgentRegistrationTestSuite) TearDownTest() {
	// Delete self-registered secret and restore original manual secret
	suite.restoreOriginalSecret()

	// Always disable self agent registration and restart principal to ensure clean state
	_ = fixture.DisableSelfAgentRegistration(suite.Ctx, suite.PrincipalClient)
	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	// Ensure agent is running
	if !fixture.IsProcessRunning(fixture.AgentManagedName) {
		suite.Require().NoError(fixture.StartProcess(fixture.AgentManagedName))
	}
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)

	suite.BaseSuite.TearDownTest()
}

// ensureOriginalSecretExists ensures the original manually-created cluster secret exists before each test
func (suite *SelfAgentRegistrationTestSuite) ensureOriginalSecretExists() {
	if suite.originalSecret == nil {
		return
	}

	currentSecret, err := fixture.GetClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	if errors.IsNotFound(err) {
		// Secret doesn't exist, restore the original
		suite.T().Log("Original cluster secret not found, restoring it")
		suite.createOriginalSecret()
		return
	}

	if err != nil {
		suite.T().Logf("Error checking cluster secret: %v", err)
		return
	}

	// If current secret is self-registered, delete it and restore original
	if currentSecret.Labels[cluster.LabelKeySelfRegisteredCluster] == "true" {
		suite.T().Log("Found self-registered secret, replacing with original manual secret")
		_ = fixture.DeleteClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
		suite.createOriginalSecret()
	}
}

// restoreOriginalSecret deletes any self-registered secret and restores the original manual secret
func (suite *SelfAgentRegistrationTestSuite) restoreOriginalSecret() {
	if suite.originalSecret == nil {
		return
	}

	currentSecret, err := fixture.GetClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	if errors.IsNotFound(err) {
		// Secret doesn't exist, restore the original
		suite.T().Log("Cluster secret not found after test, restoring original")
		suite.createOriginalSecret()
		return
	}

	if err != nil {
		suite.T().Logf("Error checking cluster secret in TearDown: %v", err)
		return
	}

	// If current secret is self-registered, delete it and restore original
	if currentSecret.Labels[cluster.LabelKeySelfRegisteredCluster] == "true" {
		suite.T().Log("Deleting self-registered secret and restoring original manual secret")
		_ = fixture.DeleteClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
		suite.createOriginalSecret()
	}
}

// createOriginalSecret creates the original manually-created cluster secret
func (suite *SelfAgentRegistrationTestSuite) createOriginalSecret() {
	if suite.originalSecret == nil {
		return
	}

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suite.originalSecret.Name,
			Namespace: suite.originalSecret.Namespace,
			Labels:    suite.originalSecret.Labels,
		},
		Data: suite.originalSecret.Data,
		Type: suite.originalSecret.Type,
	}

	err := suite.PrincipalClient.Create(suite.Ctx, newSecret, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		suite.T().Logf("Error restoring original secret: %v", err)
	} else {
		suite.T().Log("Original cluster secret restored successfully")
	}
}

// Create secret manually and enable self registration, agent should connect successfully
// but self registration should skip because manually created secret already exists
func (suite *SelfAgentRegistrationTestSuite) Test_ManualSecretWithSelfRegistrationEnabled() {
	requires := suite.Require()

	// Store original secret UID
	originalSecret, err := fixture.GetClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)
	originalUID := originalSecret.UID

	// Enable self agent registration
	requires.NoError(fixture.EnableSelfAgentRegistration(suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient))
	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	// Restart agent to trigger agent connection with self-registration enabled
	fixture.RestartAgent(suite.T(), fixture.AgentManagedName)
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)

	// Verify agent connects successfully
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentManagedName, appv1.ConnectionState{
			Status:  appv1.ConnectionStatusSuccessful,
			Message: fmt.Sprintf("Agent: '%s' is %s with principal", fixture.AgentManagedName, "connected"),
		}, suite.ClusterDetails)
	}, 30*time.Second, 1*time.Second)

	// Verify manually created cluster secret still exists and has the same UID
	// This confirms self-registration was skipped
	currentSecret, err := fixture.GetClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)
	requires.Equal(originalUID, currentSecret.UID,
		"Cluster secret UID should not change - self registration should skip existing secret")

	// Verify secret doesn't have self-registered label
	requires.NotEqual("true", currentSecret.Labels[cluster.LabelKeySelfRegisteredCluster],
		"Manually created secret should not have self-registered label")
}

// Create secret manually and disable self registration, agent should connect successfully
func (suite *SelfAgentRegistrationTestSuite) Test_ManualSecretWithSelfRegistrationDisabled() {
	requires := suite.Require()

	// Store original secret UID
	originalSecret, err := fixture.GetClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)
	originalUID := originalSecret.UID

	// Restart agent to trigger agent connection
	fixture.RestartAgent(suite.T(), fixture.AgentManagedName)
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)

	// Verify agent connects successfully
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentManagedName, appv1.ConnectionState{
			Status:  appv1.ConnectionStatusSuccessful,
			Message: fmt.Sprintf("Agent: '%s' is %s with principal", fixture.AgentManagedName, "connected"),
		}, suite.ClusterDetails)
	}, 30*time.Second, 1*time.Second)

	// Verify manually created cluster secret still exists and has the same UID
	currentSecret, err := fixture.GetClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)
	requires.Equal(originalUID, currentSecret.UID,
		"Manually created cluster secret UID should not change")

	// Verify secret doesn't have self-registered label
	requires.NotEqual("true", currentSecret.Labels[cluster.LabelKeySelfRegisteredCluster],
		"Manually created secret should not have self-registered label")
}

// Enable self registration without manually created secret, self registration should register cluster successfully
func (suite *SelfAgentRegistrationTestSuite) Test_SelfRegistrationCreatesSecret() {
	requires := suite.Require()

	// Enable self agent registration
	requires.NoError(fixture.EnableSelfAgentRegistration(suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient))
	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	// Stop the agent
	err := fixture.StopProcess(fixture.AgentManagedName)
	requires.NoError(err)

	// Wait for agent to disconnect
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentManagedName, appv1.ConnectionState{
			Status:     appv1.ConnectionStatusFailed,
			Message:    fmt.Sprintf("Agent: '%s' is %s with principal", fixture.AgentManagedName, "disconnected"),
			ModifiedAt: &metav1.Time{Time: time.Now()},
		}, suite.ClusterDetails)
	}, 30*time.Second, 1*time.Second)

	// Store secret UID of manually created secret
	originalSecret, err := fixture.GetClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)
	originalUID := originalSecret.UID

	// Delete manually created cluster secret
	err = fixture.DeleteClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)

	// Wait for manually created cluster secret to be deleted
	requires.Eventually(func() bool {
		return !fixture.ClusterSecretExists(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	}, 10*time.Second, 1*time.Second)

	// Restart the agent to trigger agent connection with self-registration enabled
	fixture.RestartAgent(suite.T(), fixture.AgentManagedName)
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)

	// Verify agent connects successfully
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentManagedName, appv1.ConnectionState{
			Status:     appv1.ConnectionStatusSuccessful,
			Message:    fmt.Sprintf("Agent: '%s' is %s with principal", fixture.AgentManagedName, "connected"),
			ModifiedAt: &metav1.Time{Time: time.Now()},
		}, suite.ClusterDetails)
	}, 60*time.Second, 1*time.Second)

	// Verify secret was created
	requires.Eventually(func() bool {
		return fixture.ClusterSecretExists(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	}, 30*time.Second, 1*time.Second)

	// Verify it's a new secret and has different UID
	newSecret, err := fixture.GetClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)
	requires.NotEqual(originalUID, newSecret.UID,
		"New cluster secret should have different UID")

	// Verify self-registered label is present
	requires.NotNil(newSecret.Labels, "Secret should have labels")
	requires.Equal("true", newSecret.Labels[cluster.LabelKeySelfRegisteredCluster],
		"Self-registered secret should have self-registered label")
	requires.Equal(common.LabelValueSecretTypeCluster, newSecret.Labels[common.LabelKeySecretType],
		"Secret should have cluster type label")
	requires.Equal(fixture.AgentManagedName, newSecret.Labels[cluster.LabelKeyClusterAgentMapping],
		"Secret should have agent mapping label")
}

// Compare structure of manually created vs self-registered secrets, both should have same structure
func (suite *SelfAgentRegistrationTestSuite) Test_ManuallyCreatedAndSelfRegisteredSecrets() {
	requires := suite.Require()

	// Get manually created cluster secret structure
	manualSecret, err := fixture.GetClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)

	// Enable self registration
	requires.NoError(fixture.EnableSelfAgentRegistration(suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient))
	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	// Stop agent and delete manually created cluster secret
	err = fixture.StopProcess(fixture.AgentManagedName)
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning(fixture.AgentManagedName)
	}, 30*time.Second, 1*time.Second)

	// Delete manually created cluster secret
	err = fixture.DeleteClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)

	// Wait for manually created cluster secret to be deleted
	requires.Eventually(func() bool {
		return !fixture.ClusterSecretExists(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	}, 10*time.Second, 1*time.Second)

	// Restart agent to trigger agent connection with self-registration enabled
	fixture.RestartAgent(suite.T(), fixture.AgentManagedName)
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)

	// Verify self-registered cluster secret was created
	requires.Eventually(func() bool {
		return fixture.ClusterSecretExists(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	}, 30*time.Second, 1*time.Second)

	// Get self-registered cluster secret
	selfRegSecret, err := fixture.GetClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)

	// Compare structures, both should have same secret type and agent mapping labels
	requires.Equal(
		manualSecret.Labels[common.LabelKeySecretType],
		selfRegSecret.Labels[common.LabelKeySecretType],
		"Both secrets should have same secret type label")
	requires.Equal(
		manualSecret.Labels[cluster.LabelKeyClusterAgentMapping],
		selfRegSecret.Labels[cluster.LabelKeyClusterAgentMapping],
		"Both secrets should have same agent mapping label")

	// Both should have same data fields
	for key := range manualSecret.Data {
		requires.Contains(selfRegSecret.Data, key,
			fmt.Sprintf("Self-registered secret should have '%s' field like manual secret", key))
	}
}

// Delete manually created cluster secret and disable self registration, cluster secret should NOT be created
func (suite *SelfAgentRegistrationTestSuite) Test_NoSecretAndSelfRegistrationDisabled() {
	requires := suite.Require()

	// Disable self agent registration
	_ = fixture.DisableSelfAgentRegistration(suite.Ctx, suite.PrincipalClient)
	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	// Stop agent to trigger agent disconnection
	err := fixture.StopProcess(fixture.AgentManagedName)
	requires.NoError(err)

	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning(fixture.AgentManagedName)
	}, 30*time.Second, 1*time.Second)

	// Delete manually created cluster secret
	err = fixture.DeleteClusterSecret(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	requires.NoError(err)

	// Wait for manually created cluster secret to be deleted
	requires.Eventually(func() bool {
		return !fixture.ClusterSecretExists(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	}, 10*time.Second, 1*time.Second)

	// Restart agent to trigger agent connection with self-registration disabled
	fixture.RestartAgent(suite.T(), fixture.AgentManagedName)
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)

	// Verify the secret is never created
	requires.Never(func() bool {
		return fixture.ClusterSecretExists(suite.Ctx, suite.PrincipalClient, fixture.AgentManagedName)
	}, 30*time.Second, 1*time.Second,
		"Cluster secret should NOT be created when self-registration is disabled")
}
