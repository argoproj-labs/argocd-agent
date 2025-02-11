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
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

type ResourceProxyTestSuite struct {
	fixture.BaseSuite
}

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

func (suite *ResourceProxyTestSuite) getArgoClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
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

	// Read admin secret from principal's cluster
	pwdSecret := &corev1.Secret{}
	err := suite.PrincipalClient.Get(context.Background(),
		types.NamespacedName{Namespace: "argocd", Name: "argocd-initial-admin-secret"}, pwdSecret, v1.GetOptions{})
	requires.NoError(err)

	argoClient := fixture.NewArgoClient(argoEndpoint, "admin", string(pwdSecret.Data["password"]))
	err = argoClient.Login()
	requires.NoError(err)

	resource, err := argoClient.GetResource(&v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "argocd"}},
		"ks-guestbook-demo", "apps", "v1", "deployment")
	requires.NoError(err)
	suite.T().Log(resource)
}

func TestResourceProxyTestSuite(t *testing.T) {
	suite.Run(t, new(ResourceProxyTestSuite))
}
