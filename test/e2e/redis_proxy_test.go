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
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"

	"github.com/argoproj/argo-cd/gitops-engine/pkg/health"
	argocdclient "github.com/argoproj/argo-cd/v3/pkg/apiclient"
	"github.com/argoproj/argo-cd/v3/pkg/apiclient/application"
	sessionpkg "github.com/argoproj/argo-cd/v3/pkg/apiclient/session"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type RedisProxyTestSuite struct {
	fixture.BaseSuite
}

func (suite *RedisProxyTestSuite) Test_RedisProxy_ManagedAgent_Argo() {
	appOnPrincipal, appClient, argoEndpoint, sessionToken := suite.setupManagedAppWithArgoCDClient()

	verifyResourceTreeViaRedisProxy(&suite.BaseSuite, appOnPrincipal, appClient, argoEndpoint, sessionToken)
}

// Test_RedisProxy_ManagedAgent_LiveViewSurvivesAgentRestart verifies that a live resource view that was opened before
// the agent restarted keeps receiving updates afterwards. The agent keeps its redis subscriptions in memory only, so
// after a restart the principal has to make Argo CD subscribe again (see #1104).
func (suite *RedisProxyTestSuite) Test_RedisProxy_ManagedAgent_LiveViewSurvivesAgentRestart() {
	appOnPrincipal, appClient, argoEndpoint, sessionToken := suite.setupManagedAppWithArgoCDClient()

	// Open the live view and make sure it works before the restart
	msgChan, cancelFunc := openResourceTreeStream(&suite.BaseSuite, appOnPrincipal, argoEndpoint, sessionToken)
	defer cancelFunc()
	verifyResourceTreeUpdatesOnStream(&suite.BaseSuite, appOnPrincipal, appClient, msgChan)

	fixture.RestartAgent(suite.T(), fixture.AgentManagedName)
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)
	// The principal only acts on the restart once the agent is connected again
	fixture.VerifyAgentConnected(suite.T(), fixture.AgentManagedName, suite.ClusterDetails)

	// The same, still open, live view must keep receiving updates from the restarted agent
	verifyResourceTreeUpdatesOnStream(&suite.BaseSuite, appOnPrincipal, appClient, msgChan)
}

// setupManagedAppWithArgoCDClient creates a synced and healthy managed guestbook app on the principal and returns it
// together with an Argo CD API client, the Argo CD server endpoint and a session token for it.
func (suite *RedisProxyTestSuite) setupManagedAppWithArgoCDClient() (*v1alpha1.Application, application.ApplicationServiceClient, string, string) {
	requires := suite.Require()

	// Get the Argo server endpoint to use
	argoEndpoint, err := fixture.GetArgoCDServerEndpoint(suite.PrincipalClient)
	requires.NoError(err)

	// Read admin secret from principal's cluster
	password, err := fixture.GetInitialAdminSecret(suite.PrincipalClient)
	requires.NoError(err)

	// Create a managed application in the principal's cluster
	appOnPrincipal := v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: fixture.ManagedPrincipalAppNamespace(),
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
			},
			Destination: fixture.ManagedDestination("guestbook"),
			SyncPolicy: &v1alpha1.SyncPolicy{
				Automated: &v1alpha1.SyncPolicyAutomated{},
				SyncOptions: v1alpha1.SyncOptions{
					"CreateNamespace=true",
				},
			},
		},
	}

	argocdClient, sessionToken, sessionCloser, err := createArgoCDAPIClient(suite.Ctx, argoEndpoint, password)
	requires.NoError(err)
	suite.T().Cleanup(func() { _ = sessionCloser.Close() })

	appCloser, appClient, err := argocdClient.NewApplicationClient()
	requires.NoError(err)
	suite.T().Cleanup(func() { _ = appCloser.Close() })

	argoClient := fixture.NewArgoClient(argoEndpoint, "admin", password)
	err = argoClient.Login()
	requires.NoError(err)

	err = ensureAppExistsAndIsSyncedAndHealthy(&appOnPrincipal, suite.PrincipalClient, &suite.BaseSuite)
	requires.NoError(err)

	return &appOnPrincipal, appClient, argoEndpoint, sessionToken
}

func verifyResourceTreeViaRedisProxy(suite *fixture.BaseSuite,
	appOnPrincipal *v1alpha1.Application,
	appClient application.ApplicationServiceClient,
	endpoint string,
	sessionToken string) {

	msgChan, cancelFunc := openResourceTreeStream(suite, appOnPrincipal, endpoint, sessionToken)
	defer cancelFunc()

	verifyResourceTreeUpdatesOnStream(suite, appOnPrincipal, appClient, msgChan)
}

// openResourceTreeStream opens the resource tree event stream of the given app on Argo CD server, which is what the
// UI's live resource view uses. The returned channel receives the raw events; call the returned function to close the stream.
func openResourceTreeStream(suite *fixture.BaseSuite,
	appOnPrincipal *v1alpha1.Application,
	endpoint string,
	sessionToken string) (chan string, context.CancelFunc) {

	requires := suite.Require()
	t := suite.T()

	cancellableContext, cancelFunc := context.WithCancel(suite.Ctx)

	resourceTreeURL := "https://" + endpoint + "/api/v1/stream/applications/" + appOnPrincipal.Name + "/resource-tree?appNamespace=" + appOnPrincipal.Namespace

	// Wait for sucessful connection to event source
	var msgChan chan string
	requires.Eventually(func() bool {
		var err error
		msgChan, err = streamFromEventSourceNew(cancellableContext, resourceTreeURL, sessionToken, t)
		if err != nil {
			t.Logf("streamFromEventSource returned error: %v", err)
			return false
		}
		return true

	}, 5*time.Minute, 5*time.Second)

	requires.NotNil(msgChan)

	return msgChan, cancelFunc
}

// verifyResourceTreeUpdatesOnStream deletes the guestbook pod on the managed agent cluster and verifies that the
// replacement pod shows up on the already open resource tree stream msgChan, which only happens if the redis proxy
// subscription for the app is working.
func verifyResourceTreeUpdatesOnStream(suite *fixture.BaseSuite,
	appOnPrincipal *v1alpha1.Application,
	appClient application.ApplicationServiceClient,
	msgChan chan string) {

	requires := suite.Require()
	t := suite.T()

	// Find pod on managed-agent client. A pod deleted by an earlier call may still be terminating, so wait until
	// exactly one guestbook pod is not being deleted.
	var oldPod corev1.Pod
	requires.Eventually(func() bool {
		var podList corev1.PodList
		// Don't fail from inside the condition: testify runs it on its own goroutine. Retry instead.
		if err := suite.ManagedAgentClient.List(suite.Ctx, "guestbook", &podList, metav1.ListOptions{}); err != nil {
			t.Logf("listing guestbook pods failed, retrying: %v", err)
			return false
		}

		var livePods []corev1.Pod
		for idx := range podList.Items {
			pod := podList.Items[idx]
			if strings.Contains(pod.Name, "kustomize-guestbook-ui") && pod.DeletionTimestamp == nil {
				livePods = append(livePods, pod)
			}
		}
		if len(livePods) != 1 {
			t.Logf("expected exactly one kustomize-guestbook pod, found %d", len(livePods))
			return false
		}
		oldPod = livePods[0]
		return true
	}, time.Second*60, time.Second*5)
	requires.NotEmpty(oldPod.Name)

	// Wait for the old pod to appear in the resource tree
	// This ensures that Redis proxy is fully working before proceeding
	// (the pod will only appear in the resource tree if redis is fully available)
	t.Logf("Waiting for pod %s to appear in resource tree (verifying Redis proxy is working)...", oldPod.Name)
	var tree *v1alpha1.ApplicationTree
	requires.Eventually(func() bool {
		var err error
		tree, err = appClient.ResourceTree(suite.Ctx, &application.ResourcesQuery{
			ApplicationName: &appOnPrincipal.Name,
			AppNamespace:    &appOnPrincipal.Namespace,
			Project:         &appOnPrincipal.Spec.Project,
		})
		if err != nil {
			t.Logf("ResourceTree error: %v", err)
			return false
		}
		if tree == nil {
			t.Logf("ResourceTree returned nil")
			return false
		}

		// Check if the old pod appears in the resource tree
		for _, node := range tree.Nodes {
			if node.Kind == "Pod" && node.Name == oldPod.Name {
				t.Logf("Pod %s found in resource tree - Redis proxy is working", oldPod.Name)
				return true
			}
		}
		return false

	}, time.Second*60, time.Second*5)

	requires.NotNil(tree)

	// Delete pod on managed agent cluster
	err := suite.ManagedAgentClient.Delete(suite.Ctx, &oldPod, metav1.DeleteOptions{})
	requires.NoError(err)

	// Wait for new pod to be created, to replace the old one that was deleted. A pod deleted by an
	// earlier call may still be terminating, so it must not be mistaken for the replacement.
	var newPod corev1.Pod
	requires.Eventually(func() bool {
		var podList corev1.PodList
		if err := suite.ManagedAgentClient.List(suite.Ctx, "guestbook", &podList, metav1.ListOptions{}); err != nil {
			t.Logf("listing guestbook pods failed, retrying: %v", err)
			return false
		}

		for idx := range podList.Items {
			pod := podList.Items[idx]
			if strings.Contains(pod.Name, "kustomize-guestbook-ui") && pod.Name != oldPod.Name && pod.DeletionTimestamp == nil {
				newPod = pod
				break
			}
		}

		return newPod.Name != ""

	}, time.Second*60, time.Second*5)

	// Verify the name of the new pod exists in what has been sent from the channel (this will only be true if redis
	// proxy subscription is working). The stream producer sends on an unbuffered channel and, after an agent restart,
	// there can be a backlog of tree snapshots in front of the one we want, so drain until the deadline rather than
	// giving up as soon as no message is ready.
	deadline := time.After(60 * time.Second)
	found := false
	for !found {
		select {
		case msg := <-msgChan:
			// Each message is a full resource tree snapshot, so only log its size
			t.Logf("Processing message of %d bytes", len(msg))
			if strings.Contains(msg, newPod.Name) {
				t.Log("new pod name found:", newPod.Name)
				found = true
			}
		case <-deadline:
			requires.Fail("new pod never showed up on the resource tree stream", "pod %s", newPod.Name)
		}
	}

	// Ensure that the pod appears in the new resource tree value returned by Argo CD server
	tree, err = appClient.ResourceTree(suite.Ctx, &application.ResourcesQuery{
		ApplicationName: &appOnPrincipal.Name,
		AppNamespace:    &appOnPrincipal.Namespace,
		Project:         &appOnPrincipal.Spec.Project,
	})
	requires.NoError(err)
	requires.NotNil(tree)

	// Verify that the new pod appears in the resource tree
	matchFound := false
	for _, node := range tree.Nodes {
		if node.Kind == "Pod" && node.Name == newPod.Name {
			matchFound = true
			break
		}
	}
	requires.True(matchFound)

}

func (suite *RedisProxyTestSuite) Test_RedisProxy_AutonomousAgent_Argo() {
	requires := suite.Require()

	t := suite.T()

	// Get the Argo server endpoint to use
	argoEndpoint, err := fixture.GetArgoCDServerEndpoint(suite.PrincipalClient)
	requires.NoError(err)

	// Read admin secret from principal's cluster
	password, err := fixture.GetInitialAdminSecret(suite.PrincipalClient)
	requires.NoError(err)

	// Create an autonomous agent application in the principal's cluster
	appOnAutonomous := v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: fixture.AutonomousAgentNamespace,
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
			},
			Destination: v1alpha1.ApplicationDestination{
				Server:    "https://kubernetes.default.svc",
				Namespace: "guestbook",
			},
			SyncPolicy: &v1alpha1.SyncPolicy{
				Automated: &v1alpha1.SyncPolicyAutomated{},
				SyncOptions: v1alpha1.SyncOptions{
					"CreateNamespace=true",
				},
			},
		},
	}

	argocdClient, sessionToken, closer, err := createArgoCDAPIClient(suite.Ctx, argoEndpoint, password)
	requires.NoError(err)
	defer closer.Close()

	closer, appClient, err := argocdClient.NewApplicationClient()
	requires.NoError(err)
	defer closer.Close()

	// Sync the app
	appOnPrincipal := v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: "agent-autonomous",
		},
	}

	argoClient := fixture.NewArgoClient(argoEndpoint, "admin", password)
	err = argoClient.Login()
	requires.NoError(err)

	err = ensureAppExistsAndIsSyncedAndHealthy(&appOnAutonomous, suite.AutonomousAgentClient, &suite.BaseSuite)
	requires.NoError(err)

	cancellableContext, cancelFunc := context.WithCancel(suite.Ctx)
	defer cancelFunc()

	// Stream resource tree events for the application
	resourceTreeURL := "https://" + argoEndpoint + "/api/v1/stream/applications/" + appOnPrincipal.Name + "/resource-tree?appNamespace=" + appOnPrincipal.Namespace

	t.Log("beginning stream", time.Now())

	// Wait for successful connection to event source
	var msgChan chan string
	requires.Eventually(func() bool {
		var err error
		msgChan, err = streamFromEventSourceNew(cancellableContext, resourceTreeURL, sessionToken, t)
		if err != nil {
			t.Logf("streamFromEventSource returned error: %v", err)
			return false
		}
		return true

	}, 5*time.Minute, 5*time.Second)

	requires.NotNil(msgChan)

	// Find pod of deployed Application, on autonomous cluster
	var podList corev1.PodList
	err = suite.AutonomousAgentClient.List(suite.Ctx, "guestbook", &podList, metav1.ListOptions{})
	requires.NoError(err)

	requires.True(len(podList.Items) == 1, "should only be one kustomize-guestbook pod")

	// Locate guestbook pod
	var oldPod corev1.Pod
	for idx := range podList.Items {
		pod := podList.Items[idx]
		if strings.Contains(pod.Name, "kustomize-guestbook-ui") {
			oldPod = pod
			break
		}
	}
	requires.NotEmpty(oldPod.Name)

	// Wait for the old pod to appear in the resource tree
	// This ensures that Redis proxy is fully working before proceeding
	// (the pod will only appear in the resource tree if redis is fully available)
	t.Logf("Waiting for pod %s to appear in resource tree (verifying Redis proxy is working)...", oldPod.Name)
	var tree *v1alpha1.ApplicationTree
	requires.Eventually(func() bool {
		var err error
		tree, err = appClient.ResourceTree(suite.Ctx, &application.ResourcesQuery{
			ApplicationName: &appOnPrincipal.Name,
			AppNamespace:    &appOnPrincipal.Namespace,
			Project:         &appOnPrincipal.Spec.Project,
		})
		if err != nil {
			t.Logf("ResourceTree error: %v", err)
			return false
		}
		if tree == nil {
			t.Logf("ResourceTree returned nil")
			return false
		}

		// Check if the old pod appears in the resource tree
		for _, node := range tree.Nodes {
			if node.Kind == "Pod" && node.Name == oldPod.Name {
				t.Logf("Pod %s found in resource tree - Redis proxy is working", oldPod.Name)
				return true
			}
		}
		return false

	}, time.Second*60, time.Second*5)

	requires.NotNil(tree)

	t.Log("deleting pod", time.Now())

	// Delete pod on managed agent cluster
	err = suite.AutonomousAgentClient.Delete(suite.Ctx, &oldPod, metav1.DeleteOptions{})
	requires.NoError(err)

	// Wait for new pod to be created, to replace the old one that was deleted
	var newPod corev1.Pod
	requires.Eventually(func() bool {
		var podList corev1.PodList
		err = suite.AutonomousAgentClient.List(suite.Ctx, "guestbook", &podList, metav1.ListOptions{})
		requires.NoError(err)

		for idx := range podList.Items {
			pod := podList.Items[idx]
			if strings.Contains(pod.Name, "kustomize-guestbook-ui") && pod.Name != oldPod.Name {
				newPod = pod
				break
			}
		}

		return newPod.Name != ""

	}, time.Second*60, time.Second*5)

	// Verify the name of the new pod exists in what has been sent on the subscribe channel

	requires.Eventually(func() bool {
		for {
			// drain channel looking for name of new pod
			select {
			case msg := <-msgChan:
				t.Log("Processing message:", msg)
				if strings.Contains(msg, newPod.Name) {
					t.Log("new pod name found:", newPod.Name)
					return true
				}
			default:
				return false
			}
		}
	}, time.Second*30, time.Second*5)

	// Ensure that the pod appears in the new resource tree value returned by Argo CD server
	tree, err = appClient.ResourceTree(suite.Ctx, &application.ResourcesQuery{
		ApplicationName: &appOnPrincipal.Name,
		AppNamespace:    &appOnPrincipal.Namespace,
		Project:         &appOnPrincipal.Spec.Project,
	})
	requires.NoError(err)
	requires.NotNil(tree)

	// Verify that the new pod appears in the resource tree
	matchFound := false
	for _, node := range tree.Nodes {
		if node.Kind == "Pod" && node.Name == newPod.Name {
			matchFound = true
			break
		}
	}
	requires.True(matchFound)
}

// ensureAppExistsAndIsSyncedAndHealthy ensures that a given Argo CD Application exists, and is synced/healthy
// - If the app does not exist, it is created.
// - If the app exists, it is deleted, then created.
// - If the created app never becomes healthy, it is deleted and recreated.
// - After X minutes, if the synced/healthy condition is never met, it stops trying and returns an error.
func ensureAppExistsAndIsSyncedAndHealthy(appParam *v1alpha1.Application, k8sClient fixture.KubeClient, suite *fixture.BaseSuite) error {

	overallExpireTime := time.Now().Add(time.Minute * 10)

	count := 0
outer:
	for {
		count++

		if count > 1 {
			time.Sleep(5 * time.Second) // Short delay between failures
		}

		if time.Now().After(overallExpireTime) {
			return fmt.Errorf("ensureSyncedAndHealthyApp expired")
		}

		appForGet := v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      appParam.Name,
				Namespace: appParam.Namespace,
			},
		}

		// Get the current application data
		err := k8sClient.Get(suite.Ctx, types.NamespacedName{Namespace: appForGet.Namespace, Name: appForGet.Name}, &appForGet, metav1.GetOptions{})
		if err == nil {
			// It exists, so needs to be deleted
			suite.T().Logf("Deleting: %s", appForGet.Name)
			if err := fixture.EnsureDeletion(suite.Ctx, k8sClient, &appForGet); err != nil {
				suite.T().Logf("unable to delete: %s %v", appForGet.Name, err)
				continue
			} else {
				// delete succeeded, so continue
			}
		} else {
			if !apierrors.IsNotFound(err) {
				suite.T().Logf("unexpected error when deleting: %s %v", appForGet.Name, err)
			} else {
				// error is a 'not found' error, which is expected
			}
		}

		appFromCreate := appParam.DeepCopy()
		suite.T().Logf("Creating: %s", appFromCreate.Name)
		err = k8sClient.Create(suite.Ctx, appFromCreate, metav1.CreateOptions{})
		if err != nil {
			suite.T().Logf("unexpected error when creating: %s %v", appFromCreate.Name, err)
			continue
		}

		// Wait X seconds for app to become synced/healthy
		singleRoundExpireTime := time.Now().Add(time.Minute * 3)
		for {

			err := k8sClient.Get(suite.Ctx, types.NamespacedName{Namespace: appFromCreate.Namespace, Name: appFromCreate.Name}, appFromCreate, metav1.GetOptions{})
			if err != nil {
				suite.T().Logf("unexpected error when getting after creation %s %v", appFromCreate.Name, err)
				continue
			}

			if appFromCreate.Status.Health.Status == health.HealthStatusHealthy && appFromCreate.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced {
				suite.T().Logf("success: application is synched/healthy: %s", appFromCreate.Name)
				// success
				return nil
			} else {
				suite.T().Logf("waiting for application to be synched/healthy: %s %s/%s", appFromCreate.Name, appFromCreate.Status.Health.Status, appFromCreate.Status.Sync.Status)
			}

			if time.Now().After(singleRoundExpireTime) {
				suite.T().Logf("application never became synched/healthy, so restarting: %s", appFromCreate.Name)
				continue outer
			}

			time.Sleep(time.Second * 5)

		}
	}
}

func createArgoCDAPIClient(ctx context.Context, argoServerEndpoint string, password string) (argocdclient.Client, string, io.Closer, error) {
	var token string

	clientOpts := &argocdclient.ClientOptions{
		ServerAddr: argoServerEndpoint,
		Insecure:   true,
		AuthToken:  password,
	}

	client, err := argocdclient.NewClient(clientOpts)
	if err != nil {
		return nil, "", nil, fmt.Errorf("unable to create new Argo CD client: %v", err)
	}

	closer, sessionClient, err := client.NewSessionClient()
	if err != nil {
		return nil, "", nil, fmt.Errorf("unable to create new Argo CD session client: %v", err)
	}

	sessionResponse, err := sessionClient.Create(ctx, &sessionpkg.SessionCreateRequest{Username: "admin", Password: password})
	if err != nil {
		return nil, "", nil, fmt.Errorf("unable to create invoke session client: %v", err)
	}
	token = sessionResponse.Token

	clientOpts = &argocdclient.ClientOptions{
		ServerAddr: argoServerEndpoint,
		Insecure:   true,
		AuthToken:  token,
	}

	client, err = argocdclient.NewClient(clientOpts)
	if err != nil {
		return nil, "", nil, fmt.Errorf("unable to create new argocd client: %v", err)
	}

	return client, token, closer, nil

}

// streamFromEventSource connects to event source API at given URL (using given token), and sends received data back on channel
// - resource tree events (changes in Application resources) are an example of one type of data that can be received via this API
func streamFromEventSourceNew(ctx context.Context, eventSourceAPIURL string, sessionToken string, t *testing.T) (chan string, error) {

	msgChan := make(chan string)

	go func() {

		connect := func(client *http.Client, req *http.Request) bool {
			// Once the context is cancelled the test may already be over, so return without touching t.
			resp, err := client.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return true
				}
				t.Logf("Error performing request: %v", err)

				return false
			}

			defer resp.Body.Close()

			reader := bufio.NewReader(resp.Body)
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					if ctx.Err() != nil {
						return true
					}
					t.Logf("Error reading from stream: %v", err)

					return false
				}

				if after, ok := strings.CutPrefix(line, "data:"); ok {
					data := strings.TrimSpace(after)
					// The send must stay cancellable: once the test stops reading, a plain send would block
					// this goroutine (and the response body) until the process exits.
					select {
					case <-ctx.Done():
						return true
					case msgChan <- data:
					}

				}
			}
		}

		for {

			req, err := http.NewRequest("GET", eventSourceAPIURL, nil)
			if err != nil {
				t.Errorf("Error creating request: %v", err)
				return
			}
			req.Header.Set("Accept", "text/event-stream") // server sent event mime type

			req = req.WithContext(ctx)

			cookie := &http.Cookie{
				Name:  "argocd.token",
				Value: sessionToken, // session token
			}
			req.AddCookie(cookie)

			tr := &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
			client := &http.Client{Transport: tr}

			contextCancelled := connect(client, req)

			if contextCancelled {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}

	}()

	return msgChan, nil
}

func TestRedisProxyTestSuite(t *testing.T) {
	suite.Run(t, new(RedisProxyTestSuite))
}
