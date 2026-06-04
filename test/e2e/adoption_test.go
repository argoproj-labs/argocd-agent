// Copyright 2024 The argocd-agent Authors
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
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

type AdoptionTestSuite struct {
	fixture.BaseSuite
}

func TestAdoptionTestSuite(t *testing.T) {
	suite.Run(t, new(AdoptionTestSuite))
}

func (suite *AdoptionTestSuite) SetupTest() {
	suite.BaseSuite.SetupTest()

	// Ensure principal and managed agent are running
	if !fixture.IsProcessRunning(fixture.PrincipalName) {
		suite.Require().NoError(fixture.StartProcess(fixture.PrincipalName))
		fixture.CheckReadiness(suite.T(), fixture.PrincipalName)
	} else {
		fixture.CheckReadiness(suite.T(), fixture.PrincipalName)
	}

	if !fixture.IsProcessRunning(fixture.AgentManagedName) {
		suite.Require().NoError(fixture.StartProcess(fixture.AgentManagedName))
		fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)
	} else {
		fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)
	}
}

func (suite *AdoptionTestSuite) Test_ApplicationIsAdoptedIfExists() {
	requires := suite.Require()

	// Create application on managed agent
	app := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "guestbook-adopt",
			Namespace: fixture.ManagedAgentNamespace,
		},
		Spec: argoapp.ApplicationSpec{
			Project: "default",
			Source: &argoapp.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
			},
			Destination: argoapp.ApplicationDestination{
				Server:    "https://kubernetes.default.svc",
				Namespace: "adoption-test",
			},
			SyncPolicy: &argoapp.SyncPolicy{
				SyncOptions: argoapp.SyncOptions{
					"CreateNamespace=true",
				},
			},
		},
	}

	err := suite.ManagedAgentClient.Create(suite.Ctx, &app, metav1.CreateOptions{})
	requires.NoError(err)

	// Make sure app exists on managed agent
	agentKey := fixture.ToNamespacedName(&app)
	managedApp := argoapp.Application{}
	requires.Eventually(func() bool {
		err := suite.ManagedAgentClient.Get(suite.Ctx, agentKey, &managedApp, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)

	// Create application that would create the already existing app on the managed agent
	app2 := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "guestbook-adopt",
			Namespace: "agent-managed",
		},
		Spec: argoapp.ApplicationSpec{
			Project: "default",
			Source: &argoapp.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
			},
			Destination: argoapp.ApplicationDestination{
				Name:      "agent-managed",
				Namespace: "adoption-test",
			},
		},
	}

	err = suite.PrincipalClient.Create(suite.Ctx, &app2, metav1.CreateOptions{})
	requires.NoError(err)

	// Make sure app exists on principal and get the application
	principalKey := fixture.ToNamespacedName(&app2)
	principalApp := argoapp.Application{}
	requires.Eventually(func() bool {
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &principalApp, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)

	// Recapture the managed appliction and check if it has been adopted by the principal
	requires.Eventually(func() bool {
		err := suite.ManagedAgentClient.Get(suite.Ctx, agentKey, &managedApp, metav1.GetOptions{})
		if err != nil {
			return false
		}

		annotations := managedApp.GetAnnotations()
		if annotations == nil {
			return false
		}

		sourceUID, exists := annotations[manager.SourceUIDAnnotation]
		return exists && k8stypes.UID(sourceUID) == principalApp.UID
	}, 30*time.Second, 1*time.Second)
}

func (suite *AdoptionTestSuite) Test_ApplicationIsNotAdoptedIfAnnotationIsPresent() {
	requires := suite.Require()

	// Create application on managed agent with ignore annotation
	app := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "guestbook-dontadopt",
			Namespace: fixture.ManagedAgentNamespace,
			Annotations: map[string]string{
				manager.DontAdoptAnnotation: "true",
			},
		},
		Spec: argoapp.ApplicationSpec{
			Project: "default",
			Source: &argoapp.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
			},
			Destination: argoapp.ApplicationDestination{
				Server:    "https://kubernetes.default.svc",
				Namespace: "adoption-test",
			},
			SyncPolicy: &argoapp.SyncPolicy{
				SyncOptions: argoapp.SyncOptions{
					"CreateNamespace=true",
				},
			},
		},
	}

	err := suite.ManagedAgentClient.Create(suite.Ctx, &app, metav1.CreateOptions{})
	requires.NoError(err)

	// Make sure app exists on managed agent
	agentKey := fixture.ToNamespacedName(&app)
	managedApp := argoapp.Application{}
	requires.Eventually(func() bool {
		err := suite.ManagedAgentClient.Get(suite.Ctx, agentKey, &managedApp, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)

	// Create application that would create the already existing app on the managed agent
	app2 := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "guestbook-dontadopt",
			Namespace: "agent-managed",
		},
		Spec: argoapp.ApplicationSpec{
			Project: "default",
			Source: &argoapp.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
			},
			Destination: argoapp.ApplicationDestination{
				Name:      "agent-managed",
				Namespace: "adoption-test",
			},
		},
	}

	err = suite.PrincipalClient.Create(suite.Ctx, &app2, metav1.CreateOptions{})
	requires.NoError(err)

	// Make sure app exists on principal and get the application
	principalKey := fixture.ToNamespacedName(&app2)
	principalApp := argoapp.Application{}
	requires.Eventually(func() bool {
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &principalApp, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)

	// Wait a short time for principal to sync
	time.Sleep(3 * time.Second)

	// Recapture the managed appliction and check if it has not been adopted by the principal
	requires.Eventually(func() bool {
		err := suite.ManagedAgentClient.Get(suite.Ctx, agentKey, &managedApp, metav1.GetOptions{})
		if err != nil {
			return false
		}

		annotations := managedApp.GetAnnotations()
		if annotations == nil {
			return false
		}

		_, exists := annotations[manager.SourceUIDAnnotation]
		return !exists
	}, 30*time.Second, 1*time.Second)
}
