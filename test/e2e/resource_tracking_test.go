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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/gitops-engine/pkg/health"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// ResourceTrackingTestSuite tests that the argocd-agent correctly identifies
// resources managed by Argo CD using different tracking methods:
// - annotation
// - label
// - annotation+label
type ResourceTrackingTestSuite struct {
	fixture.BaseSuite
}

// getTestNamespace returns a unique namespace name for the current test
func (suite *ResourceTrackingTestSuite) getTestNamespace() string {
	// Use test name to create unique namespace per test
	testName := suite.T().Name()
	// Extract just the test method name (after the last /)
	parts := []rune(testName)
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == '/' {
			testName = string(parts[i+1:])
			break
		}
	}
	// Convert to lowercase and replace underscores with hyphens
	testName = strings.ToLower(testName)
	testName = strings.ReplaceAll(testName, "_", "-")
	return testName
}

func (suite *ResourceTrackingTestSuite) SetupTest() {
	suite.BaseSuite.SetupTest()
	requires := suite.Require()

	// Create a unique namespace for this test
	namespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: suite.getTestNamespace(),
		},
	}
	err := suite.ManagedAgentClient.Create(suite.Ctx, &namespace, metav1.CreateOptions{})
	requires.NoError(err)
}

func (suite *ResourceTrackingTestSuite) TearDownTest() {
	// Clean up the namespace
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: suite.getTestNamespace(),
		},
	}
	err := fixture.EnsureDeletion(suite.Ctx, suite.ManagedAgentClient, namespace)
	if err != nil {
		suite.T().Logf("Failed to delete namespace: %v", err)
	}

	// Reset the argocd-cm ConfigMap to default tracking method
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-cm",
			Namespace: "argocd",
		},
	}
	err = fixture.EnsureUpdate(suite.Ctx, suite.ManagedAgentClient, cm, func(obj fixture.KubeObject) {
		cm := obj.(*corev1.ConfigMap)
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data["application.resourceTrackingMethod"] = "annotation"
	})
	if err != nil {
		suite.T().Logf("Failed to reset tracking method: %v", err)
	} else {
		// Wait for the ConfigMap update to be persisted and visible via API
		// The informer will pick it up shortly after this verification succeeds
		suite.T().Logf("Reset tracking method to 'annotation', waiting for update to propagate...")
		suite.Require().Eventually(func() bool {
			verifyConfig := &corev1.ConfigMap{}
			err := suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
				Name:      "argocd-cm",
				Namespace: "argocd",
			}, verifyConfig, metav1.GetOptions{})
			if err != nil {
				return false
			}
			method, ok := verifyConfig.Data["application.resourceTrackingMethod"]
			return ok && method == "annotation"
		}, 10*time.Second, 500*time.Millisecond)
	}

	suite.BaseSuite.TearDownTest()
}

// runTrackingTest is a helper function that executes the common test logic
// for different resource tracking methods
func (suite *ResourceTrackingTestSuite) runTrackingTest(
	trackingMethod string,
	appName string,
	verifyTracking func(*appsv1.Deployment),
) {
	requires := suite.Require()

	// Configure Argo CD tracking method
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-cm",
			Namespace: "argocd",
		},
	}
	err := fixture.EnsureUpdate(suite.Ctx, suite.ManagedAgentClient, cm, func(obj fixture.KubeObject) {
		cm := obj.(*corev1.ConfigMap)
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data["application.resourceTrackingMethod"] = trackingMethod
	})
	requires.NoError(err)

	// Create a managed application in the principal's cluster
	app := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appName,
			Namespace: "agent-managed",
		},
		Spec: argoapp.ApplicationSpec{
			Project: "default",
			Source: &argoapp.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "guestbook",
			},
			Destination: argoapp.ApplicationDestination{
				Name:      "agent-managed",
				Namespace: suite.getTestNamespace(),
			},
			SyncPolicy: &argoapp.SyncPolicy{
				Automated: &argoapp.SyncPolicyAutomated{
					Prune:    true,
					SelfHeal: true,
				},
			},
		},
	}
	err = suite.PrincipalClient.Create(suite.Ctx, &app, metav1.CreateOptions{})
	requires.NoError(err)

	// Register cleanup immediately to ensure it runs even if test fails early
	suite.T().Cleanup(func() {
		// Clean up application from principal
		if err := fixture.EnsureDeletion(suite.Ctx, suite.PrincipalClient, &app); err != nil {
			suite.T().Logf("Failed to delete application from principal: %v", err)
		}

		// Wait for application to be deleted from managed-agent
		agentApp := &argoapp.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      app.Name,
				Namespace: "argocd",
			},
		}
		if err := fixture.WaitForDeletion(suite.Ctx, suite.ManagedAgentClient, agentApp); err != nil {
			suite.T().Logf("Failed to wait for application deletion from managed-agent: %v", err)
		}
	})

	agentKey := types.NamespacedName{Name: app.Name, Namespace: "argocd"}

	// Ensure the app has been pushed to the managed-agent
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, agentKey, &app, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)

	// Wait for the app to sync and deploy resources
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, agentKey, &app, metav1.GetOptions{})
		return err == nil && app.Status.Sync.Status == argoapp.SyncStatusCodeSynced
	}, 120*time.Second, 2*time.Second)

	// Verify that the deployed resources have the correct tracking
	deployment := &appsv1.Deployment{}
	err = suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
		Name:      "guestbook-ui",
		Namespace: suite.getTestNamespace(),
	}, deployment, metav1.GetOptions{})
	requires.NoError(err)

	// Run tracking-specific verification
	verifyTracking(deployment)

	// Test that resource proxy can fetch the resource
	argoEndpoint, err := fixture.GetArgoCDServerEndpoint(suite.PrincipalClient)
	requires.NoError(err)

	password, err := fixture.GetInitialAdminSecret(suite.PrincipalClient)
	requires.NoError(err)

	argoClient := fixture.NewArgoClient(argoEndpoint, "admin", password)
	err = argoClient.Login()
	requires.NoError(err)

	// Wait for the app to be healthy AND for Argo CD to discover the guestbook-ui deployment
	// The resource must appear in the Application's status before the resource proxy can fetch it
	var principalApp argoapp.Application
	requires.Eventually(func() bool {
		err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{
			Name:      appName,
			Namespace: "agent-managed",
		}, &principalApp, metav1.GetOptions{})
		if err != nil || principalApp.Status.Health.Status != health.HealthStatusHealthy {
			return false
		}
		// Check if the guestbook-ui deployment is in the managed resources list
		for _, res := range principalApp.Status.Resources {
			if res.Kind == "Deployment" && res.Name == "guestbook-ui" && res.Namespace == suite.getTestNamespace() {
				return true
			}
		}
		return false
	}, 120*time.Second, 2*time.Second)

	// Verify that the resource proxy can fetch the tracked resource
	resource, err := argoClient.GetResource(&principalApp,
		"apps", "v1", "Deployment", suite.getTestNamespace(), "guestbook-ui")
	requires.NoError(err, "Resource proxy should be able to fetch resource with %s tracking", trackingMethod)

	// Verify the fetched resource is correct
	fetchedDeployment := &appsv1.Deployment{}
	err = json.Unmarshal([]byte(resource), fetchedDeployment)
	requires.NoError(err)
	requires.Equal("guestbook-ui", fetchedDeployment.Name)
	requires.Equal(suite.getTestNamespace(), fetchedDeployment.Namespace)
}

// Test_ResourceTracking_Annotation tests that resources are correctly identified
// when using annotation-only tracking method
func (suite *ResourceTrackingTestSuite) Test_ResourceTracking_Annotation() {
	suite.runTrackingTest("annotation", "tracking-annotation-test", func(deployment *appsv1.Deployment) {
		requires := suite.Require()
		trackingAnnotation := "argocd.argoproj.io/tracking-id"
		requires.Contains(deployment.Annotations, trackingAnnotation, "Deployment should have tracking annotation")
		requires.NotEmpty(deployment.Annotations[trackingAnnotation], "Tracking annotation should not be empty")
	})
}

// Test_ResourceTracking_Label tests that resources are correctly identified
// when using label-only tracking method
func (suite *ResourceTrackingTestSuite) Test_ResourceTracking_Label() {
	suite.runTrackingTest("label", "tracking-label-test", func(deployment *appsv1.Deployment) {
		requires := suite.Require()
		trackingLabel := "app.kubernetes.io/instance"
		requires.Contains(deployment.Labels, trackingLabel, "Deployment should have tracking label")
		requires.NotEmpty(deployment.Labels[trackingLabel], "Tracking label should not be empty")
	})
}

// Test_ResourceTracking_AnnotationAndLabel tests that resources are correctly identified
// when using both annotation and label tracking method
func (suite *ResourceTrackingTestSuite) Test_ResourceTracking_AnnotationAndLabel() {
	suite.runTrackingTest("annotation+label", "tracking-both-test", func(deployment *appsv1.Deployment) {
		requires := suite.Require()

		// Check for the tracking annotation
		trackingAnnotation := "argocd.argoproj.io/tracking-id"
		requires.Contains(deployment.Annotations, trackingAnnotation, "Deployment should have tracking annotation")
		requires.NotEmpty(deployment.Annotations[trackingAnnotation], "Tracking annotation should not be empty")

		// Check for the tracking label
		trackingLabel := "app.kubernetes.io/instance"
		requires.Contains(deployment.Labels, trackingLabel, "Deployment should have tracking label")
		requires.NotEmpty(deployment.Labels[trackingLabel], "Tracking label should not be empty")
	})
}

func TestResourceTrackingTestSuite(t *testing.T) {
	suite.Run(t, new(ResourceTrackingTestSuite))
}
