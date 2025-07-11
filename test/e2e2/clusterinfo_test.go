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
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e2/fixture"
	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ClusterTestSuite struct {
	fixture.BaseSuite
}

func (suite *ClusterTestSuite) TearDownTest() {
	suite.BaseSuite.TearDownTest()
}

var message = "Agent: '%s' is %s with principal"

func (suite *ClusterTestSuite) Test_ClusterInfo_Managed() {
	agentName := "agent-managed"
	requires := suite.Require()

	// Verify that connection status has been updated when agent is already connected
	requires.Eventually(func() bool {
		return fixture.HasConnectionInfo(os.Getenv(fixture.ManagedAgentServerKey), appv1.ConnectionState{
			Status:  appv1.ConnectionStatusSuccessful,
			Message: fmt.Sprintf(message, agentName, "connected"),
		})
	}, 30*time.Second, 1*time.Second)

	// Stop the agent
	err := fixture.StopProcess(agentName)
	requires.NoError(err)

	// Verify that connection status is updated when agent is disconnected
	requires.Eventually(func() bool {
		return fixture.HasConnectionInfo(os.Getenv(fixture.ManagedAgentServerKey), appv1.ConnectionState{
			Status:     appv1.ConnectionStatusFailed,
			Message:    fmt.Sprintf(message, agentName, "disconnected"),
			ModifiedAt: &metav1.Time{Time: time.Now()},
		})
	}, 30*time.Second, 1*time.Second)

	// Restart the agent
	err = fixture.StartProcess(agentName)
	requires.NoError(err)

	// Verify that connection status is updated again when agent is re-connected
	requires.Eventually(func() bool {
		return fixture.HasConnectionInfo(os.Getenv(fixture.ManagedAgentServerKey), appv1.ConnectionState{
			Status:     appv1.ConnectionStatusSuccessful,
			Message:    fmt.Sprintf(message, agentName, "connected"),
			ModifiedAt: &metav1.Time{Time: time.Now()},
		})
	}, 30*time.Second, 1*time.Second)
}

func (suite *ClusterTestSuite) Test_ClusterInfo_Autonomous() {
	agentName := "agent-autonomous"
	requires := suite.Require()

	// Verify the connection status is updated when agent is already connected
	requires.Eventually(func() bool {
		return fixture.HasConnectionInfo(os.Getenv(fixture.AutonomousAgentServerKey), appv1.ConnectionState{
			Status:  appv1.ConnectionStatusSuccessful,
			Message: fmt.Sprintf(message, agentName, "connected"),
		})
	}, 30*time.Second, 1*time.Second)

	// Stop the agent
	err := fixture.StopProcess(agentName)
	requires.NoError(err)

	// Verify that connection status is updated when agent is disconnected
	requires.Eventually(func() bool {
		return fixture.HasConnectionInfo(os.Getenv(fixture.AutonomousAgentServerKey), appv1.ConnectionState{
			Status:     appv1.ConnectionStatusFailed,
			Message:    fmt.Sprintf(message, agentName, "disconnected"),
			ModifiedAt: &metav1.Time{Time: time.Now()},
		})
	}, 30*time.Second, 1*time.Second)

	// Restart the agent
	err = fixture.StartProcess(agentName)
	requires.NoError(err)

	// Verify that connection status is updated again when agent is re-connected
	requires.Eventually(func() bool {
		return fixture.HasConnectionInfo(os.Getenv(fixture.AutonomousAgentServerKey), appv1.ConnectionState{
			Status:     appv1.ConnectionStatusSuccessful,
			Message:    fmt.Sprintf(message, agentName, "connected"),
			ModifiedAt: &metav1.Time{Time: time.Now()},
		})
	}, 30*time.Second, 1*time.Second)
}

func TestClusterTestSuite(t *testing.T) {
	suite.Run(t, new(ClusterTestSuite))
}
