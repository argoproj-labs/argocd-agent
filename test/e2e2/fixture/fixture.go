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
	"time"

	argoapp "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
	suite.Assert().Nil(err)
	suite.T().Logf("Test begun at: %v", time.Now())
}

func (suite *BaseSuite) TearDownTest() {
	suite.T().Logf("Test ended at: %v", time.Now())
	err := CleanUp(suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient, suite.AutonomousAgentClient)
	suite.Assert().Nil(err)
}

func ensureDeletion(ctx context.Context, kclient KubeClient, app argoapp.Application) error {
	err := kclient.Delete(ctx, &app, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		// application is already deleted
		return nil
	} else if err != nil {
		return err
	}

	key := types.NamespacedName{Name: app.Name, Namespace: app.Namespace}
	for count := 0; count < 120; count++ {
		app := argoapp.Application{}
		err := kclient.Get(ctx, key, &app, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return nil
		} else if err == nil {
			time.Sleep(1 * time.Second)
		} else {
			return err
		}
	}

	return fmt.Errorf("ensureDeletion: timeout waiting for deletion of %s/%s", key.Namespace, key.Name)
}

func CleanUp(ctx context.Context, principalClient KubeClient, managedAgentClient KubeClient, autonomousAgentClient KubeClient) error {

	var list argoapp.ApplicationList
	var err error

	// Delete all managed applications from the principal
	list = argoapp.ApplicationList{}
	err = principalClient.List(ctx, "agent-managed", &list, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, app := range list.Items {
		err = ensureDeletion(ctx, principalClient, app)
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
		err = ensureDeletion(ctx, autonomousAgentClient, app)
		if err != nil {
			return err
		}
	}

	// Delete any remaining managed applications left on the managed agent
	list = argoapp.ApplicationList{}
	err = managedAgentClient.List(ctx, "agent-managed", &list, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, app := range list.Items {
		err = ensureDeletion(ctx, managedAgentClient, app)
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
		err = ensureDeletion(ctx, principalClient, app)
		if err != nil {
			return err
		}
	}

	return nil
}
