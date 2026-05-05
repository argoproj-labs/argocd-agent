// Copyright 2026 The argocd-agent Authors
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

type RepoCredsTestSuite struct {
	fixture.BaseSuite
}

// Create a new repo-creds secret
func newRepoCreds() corev1.Secret {
	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo-creds",
			Namespace: fixture.PrincipalNamespace,
			Labels: map[string]string{
				common.LabelKeySecretType: common.LabelValueSecretTypeRepoCreds,
			},
		},
		Data: map[string][]byte{
			"project": []byte("test-app-project"),
			"url":     []byte("https://github.com/example/"),
		},
	}
}

// Create a new appProject on the principal's cluster and ensure it is synced to the managed-agent
func (suite *RepoCredsTestSuite) createAppProjectAndWaitForSync() *argoapp.AppProject {
	requires := suite.Require()
	appProject := &argoapp.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app-project",
			Namespace: fixture.PrincipalNamespace,
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

	requires.NoError(suite.PrincipalClient.Create(suite.Ctx, appProject, metav1.CreateOptions{}))

	key := fixture.ToNamespacedName(appProject)
	requires.Eventually(func() bool {
		tempAppProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &tempAppProject, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second, "appProject should be synced to the managed-agent")

	return appProject
}

// Create a new repo-creds secret on the principal's cluster and ensure it is synced to the managed-agent
func (suite *RepoCredsTestSuite) createRepoCredsAndWaitForSync() corev1.Secret {
	requires := suite.Require()
	repoCreds := newRepoCreds()

	requires.NoError(suite.PrincipalClient.Create(suite.Ctx, &repoCreds, metav1.CreateOptions{}))

	key := fixture.ToNamespacedName(&repoCreds)
	requires.Eventually(func() bool {
		tempRepoCreds := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &tempRepoCreds, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second, "repo-creds should be synced to the managed-agent")

	return repoCreds
}

// This test verifies that repo credential secret's creation/deletion/update on the principal's cluster
// is propagated to the managed-agent.
func (suite *RepoCredsTestSuite) Test_RepoCreds_Managed() {
	requires := suite.Require()

	// Create the appProject and repo-creds on the principal's cluster
	// and ensure they are synced to the managed-agent
	suite.createAppProjectAndWaitForSync()
	repoCredsPrincipal := suite.createRepoCredsAndWaitForSync()
	key := fixture.ToNamespacedName(&repoCredsPrincipal)

	// Ensure the repo-creds on the managed agent has the source UID annotation
	repoCredsAgent := corev1.Secret{}
	requires.NoError(suite.ManagedAgentClient.Get(suite.Ctx, key, &repoCredsAgent, metav1.GetOptions{}))
	requires.Equal(string(repoCredsPrincipal.UID), repoCredsAgent.Annotations[manager.SourceUIDAnnotation])
	requires.Equal(repoCredsAgent.Data, repoCredsPrincipal.Data)

	// Ensure the repo-creds is not pushed to the autonomous agent
	requires.Never(func() bool {
		rc := corev1.Secret{}
		err := suite.AutonomousAgentClient.Get(suite.Ctx, key, &rc, metav1.GetOptions{})
		return err == nil
	}, 20*time.Second, 1*time.Second)

	// Update the repo-creds and verify changes are propagated to the managed-agent
	updatedURL := "https://github.com/example/updated/"
	err := suite.PrincipalClient.EnsureRepositoryUpdate(suite.Ctx, key, func(repo *corev1.Secret) error {
		repo.Data["url"] = []byte(updatedURL)
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	requires.Eventually(func() bool {
		tempRepoCreds := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &tempRepoCreds, metav1.GetOptions{})
		if err != nil {
			fmt.Println("error getting repo-creds", err)
			return false
		}
		if string(tempRepoCreds.Data["url"]) != updatedURL {
			fmt.Println("repo-creds url does not match", string(tempRepoCreds.Data["url"]), updatedURL)
			return false
		}
		return true
	}, 60*time.Second, 1*time.Second, "repo-creds should be updated on the managed-agent")

	// Delete the repo-creds on the principal and ensure it is deleted from the managed-agent
	requires.NoError(suite.PrincipalClient.Delete(suite.Ctx, &repoCredsPrincipal, metav1.DeleteOptions{}))
	requires.Eventually(func() bool {
		tempRepoCreds := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &tempRepoCreds, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 60*time.Second, 1*time.Second, "repo-creds should be deleted from the managed-agent")
}

// This test verifies:
// 1. A repo-creds secret is created on the principal's cluster and not synced to the managed-agent without matching AppProject
// 2. Deletion of appProject on the principal cluster deletes the repo-creds from the managed-agent
func (suite *RepoCredsTestSuite) Test_RepoCreds_without_AppProject() {
	requires := suite.Require()

	// Create the repo-creds on the principal's cluster
	repoCredsPrincipal := newRepoCreds()
	requires.NoError(suite.PrincipalClient.Create(suite.Ctx, &repoCredsPrincipal, metav1.CreateOptions{}))
	repoCredsKey := fixture.ToNamespacedName(&repoCredsPrincipal)

	// Ensure the repo-creds is not synced to the managed-agent without a matching AppProject
	requires.Never(func() bool {
		tempRepoCreds := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, repoCredsKey, &tempRepoCreds, metav1.GetOptions{})
		return err == nil
	}, 20*time.Second, 1*time.Second, "repo-creds should not be created on the managed-agent without an AppProject")

	// Create the appProject on the principal's cluster and ensure it is synced to the managed-agent
	appProject := suite.createAppProjectAndWaitForSync()

	// Now the repo-creds should be synced to the managed-agent, since the appProject is present
	requires.Eventually(func() bool {
		rc := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, repoCredsKey, &rc, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second, "repo-creds should be created on the managed-agent")

	// Delete the appProject on the principal's cluster
	requires.NoError(suite.PrincipalClient.Delete(suite.Ctx, appProject, metav1.DeleteOptions{}))

	// Ensure the repo-creds is deleted from the managed-agent, since the appProject is deleted
	requires.Eventually(func() bool {
		tempRepoCreds := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, repoCredsKey, &tempRepoCreds, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 60*time.Second, 1*time.Second, "repo-creds should be deleted from the managed-agent")

	// Clean up the repo-creds from the principal
	requires.NoError(suite.PrincipalClient.Delete(suite.Ctx, &repoCredsPrincipal, metav1.DeleteOptions{}))
}

// This test verifies:
//  1. A repo-creds secret is created on the principal's cluster and synced to the managed-agent
//  2. Updating the appProject on the principal cluster to no longer match
//     the agent name results in the repo-creds being deleted from the managed-agent
func (suite *RepoCredsTestSuite) Test_RepoCreds_AppProjectUpdate() {
	requires := suite.Require()

	// Create the appProject and repo-creds on the principal's cluster
	// and ensure they are synced to the managed-agent
	appProjectPrincipal := suite.createAppProjectAndWaitForSync()
	repoCredsPrincipal := suite.createRepoCredsAndWaitForSync()

	appProjectKey := fixture.ToNamespacedName(appProjectPrincipal)
	repoCredsKey := fixture.ToNamespacedName(&repoCredsPrincipal)

	// Update the appProject on the principal's cluster to no longer match the agent name
	err := suite.PrincipalClient.EnsureAppProjectUpdate(suite.Ctx, appProjectKey, func(ap *argoapp.AppProject) error {
		ap.Spec.SourceNamespaces = []string{"random"}
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Ensure the repo-creds is deleted from the managed-agent, since the appProject no longer matches the agent name
	requires.Eventually(func() bool {
		tempRepoCreds := corev1.Secret{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, repoCredsKey, &tempRepoCreds, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 60*time.Second, 1*time.Second, "repo-creds should be deleted from the managed-agent")
}

func TestRepoCredsTestSuite(t *testing.T) {
	suite.Run(t, new(RepoCredsTestSuite))
}
