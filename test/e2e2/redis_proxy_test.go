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

package e2e2

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

	"github.com/argoproj-labs/argocd-agent/test/e2e2/fixture"

	argocdclient "github.com/argoproj/argo-cd/v2/pkg/apiclient"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/application"
	sessionpkg "github.com/argoproj/argo-cd/v2/pkg/apiclient/session"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/gitops-engine/pkg/health"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type RedisProxyTestSuite struct {
	fixture.BaseSuite
}

func (suite *RedisProxyTestSuite) Test_RedisProxy_ManagedAgent_Argo() {
	requires := suite.Require()

	t := suite.T()

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
			Namespace: "agent-managed",
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps",
				TargetRevision: "HEAD",
				Path:           "kustomize-guestbook",
			},
			Destination: v1alpha1.ApplicationDestination{
				Name:      "agent-managed",
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

	err = suite.PrincipalClient.Create(suite.Ctx, &appOnPrincipal, metav1.CreateOptions{})
	requires.NoError(err)

	argocdClient, sessionToken, closer, err := createArgoCDAPIClient(suite.Ctx, argoEndpoint, password)
	requires.NoError(err)
	defer closer.Close()

	closer, appClient, err := argocdClient.NewApplicationClient()
	requires.NoError(err)
	defer closer.Close()

	syncAppWithAutoResync(&appOnPrincipal, appClient, &suite.BaseSuite)

	cancellableContext, cancelFunc := context.WithCancel(suite.Ctx)
	defer cancelFunc()

	resourceTreeURL := "https://" + argoEndpoint + "/api/v1/stream/applications/" + appOnPrincipal.Name + "/resource-tree?appNamespace=" + appOnPrincipal.Namespace

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

	// Find pod on managed-agent client

	var podList corev1.PodList
	err = suite.ManagedAgentClient.List(suite.Ctx, "guestbook", &podList, metav1.ListOptions{})
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

	// Ensure that the pod appears in the resource tree value returned by Argo CD server (this will only be true if redis proxy is working)
	tree, err := appClient.ResourceTree(suite.Ctx, &application.ResourcesQuery{
		ApplicationName: &appOnPrincipal.Name,
		AppNamespace:    &appOnPrincipal.Namespace,
		Project:         &appOnPrincipal.Spec.Project,
	})
	requires.NoError(err)

	requires.NotNil(tree)

	matchFound := false
	for _, node := range tree.Nodes {
		if node.Kind == "Pod" && node.Name == oldPod.Name {
			matchFound = true
			break
		}
	}
	requires.True(matchFound)

	// Delete pod on managed agent cluster
	err = suite.ManagedAgentClient.Delete(suite.Ctx, &oldPod, metav1.DeleteOptions{})
	requires.NoError(err)

	// Wait for new pod to be created, to replace the old one that was deleted
	var newPod corev1.Pod
	requires.Eventually(func() bool {
		var podList corev1.PodList
		err = suite.ManagedAgentClient.List(suite.Ctx, "guestbook", &podList, metav1.ListOptions{})
		requires.NoError(err)

		for idx := range podList.Items {
			pod := podList.Items[idx]
			if strings.Contains(pod.Name, "kustomize-guestbook-ui") && newPod.Name != oldPod.Name {
				newPod = pod
				break
			}
		}

		return newPod.Name != ""

	}, time.Second*30, time.Second*5)

	// Verify the name of the new pod exists in what has been sent from the channel (this will only be true if redis proxy subscription is working)
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

	matchFound = false
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

	// Create a autonomous agent application in the principal's cluster
	appOnAutonomous := v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: "argocd",
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

	err = suite.AutonomousAgentClient.Create(suite.Ctx, &appOnAutonomous, metav1.CreateOptions{})
	requires.NoError(err)

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

	syncAppWithAutoResync(&appOnPrincipal, appClient, &suite.BaseSuite)

	cancellableContext, cancelFunc := context.WithCancel(suite.Ctx)
	defer cancelFunc()

	// Stream resource tree events for the application
	resourceTreeURL := "https://" + argoEndpoint + "/api/v1/stream/applications/" + appOnPrincipal.Name + "/resource-tree?appNamespace=" + appOnPrincipal.Namespace

	t.Log("beginning stream", time.Now())

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

	var tree *v1alpha1.ApplicationTree
	requires.Eventually(func() bool {
		var err error
		// Ensure that the pod appears in the resource tree value returned by Argo CD server
		tree, err = appClient.ResourceTree(suite.Ctx, &application.ResourcesQuery{
			ApplicationName: &appOnPrincipal.Name,
			AppNamespace:    &appOnPrincipal.Namespace,
			Project:         &appOnPrincipal.Spec.Project,
		})
		if err != nil {
			t.Log(err)
			return false
		}

		return true

	}, time.Second*60, time.Second*5)

	requires.NotNil(tree)

	matchFound := false
	for _, node := range tree.Nodes {
		if node.Kind == "Pod" && node.Name == oldPod.Name {
			matchFound = true
			break
		}
	}
	requires.True(matchFound)

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
			if strings.Contains(pod.Name, "kustomize-guestbook-ui") && newPod.Name != oldPod.Name {
				newPod = pod
				break
			}
		}

		return newPod.Name != ""

	}, time.Second*30, time.Second*5)

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

	matchFound = false
	for _, node := range tree.Nodes {
		if node.Kind == "Pod" && node.Name == newPod.Name {
			matchFound = true
			break
		}
	}
	requires.True(matchFound)
}

func syncAppWithAutoResync(app *v1alpha1.Application, appClient application.ApplicationServiceClient, suite *fixture.BaseSuite) {

	requires := suite.Require()

	// Wait until the app is synced and healthy
	retries := 0
	requires.Eventually(func() bool {
		err := suite.PrincipalClient.Get(suite.Ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, app, metav1.GetOptions{})
		if err != nil {
			suite.T().Logf("Error on get: %v", err)
			return false
		}

		if app.Status.Sync.Status == v1alpha1.SyncStatusCodeSynced && app.Status.Health.Status == health.HealthStatusHealthy {
			return true
		} else {
			// Sometimes, the sync hangs on the workload cluster. We trigger
			// a sync every 5th or so retry.
			if retries > 0 && retries%5 == 0 {
				suite.T().Logf("Triggering re-sync")

				_, err := appClient.Sync(suite.Ctx, &application.ApplicationSyncRequest{
					Name:         &app.Name,
					AppNamespace: &app.Namespace,
				})
				if err != nil {
					suite.T().Logf("Error on sync: %v", err)
					return true
				}
			}
			retries += 1
		}
		return false
	}, 60*time.Second, 1*time.Second)

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

// streamFromEventSource connections to event source API at given URL (using given token), and sends received data back on channel
func streamFromEventSourceNew(ctx context.Context, eventSourceAPIURL string, sessionToken string, t *testing.T) (chan string, error) {

	msgChan := make(chan string)

	go func() {

		connect := func(client *http.Client, req *http.Request) bool {
			resp, err := client.Do(req)
			if err != nil {
				t.Logf("Error performing request: %v", err)

				return strings.Contains(err.Error(), "context canceled")
			}

			defer resp.Body.Close()

			reader := bufio.NewReader(resp.Body)
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					t.Logf("Error reading from stream: %v", err)

					return strings.Contains(err.Error(), "context canceled")
				}

				if strings.HasPrefix(line, "data:") {
					data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
					select {
					case <-ctx.Done():
						t.Log("Context is complete")
						return true
					default:
						msgChan <- data
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
				t.Log("context cancelled on event source stream")
				return
			} else {
				time.Sleep(250 * time.Millisecond)
			}
		}

	}()

	return msgChan, nil
}

// streamFromEventSource connections to event source API at given URL (using given token), and sends received data back on channel
// func streamFromEventSourceOld(ctx context.Context, eventSourceAPIURL string, sessionToken string, t *testing.T) (chan string, error) {

// 	msgChan := make(chan string)

// 	req, err := http.NewRequest("GET", eventSourceAPIURL, nil)
// 	if err != nil {
// 		t.Logf("Error creating request: %v", err)
// 		return nil, fmt.Errorf("error creating request: %v", err)
// 	}
// 	req.Header.Set("Accept", "text/event-stream") // server sent event mime type

// 	req = req.WithContext(ctx)

// 	cookie := &http.Cookie{
// 		Name:  "argocd.token",
// 		Value: sessionToken, // session token
// 	}
// 	req.AddCookie(cookie)

// 	tr := &http.Transport{
// 		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
// 	}
// 	client := &http.Client{Transport: tr}

// 	go func() {
// 		resp, err := client.Do(req)
// 		if err != nil {
// 			t.Logf("Error performing request: %v", err)
// 			return
// 		}

// 		defer resp.Body.Close()

// 		reader := bufio.NewReader(resp.Body)
// 		for {
// 			line, err := reader.ReadString('\n')
// 			if err != nil {
// 				t.Logf("Error reading from stream: %v", err)
// 				return
// 			}

// 			if strings.HasPrefix(line, "data:") {
// 				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
// 				select {
// 				case <-ctx.Done():
// 					t.Log("Context is complete")
// 					return
// 				default:
// 					msgChan <- data
// 				}

// 			}
// 		}

// 	}()

// 	return msgChan, nil
// }

func TestRedisProxyTestSuite(t *testing.T) {
	suite.Run(t, new(RedisProxyTestSuite))
}
