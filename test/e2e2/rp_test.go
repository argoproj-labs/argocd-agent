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
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/test/e2e2/fixture"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/gitops-engine/pkg/health"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

type ResourceProxyTestSuite struct {
	fixture.BaseSuite
}

// getRpClient returns a http.Client suitable to access the resource proxy
func (suite *ResourceProxyTestSuite) getRpClient(agentName string) *http.Client {
	requires := suite.Require()

	pc, err := kubernetes.NewForConfig(suite.PrincipalClient.Config)
	requires.NoError(err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Read agent root CA
	caCert, err := tlsutil.TLSCertFromSecret(ctx, pc, "argocd", config.SecretNamePrincipalCA)
	requires.NoError(err)
	requires.NotNil(caCert)
	certPool := x509.NewCertPool()
	certPool.AddCert(caCert.Leaf)

	// Generate client certificate
	ccert, ckey, err := tlsutil.GenerateClientCertificate(agentName, caCert.Leaf, caCert.PrivateKey)
	requires.NoError(err)
	tlsCert, err := tls.X509KeyPair([]byte(ccert), []byte(ckey))
	requires.NoError(err)

	// Build our HTTP client
	tlsConfig := &tls.Config{
		RootCAs:      certPool,
		Certificates: []tls.Certificate{tlsCert},
	}
	client := http.Client{Transport: &http.Transport{
		TLSClientConfig: tlsConfig,
	}}

	return &client
}

func (suite *ResourceProxyTestSuite) Test_ResourceProxy_HTTP() {
	requires := suite.Require()

	rpClient := suite.getRpClient("agent-managed")

	depl := &appsv1.Deployment{}
	err := suite.ManagedAgentClient.Get(context.TODO(), types.NamespacedName{Namespace: "argocd", Name: "argocd-repo-server"}, depl, v1.GetOptions{})
	requires.NoError(err)
	requires.Equal("argocd", depl.Namespace)
	requires.Equal("argocd-repo-server", depl.Name)
	depl.Labels["app.kubernetes.io/instance"] = "argocd-repo-server"
	err = suite.ManagedAgentClient.Update(context.TODO(), depl, v1.UpdateOptions{})
	requires.NoError(err)

	// Managed agents should respond swiftly
	resp, err := rpClient.Get("https://127.0.0.1:9090/apis/apps/v1/namespaces/argocd/deployments/argocd-repo-server")
	requires.NoError(err)
	requires.NotNil(resp)
	requires.Equal(http.StatusOK, resp.StatusCode)

	// Check whether we have a "good" resource
	resource, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	requires.NoError(err)
	depl = &appsv1.Deployment{}
	err = json.Unmarshal(resource, depl)
	requires.NoError(err)
	requires.Equal("argocd-repo-server", depl.Name)
	requires.Equal("argocd", depl.Namespace)

	// Request an unmanaged resource
	resp, err = rpClient.Get("https://127.0.0.1:9090/apis/apps/v1/namespaces/argocd/deployments/argocd-redis")
	requires.NoError(err)
	requires.NotNil(resp)
	requires.NotEqual(http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// Request the API discovery endpoint
	resp, err = rpClient.Get("https://127.0.0.1:9090/apis/apps/v1")
	requires.NoError(err)
	requires.NotNil(resp)
	requires.NotEqual(http.StatusNotFound, resp.StatusCode)
	resource, err = io.ReadAll(resp.Body)
	requires.NoError(err)
	fmt.Printf("%s", resource)
	resp.Body.Close()

	// argocd-server should not exist on the agent
	resp, err = rpClient.Get("https://127.0.0.1:9090/apis/apps/v1/namespaces/argocd/deployments/argocd-server")
	requires.NoError(err)
	requires.NotNil(resp)
	requires.Equal(http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// Other methods should be forbidden as of now
	for _, m := range []string{http.MethodConnect, http.MethodDelete, http.MethodOptions, http.MethodPut} {
		resp, err = rpClient.Do(&http.Request{
			Method: m,
			URL:    &url.URL{Scheme: "https", Host: "127.0.0.1:9090", Path: "/apis/apps/v1/namespaces/argocd/deployments/argocd-server"},
		})
		requires.NoError(err)
		requires.NotNil(resp)
		requires.Equal(http.StatusForbidden, resp.StatusCode)
	}

	// Unknown agent
	rpClient = suite.getRpClient("unknown-agent")
	resp, err = rpClient.Get("https://127.0.0.1:9090/apis/apps/v1/namespaces/argocd/deployments/argocd-repo-server")
	requires.NoError(err)
	requires.NotNil(resp)
	requires.Equal(http.StatusBadGateway, resp.StatusCode)
}

func (suite *ResourceProxyTestSuite) Test_ResourceProxy_Argo() {
	requires := suite.Require()

	// Get the Argo server endpoint to use
	srvService := &corev1.Service{}
	err := suite.PrincipalClient.Get(context.Background(),
		types.NamespacedName{Namespace: "argocd", Name: "argocd-server"}, srvService, v1.GetOptions{})
	requires.NoError(err)
	argoEndpoint := srvService.Spec.LoadBalancerIP

	if len(srvService.Status.LoadBalancer.Ingress) > 0 {
		hostname := srvService.Status.LoadBalancer.Ingress[0].Hostname
		if hostname != "" {
			argoEndpoint = hostname
		}
	}

	appName := "guestbook-rp"

	// Read admin secret from principal's cluster
	pwdSecret := &corev1.Secret{}
	err = suite.PrincipalClient.Get(context.Background(),
		types.NamespacedName{Namespace: "argocd", Name: "argocd-initial-admin-secret"}, pwdSecret, v1.GetOptions{})
	requires.NoError(err)

	// Create a managed application in the principal's cluster
	app := v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{
			Name:      appName,
			Namespace: "agent-managed",
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
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

	err = suite.PrincipalClient.Create(suite.Ctx, &app, metav1.CreateOptions{})
	requires.NoError(err)

	argoClient := fixture.NewArgoClient(argoEndpoint, "admin", string(pwdSecret.Data["password"]))
	err = argoClient.Login()
	requires.NoError(err)

	// Wait until the app is synced and healthy
	retries := 0
	requires.Eventually(func() bool {
		app := &v1alpha1.Application{}
		err = suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{Namespace: "agent-managed", Name: appName}, app, v1.GetOptions{})
		if err != nil {
			return false
		}
		if app.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced && app.Status.Health.Status == health.HealthStatusHealthy {
			return true
		} else {
			// Sometimes, the sync hangs on the workload cluster. We trigger
			// a sync every 5th or so retry.
			if retries > 0 && retries%5 == 0 {
				suite.T().Logf("Triggering re-sync")
				err = argoClient.Sync(app)
				if err != nil {
					return true
				}
			}
			retries += 1
		}
		return false
	}, 60*time.Second, 1*time.Second)
	requires.NoError(err)

	// Getting an existing resource belonging to the synced app through Argo's
	// API must result in success.
	resource, err := argoClient.GetResource(&app,
		"apps", "v1", "Deployment", "guestbook", "kustomize-guestbook-ui")
	requires.NoError(err)
	napp := &v1alpha1.Application{}
	err = json.Unmarshal([]byte(resource), napp)
	requires.NoError(err)
	requires.Equal("Deployment", napp.Kind)
	requires.Equal("kustomize-guestbook-ui", napp.Name)

	// Getting a non-existing resource must result in failure
	_, err = argoClient.GetResource(&app,
		"apps", "v1", "Deployment", "guestbook", "kustomize-guestbook-backend")
	requires.Error(err)
}

func (suite *ResourceProxyTestSuite) Test_ResourceProxy_ResourceActions() {
	requires := suite.Require()

	ctx := context.Background()
	deploymentActionKey := "resource.customizations.actions.apps_Deployment"

	updateResourceAction := func(updateFn func(argocdCM *corev1.ConfigMap)) {
		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			argocdCM := &corev1.ConfigMap{}
			err := suite.PrincipalClient.Get(ctx, types.NamespacedName{Name: "argocd-cm", Namespace: "argocd"}, argocdCM, metav1.GetOptions{})
			if err != nil {
				return err
			}

			// update resource action
			updateFn(argocdCM)
			return suite.PrincipalClient.Update(ctx, argocdCM, metav1.UpdateOptions{})
		})
		requires.NoError(err)
	}

	// Create a custom action for a deployment in the argocd-cm
	updateResourceAction(func(argocdCM *corev1.ConfigMap) {
		argocdCM.Data[deploymentActionKey] = getCustomResourceAction()
	})

	defer func() {
		updateResourceAction(func(argocdCM *corev1.ConfigMap) {
			if _, ok := argocdCM.Data[deploymentActionKey]; !ok {
				return
			}
			delete(argocdCM.Data, deploymentActionKey)
		})

		// delete a resource created by action
		newCM := &corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(ctx, types.NamespacedName{Name: "test-cm", Namespace: "guestbook"},
			newCM, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return
		}
		requires.NoError(err)

		err = suite.ManagedAgentClient.Delete(ctx, newCM, metav1.DeleteOptions{})
		requires.NoError(err)
	}()

	// Get the Argo server endpoint to use
	srvService := &corev1.Service{}
	err := suite.PrincipalClient.Get(context.Background(),
		types.NamespacedName{Namespace: "argocd", Name: "argocd-server"}, srvService, v1.GetOptions{})
	requires.NoError(err)
	argoEndpoint := srvService.Spec.LoadBalancerIP

	if len(srvService.Status.LoadBalancer.Ingress) > 0 {
		hostname := srvService.Status.LoadBalancer.Ingress[0].Hostname
		if hostname != "" {
			argoEndpoint = hostname
		}
	}
	appName := "guestbook-ui"

	// Read admin secret from principal's cluster
	pwdSecret := &corev1.Secret{}
	err = suite.PrincipalClient.Get(context.Background(),
		types.NamespacedName{Namespace: "argocd", Name: "argocd-initial-admin-secret"}, pwdSecret, v1.GetOptions{})
	requires.NoError(err)

	argoClient := fixture.NewArgoClient(argoEndpoint, "admin", string(pwdSecret.Data["password"]))
	err = argoClient.Login()
	requires.NoError(err)

	// Create a managed application in the principal's cluster
	app := v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{
			Name:      appName,
			Namespace: "agent-managed",
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
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

	err = suite.PrincipalClient.Create(suite.Ctx, &app, metav1.CreateOptions{})
	requires.NoError(err)

	// Wait until the app is synced and healthy
	retries := 0
	requires.Eventually(func() bool {
		app := &v1alpha1.Application{}
		err = suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{Namespace: "agent-managed", Name: appName}, app, v1.GetOptions{})
		if err != nil {
			return false
		}
		if app.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced && app.Status.Health.Status == health.HealthStatusHealthy {
			return true
		} else {
			// Sometimes, the sync hangs on the workload cluster. We trigger
			// a sync every 5th or so retry.
			if retries > 0 && retries%5 == 0 {
				suite.T().Logf("Triggering re-sync")
				err = argoClient.Sync(app)
				if err != nil {
					return true
				}
			}
			retries += 1
		}
		return false
	}, 60*time.Second, 1*time.Second)
	requires.NoError(err)

	// Execute the action
	err = argoClient.RunResourceAction(&app, "test",
		"apps", "v1", "Deployment", "guestbook", "kustomize-guestbook-ui")
	requires.NoError(err)

	// Check if a new resource is created after running the resource action
	requires.Eventually(func() bool {
		newCM := &corev1.ConfigMap{}
		err = suite.ManagedAgentClient.Get(ctx, types.NamespacedName{Name: "test-cm", Namespace: "guestbook"},
			newCM, metav1.GetOptions{})

		if err != nil {
			return false
		}

		return newCM.Data["testKey"] == "testValue"
	}, 20*time.Second, 1*time.Second)

	// Check if an existing resource is patched after running the resource action
	requires.Eventually(func() bool {
		deployment := &appsv1.Deployment{}
		err = suite.ManagedAgentClient.Get(ctx, types.NamespacedName{Name: "kustomize-guestbook-ui", Namespace: "guestbook"},
			deployment, metav1.GetOptions{})

		if err != nil {
			return false
		}

		return deployment.Labels["test-label"] == "test"
	}, 20*time.Second, 1*time.Second)
}

func getCustomResourceAction() string {
	return `discovery.lua: |
    actions = {}
    actions["test"] = {}
    return actions
definitions:
  - name: test
    action.lua: |
      -- Create a new ConfigMap
      cm = {}
      cm.apiVersion = "v1"
      cm.kind = "ConfigMap"
      cm.metadata = {}
      cm.metadata.name = "test-cm"
      cm.metadata.namespace = obj.metadata.namespace     
      cm.data = {}
      cm.data.testKey = "testValue"
      impactedResource1 = {}
      impactedResource1.operation = "create"
      impactedResource1.resource = cm
      	  
      -- Patch the original Object
      if obj.metadata.labels == nil then
        obj.metadata.labels = {}
      end
      obj.metadata.labels["test-label"] = "test"
      impactedResource2 = {}
      impactedResource2.operation = "patch"
      impactedResource2.resource = obj
      result = {}
      result[1] = impactedResource1
      result[2] = impactedResource2
      return result`
}

func TestResourceProxyTestSuite(t *testing.T) {
	suite.Run(t, new(ResourceProxyTestSuite))
}
