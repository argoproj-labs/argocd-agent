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
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/manager/appproject"
	"github.com/argoproj-labs/argocd-agent/test/e2e2/fixture"
	argoapp "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type AppProjectTestSuite struct {
	fixture.BaseSuite
}

func (suite *AppProjectTestSuite) Test_AppProject_Managed() {
	requires := suite.Require()

	// Create an appProject on the principal's cluster
	appProject := argoapp.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample",
			Namespace: "argocd",
		},
		Spec: argoapp.AppProjectSpec{
			Destinations: []argoapp.ApplicationDestination{
				{
					Namespace: "*",
					Name:      "agent-*",
				},
			},
			SourceNamespaces: []string{"agent-*"},
		},
	}

	err := suite.PrincipalClient.Create(suite.Ctx, &appProject, metav1.CreateOptions{})
	requires.NoError(err)

	key := fixture.ToNamespacedName(&appProject)

	// Ensure the appProject has been pushed to the managed-agent
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)

	// Ensure that the appProject is specific to the agent it is synced to
	appProject = argoapp.AppProject{}
	err = suite.PrincipalClient.Get(suite.Ctx, key, &appProject, metav1.GetOptions{})
	requires.NoError(err)
	mappProject := argoapp.AppProject{}
	err = suite.ManagedAgentClient.Get(suite.Ctx, key, &mappProject, metav1.GetOptions{})
	requires.NoError(err)
	requires.Len(mappProject.Spec.Destinations, 1)
	requires.Equal("in-cluster", mappProject.Spec.Destinations[0].Name)
	requires.Equal("https://kubernetes.default.svc", mappProject.Spec.Destinations[0].Server)

	requires.Len(mappProject.Spec.SourceNamespaces, 1)
	requires.Equal("agent-managed", mappProject.Spec.SourceNamespaces[0])

	// Modify the appProject on the principal and ensure the change is propagated
	// to the managed-agent
	err = suite.PrincipalClient.EnsureAppProjectUpdate(suite.Ctx, key, func(appProject *argoapp.AppProject) error {
		appProject.Spec.Description = "sample description"
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &appProject, metav1.GetOptions{})
		return err == nil &&
			len(appProject.Spec.Destinations) == 1 &&
			appProject.Spec.Destinations[0].Name == "in-cluster" &&
			appProject.Spec.Destinations[0].Namespace == "*" &&
			appProject.Spec.Description == "sample description"
	}, 30*time.Second, 1*time.Second)

	// Delete the appProject from the principal
	err = suite.PrincipalClient.Delete(suite.Ctx, &appProject, metav1.DeleteOptions{})
	requires.NoError(err)

	// Ensure the appProject has been deleted from the managed-agent
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &appProject, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)
}

func (suite *AppProjectTestSuite) Test_AppProject_Autonomous() {
	requires := suite.Require()

	// Create an appProject on the autonomous-agent's cluster
	appProject := argoapp.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample",
			Namespace: "argocd",
		},
		Spec: argoapp.AppProjectSpec{
			Destinations: []argoapp.ApplicationDestination{
				{
					Namespace: "*",
					Name:      "agent-*",
				},
			},
			SourceNamespaces: []string{"agent-*"},
		},
	}

	err := suite.AutonomousAgentClient.Create(suite.Ctx, &appProject, metav1.CreateOptions{})
	requires.NoError(err)

	principalKey := types.NamespacedName{
		Name:      "agent-autonomous-sample",
		Namespace: "argocd",
	}
	agentKey := types.NamespacedName{
		Name:      appProject.Name,
		Namespace: "argocd",
	}

	// Ensure the appProject has been pushed to the principal
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)

	// Ensure that the appProject has the annotation mode
	pappProject := argoapp.AppProject{}
	err = suite.PrincipalClient.Get(suite.Ctx, principalKey, &pappProject, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal("autonomous", pappProject.Annotations[appproject.AppProjectAgentModeAnnotation])

	// Modify the appProject on the autonomous-agent and ensure the change is
	// propagated to the principal
	err = suite.AutonomousAgentClient.EnsureAppProjectUpdate(suite.Ctx, agentKey, func(appProject *argoapp.AppProject) error {
		appProject.Spec.Description = "sample description"
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &appProject, metav1.GetOptions{})
		return err == nil &&
			appProject.Spec.Description == "sample description"
	}, 30*time.Second, 1*time.Second)

	// Delete the appProject from the autonomous-agent
	err = suite.AutonomousAgentClient.Delete(suite.Ctx, &appProject, metav1.DeleteOptions{})
	requires.NoError(err)

	// Ensure the appProject has been deleted from the principal
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &appProject, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)
}

func TestAppProjectTestSuite(t *testing.T) {
	suite.Run(t, new(AppProjectTestSuite))
}
