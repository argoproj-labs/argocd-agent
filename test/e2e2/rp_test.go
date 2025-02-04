package e2e2

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/test/e2e2/fixture"
	"github.com/stretchr/testify/suite"
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

func (suite *ResourceProxyTestSuite) Test_GetResource() {
	requires := suite.Require()

	client := suite.getHttpClient("agent-managed")
	// Request a resource from the managed agent
	resp, err := client.Get("https://127.0.0.1:9090/apis/apps/v1/namespaces/argocd/deployments/argocd-repo-server")
	requires.NoError(err)
	requires.NotNil(resp)
	requires.Equal(http.StatusOK, resp.StatusCode)

	client = suite.getHttpClient("agent-autonomou")
	resp, err = client.Get("https://127.0.0.1:9090/apis/apps/v1/namespaces/argocd/deployments/argocd-repo-server")
	requires.NoError(err)
	requires.NotNil(resp)
	requires.Equal(http.StatusOK, resp.StatusCode)

}

func (suite *ResourceProxyTestSuite) Test_DeleteResource() {

}

func TestResourceProxyTestSuite(t *testing.T) {
	suite.Run(t, new(ResourceProxyTestSuite))
}
