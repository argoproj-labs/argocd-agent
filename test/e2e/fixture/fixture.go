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
	"reflect"
	"strings"
	"sync"
	"testing"
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

	// PrincipalNamespace is the namespace where principal components run in the control-plane vcluster.
	PrincipalNamespace = "argocd-principal"

	// ManagedAgentNamespace is the namespace where managed agent components run in the agent-managed vcluster.
	ManagedAgentNamespace = "argocd-managed"

	// AutonomousAgentNamespace is the namespace where autonomous agent components run in the agent-autonomous vcluster.
	AutonomousAgentNamespace = "argocd-autonomous"

	// E2EDestinationBasedEnv is the environment variable that enables
	// destination-based mapping mode for e2e tests.
	E2EDestinationBasedEnv = "E2E_DESTINATION_BASED_MAPPING"

	// DestMappingAppNamespace is the namespace used on the principal for
	// Application CRs when running in destination-based mapping mode. It
	// intentionally differs from the agent name to verify that apps in
	// arbitrary namespaces are routed correctly.
	DestMappingAppNamespace = "agent-test"
)

// IsDestinationBased returns true when the e2e suite is running in
// destination-based mapping mode.
func IsDestinationBased() bool {
	return strings.ToLower(os.Getenv(E2EDestinationBasedEnv)) == "true"
}

// ManagedDestination returns the ApplicationDestination for a managed-agent
// application. In namespace-scoped mode it uses the server URL; in
// destination-based mode it uses the cluster name.
func ManagedDestination(targetNs string) argoapp.ApplicationDestination {
	if IsDestinationBased() {
		return argoapp.ApplicationDestination{
			Name:      "agent-managed",
			Namespace: targetNs,
		}
	}
	return argoapp.ApplicationDestination{
		Server:    "https://kubernetes.default.svc",
		Namespace: targetNs,
	}
}

// ManagedAgentAppNamespace returns the namespace where an application exists
// on the managed agent cluster. In namespace-scoped mode apps are placed in
// the agent's argocd namespace; in destination-based mode the app stays in
// DestMappingAppNamespace.
func ManagedAgentAppNamespace() string {
	if IsDestinationBased() {
		return DestMappingAppNamespace
	}
	return ManagedAgentNamespace
}

// ManagedPrincipalAppNamespace returns the namespace where a managed
// application is created on the principal cluster. In namespace-scoped mode
// this is always "agent-managed"; in destination-based mode it is
// DestMappingAppNamespace.
func ManagedPrincipalAppNamespace() string {
	if IsDestinationBased() {
		return DestMappingAppNamespace
	}
	return "agent-managed"
}

func managedPrincipalCleanupNamespaces() []string {
	if IsDestinationBased() {
		return []string{"agent-managed", DestMappingAppNamespace}
	}
	return []string{"agent-managed"}
}

func managedAgentCleanupNamespaces() []string {
	if IsDestinationBased() {
		return []string{ManagedAgentNamespace, DestMappingAppNamespace}
	}
	return []string{ManagedAgentNamespace, "agent-managed"}
}

// WriteEnvVarsToFile writes environment variables to the e2e config file.
// In destination-based mode the destination mapping variables are always
// included. Pass nil to write only the base destination variables (or clear
// the file in namespace-scoped mode).
func WriteEnvVarsToFile(vars map[string]string) error {
	allVars := make(map[string]string)
	if IsDestinationBased() {
		allVars["ARGOCD_AGENT_DESTINATION_BASED_MAPPING"] = "true"
		allVars["ARGOCD_AGENT_CREATE_NAMESPACE"] = "true"
		allVars["ARGOCD_PRINCIPAL_DESTINATION_BASED_MAPPING"] = "true"
	}
	for k, v := range vars {
		allVars[k] = v
	}
	if len(allVars) == 0 {
		os.Remove(EnvVariablesFromE2EFile)
		return nil
	}
	var buf strings.Builder
	for k, v := range allVars {
		fmt.Fprintf(&buf, "%s=%s\n", k, v)
	}
	return os.WriteFile(EnvVariablesFromE2EFile, []byte(buf.String()), 0644)
}

// ClearEnvVarsFile removes extra environment variables from the config file,
// keeping destination-based mapping variables when running in that mode.
func ClearEnvVarsFile() {
	if IsDestinationBased() {
		_ = WriteEnvVarsToFile(nil)
	} else {
		os.Remove(EnvVariablesFromE2EFile)
	}
}

var destinationSetupOnce sync.Once

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

	if IsDestinationBased() {
		destinationSetupOnce.Do(func() {
			suite.setupDestinationBasedMapping()
		})
	}
}

// setupDestinationBasedMapping configures the cluster for destination-based
// mapping: writes env vars to the config file, patches the default AppProject
// on the principal, patches argocd-cmd-params-cm on the managed agent, and
// restarts the application controller so it watches all namespaces.
//
// The agent and principal processes are expected to have been started in
// destination-based mode already.
func (suite *BaseSuite) setupDestinationBasedMapping() {
	requires := suite.Require()
	suite.T().Log("Setting up destination-based mapping mode")

	err := WriteEnvVarsToFile(nil)
	requires.NoError(err)

	// Create the agent-test namespace on the principal so Application CRs can be
	// placed there. The agent will create the namespace on its side
	// automatically when create-namespace is enabled.
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: DestMappingAppNamespace}}
	if createErr := suite.PrincipalClient.Create(suite.Ctx, ns, metav1.CreateOptions{}); createErr != nil {
		if !errors.IsAlreadyExists(createErr) {
			requires.NoError(createErr)
		}
	}

	// Update the principal's default AppProject to allow apps in any source namespace.
	defaultAppProject := &argoapp.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: PrincipalNamespace,
		},
	}
	err = EnsureUpdate(suite.Ctx, suite.PrincipalClient, defaultAppProject, func(obj KubeObject) {
		appProject := obj.(*argoapp.AppProject)
		appProject.Spec.SourceNamespaces = []string{"*"}
	})
	requires.NoError(err)

	// Wait for the SourceNamespaces change to propagate to the managed agent
	requires.Eventually(func() bool {
		proj := &argoapp.AppProject{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
			Name: "default", Namespace: ManagedAgentNamespace,
		}, proj, metav1.GetOptions{})
		if err != nil {
			return false
		}
		for _, ns := range proj.Spec.SourceNamespaces {
			if ns == "*" {
				return true
			}
		}
		return false
	}, 30*time.Second, 1*time.Second, "SourceNamespaces should propagate to managed agent")

	// Update argocd-cmd-params-cm to allow applications in any namespace.
	// This ConfigMap is local Argo CD configuration and is not synced by
	// the agent, so a direct update is safe.
	acm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-cmd-params-cm",
			Namespace: ManagedAgentNamespace,
		},
	}
	err = EnsureUpdate(suite.Ctx, suite.ManagedAgentClient, acm, func(obj KubeObject) {
		configMap := obj.(*corev1.ConfigMap)
		if configMap.Data == nil {
			configMap.Data = map[string]string{}
		}
		configMap.Data["application.namespaces"] = "*"
	})
	requires.NoError(err)

	suite.RestartApplicationController()
}

// RestartApplicationController deletes the application controller pod on the
// managed agent and waits for the replacement to become ready.
func (suite *BaseSuite) RestartApplicationController() {
	requires := suite.Require()
	podList := &corev1.PodList{}
	err := suite.ManagedAgentClient.List(suite.Ctx, ManagedAgentNamespace, podList, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=argocd-application-controller",
	})
	requires.NoError(err)
	requires.True(len(podList.Items) > 0, "expected at least one application controller pod")

	err = suite.ManagedAgentClient.Delete(suite.Ctx, &podList.Items[0], metav1.DeleteOptions{})
	requires.NoError(err)

	requires.Eventually(func() bool {
		pod := &corev1.Pod{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, types.NamespacedName{
			Name: podList.Items[0].Name, Namespace: ManagedAgentNamespace,
		}, pod, metav1.GetOptions{})
		if err != nil {
			return false
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true
			}
		}
		return false
	}, 120*time.Second, 2*time.Second, "Application controller pod should become ready")
}

func (suite *BaseSuite) SetupTest() {
	err := CleanUp(suite.Ctx, suite.PrincipalClient, suite.ManagedAgentClient, suite.AutonomousAgentClient, suite.ClusterDetails)
	suite.Require().Nil(err)

	// Ensure that the autonomous agent's default AppProject exists on the principal
	project := &argoapp.AppProject{}
	key := types.NamespacedName{Name: "default", Namespace: AutonomousAgentNamespace}
	err = suite.AutonomousAgentClient.Get(suite.Ctx, key, project, metav1.GetOptions{})
	suite.Require().Nil(err)
	now := time.Now().Format(time.RFC3339)

	err = suite.AutonomousAgentClient.EnsureAppProjectUpdate(suite.Ctx, ToNamespacedName(project), func(ap *argoapp.AppProject) error {
		ap.Annotations = map[string]string{"created": now}
		return nil
	}, metav1.UpdateOptions{})
	suite.Require().Nil(err)

	suite.Require().Eventually(func() bool {
		project := &argoapp.AppProject{}
		key := types.NamespacedName{Name: "agent-autonomous-default", Namespace: PrincipalNamespace}
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
	// Wait for the object to be deleted for 180 seconds
	// - Primarily this will be waiting for the finalizer to be removed, so that the object is deleted
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	for count := 0; count < 180; count++ {
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
	for count := 0; count < 180; count++ {
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
func WaitForDeletion(ctx context.Context, kclient KubeClient, obj KubeObject, debugContext string) error {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	for count := 0; count < 180; count++ {
		err := kclient.Get(ctx, key, obj, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}
		time.Sleep(1 * time.Second)
	}

	if debugContext != "" {
		debugContext = "(" + debugContext + ")"
	}

	typeName := reflect.TypeOf(obj).Elem().Name()
	return fmt.Errorf("WaitForDeletion: timeout waiting for deletion of %s %s/%s %s", typeName, key.Namespace, key.Name, debugContext)
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

	// Remove any previously configured env variables from the config file,
	// preserving destination-based mapping vars when running in that mode.
	ClearEnvVarsFile()

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
	err = autonomousAgentClient.List(ctx, AutonomousAgentNamespace, &list, metav1.ListOptions{})
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
		err = WaitForDeletion(ctx, principalClient, &app, "principal")
		if err != nil {
			return err
		}
	}

	// Delete all managed applications from the principal
	for _, principalNs := range managedPrincipalCleanupNamespaces() {
		list = argoapp.ApplicationList{}
		err = principalClient.List(ctx, principalNs, &list, metav1.ListOptions{})
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
			app.SetNamespace(ManagedAgentAppNamespace())
			err = WaitForDeletion(ctx, managedAgentClient, &app, "managed agent")
			if err != nil {
				return err
			}
		}
	}

	// Delete any remaining managed applications left on the managed agent
	for _, agentNs := range managedAgentCleanupNamespaces() {
		list = argoapp.ApplicationList{}
		err = managedAgentClient.List(ctx, agentNs, &list, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for _, app := range list.Items {
			err = EnsureDeletion(ctx, managedAgentClient, &app)
			if err != nil {
				return err
			}
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
	err = autonomousAgentClient.List(ctx, AutonomousAgentNamespace, &appProjectList, metav1.ListOptions{})
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
		appProject.SetNamespace(PrincipalNamespace)
		err = WaitForDeletion(ctx, principalClient, &appProject, "principal")
		if err != nil {
			return err
		}
	}

	// Delete all appProjects from the principal
	appProjectList = argoapp.AppProjectList{}
	err = principalClient.List(ctx, PrincipalNamespace, &appProjectList, metav1.ListOptions{})
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
		appProject.SetNamespace(ManagedAgentNamespace)
		err = WaitForDeletion(ctx, managedAgentClient, &appProject, "managed agent")
		if err != nil {
			return err
		}
	}

	// Delete all appProjects from the managed agent
	appProjectList = argoapp.AppProjectList{}
	err = managedAgentClient.List(ctx, ManagedAgentNamespace, &appProjectList, metav1.ListOptions{})
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

	// Delete all repository and repo-creds secrets from all clusters
	for _, secretType := range []string{common.LabelValueSecretTypeRepository, common.LabelValueSecretTypeRepoCreds} {
		repoListOpts := metav1.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{
				common.LabelKeySecretType: secretType,
			}).String(),
		}

		repoList := corev1.SecretList{}
		err = principalClient.List(ctx, PrincipalNamespace, &repoList, repoListOpts)
		if err != nil {
			return err
		}
		for _, repo := range repoList.Items {
			err = EnsureDeletion(ctx, principalClient, &repo)
			if err != nil {
				return err
			}

			repo.SetNamespace(ManagedAgentNamespace)
			err = WaitForDeletion(ctx, managedAgentClient, &repo, "managed agent")
			if err != nil {
				return err
			}
		}

		repoList = corev1.SecretList{}
		err = autonomousAgentClient.List(ctx, AutonomousAgentNamespace, &repoList, repoListOpts)
		if err != nil {
			return err
		}
		for _, repo := range repoList.Items {
			err = EnsureDeletion(ctx, autonomousAgentClient, &repo)
			if err != nil {
				return err
			}
		}
		repoList = corev1.SecretList{}
		err = managedAgentClient.List(ctx, ManagedAgentNamespace, &repoList, repoListOpts)
		if err != nil {
			return err
		}
		for _, repo := range repoList.Items {
			err = EnsureDeletion(ctx, managedAgentClient, &repo)
			if err != nil {
				return err
			}
		}
	}

	// Delete GPG keys ConfigMap from the principal
	gpgKeysCM := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: PrincipalNamespace}}
	err = EnsureDeletion(ctx, principalClient, gpgKeysCM)
	if err != nil {
		return err
	}

	// Wait for the GPG keys ConfigMap to be deleted from the managed agent
	gpgKeysCM = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: ManagedAgentNamespace}}
	err = WaitForDeletion(ctx, managedAgentClient, gpgKeysCM, "managed agent")
	if err != nil {
		return err
	}

	// Delete GPG keys ConfigMap from the autonomous agent
	gpgKeysCM = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: AutonomousAgentNamespace}}
	err = EnsureDeletion(ctx, autonomousAgentClient, gpgKeysCM)
	if err != nil {
		return err
	}

	// Remove known finalizer-containing resources from guestbook ns (before we delete the NS in the next step)
	deploymentList := appsv1.DeploymentList{}
	err = managedAgentClient.List(ctx, "guestbook", &deploymentList, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, deployment := range deploymentList.Items {
		if len(deployment.Finalizers) > 0 {
			err := EnsureUpdate(ctx, managedAgentClient, &deployment, func(obj KubeObject) {
				obj.SetFinalizers(nil)
			})
			if err != nil {
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

// SkipIfAgentInClusterEnvVarIsSet should be called to verify that the test (or utility function) is running locally (that is, not on cluster). If the test is running on cluster, the correct step is to skip the test.
func SkipIfAgentInClusterEnvVarIsSet(t *testing.T) {
	t.Helper()
	if os.Getenv("ARGOCD_AGENT_IN_CLUSTER") == "true" {
		t.Skip("skipping because ARGOCD_AGENT_IN_CLUSTER=true")
	}
}
