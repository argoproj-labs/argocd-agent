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

	"github.com/argoproj-labs/argocd-agent/internal/manager/appproject"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
}

func (suite *BaseSuite) SetupTest() {
	err := CleanUp(suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient, suite.AutonomousAgentClient)
	suite.Require().Nil(err)
	suite.T().Logf("Test begun at: %v", time.Now())
}

func (suite *BaseSuite) TearDownTest() {
	suite.T().Logf("Test ended at: %v", time.Now())
	err := CleanUp(suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient, suite.AutonomousAgentClient)
	suite.Require().Nil(err)
}

// EnsureDeletion will issue a delete for a namespace-scoped K8s resource, then wait for it to no longer exist
func EnsureDeletion(ctx context.Context, kclient KubeClient, obj KubeObject) error {
	err := kclient.Delete(ctx, obj, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		// object is already deleted
		return nil
	} else if err != nil {
		return err
	}

	// Wait for the object to be deleted  for 60 seconds
	// - Primarily this will be waiting for the finalizer to be removed, so that the object is deleted
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
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

	// After X seconds, give up waiting for the child objects to be deleted, and remove any finalizers on the object
	if len(obj.GetFinalizers()) > 0 {
		obj.SetFinalizers(nil)
		if err := kclient.Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
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

func CleanUp(ctx context.Context, principalClient KubeClient, managedAgentClient KubeClient, autonomousAgentClient KubeClient) error {

	var list argoapp.ApplicationList
	var err error

	// Remove any previously configured env variables from the config file
	os.Remove(EnvVariablesFromE2EFile)

	// Delete all managed applications from the principal
	list = argoapp.ApplicationList{}
	err = principalClient.List(ctx, "agent-managed", &list, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, app := range list.Items {
		err = EnsureDeletion(ctx, principalClient, &app)
		if err != nil {
			return err
		}
	}

	// Delete all applications from the autonomous agent
	list = argoapp.ApplicationList{}
	err = autonomousAgentClient.List(ctx, "argocd", &list, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, app := range list.Items {
		err = EnsureDeletion(ctx, autonomousAgentClient, &app)
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

	// Delete all appProjects from the principal
	appProjectList := argoapp.AppProjectList{}
	err = principalClient.List(ctx, "argocd", &appProjectList, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, appProject := range appProjectList.Items {
		if appProject.Name == appproject.DefaultAppProjectName {
			continue
		}
		err = EnsureDeletion(ctx, principalClient, &appProject)
		if err != nil {
			return err
		}
	}

	// Delete all appProjects from the autonomous agent
	appProjectList = argoapp.AppProjectList{}
	err = autonomousAgentClient.List(ctx, "argocd", &appProjectList, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, appProject := range appProjectList.Items {
		if appProject.Name == appproject.DefaultAppProjectName {
			continue
		}
		err = EnsureDeletion(ctx, autonomousAgentClient, &appProject)
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

	return nil
}
