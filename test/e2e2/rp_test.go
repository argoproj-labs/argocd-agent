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
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
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

	// Managed agents should respond swiftly
	resp, err := rpClient.Get("https://127.0.0.1:9090/apis/apps/v1/namespaces/argocd/deployments/argocd-repo-server")
	requires.NoError(err)
	requires.NotNil(resp)
	requires.Equal(http.StatusOK, resp.StatusCode)

	// Check whether we have a "good" resource
	resource, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	requires.NoError(err)
	depl := &appsv1.Deployment{}
	err = json.Unmarshal(resource, depl)
	requires.NoError(err)
	requires.Equal("argocd-repo-server", depl.Name)
	requires.Equal("argocd", depl.Namespace)

	// argocd-server should not exist on the agent
	resp, err = rpClient.Get("https://127.0.0.1:9090/apis/apps/v1/namespaces/argocd/deployments/argocd-server")
	requires.NoError(err)
	requires.NotNil(resp)
	requires.Equal(http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// Other methods should be forbidden as of now
	for _, m := range []string{http.MethodConnect, http.MethodDelete, http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut} {
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
	argoEndpoint := "192.168.56.220"
	requires := suite.Require()
	appName := "guestbook-rp"
	// Read admin secret from principal's cluster
	pwdSecret := &corev1.Secret{}
	err := suite.PrincipalClient.Get(context.Background(),
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

	err = wait.PollUntilContextTimeout(suite.Ctx, 1*time.Second, 10*time.Second, true, func(ctx context.Context) (done bool, err error) {
		app := &v1alpha1.Application{}
		err = suite.PrincipalClient.Get(ctx, types.NamespacedName{Namespace: "agent-managed", Name: appName}, app, v1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			} else {
				return true, err
			}
		}
		if app.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced && app.Status.Health.Status == health.HealthStatusHealthy {
			return true, nil
		}
		return false, nil
	})
	requires.NoError(err)

	argoClient := fixture.NewArgoClient(argoEndpoint, "admin", string(pwdSecret.Data["password"]))
	err = argoClient.Login()
	requires.NoError(err)

	resource, err := argoClient.GetResource(&app,
		"apps", "v1", "Deployment", "guestbook", "kustomize-guestbook-ui")
	requires.NoError(err)
	napp := &v1alpha1.Application{}
	err = json.Unmarshal([]byte(resource), napp)
	requires.NoError(err)
	requires.Equal("kustomize-guestbook-ui", napp.Name)
}

func TestResourceProxyTestSuite(t *testing.T) {
	suite.Run(t, new(ResourceProxyTestSuite))
}
