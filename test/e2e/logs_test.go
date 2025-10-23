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
	"strings"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	v1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/gitops-engine/pkg/health"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type LogsStreamingTestSuite struct {
	fixture.BaseSuite
}

func (suite *LogsStreamingTestSuite) Test_logs_streaming() {
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

	requires.Eventually(func() bool {
		a := &v1alpha1.Application{}
		if err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{Namespace: "argocd", Name: appName}, a, metav1.GetOptions{}); err != nil {
			return false
		}
		return a.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced && a.Status.Health.Status == health.HealthStatusHealthy
	}, 60*time.Second, 1*time.Second)

	// Find a guestbook pod and its first container
	var podName, containerName string
	pods := &corev1.PodList{}
	err = suite.ManagedAgentClient.List(suite.Ctx, "default", pods, metav1.ListOptions{})
	requires.NoError(err)
	requires.True(len(pods.Items) > 0, "expected at least one pod in default namespace")
	for _, p := range pods.Items {
		if strings.Contains(p.Name, "guestbook-ui") {
			podName = p.Name
			if len(p.Spec.Containers) > 0 {
				containerName = p.Spec.Containers[0].Name
			}
			break
		}
	}
	requires.NotEmpty(podName, "could not find guestbook pod")
	requires.NotEmpty(containerName, "could not determine container name")

	argoEndpoint, err := fixture.GetArgoCDServerEndpoint(suite.PrincipalClient)
	requires.NoError(err)
	password, err := fixture.GetInitialAdminSecret(suite.PrincipalClient)
	requires.NoError(err)

	argoClient := fixture.NewArgoClient(argoEndpoint, "admin", password)
	err = argoClient.Login()
	requires.NoError(err)

	var logs string
	requires.Eventually(func() bool {
		s, e := argoClient.GetLogs(app, "default", podName, containerName, 100)
		if e != nil {
			return false
		}
		if len(s) == 0 {
			return false
		}
		logs = s
		return true
	}, 30*time.Second, 1*time.Second)

	requires.Greater(len(logs), 0)
}

func TestLogsStreamingTestSuite(t *testing.T) {
	suite.Run(t, new(LogsStreamingTestSuite))
}
