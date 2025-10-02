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
	"fmt"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	"github.com/argoproj/argo-cd/v3/common"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RepositoryTestSuite struct {
	fixture.BaseSuite
}

func (suite *RepositoryTestSuite) Test_Repository_Managed() {
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

	projKey := fixture.ToNamespacedName(&appProject)

	// Ensure the appProject has been pushed to the managed-agent
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET appProject from managed-agent")

	// Create a repository on the principal's cluster
	sourceRepo := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample",
			Namespace: "argocd",
			Labels: map[string]string{
				common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
			},
		},
		Data: map[string][]byte{
			"project": []byte("sample"),
			"url":     []byte("https://github.com/example/repo.git"),
		},
	}

	err = suite.PrincipalClient.Create(suite.Ctx, &sourceRepo, metav1.CreateOptions{})
	requires.NoError(err)

	key := fixture.ToNamespacedName(&sourceRepo)
	// Ensure the repository has been pushed to the managed-agent
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")

	// Ensure the repository on the managed agent has the source UID annotation
	repository := corev1.Secret{}
	err = suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
	requires.NoError(err)
	requires.Equal(string(sourceRepo.UID), repository.Annotations[manager.SourceUIDAnnotation])
	requires.Equal(repository.Data, sourceRepo.Data)

	// Ensure the repository is not pushed to the autonomous agent
	requires.Never(func() bool {
		repository := corev1.Secret{}
		err := suite.AutonomousAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err == nil
	}, 10*time.Second, 1*time.Second)

	// Update the repository and verify if the changes are propagated to the agent
	updatedRepo := "https://github.com/example/repo-updated.git"
	err = suite.PrincipalClient.EnsureRepositoryUpdate(suite.Ctx, key, func(repo *corev1.Secret) error {
		repo.Data["url"] = []byte(updatedRepo)
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Ensure the repository is updated on the managed agent
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		if err != nil {
			fmt.Println("error getting repository", err)
			return false
		}
		if string(repository.Data["url"]) != updatedRepo {
			fmt.Println("repository url does not match", string(repository.Data["url"]), updatedRepo)
			return false
		}
		return true
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")

	// Delete the repository on the principal
	err = suite.PrincipalClient.Delete(suite.Ctx, &sourceRepo, metav1.DeleteOptions{})
	requires.NoError(err)

	// Ensure the repository has been deleted from the managed-agent
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")
}

func (suite *RepositoryTestSuite) Test_Repository_Late_AppProjectCreation() {
	requires := suite.Require()

	// Create a repository on the principal's cluster
	sourceRepo := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample",
			Namespace: "argocd",
			Labels: map[string]string{
				common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
			},
		},
		Data: map[string][]byte{
			"project": []byte("sample"),
			"url":     []byte("https://github.com/example/repo.git"),
		},
	}

	err := suite.PrincipalClient.Create(suite.Ctx, &sourceRepo, metav1.CreateOptions{})
	requires.NoError(err)

	key := fixture.ToNamespacedName(&sourceRepo)
	// Ensure the repository is not created on the managed-agent
	requires.Never(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err == nil
	}, 5*time.Second, 1*time.Second, "GET repository from managed-agent")

	// Create the appProject on the principal's cluster
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

	err = suite.PrincipalClient.Create(suite.Ctx, &appProject, metav1.CreateOptions{})
	requires.NoError(err)

	projKey := fixture.ToNamespacedName(&appProject)

	// Ensure the appProject has been pushed to the managed-agent
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET appProject from managed-agent")

	// Ensure the repository is created on the managed-agent
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")

	// Delete the appProject on the principal's cluster
	err = suite.PrincipalClient.Delete(suite.Ctx, &appProject, metav1.DeleteOptions{})
	requires.NoError(err)

	// Delete the repository on the principal's cluster
	// The principal should rely on the cached repo to agent mapping to delete the repository
	err = suite.PrincipalClient.Delete(suite.Ctx, &sourceRepo, metav1.DeleteOptions{})
	requires.NoError(err)

	// Ensure the repository has been deleted from the managed-agent
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")
}

func (suite *RepositoryTestSuite) Test_Repository_AppProjectUpdate() {
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

	projKey := fixture.ToNamespacedName(&appProject)

	// Ensure the appProject has been pushed to the managed-agent
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET appProject from managed-agent")

	// Create a repository on the principal's cluster
	sourceRepo := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample",
			Namespace: "argocd",
			Labels: map[string]string{
				common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
			},
		},
		Data: map[string][]byte{
			"project": []byte("sample"),
			"url":     []byte("https://github.com/example/repo.git"),
		},
	}

	err = suite.PrincipalClient.Create(suite.Ctx, &sourceRepo, metav1.CreateOptions{})
	requires.NoError(err)

	key := fixture.ToNamespacedName(&sourceRepo)
	// Ensure the repository has been pushed to the managed-agent
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")

	// Update the appProject on the principal's cluster to no longer match the agent name
	err = suite.PrincipalClient.EnsureAppProjectUpdate(suite.Ctx, projKey, func(appProject *argoapp.AppProject) error {
		appProject.Spec.SourceNamespaces = []string{"random"}
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Ensure the repository is deleted from the managed-agent
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")
}

func (suite *RepositoryTestSuite) Test_Repository_Change_AppProject() {
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

	projKey := fixture.ToNamespacedName(&appProject)

	// Ensure the appProject has been pushed to the managed-agent
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET appProject from managed-agent")

	// Create a repository on the principal's cluster
	sourceRepo := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample",
			Namespace: "argocd",
			Labels: map[string]string{
				common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
			},
		},
		Data: map[string][]byte{
			"project": []byte("sample"),
			"url":     []byte("https://github.com/example/repo.git"),
		},
	}

	err = suite.PrincipalClient.Create(suite.Ctx, &sourceRepo, metav1.CreateOptions{})
	requires.NoError(err)

	key := fixture.ToNamespacedName(&sourceRepo)
	// Ensure the repository has been pushed to the managed-agent
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")

	// Create a new appProject that no longer matches the agent name
	appProject2 := appProject.DeepCopy()
	appProject2.Name = "sample2"
	appProject2.ResourceVersion = ""
	appProject2.Spec.SourceNamespaces = []string{"random"}

	err = suite.PrincipalClient.Create(suite.Ctx, appProject2, metav1.CreateOptions{})
	requires.NoError(err)

	// Update the repository to refer the new appProject
	err = suite.PrincipalClient.EnsureRepositoryUpdate(suite.Ctx, key, func(repo *corev1.Secret) error {
		repo.Data["project"] = []byte("sample2")
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Ensure the repository is deleted from the managed-agent since it no longer matches the agent name
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")
}

func (suite *RepositoryTestSuite) Test_Repository_Change_Reference_SameAgent() {
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

	projKey := fixture.ToNamespacedName(&appProject)

	// Ensure the appProject has been pushed to the managed-agent
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, projKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET appProject from managed-agent")

	// Create a repository on the principal's cluster
	sourceRepo := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample",
			Namespace: "argocd",
			Labels: map[string]string{
				common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
			},
		},
		Data: map[string][]byte{
			"project": []byte("sample"),
			"url":     []byte("https://github.com/example/repo.git"),
		},
	}

	err = suite.PrincipalClient.Create(suite.Ctx, &sourceRepo, metav1.CreateOptions{})
	requires.NoError(err)

	key := fixture.ToNamespacedName(&sourceRepo)
	// Ensure the repository has been pushed to the managed-agent
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")

	// Delete the appProject on the principal's cluster
	err = suite.PrincipalClient.Delete(suite.Ctx, &appProject, metav1.DeleteOptions{})
	requires.NoError(err)

	// Update the reference to a new appProject
	appProject2 := argoapp.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample2",
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

	err = suite.PrincipalClient.Create(suite.Ctx, &appProject2, metav1.CreateOptions{})
	requires.NoError(err)

	// Update the repository to refer the new appProject
	err = suite.PrincipalClient.EnsureRepositoryUpdate(suite.Ctx, key, func(repo *corev1.Secret) error {
		repo.Data["project"] = []byte("sample2")
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Ensure the repository is present on the managed-agent
	requires.Eventually(func() bool {
		repository := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &repository, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET repository from managed-agent")
}

func TestRepositoryTestSuite(t *testing.T) {
	suite.Run(t, new(RepositoryTestSuite))
}
