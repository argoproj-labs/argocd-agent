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
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/kubernetes"
)

type ResourceProxyTestSuite struct {
	fixture.BaseSuite
}

func (suite *ResourceProxyTestSuite) getHttpClient(agentName string) *http.Client {
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

func (suite *ResourceProxyTestSuite) Test_ResourceProxy() {
	requires := suite.Require()

	client := suite.getHttpClient("agent-managed")

	// Managed agents should respond swiftly
	resp, err := client.Get("https://127.0.0.1:9090/apis/apps/v1/namespaces/argocd/deployments/argocd-repo-server")
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
	resp, err = client.Get("https://127.0.0.1:9090/apis/apps/v1/namespaces/argocd/deployments/argocd-server")
	requires.NoError(err)
	requires.NotNil(resp)
	requires.Equal(http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// Other methods should be forbidden as of now
	for _, m := range []string{http.MethodConnect, http.MethodDelete, http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut} {
		resp, err = client.Do(&http.Request{
			Method: m,
			URL:    &url.URL{Scheme: "https", Host: "127.0.0.1:9090", Path: "/apis/apps/v1/namespaces/argocd/deployments/argocd-server"},
		})
		requires.NoError(err)
		requires.NotNil(resp)
		requires.Equal(http.StatusForbidden, resp.StatusCode)
	}

	// Unknown agent
	client = suite.getHttpClient("unknown-agent")
	resp, err = client.Get("https://127.0.0.1:9090/apis/apps/v1/namespaces/argocd/deployments/argocd-repo-server")
	requires.NoError(err)
	requires.NotNil(resp)
	requires.Equal(http.StatusBadGateway, resp.StatusCode)

}

func TestResourceProxyTestSuite(t *testing.T) {
	suite.Run(t, new(ResourceProxyTestSuite))
}
