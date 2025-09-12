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
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	"github.com/argoproj/argo-cd/v3/common"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type SkipSyncTestSuite struct {
	fixture.BaseSuite
}

func (suite *SkipSyncTestSuite) Test_Application_SkipSync() {
	requires := suite.Require()

	// Create an Application on the principal's cluster with skip sync label
	app := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "skip-sync-app",
			Namespace: "agent-managed",
			Labels: map[string]string{
				config.SkipSyncLabel: "true",
			},
		},
		Spec: argoapp.ApplicationSpec{
			Project: "default",
			Source: &argoapp.ApplicationSource{
				RepoURL: "https://github.com/argoproj/argocd-example-apps.git",
				Path:    "guestbook",
			},
			Destination: argoapp.ApplicationDestination{
				Name:      "agent-managed",
				Namespace: "guestbook",
			},
		},
	}

	err := suite.PrincipalClient.Create(suite.Ctx, &app, metav1.CreateOptions{})
	requires.NoError(err)

	appKey := fixture.ToNamespacedName(&app)

	// Ensure the Application is NOT pushed to the managed-agent (should be filtered out)
	suite.Require().Never(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, appKey, &app, metav1.GetOptions{})
		return err == nil
	}, 5*time.Second, 1*time.Second, "Application with skip sync label should not be synced to agent")

	// Verify the Application still exists on the principal
	principalApp := argoapp.Application{}
	err = suite.PrincipalClient.Get(suite.Ctx, appKey, &principalApp, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal("skip-sync-app", principalApp.Name)
}

func (suite *SkipSyncTestSuite) Test_Application_SkipSync_False() {
	requires := suite.Require()

	// Create an Application on the principal's cluster with skip sync label set to false
	app := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-skip-sync-app",
			Namespace: "agent-managed",
			Labels: map[string]string{
				config.SkipSyncLabel: "false",
			},
		},
		Spec: argoapp.ApplicationSpec{
			Project: "default",
			Source: &argoapp.ApplicationSource{
				RepoURL: "https://github.com/argoproj/argocd-example-apps.git",
				Path:    "guestbook",
			},
			Destination: argoapp.ApplicationDestination{
				Server:    "https://kubernetes.default.svc",
				Namespace: "default",
			},
		},
	}

	err := suite.PrincipalClient.Create(suite.Ctx, &app, metav1.CreateOptions{})
	requires.NoError(err)

	appKey := types.NamespacedName{Name: app.Name, Namespace: "argocd"}

	// Ensure the Application IS pushed to the managed-agent (skip sync is false)
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, appKey, &app, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "Application with skip sync=false should be synced to agent")
}

func (suite *SkipSyncTestSuite) Test_AppProject_SkipSync() {
	requires := suite.Require()

	// Create an AppProject on the principal's cluster with skip sync label
	appProject := argoapp.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "skip-sync-project",
			Namespace: "argocd",
			Labels: map[string]string{
				config.SkipSyncLabel: "true",
			},
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

	projKey := fixture.ToNamespacedName(&appProject)

	// Ensure the AppProject is NOT pushed to the managed-agent
	suite.Require().Never(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 5*time.Second, 1*time.Second, "AppProject with skip sync label should not be synced to agent")

	// Verify the AppProject still exists on the principal
	principalProject := argoapp.AppProject{}
	err = suite.PrincipalClient.Get(suite.Ctx, projKey, &principalProject, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal("skip-sync-project", principalProject.Name)
}

func (suite *SkipSyncTestSuite) Test_Repository_SkipSync() {
	requires := suite.Require()

	// First create a valid AppProject that would normally allow repository sync
	appProject := argoapp.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo-project",
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

	projKey := fixture.ToNamespacedName(&appProject)

	// Wait for the AppProject to be synced to the agent
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "AppProject should be synced to agent first")

	// Create a Repository on the principal's cluster with skip sync label
	sourceRepo := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "skip-sync-repo",
			Namespace: "argocd",
			Labels: map[string]string{
				common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
				config.SkipSyncLabel:      "true",
			},
		},
		Data: map[string][]byte{
			"project": []byte("repo-project"),
			"url":     []byte("https://github.com/example/repo.git"),
		},
	}

	err = suite.PrincipalClient.Create(suite.Ctx, &sourceRepo, metav1.CreateOptions{})
	requires.NoError(err)

	repoKey := fixture.ToNamespacedName(&sourceRepo)

	// Ensure the Repository is NOT pushed to the managed-agent
	suite.Require().Never(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, repoKey, &repository, metav1.GetOptions{})
		return err == nil
	}, 5*time.Second, 1*time.Second, "Repository with skip sync label should not be synced to agent")

	// Verify the Repository still exists on the principal
	principalRepo := corev1.Secret{}
	err = suite.PrincipalClient.Get(suite.Ctx, repoKey, &principalRepo, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal("skip-sync-repo", principalRepo.Name)
}

func (suite *SkipSyncTestSuite) Test_Application_SkipSync_Update() {
	requires := suite.Require()

	// Create an Application on the principal's cluster without skip sync label
	app := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "update-skip-sync-app",
			Namespace: "agent-managed",
		},
		Spec: argoapp.ApplicationSpec{
			Project: "default",
			Source: &argoapp.ApplicationSource{
				RepoURL: "https://github.com/argoproj/argocd-example-apps.git",
				Path:    "guestbook",
			},
			Destination: argoapp.ApplicationDestination{
				Name:      "agent-managed",
				Namespace: "guestbook",
			},
		},
	}

	err := suite.PrincipalClient.Create(suite.Ctx, &app, metav1.CreateOptions{})
	requires.NoError(err)

	appKeyPrincipal := fixture.ToNamespacedName(&app)
	appKeyAgent := types.NamespacedName{Name: app.Name, Namespace: "argocd"}

	// Ensure the Application IS pushed to the managed-agent initially
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, appKeyAgent, &app, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "Application should be synced to agent initially")

	// Update the Application to add the skip sync label
	err = suite.PrincipalClient.EnsureApplicationUpdate(suite.Ctx, appKeyPrincipal, func(app *argoapp.Application) error {
		if app.Labels == nil {
			app.Labels = make(map[string]string)
		}
		app.Labels[config.SkipSyncLabel] = "true"
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Ensure the Application is removed from the managed-agent after the update
	requires.Eventually(func() bool {
		app := argoapp.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, appKeyAgent, &app, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second, "Application should be removed from agent after adding skip sync label")

	// Verify the Application still exists on the principal with the skip sync label
	principalApp := argoapp.Application{}
	err = suite.PrincipalClient.Get(suite.Ctx, appKeyPrincipal, &principalApp, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal("update-skip-sync-app", principalApp.Name)
	requires.Equal("true", principalApp.Labels[config.SkipSyncLabel])
}

func TestSkipSyncTestSuite(t *testing.T) {
	suite.Run(t, new(SkipSyncTestSuite))
}
