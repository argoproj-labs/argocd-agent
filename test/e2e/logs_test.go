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
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	v1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/gitops-engine/pkg/health"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

type LogsStreamingTestSuite struct {
	fixture.BaseSuite
}

// getRpClient returns a http.Client suitable to access the resource proxy
func (suite *LogsStreamingTestSuite) getRpClient(agentName string) *http.Client {
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

func (suite *LogsStreamingTestSuite) Test_Logs_Static() {
	requires := suite.Require()

	// Create a managed application in the principal's cluster
	appName := "guestbook-logs"
	app := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appName,
			Namespace: "argocd", // Argo CD API creates applications in the argocd namespace
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps.git",
				Path:           "guestbook",
				TargetRevision: "HEAD",
			},
			Destination: v1alpha1.ApplicationDestination{
				Server:    "https://kubernetes.default.svc",
				Namespace: "default",
			},
			SyncPolicy: &v1alpha1.SyncPolicy{
				Automated: &v1alpha1.SyncPolicyAutomated{},
				SyncOptions: v1alpha1.SyncOptions{
					"CreateNamespace=true",
				},
			},
		},
	}

	err := suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)

	// Wait until the app is synced and healthy
	requires.Eventually(func() bool {
		a := &v1alpha1.Application{}
		if err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{Namespace: "agent-managed", Name: appName}, a, metav1.GetOptions{}); err != nil {
			return false
		}
		return a.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced && a.Status.Health.Status == health.HealthStatusHealthy
	}, 60*time.Second, 1*time.Second)

	// Find a guestbook pod and its first container
	var podName, containerName string
	pods := &corev1.PodList{}
	err = suite.ManagedAgentClient.List(suite.Ctx, "guestbook", pods, metav1.ListOptions{})
	requires.NoError(err)
	requires.True(len(pods.Items) > 0, "expected at least one pod in guestbook namespace")
	for _, p := range pods.Items {
		if strings.Contains(p.Name, "kustomize-guestbook-ui") || strings.Contains(p.Name, "guestbook") {
			podName = p.Name
			if len(p.Spec.Containers) > 0 {
				containerName = p.Spec.Containers[0].Name
			}
			break
		}
	}
	requires.NotEmpty(podName, "could not find guestbook pod")
	requires.NotEmpty(containerName, "could not determine container name")

	// Call the resource proxy logs endpoint (static logs)
	rpClient := suite.getRpClient("agent-managed")
	q := url.Values{}
	q.Set("container", containerName)
	q.Set("tailLines", "100")
	logsURL := &url.URL{Scheme: "https", Host: "127.0.0.1:9090", Path: fmt.Sprintf("/api/v1/namespaces/guestbook/pods/%s/log", podName), RawQuery: q.Encode()}

	// Retry for a short period in case logs aren’t immediately available
	var resp *http.Response
	var body []byte
	requires.Eventually(func() bool {
		r, e := rpClient.Get(logsURL.String())
		if e != nil {
			return false
		}
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK {
			return false
		}
		b, e := io.ReadAll(r.Body)
		if e != nil {
			return false
		}
		if len(b) == 0 {
			return false
		}
		resp, body = r, b
		return true
	}, 30*time.Second, 1*time.Second)

	// Final assertions
	requires.NotNil(resp)
	requires.Equal(http.StatusOK, resp.StatusCode)
	requires.Greater(len(body), 0)
}

func TestLogsStreamingTestSuite(t *testing.T) {
	suite.Run(t, new(LogsStreamingTestSuite))
}
