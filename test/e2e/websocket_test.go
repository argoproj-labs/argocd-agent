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
	"os"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	"github.com/argoproj-labs/argocd-agent/test/proxy"
	"github.com/stretchr/testify/suite"
	"k8s.io/client-go/kubernetes"
)

type HTTP1DowngradeTestSuite struct {
	fixture.BaseSuite
}

func (suite *HTTP1DowngradeTestSuite) TearDownTest() {
	suite.BaseSuite.TearDownTest()
	requires := suite.Require()

	if _, err := os.Stat(fixture.EnvVariablesFromE2EFile); err == nil {
		requires.NoError(os.Remove(fixture.EnvVariablesFromE2EFile))
		fixture.RestartAgent(suite.T(), "agent-managed")
		fixture.RestartAgent(suite.T(), "agent-autonomous")
	}

	// Ensure that all the components are running after runnings the tests
	if !fixture.IsProcessRunning("process") {
		err := fixture.StartProcess("principal")
		requires.NoError(err)
		fixture.CheckReadiness(suite.T(), "principal")
	}

	if !fixture.IsProcessRunning("agent-managed") {
		err := fixture.StartProcess("agent-managed")
		requires.NoError(err)
		fixture.CheckReadiness(suite.T(), "agent-managed")
	}

	if !fixture.IsProcessRunning("agent-autonomous") {
		err := fixture.StartProcess("agent-autonomous")
		requires.NoError(err)
		fixture.CheckReadiness(suite.T(), "agent-autonomous")
	}
}

func (suite *HTTP1DowngradeTestSuite) Test_WithHTTP1Downgrade() {
	requires := suite.Require()

	// Verify that the agent has connected to the principal
	fixture.CheckReadiness(suite.T(), "agent-managed")

	// Create a reverse proxy that downgrades the incoming requests to HTTP/1.1
	hostPort := func(host string, port int) string {
		return fmt.Sprintf("%s:%d", host, port)
	}

	principalClient, err := kubernetes.NewForConfig(suite.PrincipalClient.Config)
	requires.NoError(err)

	agentClient, err := kubernetes.NewForConfig(suite.ManagedAgentClient.Config)
	requires.NoError(err)

	ctx := context.Background()

	// Load the principal's certificate for incoming connections (agent → proxy)
	principalServerCert, err := tlsutil.TLSCertFromSecret(ctx, principalClient, "argocd", config.SecretNamePrincipalTLS)
	requires.NoError(err)

	// Load the agent's client certificate for outgoing connections (proxy → principal)
	agentClientCert, err := tlsutil.TLSCertFromSecret(ctx, agentClient, "argocd", config.SecretNameAgentClientCert)
	requires.NoError(err)

	proxyPort := 9091
	principalPort := 8443

	// Configure the proxy with TLS certificates
	http1Proxy := proxy.StartHTTP2DowngradingProxy(suite.T(), hostPort("", proxyPort), hostPort("", principalPort), agentClientCert, principalServerCert)
	defer http1Proxy.Close()

	// The agent must connect to the principal via the proxy and explicitly disable WebSocket
	envVar := fmt.Sprintf(`ARGOCD_AGENT_REMOTE_PORT=%d
ARGOCD_AGENT_ENABLE_WEBSOCKET=false
ARGOCD_PRINCIPAL_ENABLE_WEBSOCKET=false`, proxyPort)
	err = os.WriteFile(fixture.EnvVariablesFromE2EFile, []byte(envVar+"\n"), 0644)
	requires.NoError(err)

	defer func() {
		if err := os.Remove(fixture.EnvVariablesFromE2EFile); err != nil {
			suite.T().Errorf("failed to remove env file: %v", err)
		}

		// Restart the agent process
		fixture.RestartAgent(suite.T(), "agent-managed")

		// Give some time for the agent to be ready
		fixture.CheckReadiness(suite.T(), "agent-managed")

		// Restart the principal process
		fixture.RestartAgent(suite.T(), "principal")

		// Give some time for the principal to be ready
		fixture.CheckReadiness(suite.T(), "principal")
	}()

	// Restart the agent process
	fixture.RestartAgent(suite.T(), "agent-managed")

	// Agent should not be able to connect to the principal when the Websocket is disabled because the
	// proxy downgrades the requests to HTTP/1.1
	fixture.IsNotReady(suite.T(), "agent-managed")

	// Restart the principal and the agent with the Websocket enabled
	envVar = fmt.Sprintf(`ARGOCD_AGENT_REMOTE_PORT=%d
ARGOCD_AGENT_ENABLE_WEBSOCKET=true
ARGOCD_PRINCIPAL_ENABLE_WEBSOCKET=true`, proxyPort)
	err = os.WriteFile(fixture.EnvVariablesFromE2EFile, []byte(envVar+"\n"), 0644)
	requires.NoError(err)

	fixture.RestartAgent(suite.T(), "principal")

	fixture.RestartAgent(suite.T(), "agent-managed")

	// Give some time for the principal to be ready
	fixture.CheckReadiness(suite.T(), "principal")

	// Give some time for the agent to be ready
	fixture.CheckReadiness(suite.T(), "agent-managed")
}

func TestHTTP1DowngradeTestSuite(t *testing.T) {
	suite.Run(t, new(HTTP1DowngradeTestSuite))
}
