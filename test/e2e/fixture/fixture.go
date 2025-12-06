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

package fixture

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/manager/appproject"
	"github.com/argoproj/argo-cd/v3/common"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

const (
	// EnvVariablesFromE2EFile holds the env variables that are configured from the e2e tests
	EnvVariablesFromE2EFile = "/tmp/argocd-agent-e2e"
)

type BaseSuite struct {
	suite.Suite
	Ctx                   context.Context
	PrincipalClient       KubeClient
	ManagedAgentClient    KubeClient
	AutonomousAgentClient KubeClient
	ClusterDetails        *ClusterDetails
}

func (suite *BaseSuite) SetupSuite() {
	requires := suite.Require()

	suite.Ctx = context.Background()

	config, err := GetSystemKubeConfig("vcluster-control-plane")
	requires.Nil(err)
	suite.PrincipalClient, err = NewKubeClient(config)
	requires.Nil(err)

	config, err = GetSystemKubeConfig("vcluster-agent-managed")
	requires.Nil(err)
	suite.ManagedAgentClient, err = NewKubeClient(config)
	requires.Nil(err)

	config, err = GetSystemKubeConfig("vcluster-agent-autonomous")
	requires.Nil(err)
	suite.AutonomousAgentClient, err = NewKubeClient(config)
	requires.Nil(err)

	// Set cluster configurations
	suite.ClusterDetails = &ClusterDetails{}
	err = getClusterConfigurations(suite.Ctx, suite.ManagedAgentClient, suite.PrincipalClient, suite.ClusterDetails)
	requires.Nil(err)
}

func (suite *BaseSuite) SetupTest() {
	err := CleanUp(suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient, suite.AutonomousAgentClient, suite.ClusterDetails)
	suite.Require().Nil(err)

	// Ensure that the autonomous agent's default AppProject exists on the principal
	project := &argoapp.AppProject{}
	key := types.NamespacedName{Name: "default", Namespace: "argocd"}
	err = suite.AutonomousAgentClient.Get(suite.Ctx, key, project, metav1.GetOptions{})
	suite.Require().Nil(err)
	now := time.Now().Format(time.RFC3339)
	project.Annotations = map[string]string{"created": now}
	err = suite.AutonomousAgentClient.Update(suite.Ctx, project, metav1.UpdateOptions{})
	suite.Require().Nil(err)

	suite.Require().Eventually(func() bool {
		project := &argoapp.AppProject{}
		key := types.NamespacedName{Name: "agent-autonomous-default", Namespace: "argocd"}
		err := suite.PrincipalClient.Get(suite.Ctx, key, project, metav1.GetOptions{})
		return err == nil && len(project.Annotations) > 0 && project.Annotations["created"] == now
	}, 30*time.Second, 1*time.Second)

	suite.T().Logf("Test begun at: %v", time.Now())
}

func (suite *BaseSuite) TearDownTest() {
	suite.T().Logf("Test ended at: %v", time.Now())
	err := CleanUp(suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient, suite.AutonomousAgentClient, suite.ClusterDetails)
	suite.Require().Nil(err)
}

// EnsureDeletion will issue a delete for a namespace-scoped K8s resource, then wait for it to no longer exist
func EnsureDeletion(ctx context.Context, kclient KubeClient, obj KubeObject) error {
	// Wait for the object to be deleted  for 60 seconds
	// - Primarily this will be waiting for the finalizer to be removed, so that the object is deleted
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	for count := 0; count < 60; count++ {
		err := kclient.Delete(ctx, obj, metav1.DeleteOptions{})
		if errors.IsNotFound(err) {
			// object is already deleted
			return nil
		} else if err != nil {
			return err
		}

		err = kclient.Get(ctx, key, obj, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return nil
		} else if err == nil {
			time.Sleep(1 * time.Second)
		} else {
			return err
		}
	}

	// After X seconds, give up waiting for the child objects to be deleted, and remove any finalizers on the object
	if len(obj.GetFinalizers()) > 0 {
		err := EnsureUpdate(ctx, kclient, obj, func(obj KubeObject) {
			obj.SetFinalizers(nil)
		})
		if err != nil {
			return err
		}
	}

	// Continue waiting for object to be deleted, now that finalizers have been removed.
	key = types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	for count := 0; count < 60; count++ {
		err := kclient.Get(ctx, key, obj, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return nil
		} else if err == nil {
			time.Sleep(1 * time.Second)
		} else {
			return err
		}
	}

	return fmt.Errorf("EnsureDeletion: timeout waiting for deletion of %s/%s", key.Namespace, key.Name)
}

// WaitForDeletion will wait for a resource to be deleted
func WaitForDeletion(ctx context.Context, kclient KubeClient, obj KubeObject) error {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	for count := 0; count < 60; count++ {
		err := kclient.Get(ctx, key, obj, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("WaitForDeletion: timeout waiting for deletion of %s/%s", key.Namespace, key.Name)
}

// EnsureUpdate will ensure that the object is updated by retrying if there is a conflict.
func EnsureUpdate(ctx context.Context, kclient KubeClient, obj KubeObject, updateFn func(obj KubeObject)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
		err := kclient.Get(ctx, key, obj, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}

		// Apply the update function to the object
		updateFn(obj)

		err = kclient.Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}

		return nil
	})
}

func CleanUp(ctx context.Context, principalClient KubeClient, managedAgentClient KubeClient, autonomousAgentClient KubeClient, clusterDetails *ClusterDetails) error {

	var list argoapp.ApplicationList
	var err error

	// Remove any previously configured env variables from the config file
	os.Remove(EnvVariablesFromE2EFile)

	// Deletion should always propagate from the source of truth to the managed cluster
	// Skip deletion of resources that have been created from a source
	isFromSource := func(annotations map[string]string) bool {
		if annotations == nil {
			return false
		}
		_, ok := annotations[manager.SourceUIDAnnotation]
		return ok
	}

	// Delete all applications from the autonomous agent
	list = argoapp.ApplicationList{}
	err = autonomousAgentClient.List(ctx, "argocd", &list, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, app := range list.Items {
		if isFromSource(app.GetAnnotations()) {
			continue
		}

		err = EnsureDeletion(ctx, autonomousAgentClient, &app)
		if err != nil {
			return err
		}

		// Wait for the app to be deleted from the control plane
		app.SetNamespace("agent-autonomous")
		err = WaitForDeletion(ctx, principalClient, &app)
		if err != nil {
			return err
		}
	}

	// Delete all managed applications from the principal
	list = argoapp.ApplicationList{}
	err = principalClient.List(ctx, "agent-managed", &list, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, app := range list.Items {
		if isFromSource(app.GetAnnotations()) {
			continue
		}

		err = EnsureDeletion(ctx, principalClient, &app)
		if err != nil {
			return err
		}

		// Wait for the app to be deleted from the managed cluster
		app.SetNamespace("argocd")
		err = WaitForDeletion(ctx, managedAgentClient, &app)
		if err != nil {
			return err
		}
	}

	// Delete any remaining managed applications left on the managed agent
	list = argoapp.ApplicationList{}
	err = managedAgentClient.List(ctx, "argocd", &list, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, app := range list.Items {
		err = EnsureDeletion(ctx, managedAgentClient, &app)
		if err != nil {
			return err
		}
	}

	// Delete any remaining autonomous applications left on the principal
	list = argoapp.ApplicationList{}
	err = principalClient.List(ctx, "agent-autonomous", &list, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, app := range list.Items {
		err = EnsureDeletion(ctx, principalClient, &app)
		if err != nil {
			return err
		}
	}

	// Delete all appProjects from the autonomous agent
	appProjectList := argoapp.AppProjectList{}
	err = autonomousAgentClient.List(ctx, "argocd", &appProjectList, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, appProject := range appProjectList.Items {
		if appProject.Name == appproject.DefaultAppProjectName {
			continue
		}

		if isFromSource(appProject.GetAnnotations()) {
			continue
		}

		err = EnsureDeletion(ctx, autonomousAgentClient, &appProject)
		if err != nil {
			return err
		}

		// Wait for the appProject to be deleted from the control plane
		appProject.SetName("agent-autonomous-" + appProject.Name)
		appProject.SetNamespace("argocd")
		err = WaitForDeletion(ctx, principalClient, &appProject)
		if err != nil {
			return err
		}
	}

	// Delete all appProjects from the principal
	appProjectList = argoapp.AppProjectList{}
	err = principalClient.List(ctx, "argocd", &appProjectList, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, appProject := range appProjectList.Items {
		if appProject.Name == appproject.DefaultAppProjectName {
			continue
		}

		if isFromSource(appProject.GetAnnotations()) {
			continue
		}

		err = EnsureDeletion(ctx, principalClient, &appProject)
		if err != nil {
			return err
		}

		// Wait for the appProject to be deleted from the managed cluster
		appProject.SetNamespace("argocd")
		err = WaitForDeletion(ctx, managedAgentClient, &appProject)
		if err != nil {
			return err
		}
	}

	// Delete all appProjects from the managed agent
	appProjectList = argoapp.AppProjectList{}
	err = managedAgentClient.List(ctx, "argocd", &appProjectList, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, appProject := range appProjectList.Items {
		if appProject.Name == appproject.DefaultAppProjectName {
			continue
		}
		err = EnsureDeletion(ctx, managedAgentClient, &appProject)
		if err != nil {
			return err
		}
	}

	repoLabelSelector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
		},
	}
	repoListOpts := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(repoLabelSelector.MatchLabels).String(),
	}

	// Delete all repositories from the principal
	repoList := corev1.SecretList{}
	err = principalClient.List(ctx, "argocd", &repoList, repoListOpts)
	if err != nil {
		return err
	}
	for _, repo := range repoList.Items {
		err = EnsureDeletion(ctx, principalClient, &repo)
		if err != nil {
			return err
		}

		// Wait for the repository to be deleted from the managed cluster
		repo.SetNamespace("argocd")
		err = WaitForDeletion(ctx, managedAgentClient, &repo)
		if err != nil {
			return err
		}
	}

	// Delete all repositories from the autonomous agent
	repoList = corev1.SecretList{}
	err = autonomousAgentClient.List(ctx, "argocd", &repoList, repoListOpts)
	if err != nil {
		return err
	}
	for _, repo := range repoList.Items {
		err = EnsureDeletion(ctx, autonomousAgentClient, &repo)
		if err != nil {
			return err
		}
	}

	// Delete all repositories from the managed agent
	repoList = corev1.SecretList{}
	err = managedAgentClient.List(ctx, "argocd", &repoList, repoListOpts)
	if err != nil {
		return err
	}
	for _, repo := range repoList.Items {
		err = EnsureDeletion(ctx, managedAgentClient, &repo)
		if err != nil {
			return err
		}
	}

	// Remove known finalizer-containing resources from guestbook ns (before we delete the NS in the next step)
	deploymentList := appsv1.DeploymentList{}
	err = managedAgentClient.List(ctx, "guestbook", &deploymentList, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, deployment := range deploymentList.Items {
		if len(deployment.Finalizers) > 0 {
			deployment.Finalizers = nil
			if err := managedAgentClient.Update(ctx, &deployment, metav1.UpdateOptions{}); err != nil {
				return err
			}
		}
	}

	removeJobFinalizers := func(ctx context.Context, kclient KubeClient) error {
		jobList := batchv1.JobList{}
		err = kclient.List(ctx, "guestbook", &jobList, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for _, job := range jobList.Items {
			if len(job.Finalizers) > 0 {
				err := EnsureUpdate(ctx, kclient, &job, func(obj KubeObject) {
					obj.SetFinalizers(nil)
				})
				if err != nil {
					return err
				}
			}
		}
		return nil
	}

	err = removeJobFinalizers(ctx, managedAgentClient)
	if err != nil {
		return err
	}

	err = removeJobFinalizers(ctx, autonomousAgentClient)
	if err != nil {
		return err
	}

	// Delete any left over namespaces
	ns := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "guestbook"}}
	err = EnsureDeletion(ctx, managedAgentClient, &ns)
	if err != nil {
		return err
	}
	err = EnsureDeletion(ctx, autonomousAgentClient, &ns)
	if err != nil {
		return err
	}

	return resetManagedAgentClusterInfo(clusterDetails)
}

// resetManagedAgentClusterInfo resets the cluster info in the redis cache for the managed agent
func resetManagedAgentClusterInfo(clusterDetails *ClusterDetails) error {
	// Reset cluster info in redis cache
	if err := getCacheInstance(AgentManagedName, clusterDetails).SetClusterInfo(AgentClusterServerURL, &argoapp.ClusterInfo{}); err != nil {
		fmt.Println("resetManagedAgentClusterInfo: error", err)
		return err
	}
	return nil
}
