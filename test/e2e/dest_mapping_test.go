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
	"os"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/gitops-engine/pkg/health"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Constants used across destination mapping tests
const (
	destMapAgentName           = "agent-managed"
	destMapAgentNamespace      = "agent-managed"
	destMapExampleAppsRepo     = "https://github.com/argoproj/argocd-example-apps"
	destMapGuestbookPath       = "kustomize-guestbook"
	destMapGuestbookUIDepl     = "kustomize-guestbook-ui"
	destMapCreateNsOption      = "CreateNamespace=true"
	destMapGuestbookNs         = "guestbook"
	autonomousDestMapAgentName = "agent-autonomous"
)

var (
	argoClient *fixture.ArgoRestClient
	endpoint   string
	password   string
)

type DestinationMappingTestSuite struct {
	fixture.BaseSuite

	// Original state to restore in TearDownSuite
	origSourceNamespaces        []string
	origAppNamespacesConfigured bool
	origAppNamespacesValue      string
}

// SetupSuite runs before the tests in the suite are run.
func (suite *DestinationMappingTestSuite) SetupSuite() {
	suite.BaseSuite.SetupSuite()
	var err error

	requires := suite.Require()
	endpoint, err = fixture.GetArgoCDServerEndpoint(suite.PrincipalClient)
	requires.NoError(err)

	password, err = fixture.GetInitialAdminSecret(suite.PrincipalClient)
	requires.NoError(err)

	argoClient = fixture.NewArgoClient(endpoint, "admin", password)
	err = argoClient.Login()
	requires.NoError(err)

	envVar := fmt.Sprintf(`ARGOCD_AGENT_DESTINATION_BASED_MAPPING=true
ARGOCD_AGENT_CREATE_NAMESPACE=true
ARGOCD_PRINCIPAL_DESTINATION_BASED_MAPPING=true`)
	err = os.WriteFile(fixture.EnvVariablesFromE2EFile, []byte(envVar+"\n"), 0644)
	requires.NoError(err)

	fixture.RestartAgent(suite.T(), "agent-managed")
	fixture.CheckReadiness(suite.T(), "agent-managed")

	fixture.RestartAgent(suite.T(), "principal")
	fixture.CheckReadiness(suite.T(), "principal")

	// Capture original AppProject SourceNamespaces before modifying
	defaultAppProject := &v1alpha1.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "argocd",
		},
	}
	err = suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
		Name:      "default",
		Namespace: "argocd",
	}, defaultAppProject, metav1.GetOptions{})
	requires.NoError(err, "failed to read original AppProject state")
	suite.origSourceNamespaces = defaultAppProject.Spec.SourceNamespaces

	// Capture original ConfigMap state before modifying
	origConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-cmd-params-cm",
			Namespace: "argocd",
		},
	}
	err = suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
		Name:      "argocd-cmd-params-cm",
		Namespace: "argocd",
	}, origConfigMap, metav1.GetOptions{})
	requires.NoError(err, "failed to read original ConfigMap state")
	if origConfigMap.Data != nil {
		if val, ok := origConfigMap.Data["application.namespaces"]; ok {
			suite.origAppNamespacesConfigured = true
			suite.origAppNamespacesValue = val
		}
	}

	// Enable apps in any namespace on the agent
	err = fixture.EnsureUpdate(suite.Ctx, suite.ManagedAgentClient, defaultAppProject, func(obj fixture.KubeObject) {
		appProject := obj.(*v1alpha1.AppProject)
		appProject.Spec.SourceNamespaces = []string{"*"}
	})
	requires.NoError(err)

	// Update argocd-cmd-params-cm to allow applications in any namespace
	acm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-cmd-params-cm",
			Namespace: "argocd",
		},
	}
	err = fixture.EnsureUpdate(suite.Ctx, suite.ManagedAgentClient, acm, func(obj fixture.KubeObject) {
		configMap := obj.(*corev1.ConfigMap)
		if configMap.Data == nil {
			configMap.Data = map[string]string{}
		}
		configMap.Data["application.namespaces"] = "*"
	})
	requires.NoError(err)

	suite.restartApplicationController()
}

func (suite *DestinationMappingTestSuite) TearDownSuite() {
	suite.BaseSuite.TearDownTest()
	requires := suite.Require()

	if _, err := os.Stat(fixture.EnvVariablesFromE2EFile); err == nil {
		requires.NoError(os.Remove(fixture.EnvVariablesFromE2EFile))
	}

	fixture.RestartAgent(suite.T(), "agent-managed")
	fixture.CheckReadiness(suite.T(), "agent-managed")

	fixture.RestartAgent(suite.T(), "principal")
	fixture.CheckReadiness(suite.T(), "principal")

	// Restore original AppProject SourceNamespaces
	defaultAppProject := &v1alpha1.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "argocd",
		},
	}
	err := fixture.EnsureUpdate(suite.Ctx, suite.ManagedAgentClient, defaultAppProject, func(obj fixture.KubeObject) {
		appProject := obj.(*v1alpha1.AppProject)
		appProject.Spec.SourceNamespaces = suite.origSourceNamespaces
	})
	requires.NoError(err, "failed to restore original AppProject SourceNamespaces")

	// Restore original ConfigMap state
	acm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-cmd-params-cm",
			Namespace: "argocd",
		},
	}
	err = fixture.EnsureUpdate(suite.Ctx, suite.ManagedAgentClient, acm, func(obj fixture.KubeObject) {
		configMap := obj.(*corev1.ConfigMap)
		if suite.origAppNamespacesConfigured {
			// Restore the original value
			if configMap.Data == nil {
				configMap.Data = map[string]string{}
			}
			configMap.Data["application.namespaces"] = suite.origAppNamespacesValue
		} else {
			// Key was not present originally, remove it
			if configMap.Data != nil {
				delete(configMap.Data, "application.namespaces")
			}
		}
	})
	requires.NoError(err, "failed to restore original ConfigMap state")

	suite.restartApplicationController()
}

// createAppForDestMappingTests creates a standard test application for destination mapping tests (managed mode)
func createAppForDestMappingTests(name, namespace string, autoSync bool) *v1alpha1.Application {
	app := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        destMapExampleAppsRepo,
				TargetRevision: "HEAD",
				Path:           destMapGuestbookPath,
			},
			Destination: v1alpha1.ApplicationDestination{
				Name:      destMapAgentName,
				Namespace: "guestbook",
			},
			SyncPolicy: &v1alpha1.SyncPolicy{
				SyncOptions: v1alpha1.SyncOptions{
					destMapCreateNsOption,
				},
			},
		},
	}
	if autoSync {
		app.Spec.SyncPolicy.Automated = &v1alpha1.SyncPolicyAutomated{}
	}
	return app
}

// createAutonomousAppForDestMappingTests creates a test application for the autonomous agent
func createAutonomousAppForDestMappingTests(name, namespace, targetNamespace string) *v1alpha1.Application {
	return &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        destMapExampleAppsRepo,
				TargetRevision: "HEAD",
				Path:           destMapGuestbookPath,
			},
			Destination: v1alpha1.ApplicationDestination{
				Server:    "https://kubernetes.default.svc",
				Namespace: targetNamespace,
			},
			SyncPolicy: &v1alpha1.SyncPolicy{
				SyncOptions: v1alpha1.SyncOptions{
					destMapCreateNsOption,
				},
			},
		},
	}
}

// TestAppCreatedInOriginalNamespace verifies that when destination-based
// mapping is enabled, the application is created in its original namespace on the agent
// (not in the argocd namespace).
func (suite *DestinationMappingTestSuite) TestAppCreatedInOriginalNamespace() {
	requires := suite.Require()
	t := suite.T()

	appName := "destmap-test-app"
	appNs := "test-namespace"
	app := createAppForDestMappingTests(appName, appNs, true)

	// Create the app namespace on the principal first (required for Application CR)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: appNs}}
	err := suite.PrincipalClient.Create(suite.Ctx, ns, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		requires.NoError(err)
	}

	defer func() {
		err = fixture.EnsureDeletion(suite.Ctx, suite.PrincipalClient, ns)
		requires.NoError(err)
	}()

	t.Log("Create an application on the principal with destination-based mapping")
	err = suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)

	defer func() {
		err = fixture.EnsureDeletion(suite.Ctx, suite.PrincipalClient, app)
		requires.NoError(err)
		// Also clean up namespace on agent if it was created
		agentNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: appNs}}
		err = fixture.EnsureDeletion(suite.Ctx, suite.ManagedAgentClient, agentNs)
		requires.NoError(err)
	}()

	// Ensure the namespace is created on the agent
	t.Log("Verify the target namespace is created on the agent")
	requires.Eventually(func() bool {
		agentNs := &corev1.Namespace{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
			Name: appNs,
		}, agentNs, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second, "Namespace "+appNs+" should be created on agent")

	t.Log("Verify application is created on the agent in the original namespace, not argocd")
	requires.Eventually(func() bool {
		agentApp := &v1alpha1.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
			Namespace: appNs,
			Name:      appName,
		}, agentApp, metav1.GetOptions{})
		if err != nil {
			t.Logf("App not found in %s namespace yet: %v", appNs, err)
			return false
		}
		t.Logf("App found in %s namespace", appNs)
		return true
	}, 60*time.Second, 1*time.Second, "Application should be created in "+appNs+" namespace on agent")

	t.Log("Verify application is NOT in argocd namespace on the agent")
	agentAppInArgoCD := &v1alpha1.Application{}
	err = suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
		Namespace: "argocd",
		Name:      appName,
	}, agentAppInArgoCD, metav1.GetOptions{})
	requires.True(errors.IsNotFound(err), "Application should NOT be in argocd namespace on agent")

	t.Log("Wait for application to be synced and healthy")
	requires.Eventually(func() bool {
		principalApp := &v1alpha1.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{
			Namespace: appNs,
			Name:      appName,
		}, principalApp, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return principalApp.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced &&
			principalApp.Status.Health.Status == health.HealthStatusHealthy
	}, 120*time.Second, 2*time.Second)

	t.Log("Verify status contains managed resources")
	principalApp := &v1alpha1.Application{}
	err = suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{
		Namespace: appNs,
		Name:      appName,
	}, principalApp, metav1.GetOptions{})
	requires.NoError(err)

	requires.NotEmpty(principalApp.Status.Resources, "Application status should contain managed resources")

	var foundDeployment bool
	for _, resource := range principalApp.Status.Resources {
		if resource.Kind == "Deployment" && resource.Name == destMapGuestbookUIDepl {
			foundDeployment = true
			break
		}
	}
	requires.True(foundDeployment, "Status should include the kustomize-guestbook-ui Deployment")
}

// TestRefreshPropagation verifies that refresh requests from the
// principal are correctly propagated to the agent.
func (suite *DestinationMappingTestSuite) TestRefreshPropagation() {
	requires := suite.Require()
	t := suite.T()

	appName := "destmap-refresh-test"
	app := createAppForDestMappingTests(appName, destMapAgentNamespace, true)

	t.Log("Create an application for refresh testing")
	err := suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)

	defer func() {
		err = fixture.EnsureDeletion(suite.Ctx, suite.PrincipalClient, app)
		requires.NoError(err)
	}()

	t.Log("Wait for application to be synced and healthy on agent")
	requires.Eventually(func() bool {
		agentApp := &v1alpha1.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
			Namespace: destMapAgentNamespace,
			Name:      appName,
		}, agentApp, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return agentApp.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced &&
			agentApp.Status.Health.Status == health.HealthStatusHealthy
	}, 120*time.Second, 2*time.Second)

	t.Log("Get the current reconciledAt timestamp before refresh")
	principalApp := &v1alpha1.Application{}
	err = suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{
		Namespace: destMapAgentNamespace,
		Name:      appName,
	}, principalApp, metav1.GetOptions{})
	requires.NoError(err)
	reconciledAtBefore := principalApp.Status.ReconciledAt

	t.Log("Trigger a refresh by adding the refresh annotation")
	err = suite.PrincipalClient.EnsureApplicationUpdate(suite.Ctx, types.NamespacedName{
		Namespace: destMapAgentNamespace,
		Name:      appName,
	}, func(a *v1alpha1.Application) error {
		if a.Annotations == nil {
			a.Annotations = make(map[string]string)
		}
		a.Annotations["argocd.argoproj.io/refresh"] = "normal"
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	t.Log("Verify refresh is processed - reconciledAt should be updated")
	requires.Eventually(func() bool {
		principalApp := &v1alpha1.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{
			Namespace: destMapAgentNamespace,
			Name:      appName,
		}, principalApp, metav1.GetOptions{})
		if err != nil {
			return false
		}

		if principalApp.Status.ReconciledAt == nil || reconciledAtBefore == nil {
			return principalApp.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced
		}
		return principalApp.Status.ReconciledAt.After(reconciledAtBefore.Time)
	}, 60*time.Second, 2*time.Second, "Refresh should be processed and reconciledAt updated")

	t.Log("Verify refresh annotation is removed after processing")
	requires.Eventually(func() bool {
		principalApp := &v1alpha1.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{
			Namespace: destMapAgentNamespace,
			Name:      appName,
		}, principalApp, metav1.GetOptions{})
		if err != nil {
			return false
		}
		_, hasRefreshAnnotation := principalApp.Annotations["argocd.argoproj.io/refresh"]
		return !hasRefreshAnnotation
	}, 30*time.Second, 1*time.Second, "Refresh annotation should be removed after processing")
}

// TestCascadeDelete verifies that cascade deletion works correctly
// with destination-based mapping.
func (suite *DestinationMappingTestSuite) TestCascadeDelete() {
	requires := suite.Require()
	t := suite.T()

	appName := "destmap-delete-test"
	app := createAppForDestMappingTests(appName, destMapAgentNamespace, true)

	t.Log("Create an application for cascade delete testing")
	err := suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)

	t.Log("Wait for application to be synced and healthy")
	requires.Eventually(func() bool {
		agentApp := &v1alpha1.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
			Namespace: destMapAgentNamespace,
			Name:      appName,
		}, agentApp, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return agentApp.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced &&
			agentApp.Status.Health.Status == health.HealthStatusHealthy
	}, 120*time.Second, 2*time.Second)

	t.Log("Verify deployed resources exist on agent")
	depl := &appsv1.Deployment{}
	requires.Eventually(func() bool {
		err := suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
			Namespace: destMapGuestbookNs,
			Name:      destMapGuestbookUIDepl,
		}, depl, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second)

	t.Log("Add a finalizer to the Deployment to keep it from being deleted")
	depl.Finalizers = append(depl.Finalizers, "test-e2e/my-test-finalizer")
	err = fixture.EnsureUpdate(suite.Ctx, suite.ManagedAgentClient, depl, func(obj fixture.KubeObject) {
		depl := obj.(*appsv1.Deployment)
		depl.Finalizers = append(depl.Finalizers, "test-e2e/my-test-finalizer")
	})
	requires.NoError(err)

	// Ensure that finalizer is removed from Deployment if the test ends prematurely
	defer func() {
		err = suite.ManagedAgentClient.Get(suite.Ctx, fixture.ToNamespacedName(depl), depl, metav1.GetOptions{})
		if err == nil { // If the Deployment still exists
			depl.Finalizers = nil // Remove the finalizer
			err = suite.ManagedAgentClient.Update(suite.Ctx, depl, metav1.UpdateOptions{})
			requires.NoError(err)
		}
	}()

	t.Log("Add cascade finalizer and delete the application on principal")
	err = suite.PrincipalClient.EnsureApplicationUpdate(suite.Ctx, types.NamespacedName{
		Namespace: destMapAgentNamespace,
		Name:      appName,
	}, func(a *v1alpha1.Application) error {
		a.Finalizers = append(a.Finalizers, "resources-finalizer.argocd.argoproj.io")
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	err = suite.PrincipalClient.Delete(suite.Ctx, app, metav1.DeleteOptions{})
	requires.NoError(err)

	t.Log("Ensure that finalizers and deletion timestamp are synced to managed-agent")
	requires.Eventually(func() bool {
		app := argoapp.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      appName,
				Namespace: destMapAgentNamespace,
			},
		}
		err := suite.ManagedAgentClient.Get(suite.Ctx, fixture.ToNamespacedName(&app), &app, metav1.GetOptions{})
		return err == nil && app.DeletionTimestamp != nil && len(app.Finalizers) > 0
	}, 60*time.Second, 1*time.Second)

	err = fixture.EnsureUpdate(suite.Ctx, suite.ManagedAgentClient, depl, func(obj fixture.KubeObject) {
		depl := obj.(*appsv1.Deployment)
		depl.Finalizers = nil
	})
	requires.NoError(err)

	t.Log("Verify managed agent application is eventually deleted")
	requires.Eventually(func() bool {
		agentApp := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, fixture.ToNamespacedName(app), &agentApp, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 60*time.Second, 1*time.Second)

	t.Log("Verify guestbook Deployment is deleted from managed-agent cluster, which proves it is a cascaded delete")
	requires.Eventually(func() bool {
		err := suite.ManagedAgentClient.Get(suite.Ctx, fixture.ToNamespacedName(depl), depl, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 60*time.Second, 1*time.Second)

	t.Log("Verify principal application is deleted")
	requires.Eventually(func() bool {
		principalApp := argoapp.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, fixture.ToNamespacedName(app), &principalApp, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 60*time.Second, 1*time.Second)
}

// TestRedisProxy verifies that redis proxy correctly routes
// requests for destination-mapped applications.
func (suite *DestinationMappingTestSuite) TestRedisProxy() {
	requires := suite.Require()
	t := suite.T()

	appName := "destmap-redis-test"
	app := createAppForDestMappingTests(appName, destMapAgentNamespace, true)

	t.Log("Create an application for redis proxy testing")
	err := suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)

	defer func() {
		err = fixture.EnsureDeletion(suite.Ctx, suite.PrincipalClient, app)
		requires.NoError(err)
	}()

	t.Log("Wait for application to be synced and healthy")
	requires.Eventually(func() bool {
		principalApp := &v1alpha1.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{
			Namespace: destMapAgentNamespace,
			Name:      appName,
		}, principalApp, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return principalApp.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced &&
			principalApp.Status.Health.Status == health.HealthStatusHealthy
	}, 120*time.Second, 2*time.Second)

	argocdClient, sessionToken, closer, err := createArgoCDAPIClient(suite.Ctx, endpoint, password)
	requires.NoError(err)
	defer closer.Close()

	closer, appClient, err := argocdClient.NewApplicationClient()
	requires.NoError(err)
	defer closer.Close()

	verifyResourceTreeViaRedisProxy(&suite.BaseSuite, app, appClient, endpoint, sessionToken)
}

// TestSyncOperation verifies that sync operations work correctly
// with destination-based mapping.
func (suite *DestinationMappingTestSuite) TestSyncOperation() {
	requires := suite.Require()
	t := suite.T()

	appName := "destmap-sync-test"
	app := createAppForDestMappingTests(appName, destMapAgentNamespace, false) // No auto-sync

	t.Log("Create an application without auto-sync")
	err := suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)

	defer func() {
		err = fixture.EnsureDeletion(suite.Ctx, suite.PrincipalClient, app)
		requires.NoError(err)
	}()

	t.Log("Wait for application to be created on agent")
	requires.Eventually(func() bool {
		agentApp := &v1alpha1.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
			Namespace: destMapAgentNamespace,
			Name:      appName,
		}, agentApp, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second)

	t.Log("Verify application is OutOfSync initially")
	requires.Eventually(func() bool {
		principalApp := &v1alpha1.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{
			Namespace: destMapAgentNamespace,
			Name:      appName,
		}, principalApp, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return principalApp.Status.Sync.Status == v1alpha1.SyncStatusCodeOutOfSync
	}, 60*time.Second, 2*time.Second)

	t.Log("Trigger a sync via ArgoCD API")
	principalApp := &v1alpha1.Application{}
	err = suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{
		Namespace: destMapAgentNamespace,
		Name:      appName,
	}, principalApp, metav1.GetOptions{})
	requires.NoError(err)

	fixture.WaitForAppSyncedAndHealthy(t, suite.Ctx, suite.PrincipalClient, argoClient, principalApp)

	t.Log("Verify application becomes synced and healthy")
	requires.Eventually(func() bool {
		principalApp := &v1alpha1.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{
			Namespace: destMapAgentNamespace,
			Name:      appName,
		}, principalApp, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return principalApp.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced &&
			principalApp.Status.Health.Status == health.HealthStatusHealthy
	}, 120*time.Second, 2*time.Second, "Application should become synced and healthy after sync operation")
}

// TestAutonomousAppCreation verifies that an autonomous agent works correctly
// with principal configured withdestination-based mapping.
func (suite *DestinationMappingTestSuite) TestAutonomousAppCreation() {
	requires := suite.Require()
	t := suite.T()

	appName := "autonomous-destmap-create"
	appNamespace := "argocd"
	targetNamespace := "guestbook"

	app := createAutonomousAppForDestMappingTests(appName, appNamespace, targetNamespace)

	t.Log("Creating application on autonomous agent for creation test")
	err := suite.AutonomousAgentClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)

	defer func() {
		err = fixture.EnsureDeletion(suite.Ctx, suite.AutonomousAgentClient, app)
		requires.NoError(err)
	}()

	principalKey := types.NamespacedName{
		Name:      appName,
		Namespace: autonomousDestMapAgentName,
	}

	t.Log("Verify application is propagated to the principal")
	requires.Eventually(func() bool {
		principalApp := &v1alpha1.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, principalApp, metav1.GetOptions{})
		if err != nil {
			t.Logf("App not found on principal yet: %v", err)
			return false
		}
		t.Logf("App found on principal: %s/%s", principalApp.Namespace, principalApp.Name)
		return true
	}, 60*time.Second, 1*time.Second, "Application should be created on principal")

	t.Log("Verify application spec is correctly transformed on principal")
	principalApp := &v1alpha1.Application{}
	err = suite.PrincipalClient.Get(suite.Ctx, principalKey, principalApp, metav1.GetOptions{})
	requires.NoError(err)

	// The destination should be updated to point to the agent
	requires.Equal(autonomousDestMapAgentName, principalApp.Spec.Destination.Name,
		"Destination name should be the agent name")

	t.Log("Verifying application is OutOfSync initially on agent")
	agentKey := types.NamespacedName{Name: appName, Namespace: appNamespace}
	requires.Eventually(func() bool {
		agentApp := &v1alpha1.Application{}
		err := suite.AutonomousAgentClient.Get(suite.Ctx, agentKey, agentApp, metav1.GetOptions{})
		return err == nil && agentApp.Status.Sync.Status == v1alpha1.SyncStatusCodeOutOfSync
	}, 60*time.Second, 1*time.Second)

	t.Log("Trigger sync from the principal")

	fixture.WaitForAppSyncedAndHealthy(t, suite.Ctx, suite.PrincipalClient, argoClient, principalApp)

	t.Log("Wait for application to become synced on agent")
	requires.Eventually(func() bool {
		agentApp := &v1alpha1.Application{}
		err := suite.AutonomousAgentClient.Get(suite.Ctx, agentKey, agentApp, metav1.GetOptions{})
		return err == nil && agentApp.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced
	}, 120*time.Second, 2*time.Second, "Application should become synced on agent")

	t.Log("Verify deployed resources exist on autonomous agent")
	depl := &appsv1.Deployment{}
	requires.Eventually(func() bool {
		err := suite.AutonomousAgentClient.Get(suite.Ctx, types.NamespacedName{
			Namespace: targetNamespace,
			Name:      destMapGuestbookUIDepl,
		}, depl, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second, "Deployment should exist on autonomous agent")

	t.Log("Delete the application from the autonomous agent")
	err = suite.AutonomousAgentClient.Delete(suite.Ctx, app, metav1.DeleteOptions{})
	requires.NoError(err)

	t.Log("Verify application is deleted from autonomous agent")
	requires.Eventually(func() bool {
		agentApp := &v1alpha1.Application{}
		err := suite.AutonomousAgentClient.Get(suite.Ctx, agentKey, agentApp, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 120*time.Second, 2*time.Second, "Application should be deleted from agent")

	t.Log("Verify application is deleted from principal")
	requires.Eventually(func() bool {
		principalApp := &v1alpha1.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, principalApp, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 60*time.Second, 2*time.Second, "Application should be deleted from principal")
}

// TestAutonomousSpecUpdate verifies that spec updates on the autonomous
// agent are propagated to the principal.
func (suite *DestinationMappingTestSuite) TestAutonomousSpecUpdate() {
	requires := suite.Require()
	t := suite.T()

	appName := "autonomous-destmap-specupdate"
	appNamespace := "argocd"
	targetNamespace := "guestbook"

	app := createAutonomousAppForDestMappingTests(appName, appNamespace, targetNamespace)
	app.Spec.SyncPolicy.Automated = &v1alpha1.SyncPolicyAutomated{}

	t.Log("Creating application on autonomous agent for spec update test")
	err := suite.AutonomousAgentClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)

	defer func() {
		err = fixture.EnsureDeletion(suite.Ctx, suite.AutonomousAgentClient, app)
		requires.NoError(err)
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: targetNamespace}}
		err = fixture.EnsureDeletion(suite.Ctx, suite.AutonomousAgentClient, ns)
		requires.NoError(err)
	}()

	agentKey := types.NamespacedName{Name: appName, Namespace: appNamespace}
	principalKey := types.NamespacedName{
		Name:      appName,
		Namespace: autonomousDestMapAgentName,
	}

	t.Log("Waiting for application to appear on principal")
	requires.Eventually(func() bool {
		principalApp := &v1alpha1.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, principalApp, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second)

	t.Log("Updating application spec on the autonomous agent")
	err = suite.AutonomousAgentClient.EnsureApplicationUpdate(suite.Ctx, agentKey, func(a *v1alpha1.Application) error {
		a.Spec.Info = []v1alpha1.Info{
			{
				Name:  "test-key",
				Value: "test-value",
			},
		}
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	t.Log("Verify spec update is propagated to principal")
	requires.Eventually(func() bool {
		principalApp := &v1alpha1.Application{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, principalApp, metav1.GetOptions{})
		if err != nil {
			return false
		}
		if len(principalApp.Spec.Info) == 0 {
			return false
		}
		return principalApp.Spec.Info[0].Name == "test-key" &&
			principalApp.Spec.Info[0].Value == "test-value"
	}, 60*time.Second, 2*time.Second, "Spec update should be propagated to principal")
}

func (suite *DestinationMappingTestSuite) restartApplicationController() {
	requires := suite.Require()
	podList := &corev1.PodList{}
	err := suite.ManagedAgentClient.List(suite.Ctx, "argocd", podList, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=argocd-application-controller",
	})
	requires.NoError(err)

	requires.True(len(podList.Items) > 0, "expected at least one application controller pod")
	err = suite.ManagedAgentClient.Delete(suite.Ctx, &podList.Items[0], metav1.DeleteOptions{})
	requires.NoError(err)

	requires.Eventually(func() bool {
		pod := &corev1.Pod{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{Name: podList.Items[0].Name, Namespace: "argocd"}, pod, metav1.GetOptions{})
		if err != nil {
			return false
		}

		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true
			}
		}
		return false
	}, 120*time.Second, 2*time.Second, "Application controller pod should become ready")
}

func TestDestinationMappingTestSuite(t *testing.T) {
	suite.Run(t, new(DestinationMappingTestSuite))
}
