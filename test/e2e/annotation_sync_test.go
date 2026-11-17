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
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	"github.com/argoproj/argo-cd/v3/common"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	// mirroredAnnotation follows the source-of-truth to peer behaviour.
	mirroredAnnotation = common.AnnotationKeyAppSkipReconcile
	// childAppAnnotation is only meaningful on a child Application of an
	// app-of-apps, so argocd-agent never transfers it.
	childAppAnnotation = "argocd.argoproj.io/sync-wave"
	// unknownAnnotation stands in for anything a user or another controller
	// might set that argocd-agent does not know about.
	unknownAnnotation = "e2e.example.com/owner"
)

type AnnotationSyncTestSuite struct {
	fixture.BaseSuite
}

// annotationsOf returns the annotations of the named Application, or nil when
// it cannot be read.
func annotationsOf(ctx context.Context, client fixture.KubeClient, key types.NamespacedName) (map[string]string, error) {
	app := argoapp.Application{}
	if err := client.Get(ctx, key, &app, metav1.GetOptions{}); err != nil {
		return nil, err
	}
	return app.Annotations, nil
}

// eventuallyHasAnnotation waits until key is present on the Application with
// the given value.
func eventuallyHasAnnotation(t *testing.T, ctx context.Context, client fixture.KubeClient, appKey types.NamespacedName, key, value, msg string) {
	t.Helper()
	require.Eventually(t, func() bool {
		annotations, err := annotationsOf(ctx, client, appKey)
		if err != nil {
			return false
		}
		return annotations[key] == value
	}, 60*time.Second, 1*time.Second, msg)
}

// eventuallyLacksAnnotation waits until key is absent from the Application.
func eventuallyLacksAnnotation(t *testing.T, ctx context.Context, client fixture.KubeClient, appKey types.NamespacedName, key, msg string) {
	t.Helper()
	require.Eventually(t, func() bool {
		annotations, err := annotationsOf(ctx, client, appKey)
		if err != nil {
			return false
		}
		_, ok := annotations[key]
		return !ok
	}, 60*time.Second, 1*time.Second, msg)
}

// consistentlyLacksAnnotation asserts key never appears on the Application.
// Used where the expected outcome is "nothing happens", which Eventually
// cannot express.
func consistentlyLacksAnnotation(t *testing.T, ctx context.Context, client fixture.KubeClient, appKey types.NamespacedName, key, msg string) {
	t.Helper()
	require.Never(t, func() bool {
		annotations, err := annotationsOf(ctx, client, appKey)
		require.NoError(t, err, "failed to read Application while checking annotation absence")

		_, ok := annotations[key]
		return ok
	}, 20*time.Second, 2*time.Second, msg)
}

// annotateApp sets annotations on an Application, retrying on conflict.
func annotateApp(t *testing.T, ctx context.Context, client fixture.KubeClient, appKey types.NamespacedName, kv map[string]string) {
	t.Helper()
	err := client.EnsureApplicationUpdate(ctx, appKey, func(a *argoapp.Application) error {
		if a.Annotations == nil {
			a.Annotations = make(map[string]string)
		}
		for k, v := range kv {
			a.Annotations[k] = v
		}
		return nil
	}, metav1.UpdateOptions{})
	require.NoError(t, err)
}

// removeAppAnnotations deletes annotations from an Application, retrying on conflict.
func removeAppAnnotations(t *testing.T, ctx context.Context, client fixture.KubeClient, appKey types.NamespacedName, keys ...string) {
	t.Helper()
	err := client.EnsureApplicationUpdate(ctx, appKey, func(a *argoapp.Application) error {
		for _, k := range keys {
			delete(a.Annotations, k)
		}
		return nil
	}, metav1.UpdateOptions{})
	require.NoError(t, err)
}

// reconciledAtOf returns the Application's status.reconciledAt, or "" when it
// cannot be read.
func reconciledAtOf(ctx context.Context, client fixture.KubeClient, key types.NamespacedName) string {
	app := argoapp.Application{}
	if err := client.Get(ctx, key, &app, metav1.GetOptions{}); err != nil || app.Status.ReconciledAt == nil {
		return ""
	}
	return app.Status.ReconciledAt.String()
}

// eventuallyReconciledAfter waits until the Application's reconciledAt differs
// from before.
//
// This is how a refresh is observed. Asserting that the refresh annotation
// appears on the peer does not work: the application controller consumes and
// removes it in well under a second, so a poll almost always misses it, and
// "the annotation is absent" is equally true when it never arrived at all.
// A moved reconciledAt proves the refresh was delivered and acted upon.
func eventuallyReconciledAfter(t *testing.T, ctx context.Context, client fixture.KubeClient, key types.NamespacedName, before, msg string) {
	t.Helper()
	require.Eventually(t, func() bool {
		now := reconciledAtOf(ctx, client, key)
		return now != "" && now != before
	}, 90*time.Second, 1*time.Second, msg)
}

// createManagedAnnotationApp creates an Application on the principal and waits for it to
// reach the managed agent. It returns the principal and agent keys.
func createManagedAnnotationApp(t *testing.T, ctx context.Context, principalClient, managedClient fixture.KubeClient, name string) (types.NamespacedName, types.NamespacedName) {
	t.Helper()

	app := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: fixture.ManagedPrincipalAppNamespace(),
		},
		Spec: argoapp.ApplicationSpec{
			Project: "default",
			Source: &argoapp.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
			},
			Destination: fixture.ManagedDestination("guestbook"),
		},
	}
	require.NoError(t, principalClient.Create(ctx, &app, metav1.CreateOptions{}))

	principalKey := fixture.ToNamespacedName(&app)
	agentKey := types.NamespacedName{Name: name, Namespace: fixture.ManagedAgentAppNamespace()}

	require.Eventually(t, func() bool {
		a := argoapp.Application{}
		return managedClient.Get(ctx, agentKey, &a, metav1.GetOptions{}) == nil
	}, 60*time.Second, 1*time.Second, "Application should reach the managed agent")

	return principalKey, agentKey
}

// createAutonomousAnnotationApp creates an Application on the autonomous agent and waits
// for it to reach the principal. It returns the agent and principal keys.
func createAutonomousAnnotationApp(t *testing.T, ctx context.Context, autonomousClient, principalClient fixture.KubeClient, name string) (types.NamespacedName, types.NamespacedName) {
	t.Helper()

	app := argoapp.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: fixture.AutonomousAgentNamespace,
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
				Namespace: "guestbook",
			},
		},
	}
	require.NoError(t, autonomousClient.Create(ctx, &app, metav1.CreateOptions{}))

	agentKey := fixture.ToNamespacedName(&app)
	principalKey := types.NamespacedName{Name: name, Namespace: "agent-autonomous"}

	require.Eventually(t, func() bool {
		a := argoapp.Application{}
		return principalClient.Get(ctx, principalKey, &a, metav1.GetOptions{}) == nil
	}, 60*time.Second, 1*time.Second, "Application should reach the principal")

	return agentKey, principalKey
}

// Test_MirroredAnnotation_Managed covers the source-of-truth to peer behaviour
// for a managed agent, where the principal is the source of truth.
func (suite *AnnotationSyncTestSuite) Test_MirroredAnnotation_Managed() {
	principalKey, agentKey := createManagedAnnotationApp(suite.T(), suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient, "ann-mirror-managed")

	suite.T().Log("Adding the annotation on the principal propagates it to the agent")
	annotateApp(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, map[string]string{mirroredAnnotation: "true"})
	eventuallyHasAnnotation(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, mirroredAnnotation, "true",
		"a mirrored annotation added on the principal must reach the agent")

	suite.T().Log("Updating it on the principal propagates the new value")
	annotateApp(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, map[string]string{mirroredAnnotation: "false"})
	eventuallyHasAnnotation(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, mirroredAnnotation, "false",
		"a mirrored annotation updated on the principal must reach the agent")

	suite.T().Log("Changing it directly on the agent is reverted")
	annotateApp(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, map[string]string{mirroredAnnotation: "drifted"})
	eventuallyHasAnnotation(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, mirroredAnnotation, "false",
		"a mirrored annotation changed on the agent must be reverted to the principal's value")

	suite.T().Log("Removing it on the agent is reverted")
	removeAppAnnotations(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, mirroredAnnotation)
	eventuallyHasAnnotation(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, mirroredAnnotation, "false",
		"a mirrored annotation removed on the agent must be restored")

	suite.T().Log("Removing it on the principal removes it from the agent")
	removeAppAnnotations(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, mirroredAnnotation)
	eventuallyLacksAnnotation(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, mirroredAnnotation,
		"a mirrored annotation removed on the principal must be removed from the agent")
}

// Test_IgnoredAnnotation_Managed covers the standard annotation ignoring
// behaviour: unknown and child-Application annotations are neither transferred
// nor destroyed.
func (suite *AnnotationSyncTestSuite) Test_IgnoredAnnotation_Managed() {
	principalKey, agentKey := createManagedAnnotationApp(suite.T(), suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient, "ann-ignore-managed")

	suite.T().Log("Child-app and unknown annotations on the principal do not reach the agent")
	annotateApp(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, map[string]string{
		childAppAnnotation: "5",
		unknownAnnotation:  "team-a",
	})
	consistentlyLacksAnnotation(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, childAppAnnotation,
		"a child-Application annotation must never be transferred to the agent")
	consistentlyLacksAnnotation(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, unknownAnnotation,
		"an unknown annotation must never be transferred to the agent")

	suite.T().Log("An agent-local annotation does not reach the principal")
	annotateApp(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, map[string]string{"e2e.example.com/local": "yes"})
	consistentlyLacksAnnotation(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, "e2e.example.com/local",
		"an unknown annotation set on the agent must not be transferred to the principal")

	// This is the regression the wholesale annotation copy used to cause: an
	// unrelated change on the source of truth silently deleted annotations
	// that had been set on the peer.
	suite.T().Log("An unrelated spec change on the principal does not clobber the agent-local annotation")
	err := suite.PrincipalClient.EnsureApplicationUpdate(suite.Ctx, principalKey, func(a *argoapp.Application) error {
		a.Spec.Info = []argoapp.Info{{Name: "e2e", Value: "spec-change"}}
		return nil
	}, metav1.UpdateOptions{})
	suite.Require().NoError(err)

	// Wait for the spec change to land, then confirm the annotation survived it.
	suite.Require().Eventually(func() bool {
		a := argoapp.Application{}
		if err := suite.ManagedAgentClient.Get(suite.Ctx, agentKey, &a, metav1.GetOptions{}); err != nil {
			return false
		}
		return len(a.Spec.Info) == 1 && a.Spec.Info[0].Value == "spec-change"
	}, 60*time.Second, 1*time.Second, "the spec change should reach the agent")

	annotations, err := annotationsOf(suite.Ctx, suite.ManagedAgentClient, agentKey)
	suite.Require().NoError(err, "failed to get Application annotations")

	suite.Require().Equal("yes", annotations["e2e.example.com/local"],
		"an unrelated spec update from the principal must not remove an annotation set on the agent")
}

// Test_MirroredAnnotation_Autonomous covers the same behaviour with the
// directions reversed: for an autonomous agent, the agent is the source of
// truth.
func (suite *AnnotationSyncTestSuite) Test_MirroredAnnotation_Autonomous() {
	agentKey, principalKey := createAutonomousAnnotationApp(suite.T(), suite.Ctx, suite.AutonomousAgentClient, suite.PrincipalClient, "ann-mirror-autonomous")

	suite.T().Log("Adding the annotation on the agent propagates it to the principal")
	annotateApp(suite.T(), suite.Ctx, suite.AutonomousAgentClient, agentKey, map[string]string{mirroredAnnotation: "true"})
	eventuallyHasAnnotation(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, mirroredAnnotation, "true",
		"a mirrored annotation added on the autonomous agent must reach the principal")

	suite.T().Log("Changing it directly on the principal is reverted")
	annotateApp(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, map[string]string{mirroredAnnotation: "drifted"})
	eventuallyHasAnnotation(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, mirroredAnnotation, "true",
		"a mirrored annotation changed on the principal must be reverted to the agent's value")

	suite.T().Log("Removing it on the principal is reverted")
	removeAppAnnotations(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, mirroredAnnotation)
	eventuallyHasAnnotation(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, mirroredAnnotation, "true",
		"a mirrored annotation removed on the principal must be restored from the agent")

	suite.T().Log("Removing it on the agent removes it from the principal")
	removeAppAnnotations(suite.T(), suite.Ctx, suite.AutonomousAgentClient, agentKey, mirroredAnnotation)
	eventuallyLacksAnnotation(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, mirroredAnnotation,
		"a mirrored annotation removed on the autonomous agent must be removed from the principal")
}

// Test_IgnoredAnnotation_Autonomous covers the ignoring behaviour for an
// autonomous agent.
func (suite *AnnotationSyncTestSuite) Test_IgnoredAnnotation_Autonomous() {
	agentKey, principalKey := createAutonomousAnnotationApp(suite.T(), suite.Ctx, suite.AutonomousAgentClient, suite.PrincipalClient, "ann-ignore-autonomous")

	suite.T().Log("Child-app and unknown annotations on the agent do not reach the principal")
	annotateApp(suite.T(), suite.Ctx, suite.AutonomousAgentClient, agentKey, map[string]string{
		childAppAnnotation: "5",
		unknownAnnotation:  "platform",
	})
	consistentlyLacksAnnotation(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, childAppAnnotation,
		"a child-Application annotation must never be transferred to the principal")
	consistentlyLacksAnnotation(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, unknownAnnotation,
		"an unknown annotation must never be transferred to the principal")

	suite.T().Log("A principal-local annotation is left alone and does not reach the agent")
	annotateApp(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, map[string]string{"e2e.example.com/local": "yes"})
	consistentlyLacksAnnotation(suite.T(), suite.Ctx, suite.AutonomousAgentClient, agentKey, "e2e.example.com/local",
		"an unknown annotation set on the principal must not be transferred to the agent")

	suite.T().Log("An unrelated spec change on the agent does not clobber the principal-local annotation")
	err := suite.AutonomousAgentClient.EnsureApplicationUpdate(suite.Ctx, agentKey, func(a *argoapp.Application) error {
		a.Spec.Info = []argoapp.Info{{Name: "e2e", Value: "spec-change"}}
		return nil
	}, metav1.UpdateOptions{})
	suite.Require().NoError(err)

	suite.Require().Eventually(func() bool {
		a := argoapp.Application{}
		if err := suite.PrincipalClient.Get(suite.Ctx, principalKey, &a, metav1.GetOptions{}); err != nil {
			return false
		}
		return len(a.Spec.Info) == 1 && a.Spec.Info[0].Value == "spec-change"
	}, 60*time.Second, 1*time.Second, "the spec change should reach the principal")

	annotations, err := annotationsOf(suite.Ctx, suite.PrincipalClient, principalKey)
	suite.Require().NoError(err, "failed to get Application annotations")

	suite.Require().Equal("yes", annotations["e2e.example.com/local"],
		"an unrelated spec update from the agent must not remove an annotation set on the principal")
}

// Test_RefreshAnnotation_Managed verifies the refresh special case for a
// managed agent. Until now this was only exercised in destination-based
// mapping mode, so it could regress unnoticed in the default configuration.
func (suite *AnnotationSyncTestSuite) Test_RefreshAnnotation_Managed() {
	principalKey, agentKey := createManagedAnnotationApp(suite.T(), suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient, "ann-refresh-managed")

	suite.T().Log("Setting refresh on the principal reaches the agent and is then consumed")
	before := reconciledAtOf(suite.Ctx, suite.ManagedAgentClient, agentKey)
	annotateApp(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, map[string]string{
		argoapp.AnnotationKeyRefresh: "normal",
	})

	eventuallyReconciledAfter(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, before,
		"the refresh must reach the agent and its application controller must act on it")

	// The controller removes the annotation once it has refreshed, and that
	// removal has to propagate back to the principal.
	eventuallyLacksAnnotation(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, argoapp.AnnotationKeyRefresh,
		"the agent's application controller should consume the refresh annotation")
	eventuallyLacksAnnotation(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, argoapp.AnnotationKeyRefresh,
		"the refresh annotation's removal on the agent must propagate to the principal")
}

// Test_RefreshAnnotation_Autonomous verifies the refresh special case for an
// autonomous agent, which previously had no coverage at all.
func (suite *AnnotationSyncTestSuite) Test_RefreshAnnotation_Autonomous() {
	agentKey, principalKey := createAutonomousAnnotationApp(suite.T(), suite.Ctx, suite.AutonomousAgentClient, suite.PrincipalClient, "ann-refresh-autonomous")

	suite.T().Log("Refresh requested on the principal is delivered to the agent and consumed there")
	require.Eventually(suite.T(), func() bool {
		return reconciledAtOf(suite.Ctx, suite.AutonomousAgentClient, agentKey) != ""
	}, 90*time.Second, 1*time.Second,
		"the agent should reconcile the Application before the refresh is requested")
	before := reconciledAtOf(suite.Ctx, suite.AutonomousAgentClient, agentKey)
	annotateApp(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, map[string]string{
		argoapp.AnnotationKeyRefresh: "normal",
	})
	eventuallyReconciledAfter(suite.T(), suite.Ctx, suite.AutonomousAgentClient, agentKey, before,
		"the refresh must reach the autonomous agent and its application controller must act on it")
	eventuallyLacksAnnotation(suite.T(), suite.Ctx, suite.AutonomousAgentClient, agentKey, argoapp.AnnotationKeyRefresh,
		"the autonomous agent's application controller should consume the refresh annotation")
	eventuallyLacksAnnotation(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, argoapp.AnnotationKeyRefresh,
		"the refresh annotation must not be left stranded on the principal")

	suite.T().Log("Refresh requested on the agent is consumed and does not strand on the principal")
	before = reconciledAtOf(suite.Ctx, suite.AutonomousAgentClient, agentKey)
	annotateApp(suite.T(), suite.Ctx, suite.AutonomousAgentClient, agentKey, map[string]string{
		argoapp.AnnotationKeyRefresh: "normal",
	})
	eventuallyReconciledAfter(suite.T(), suite.Ctx, suite.AutonomousAgentClient, agentKey, before,
		"an agent-initiated refresh must be acted upon")
	eventuallyLacksAnnotation(suite.T(), suite.Ctx, suite.AutonomousAgentClient, agentKey, argoapp.AnnotationKeyRefresh,
		"the autonomous agent's application controller should consume its own refresh annotation")
	eventuallyLacksAnnotation(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, argoapp.AnnotationKeyRefresh,
		"a refresh consumed on the agent must not appear or linger on the principal")
}

func TestAnnotationSyncTestSuite(t *testing.T) {
	suite.Run(t, new(AnnotationSyncTestSuite))
}

// AnnotationAllowListTestSuite covers the user-defined allow list. It is a
// separate suite because every test has to restart the principal and the
// managed agent to change their configuration, which is slow and should not
// be imposed on the rest of the annotation tests.
type AnnotationAllowListTestSuite struct {
	fixture.BaseSuite
}

// configure writes the allow list for both components and restarts them.
// Passing an empty string clears the list.
func (suite *AnnotationAllowListTestSuite) configure(allowList string) {
	t := suite.T()
	requires := suite.Require()

	if allowList == "" {
		fixture.ClearEnvVarsFile()
	} else {
		// Both sides evaluate their own copy of the list, so they have to be
		// configured identically or the annotation is stripped by whichever
		// one does not allow it.
		requires.NoError(fixture.WriteEnvVarsToFile(map[string]string{
			"ARGOCD_PRINCIPAL_APPLICATION_ANNOTATIONS_ALLOW_LIST": allowList,
			"ARGOCD_AGENT_APPLICATION_ANNOTATIONS_ALLOW_LIST":     allowList,
		}))
	}

	fixture.RestartAgent(t, "principal")
	fixture.RestartAgent(t, "agent-managed")

	// fixture.CheckReadiness allows 120s, which is enough for the suites that
	// only bounce an agent. This one restarts the principal too, and the local
	// environment runs the components via "go run", so a restart means a full
	// race-enabled recompile. That overruns 120s whenever the build cache is
	// cold, so wait on the health endpoints directly with a larger budget.
	waitHealthy(t, "principal", "http://localhost:8003/healthz")
	waitHealthy(t, "agent-managed", "http://localhost:8001/healthz")
}

// waitHealthy blocks until the component's health endpoint answers 200.
//
// It gives up immediately if the process is no longer running. A component
// that fataled during startup -- the principal exits rather than retrying if
// it cannot read its TLS secrets -- will never answer, and waiting out the
// full budget only turns a clear failure into a timeout.
func waitHealthy(t *testing.T, name, url string) {
	t.Helper()
	require.Eventually(t, func() bool {
		if !fixture.IsProcessRunning(name, t) {
			t.Fatalf("%s exited instead of becoming ready; see /tmp/argocd-agent-e2e-process-output.log", name)
		}
		resp, err := http.Get(url)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Minute, 2*time.Second, "%s did not become ready after a restart", name)
}

func (suite *AnnotationAllowListTestSuite) TearDownTest() {
	suite.BaseSuite.TearDownTest()
	suite.configure("")
}

func (suite *AnnotationAllowListTestSuite) Test_AllowListedAnnotationIsTransferred() {
	t := suite.T()
	fixture.SkipIfAgentInClusterEnvVarIsSet(t)

	const key = "argocd-image-updater.argoproj.io/image-list"

	t.Log("Without an allow list the annotation is not transferred")
	principalKey, agentKey := createManagedAnnotationApp(suite.T(), suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient, "ann-allowlist")

	annotateApp(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, map[string]string{key: "nginx"})
	consistentlyLacksAnnotation(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, key,
		"an annotation that is not allow-listed must not be transferred")

	t.Log("With the allow list configured on both sides it is transferred")
	suite.configure("argocd-image-updater.argoproj.io/*")

	// Re-stamp the annotation so there is a fresh event after the restart.
	annotateApp(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, map[string]string{key: "nginx:1.25"})
	eventuallyHasAnnotation(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, key, "nginx:1.25",
		"an allow-listed annotation must follow the source of truth")

	t.Log("Removing it on the principal removes it from the agent")
	removeAppAnnotations(suite.T(), suite.Ctx, suite.PrincipalClient, principalKey, key)
	eventuallyLacksAnnotation(suite.T(), suite.Ctx, suite.ManagedAgentClient, agentKey, key,
		"an allow-listed annotation must be removed when the source of truth drops it")
}

func TestAnnotationAllowListTestSuite(t *testing.T) {
	suite.Run(t, new(AnnotationAllowListTestSuite))
}
