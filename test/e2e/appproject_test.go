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
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
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

	err := suite.PrincipalClient.Create(suite.Ctx, &appProject, metav1.CreateOptions{})
	requires.NoError(err)

	principalKey := fixture.ToNamespacedName(&appProject)
	managedKey := types.NamespacedName{Name: appProject.Name, Namespace: fixture.ManagedAgentNamespace}

	// Ensure the appProject has been pushed to the managed-agent
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, managedKey, &appProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second, "GET appProject from managed-agent")

	// Ensure that the appProject is specific to the agent it is synced to
	appProject = argoapp.AppProject{}
	err = suite.PrincipalClient.Get(suite.Ctx, principalKey, &appProject, metav1.GetOptions{})
	requires.NoError(err)
	mappProject := argoapp.AppProject{}
	err = suite.ManagedAgentClient.Get(suite.Ctx, managedKey, &mappProject, metav1.GetOptions{})
	requires.NoError(err)
	requires.Len(mappProject.Spec.Destinations, 1)
	requires.Equal("in-cluster", mappProject.Spec.Destinations[0].Name)
	requires.Equal("https://kubernetes.default.svc", mappProject.Spec.Destinations[0].Server)

	if fixture.IsDestinationBased() {
		requires.Equal(appProject.Spec.SourceNamespaces, mappProject.Spec.SourceNamespaces)
	} else {
		requires.Nil(mappProject.Spec.SourceNamespaces)
	}

	// Modify the appProject on the principal and ensure the change is propagated
	// to the managed-agent
	err = suite.PrincipalClient.EnsureAppProjectUpdate(suite.Ctx, principalKey, func(appProject *argoapp.AppProject) error {
		appProject.Spec.Description = "sample description"
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, managedKey, &appProject, metav1.GetOptions{})
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
		err := suite.ManagedAgentClient.Get(suite.Ctx, managedKey, &appProject, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)
}

// Ensure that when an (autonomous) AppProject is created with roles already defined, that these are correctly to principal
func (suite *AppProjectTestSuite) Test_AppProject_Autonomous_CreateWithProjectRole() {
	requires := suite.Require()

	// Create an autonomousAppProject on the autonomous-agent's cluster with project roles included from creation
	autonomousAppProject := argoapp.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample",
			Namespace: fixture.AutonomousAgentNamespace,
		},
		Spec: argoapp.AppProjectSpec{
			Destinations: []argoapp.ApplicationDestination{
				{
					Namespace: "*",
					Name:      "agent-*",
				},
			},
			SourceNamespaces: []string{"agent-*"},
			Roles: []argoapp.ProjectRole{
				{
					Name:        "read-only",
					Description: "Read-only access",
					Policies: []string{
						// 1) we expect proj: field and object field to be modified
						"p, proj:sample:read-only, applications, get, sample/*, allow",
						// 2) Since 'extensions' is not one of the resource we touch, this should not be modified.
						"p, example-user, extensions, invoke, httpbin, allow",
						// 3) Since 'differentrole' doesn't match the name of the Role field, this should not be modified. (In real world this would be user config error)
						"p, proj:sample:differentrole, applications, get, sample/*, allow",
						// 4) Since 'diffproject' does match the name of the AppProject, this should not be modified.  (In real world this would be user config error)
						"p, proj:diffproject:read-only, applications, get, sample/*, allow",
					},
				},
			},
		},
	}

	err := suite.AutonomousAgentClient.Create(suite.Ctx, &autonomousAppProject, metav1.CreateOptions{})
	requires.NoError(err)

	principalKey := types.NamespacedName{
		Name:      "agent-autonomous-sample",
		Namespace: fixture.PrincipalNamespace,
	}

	// Verify that roles are synced to the corresponding AppProject on principal, with policies transformed for the autonomous agent context:
	requires.Eventually(func() bool {
		principalAppProject := argoapp.AppProject{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &principalAppProject, metav1.GetOptions{})
		if err != nil {
			suite.T().Logf("failed to get appProject from principal: %v", err)
			return false
		}
		if len(principalAppProject.Spec.Roles) != 1 {
			suite.T().Logf("expected 1 role, got %d", len(principalAppProject.Spec.Roles))
			return false
		}
		role := principalAppProject.Spec.Roles[0]
		if role.Name != "read-only" {
			suite.T().Logf("expected role name 'read-only', got %q", role.Name)
			return false
		}
		if len(role.Policies) != 4 {
			suite.T().Logf("expected 4 policies, got %d", len(role.Policies))
			return false
		}
		expectedPolicy := "p, proj:agent-autonomous-sample:read-only, applications, get, agent-autonomous-sample/agent-autonomous/*, allow"
		if role.Policies[0] != expectedPolicy {
			suite.T().Logf("policy[0] mismatch: got %q, expected %q", role.Policies[0], expectedPolicy)
			return false
		}
		expectedPolicy = "p, example-user, extensions, invoke, httpbin, allow"
		if role.Policies[1] != expectedPolicy {
			suite.T().Logf("policy[1] mismatch: got %q, expected %q", role.Policies[1], expectedPolicy)
			return false
		}
		expectedPolicy = "p, proj:sample:differentrole, applications, get, " + autonomousAppProject.Name + "/*, allow"
		if role.Policies[2] != expectedPolicy {
			suite.T().Logf("policy[2] mismatch: got %q, expected %q", role.Policies[2], expectedPolicy)
			return false
		}
		expectedPolicy = "p, proj:diffproject:read-only, applications, get, " + autonomousAppProject.Name + "/*, allow"
		if role.Policies[3] != expectedPolicy {
			suite.T().Logf("policy[3] mismatch: got %q, expected %q", role.Policies[3], expectedPolicy)
			return false
		}
		suite.T().Logf("policies match: %v", strings.Join(role.Policies, "  |  "))
		return true
	}, 30*time.Second, 1*time.Second)

	// Delete the appProject from the autonomous-agent
	err = suite.AutonomousAgentClient.Delete(suite.Ctx, &autonomousAppProject, metav1.DeleteOptions{})
	requires.NoError(err)

	// Ensure the appProject has been deleted from the principal
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &appProject, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)
}

func (suite *AppProjectTestSuite) Test_AppProject_Autonomous() {
	requires := suite.Require()

	// Create an autonomousAppProject on the autonomous-agent's cluster
	autonomousAppProject := argoapp.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample",
			Namespace: fixture.AutonomousAgentNamespace,
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

	err := suite.AutonomousAgentClient.Create(suite.Ctx, &autonomousAppProject, metav1.CreateOptions{})
	requires.NoError(err)

	principalKey := types.NamespacedName{
		Name:      "agent-autonomous-sample",
		Namespace: fixture.PrincipalNamespace,
	}
	agentKey := types.NamespacedName{
		Name:      autonomousAppProject.Name,
		Namespace: fixture.AutonomousAgentNamespace,
	}

	// Ensure the appProject has been pushed to the principal
	requires.Eventually(func() bool {
		principalAppProject := argoapp.AppProject{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &principalAppProject, metav1.GetOptions{})
		return err == nil
	}, 30*time.Second, 1*time.Second)

	// Modify the appProject on the autonomous-agent and ensure the change is
	// propagated to the principal
	err = suite.AutonomousAgentClient.EnsureAppProjectUpdate(suite.Ctx, agentKey, func(appProjectParam *argoapp.AppProject) error {
		appProjectParam.Spec.Description = "sample description"
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	requires.Eventually(func() bool {
		principalAppProject := argoapp.AppProject{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &principalAppProject, metav1.GetOptions{})
		return err == nil &&
			principalAppProject.Spec.Description == "sample description"
	}, 30*time.Second, 1*time.Second)

	// Update autonomous agent AppProject .spec.roles to add a new role with policies
	err = suite.AutonomousAgentClient.EnsureAppProjectUpdate(suite.Ctx, agentKey, func(appProjectParam *argoapp.AppProject) error {
		appProjectParam.Spec.Roles = []argoapp.ProjectRole{
			{
				Name:        "read-only",
				Description: "Read-only access",
				Policies: []string{
					// 1) we expect proj: field and object field to be modified
					"p, proj:sample:read-only, applications, get, " + appProjectParam.Name + "/*, allow",
					// 2) Since 'extensions' is not one of the resource we touch, this should not be modified.
					"p, example-user, extensions, invoke, httpbin, allow",
					// 3) Since 'differentrole' doesn't match the name of the Role field, this should not be modified. (In real world this would be user config error)
					"p, proj:sample:differentrole, applications, get, " + appProjectParam.Name + "/*, allow",
					// 4) Since 'diffproject' does match the name of the AppProject, this should not be modified.  (In real world this would be user config error)
					"p, proj:diffproject:read-only, applications, get, " + appProjectParam.Name + "/*, allow",
				},
			},
		}
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Verify that roles are synced to the corresponding AppProject on principal, with policies transformed for the autonomous agent context:
	// policy at 0:
	// - Role project reference is prefixed: proj:sample:read-only -> proj:agent-autonomous-sample:read-only
	// - Object field gains agent namespace: sample/* -> sample/agent-autonomous/*
	// policy at 1:
	// - unchanged
	requires.Eventually(func() bool {
		principalAppProject := argoapp.AppProject{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &principalAppProject, metav1.GetOptions{})
		if err != nil {
			suite.T().Logf("failed to get appProject from principal: %v", err)
			return false
		}
		if len(principalAppProject.Spec.Roles) != 1 {
			suite.T().Logf("expected 1 role, got %d", len(principalAppProject.Spec.Roles))
			return false
		}
		role := principalAppProject.Spec.Roles[0]
		if role.Name != "read-only" {
			suite.T().Logf("expected role name 'read-only', got %q", role.Name)
			return false
		}
		if len(role.Policies) != 4 {
			suite.T().Logf("expected 4 policies, got %d", len(role.Policies))
			return false
		}
		expectedPolicy := "p, proj:agent-autonomous-sample:read-only, applications, get, agent-autonomous-sample/agent-autonomous/*, allow"
		if role.Policies[0] != expectedPolicy {
			suite.T().Logf("policy[0] mismatch: got %q, expected %q", role.Policies[0], expectedPolicy)
			return false
		}
		expectedPolicy = "p, example-user, extensions, invoke, httpbin, allow"
		if role.Policies[1] != expectedPolicy {
			suite.T().Logf("policy[1] mismatch: got %q, expected %q", role.Policies[1], expectedPolicy)
			return false
		}
		expectedPolicy = "p, proj:sample:differentrole, applications, get, " + autonomousAppProject.Name + "/*, allow"
		if role.Policies[2] != expectedPolicy {
			suite.T().Logf("policy[2] mismatch: got %q, expected %q", role.Policies[2], expectedPolicy)
			return false
		}
		expectedPolicy = "p, proj:diffproject:read-only, applications, get, " + autonomousAppProject.Name + "/*, allow"
		if role.Policies[3] != expectedPolicy {
			suite.T().Logf("policy[3] mismatch: got %q, expected %q", role.Policies[3], expectedPolicy)
			return false
		}
		suite.T().Logf("policies match: %v", strings.Join(role.Policies, "  |  "))
		return true
	}, 30*time.Second, 1*time.Second)

	// Delete the appProject from the autonomous-agent
	err = suite.AutonomousAgentClient.Delete(suite.Ctx, &autonomousAppProject, metav1.DeleteOptions{})
	requires.NoError(err)

	// Ensure the appProject has been deleted from the principal
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &appProject, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second)
}

// Default AppProject updates on the principal should be propagated to the managed agent
func (suite *AppProjectTestSuite) Test_AppProject_Default_Managed() {
	requires := suite.Require()

	// Default appProject must exist on both the principal and the managed agent's cluster
	appProject := argoapp.AppProject{}
	principalKey := types.NamespacedName{
		Name:      "default",
		Namespace: fixture.PrincipalNamespace,
	}
	agentKey := types.NamespacedName{
		Name:      "default",
		Namespace: fixture.ManagedAgentNamespace,
	}

	err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &appProject, metav1.GetOptions{})
	requires.NoError(err)

	agentAppProject := argoapp.AppProject{}
	err = suite.ManagedAgentClient.Get(suite.Ctx, agentKey, &agentAppProject, metav1.GetOptions{})
	requires.NoError(err)

	// Update the default appProject on the principal's cluster
	testDescription := "default appProject"
	err = suite.PrincipalClient.EnsureAppProjectUpdate(suite.Ctx, principalKey, func(appProject *argoapp.AppProject) error {
		appProject.Spec.Description = testDescription
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Reset the default appProject on the principal's cluster
	defer func() {
		err = suite.PrincipalClient.EnsureAppProjectUpdate(suite.Ctx, principalKey, func(appProject *argoapp.AppProject) error {
			appProject.Spec.Description = ""
			return nil
		}, metav1.UpdateOptions{})
		requires.NoError(err)
	}()

	// Ensure the default appProject has been updated on the managed agent's cluster
	requires.Eventually(func() bool {
		err := suite.ManagedAgentClient.Get(suite.Ctx, agentKey, &appProject, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return appProject.Spec.Description == testDescription
	}, 30*time.Second, 1*time.Second)
}

// Default AppProject updates on the autonomous agent should be propagated to the principal
func (suite *AppProjectTestSuite) Test_AppProject_Default_Autonomous() {
	requires := suite.Require()

	// Default appProject must exist on both the autonomous agent and the principal's cluster
	appProject := argoapp.AppProject{}
	key := types.NamespacedName{
		Name:      "default",
		Namespace: fixture.AutonomousAgentNamespace,
	}

	principalKey := types.NamespacedName{
		Name:      "agent-autonomous-default",
		Namespace: fixture.PrincipalNamespace,
	}

	err := suite.AutonomousAgentClient.Get(suite.Ctx, key, &appProject, metav1.GetOptions{})
	requires.NoError(err)

	principalAppProject := argoapp.AppProject{}
	err = suite.PrincipalClient.Get(suite.Ctx, principalKey, &principalAppProject, metav1.GetOptions{})
	requires.NoError(err)

	// Update the default appProject on the autonomous agent's cluster
	testDescription := "default appProject"
	err = suite.AutonomousAgentClient.EnsureAppProjectUpdate(suite.Ctx, key, func(appProject *argoapp.AppProject) error {
		appProject.Spec.Description = testDescription
		return nil
	}, metav1.UpdateOptions{})
	requires.NoError(err)

	// Reset the default appProject on the autonomous agent's cluster
	defer func() {
		err = suite.AutonomousAgentClient.EnsureAppProjectUpdate(suite.Ctx, key, func(appProject *argoapp.AppProject) error {
			appProject.Spec.Description = ""
			return nil
		}, metav1.UpdateOptions{})
		requires.NoError(err)
	}()

	// Ensure the default appProject has been updated on the principal's cluster
	requires.Eventually(func() bool {
		appProject := argoapp.AppProject{}
		err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &appProject, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return appProject.Spec.Description == testDescription
	}, 30*time.Second, 1*time.Second)
}

func TestAppProjectTestSuite(t *testing.T) {
	suite.Run(t, new(AppProjectTestSuite))
}
