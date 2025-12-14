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

func (suite *LogsStreamingTestSuite) Test_logs_streaming_managed() {
	requires := suite.Require()

	// Create a application on the managed-agent's cluster
	appName := "guestbook-logs"
	app := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appName,
			Namespace: "agent-managed",
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				Path:           "kustomize-guestbook",
				TargetRevision: "HEAD",
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

	err := suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)
	suite.T().Cleanup(func() {
		_ = suite.PrincipalClient.Delete(suite.Ctx, app, metav1.DeleteOptions{})
	})

	// Connect to Argo API on principal
	argoEndpoint, err := fixture.GetArgoCDServerEndpoint(suite.PrincipalClient)
	requires.NoError(err)
	password, err := fixture.GetInitialAdminSecret(suite.PrincipalClient)
	requires.NoError(err)

	argoClient := fixture.NewArgoClient(argoEndpoint, "admin", password)
	err = argoClient.Login()
	requires.NoError(err)

	// Wait until the app is synced and healthy
	retries := 0
	requires.Eventually(func() bool {
		a := &v1alpha1.Application{}
		if err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{Namespace: "agent-managed", Name: appName}, a, metav1.GetOptions{}); err != nil {
			return false
		}
		if a.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced &&
			a.Status.Health.Status == health.HealthStatusHealthy {
			return true
		}
		if retries > 0 && retries%5 == 0 {
			suite.T().Logf("Triggering re-sync")
			_ = argoClient.Sync(a)
		}
		retries++
		return false
	}, 60*time.Second, 1*time.Second)

	// Find the pod on the managed cluster
	var podName, containerName string
	pods := &corev1.PodList{}
	err = suite.ManagedAgentClient.List(suite.Ctx, "guestbook", pods, metav1.ListOptions{})
	requires.NoError(err)
	requires.True(len(pods.Items) > 0, "expected at least one pod in guestbook namespace")
	for _, p := range pods.Items {
		if strings.Contains(p.Name, "kustomize-guestbook-ui") {
			podName = p.Name
			if len(p.Spec.Containers) > 0 {
				containerName = p.Spec.Containers[0].Name
			}
			break
		}
	}
	requires.NotEmpty(podName, "could not find guestbook pod")
	requires.NotEmpty(containerName, "could not determine container name")

	// Fetch logs
	var logs string
	requires.Eventually(func() bool {
		s, e := argoClient.GetApplicationLogs(app, "guestbook", podName, containerName, 100)
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

func (suite *LogsStreamingTestSuite) Test_logs_streaming_autonomous() {
	requires := suite.Require()

	// Create a application on the autonomous-agent's cluster
	appName := "guestbook-logs-autonomous"
	app := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appName,
			Namespace: "argocd",
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				Path:           "kustomize-guestbook",
				TargetRevision: "HEAD",
			},
			Destination: v1alpha1.ApplicationDestination{
				Server:    "https://kubernetes.default.svc",
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

	err := suite.AutonomousAgentClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)
	suite.T().Cleanup(func() {
		_ = suite.AutonomousAgentClient.Delete(suite.Ctx, app, metav1.DeleteOptions{})
	})

	// Connect to Argo API on principal
	endpoint, err := fixture.GetArgoCDServerEndpoint(suite.PrincipalClient)
	requires.NoError(err)
	password, err := fixture.GetInitialAdminSecret(suite.PrincipalClient)
	requires.NoError(err)
	argoClient := fixture.NewArgoClient(endpoint, "admin", password)
	requires.NoError(argoClient.Login())

	// Wait until the app is synced and healthy
	retries := 0
	requires.Eventually(func() bool {
		papp := &v1alpha1.Application{}
		if err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{Namespace: "agent-autonomous", Name: appName}, papp, metav1.GetOptions{}); err != nil {
			return false
		}
		if papp.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced && papp.Status.Health.Status == health.HealthStatusHealthy {
			return true
		}
		if retries > 0 && retries%5 == 0 {
			suite.T().Logf("Triggering re-sync")
			_ = argoClient.Sync(papp)
		}
		retries++
		return false
	}, 60*time.Second, 1*time.Second)

	// Find the pod on the autonomous cluster
	var podName, containerName string
	pods := &corev1.PodList{}
	err = suite.AutonomousAgentClient.List(suite.Ctx, "guestbook", pods, metav1.ListOptions{})
	requires.NoError(err)
	requires.True(len(pods.Items) > 0, "expected at least one pod in guestbook namespace")
	for _, p := range pods.Items {
		if strings.Contains(p.Name, "kustomize-guestbook-ui") {
			podName = p.Name
			if len(p.Spec.Containers) > 0 {
				containerName = p.Spec.Containers[0].Name
			}
			break
		}
	}
	requires.NotEmpty(podName, "could not find guestbook pod")
	requires.NotEmpty(containerName, "could not determine container name")

	// Fetch logs
	var logs string
	requires.Eventually(func() bool {
		papp := &v1alpha1.Application{}
		if e := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{Namespace: "agent-autonomous", Name: appName}, papp, metav1.GetOptions{}); e != nil {
			return false
		}
		s, e := argoClient.GetApplicationLogs(papp, "guestbook", podName, containerName, 100)
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
	t.Run("logs_streaming", func(t *testing.T) {
		suite.Run(t, new(LogsStreamingTestSuite))
	})
}
