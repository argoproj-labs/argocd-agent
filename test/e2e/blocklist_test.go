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
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type BlocklistTestSuite struct {
	fixture.BaseSuite
}

func TestBlocklistTestSuite(t *testing.T) {
	suite.Run(t, new(BlocklistTestSuite))
}

func (suite *BlocklistTestSuite) SetupTest() {
	for _, proc := range []string{fixture.PrincipalName, fixture.AgentManagedName, fixture.AgentAutonomousName} {
		if !fixture.IsProcessRunning(proc) {
			suite.Require().NoError(fixture.StartProcess(proc))
		}
		fixture.CheckReadiness(suite.T(), proc)
	}
	suite.deleteBlocklistConfigMap()
	suite.BaseSuite.SetupTest()
}

func (suite *BlocklistTestSuite) TearDownTest() {
	suite.deleteBlocklistConfigMap()
	suite.BaseSuite.TearDownTest()
}

func (suite *BlocklistTestSuite) Test_BlocklistDisconnectsManagedAgent() {
	suite.runBlocklistTest(fixture.AgentManagedName, fixture.ManagedAgentNamespace, suite.ManagedAgentClient)
}

func (suite *BlocklistTestSuite) Test_BlocklistDisconnectsAutonomousAgent() {
	suite.runBlocklistTest(fixture.AgentAutonomousName, fixture.AutonomousAgentNamespace, suite.AutonomousAgentClient)
}

// Test_BlocklistPrincipalRestart verifies that a blocklisted agent
// remains blocked after the principal is restarted, and that removing the
// fingerprint after restart allows the agent to reconnect.
func (suite *BlocklistTestSuite) Test_BlocklistPrincipalRestart() {
	requires := suite.Require()
	t := suite.T()

	agentName := fixture.AgentManagedName
	connectedMsg := fmt.Sprintf("Agent: '%s' is connected with principal", agentName)
	disconnectedMsg := fmt.Sprintf("Agent: '%s' is disconnected with principal", agentName)

	// Verify the agent is connected
	t.Log("Verifying agent is connected")
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(agentName, appv1.ConnectionState{
			Status:  appv1.ConnectionStatusSuccessful,
			Message: connectedMsg,
		}, suite.ClusterDetails)
	}, 60*time.Second, 3*time.Second)

	// Add the agent's fingerprint to the blocklist
	fingerprint := suite.getAgentFingerprint("argocd-agent-client-tls", fixture.ManagedAgentNamespace, suite.ManagedAgentClient)
	t.Logf("Agent fingerprint: %s", fingerprint)
	suite.saveBlocklistConfigMap([]string{fingerprint})

	// Verify the agent is disconnected
	t.Log("Verifying agent is disconnected")
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(agentName, appv1.ConnectionState{
			Status:  appv1.ConnectionStatusFailed,
			Message: disconnectedMsg,
		}, suite.ClusterDetails)
	}, 120*time.Second, 3*time.Second)

	// Restart the principal while the blocklist ConfigMap still exists
	t.Log("Restarting principal")
	requires.NoError(fixture.StopProcess(fixture.PrincipalName))
	requires.Eventually(func() bool {
		return !fixture.IsProcessRunning(fixture.PrincipalName)
	}, 30*time.Second, 1*time.Second)
	requires.NoError(fixture.StartProcess(fixture.PrincipalName))
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	// The agent should still be blocked after principal restart
	t.Log("Verifying agent remains blocked after principal restart")
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(agentName, appv1.ConnectionState{
			Status:  appv1.ConnectionStatusFailed,
			Message: disconnectedMsg,
		}, suite.ClusterDetails)
	}, 120*time.Second, 3*time.Second)

	// Remove the fingerprint from the blocklist
	t.Log("Removing fingerprint from blocklist")
	suite.saveBlocklistConfigMap([]string{})

	// Verify the agent reconnects
	t.Log("Verifying agent reconnects")
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(agentName, appv1.ConnectionState{
			Status:  appv1.ConnectionStatusSuccessful,
			Message: connectedMsg,
		}, suite.ClusterDetails)
	}, 120*time.Second, 3*time.Second)
}

// runBlocklistTest verifies that adding an agent's certificate fingerprint to
// the blocklist ConfigMap disconnects the agent, and removing it allows the
// agent to reconnect.
func (suite *BlocklistTestSuite) runBlocklistTest(agentName, agentNamespace string, agentClient fixture.KubeClient) {
	requires := suite.Require()
	t := suite.T()

	connectedMsg := fmt.Sprintf("Agent: '%s' is connected with principal", agentName)
	disconnectedMsg := fmt.Sprintf("Agent: '%s' is disconnected with principal", agentName)

	// Verify the agent is connected
	t.Logf("Verifying %s is connected", agentName)
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(agentName, appv1.ConnectionState{
			Status:  appv1.ConnectionStatusSuccessful,
			Message: connectedMsg,
		}, suite.ClusterDetails)
	}, 60*time.Second, 3*time.Second)

	// Get the agent's client certificate fingerprint
	fingerprint := suite.getAgentFingerprint("argocd-agent-client-tls", agentNamespace, agentClient)
	t.Logf("%s fingerprint: %s", agentName, fingerprint)
	requires.NotEmpty(fingerprint)

	// Add the fingerprint to the blocklist ConfigMap on the principal
	t.Logf("Adding %s fingerprint to blocklist", agentName)
	suite.saveBlocklistConfigMap([]string{fingerprint})

	// Verify the agent is disconnected
	t.Logf("Verifying %s is disconnected", agentName)
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(agentName, appv1.ConnectionState{
			Status:  appv1.ConnectionStatusFailed,
			Message: disconnectedMsg,
		}, suite.ClusterDetails)
	}, 120*time.Second, 3*time.Second)

	// Remove the fingerprint from the blocklist
	t.Logf("Removing %s fingerprint from blocklist", agentName)
	suite.saveBlocklistConfigMap([]string{})

	// Verify the agent reconnects
	t.Logf("Verifying %s reconnects", agentName)
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(agentName, appv1.ConnectionState{
			Status:  appv1.ConnectionStatusSuccessful,
			Message: connectedMsg,
		}, suite.ClusterDetails)
	}, 120*time.Second, 3*time.Second)
}

// getAgentFingerprint reads the agent's client TLS certificate from its
// Kubernetes secret and returns the SHA-256 fingerprint.
func (suite *BlocklistTestSuite) getAgentFingerprint(secretName, namespace string, client fixture.KubeClient) string {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: secretName, Namespace: namespace}
	err := client.Get(suite.Ctx, key, secret, metav1.GetOptions{})
	suite.Require().NoError(err)

	certPEM, ok := secret.Data["tls.crt"]
	suite.Require().True(ok, "tls.crt not found in secret %s/%s", namespace, secretName)

	block, _ := pem.Decode(certPEM)
	suite.Require().NotNil(block, "failed to decode PEM from secret %s/%s", namespace, secretName)

	cert, err := x509.ParseCertificate(block.Bytes)
	suite.Require().NoError(err)

	return tlsutil.CertificateFingerprint(cert)
}

// saveBlocklistConfigMap creates or updates the blocklist ConfigMap on the
// principal cluster with the given fingerprints.
func (suite *BlocklistTestSuite) saveBlocklistConfigMap(fingerprints []string) {
	data, err := json.Marshal(fingerprints)
	suite.Require().NoError(err)

	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Name: config.ConfigMapNameTLSBlocklist, Namespace: fixture.PrincipalNamespace}
	err = suite.PrincipalClient.Get(suite.Ctx, key, cm, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.ConfigMapNameTLSBlocklist,
				Namespace: fixture.PrincipalNamespace,
			},
			Data: map[string]string{
				config.ConfigMapKeyBlocklistChecksums: string(data),
			},
		}
		suite.Require().NoError(suite.PrincipalClient.Create(suite.Ctx, cm, metav1.CreateOptions{}))
		return
	}
	suite.Require().NoError(err)

	cm.Data[config.ConfigMapKeyBlocklistChecksums] = string(data)
	suite.Require().NoError(suite.PrincipalClient.Update(suite.Ctx, cm, metav1.UpdateOptions{}))
}

// deleteBlocklistConfigMap removes the blocklist ConfigMap from the principal
// cluster to clean up after tests.
func (suite *BlocklistTestSuite) deleteBlocklistConfigMap() {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.ConfigMapNameTLSBlocklist,
			Namespace: fixture.PrincipalNamespace,
		},
	}
	err := suite.PrincipalClient.Delete(suite.Ctx, cm, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		suite.Require().NoError(err)
	}
}
