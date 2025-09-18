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
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type ClusterInfoTestSuite struct {
	fixture.BaseSuite
}

func (suite *ClusterInfoTestSuite) TearDownTest() {
	suite.BaseSuite.TearDownTest()
}

func TestClusterTestSuite(t *testing.T) {
	suite.Run(t, new(ClusterInfoTestSuite))
}

func (suite *ClusterInfoTestSuite) SetupTest() {
	if !fixture.IsProcessRunning(fixture.PrincipalName) {
		// Start the principal if it is not running and wait for it to be ready
		suite.Require().NoError(fixture.StartProcess(fixture.PrincipalName))
		fixture.CheckReadiness(suite.T(), fixture.PrincipalName)
	} else {
		// If principal is already running, verify that it is ready
		fixture.CheckReadiness(suite.T(), fixture.PrincipalName)
	}

	if !fixture.IsProcessRunning(fixture.AgentManagedName) {
		// Start the agent if it is not running and wait for it to be ready
		suite.Require().NoError(fixture.StartProcess(fixture.AgentManagedName))
		fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)
	} else {
		// If agent is already running, verify that it is ready
		fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)
	}

	if !fixture.IsProcessRunning(fixture.AgentAutonomousName) {
		// Start the agent if it is not running and wait for it to be ready
		suite.Require().NoError(fixture.StartProcess(fixture.AgentAutonomousName))
		fixture.CheckReadiness(suite.T(), fixture.AgentAutonomousName)
	} else {
		// If agent is already running, verify that it is ready
		fixture.CheckReadiness(suite.T(), fixture.AgentAutonomousName)
	}

	suite.BaseSuite.SetupTest()
}

var message = "Agent: '%s' is %s with principal"

func (suite *ClusterInfoTestSuite) Test_ClusterInfo_Managed() {
	requires := suite.Require()

	clusterDetail := suite.ClusterDetails

	// Verify that connection status has been updated when agent is already connected
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentManagedName, appv1.ConnectionState{
			Status:  appv1.ConnectionStatusSuccessful,
			Message: fmt.Sprintf(message, fixture.AgentManagedName, "connected"),
		}, clusterDetail)
	}, 30*time.Second, 1*time.Second)

	// Stop the agent
	err := fixture.StopProcess(fixture.AgentManagedName)
	requires.NoError(err)

	// Verify that connection status is updated when agent is disconnected
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentManagedName, appv1.ConnectionState{
			Status:     appv1.ConnectionStatusFailed,
			Message:    fmt.Sprintf(message, fixture.AgentManagedName, "disconnected"),
			ModifiedAt: &metav1.Time{Time: time.Now()},
		}, clusterDetail)
	}, 30*time.Second, 1*time.Second)

	// Restart the agent
	err = fixture.StartProcess(fixture.AgentManagedName)
	requires.NoError(err)
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)

	// Verify that connection status is updated again when agent is re-connected
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentManagedName, appv1.ConnectionState{
			Status:     appv1.ConnectionStatusSuccessful,
			Message:    fmt.Sprintf(message, fixture.AgentManagedName, "connected"),
			ModifiedAt: &metav1.Time{Time: time.Now()},
		}, clusterDetail)
	}, 30*time.Second, 1*time.Second)
}

func (suite *ClusterInfoTestSuite) Test_ClusterInfo_Autonomous() {
	requires := suite.Require()
	clusterDetail := suite.ClusterDetails

	// Verify the connection status is updated when agent is already connected
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentAutonomousName, appv1.ConnectionState{
			Status:  appv1.ConnectionStatusSuccessful,
			Message: fmt.Sprintf(message, fixture.AgentAutonomousName, "connected"),
		}, clusterDetail)
	}, 30*time.Second, 1*time.Second)

	// Stop the agent
	err := fixture.StopProcess(fixture.AgentAutonomousName)
	requires.NoError(err)

	// Verify that connection status is updated when agent is disconnected
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentAutonomousName, appv1.ConnectionState{
			Status:     appv1.ConnectionStatusFailed,
			Message:    fmt.Sprintf(message, fixture.AgentAutonomousName, "disconnected"),
			ModifiedAt: &metav1.Time{Time: time.Now()},
		}, clusterDetail)
	}, 30*time.Second, 1*time.Second)

	// Restart the agent
	err = fixture.StartProcess(fixture.AgentAutonomousName)
	requires.NoError(err)
	fixture.CheckReadiness(suite.T(), fixture.AgentAutonomousName)

	// Verify that connection status is updated again when agent is re-connected
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentAutonomousName, appv1.ConnectionState{
			Status:     appv1.ConnectionStatusSuccessful,
			Message:    fmt.Sprintf(message, fixture.AgentAutonomousName, "connected"),
			ModifiedAt: &metav1.Time{Time: time.Now()},
		}, clusterDetail)
	}, 30*time.Second, 1*time.Second)
}

// This test suite validates the cluster cache info reporting for managed agent.
// It checks that the cluster cache info is correctly propagated from agent to principal.
func (suite *ClusterInfoTestSuite) Test_ClusterCacheInfo() {
	requires := suite.Require()
	clusterDetail := suite.ClusterDetails

	// We need to know the number of existing applications before running test
	agentCacheInfo, err := fixture.GetManagedAgentClusterInfo(clusterDetail)
	requires.NoError(err)
	appCountBefore := agentCacheInfo.ApplicationsCount

	// Step 1:
	// Create the first application in the principal cluster and validate deployment to managed cluster
	appFirst := createApp(suite.Ctx, suite.PrincipalClient, requires)
	appFirst = validateManagedAppCreated(suite.Ctx, suite.ManagedAgentClient, suite.PrincipalClient,
		fixture.ToNamespacedName(&appFirst), types.NamespacedName{Name: appFirst.Name, Namespace: "argocd"}, requires)

	// Step 2:
	// Verify that cluster cache info has been updated for first application in agent cluster by Argo CD
	// and then agent updated principal with this information
	requires.Eventually(func() bool {
		return fixture.HasApplicationsCount(appCountBefore+1, clusterDetail)
	}, 90*time.Second, 5*time.Second)

	requires.Eventually(func() bool {
		return fixture.HasClusterCacheInfoSynced(fixture.AgentManagedName, clusterDetail)
	}, 90*time.Second, 5*time.Second)

	// Step 3:
	// Create the second application having different name and namespace, also validate deployment to managed cluster
	appSecond := createApp(suite.Ctx, suite.PrincipalClient, requires, struct{ Name, Namespace string }{Name: "guestbook1", Namespace: "guestbook1"})
	appSecond = validateManagedAppCreated(suite.Ctx, suite.ManagedAgentClient, suite.PrincipalClient,
		fixture.ToNamespacedName(&appSecond), types.NamespacedName{Name: appSecond.Name, Namespace: "argocd"}, requires)

	// Step 4:
	// Verify that cluster cache info has been updated by Argo CD for second application in agent cluster
	// and then agent again updated principal with this new information
	requires.Eventually(func() bool {
		return fixture.HasApplicationsCount(appCountBefore+2, clusterDetail)
	}, 90*time.Second, 5*time.Second)

	requires.Eventually(func() bool {
		return fixture.HasClusterCacheInfoSynced(fixture.AgentManagedName, clusterDetail)
	}, 90*time.Second, 5*time.Second)

	// Step 5:
	// Delete the first application from the principal cluster and validate that it is removed from managed cluster
	err = suite.PrincipalClient.Delete(suite.Ctx, &appFirst, metav1.DeleteOptions{})
	requires.NoError(err)
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		return errors.IsNotFound(suite.ManagedAgentClient.Get(suite.Ctx,
			types.NamespacedName{Name: appFirst.Name, Namespace: "argocd"}, &app, metav1.GetOptions{}))
	}, 60*time.Second, 2*time.Second)

	// Step 6:
	// Verify that cluster cache info has been updated again by Argo CD after deleting the first application
	// and then agent again updated principal with this new information
	requires.Eventually(func() bool {
		return fixture.HasApplicationsCount(appCountBefore+1, clusterDetail)
	}, 90*time.Second, 5*time.Second)

	requires.Eventually(func() bool {
		return fixture.HasClusterCacheInfoSynced(fixture.AgentManagedName, clusterDetail)
	}, 90*time.Second, 5*time.Second)

	// Step 7:
	// Verify that existing agent connection status is preserved when cluster cache info is updated
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentManagedName, appv1.ConnectionState{
			Status:  appv1.ConnectionStatusSuccessful,
			Message: fmt.Sprintf(message, fixture.AgentManagedName, "connected"),
		}, clusterDetail)
	}, 30*time.Second, 1*time.Second)

	// Step 8:
	// Disconnect agent and verify that connection status is changed to Failed
	requires.NoError(fixture.StopProcess(fixture.AgentManagedName))
	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentManagedName, appv1.ConnectionState{
			Status:     appv1.ConnectionStatusFailed,
			Message:    fmt.Sprintf(message, fixture.AgentManagedName, "disconnected"),
			ModifiedAt: &metav1.Time{Time: time.Now()},
		}, clusterDetail)
	}, 60*time.Second, 2*time.Second)

	// Step 9:
	// Since agent is disconnected, cluster cache info should reset to default
	requires.Eventually(func() bool {
		ci, err := fixture.GetPrincipalClusterInfo(fixture.AgentManagedName, clusterDetail)
		if err != nil {
			return false
		}
		return ci.ApplicationsCount == 0 && ci.CacheInfo.APIsCount == 0 && ci.CacheInfo.ResourcesCount == 0
	}, 90*time.Second, 5*time.Second)

	// Step 10:
	// Reconnect agent and verify that connection status and cluster cache info are updated again with correct values
	requires.NoError(fixture.StartProcess(fixture.AgentManagedName))
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)

	requires.Eventually(func() bool {
		return fixture.HasConnectionStatus(fixture.AgentManagedName, appv1.ConnectionState{
			Status:     appv1.ConnectionStatusSuccessful,
			Message:    fmt.Sprintf(message, fixture.AgentManagedName, "connected"),
			ModifiedAt: &metav1.Time{Time: time.Now()},
		}, clusterDetail)
	}, 60*time.Second, 2*time.Second)

	requires.Eventually(func() bool {
		return fixture.HasClusterCacheInfoSynced(fixture.AgentManagedName, clusterDetail)
	}, 90*time.Second, 5*time.Second)
}
