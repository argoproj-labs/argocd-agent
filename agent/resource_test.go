package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func Test_isResourceOwnedByApp(t *testing.T) {
	t.Run("Find manager by label", func(t *testing.T) {
		pod1 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
				Labels: map[string]string{
					"app.kubernetes.io/instance": "some-app",
				},
			},
		}
		kubeclient := kube.NewDynamicFakeClient(pod1)

		un := objToUnstructured(pod1)

		isOwned, err := isResourceManaged(kubeclient, un, 5)
		assert.True(t, isOwned)
		assert.NoError(t, err)
	})
	t.Run("Find manager by annotation", func(t *testing.T) {
		pod1 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
				Annotations: map[string]string{
					"argocd.argoproj.io/tracking-id": "some-app",
				},
			},
		}
		kubeclient := kube.NewDynamicFakeClient(pod1)

		un := objToUnstructured(pod1)

		isOwned, err := isResourceManaged(kubeclient, un, 5)
		assert.True(t, isOwned)
		assert.NoError(t, err)
	})
	t.Run("Find manager by owner reference recursively", func(t *testing.T) {
		depl := &appsv1.Deployment{
			ObjectMeta: v1.ObjectMeta{
				Name:      "depl1",
				Namespace: "default",
				Labels: map[string]string{
					"app.kubernetes.io/instance": "some-app",
				},
			},
		}
		rs1 := &appsv1.ReplicaSet{
			ObjectMeta: v1.ObjectMeta{
				Name:      "rs1",
				Namespace: "default",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "depl1",
					},
				},
			},
		}
		pod1 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "ReplicaSet",
						Name:       "rs1",
					},
				},
			},
		}
		kubeclient := kube.NewDynamicFakeClient(depl, rs1, pod1)

		un := objToUnstructured(pod1)

		isOwned, err := isResourceManaged(kubeclient, un, 5)
		assert.True(t, isOwned)
		assert.NoError(t, err)
	})
	t.Run("Cluster-scoped owner reference", func(t *testing.T) {
		ns := &corev1.Namespace{
			ObjectMeta: v1.ObjectMeta{
				Name:      "ns1",
				Namespace: "",
				Labels: map[string]string{
					"app.kubernetes.io/instance": "some-app",
				},
			},
		}
		rs1 := &appsv1.ReplicaSet{
			ObjectMeta: v1.ObjectMeta{
				Name:      "rs1",
				Namespace: "default",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "v1",
						Kind:       "Namespace",
						Name:       "ns1",
					},
				},
			},
		}
		pod1 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "ReplicaSet",
						Name:       "rs1",
					},
				},
			},
		}
		kubeclient := kube.NewDynamicFakeClient(ns, rs1, pod1)

		un := objToUnstructured(pod1)

		isOwned, err := isResourceManaged(kubeclient, un, 5)
		assert.True(t, isOwned)
		assert.NoError(t, err)
	})

	t.Run("Invalid owner reference", func(t *testing.T) {
		depl := &appsv1.Deployment{
			ObjectMeta: v1.ObjectMeta{
				Name:      "depl1",
				Namespace: "argocd",
				Labels: map[string]string{
					"app.kubernetes.io/instance": "some-app",
				},
			},
		}
		rs1 := &appsv1.ReplicaSet{
			ObjectMeta: v1.ObjectMeta{
				Name:      "rs1",
				Namespace: "default",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "depl1",
					},
				},
			},
		}
		pod1 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "ReplicaSet",
						Name:       "rs1",
					},
				},
			},
		}
		kubeclient := kube.NewDynamicFakeClient(depl, rs1, pod1)

		un := objToUnstructured(pod1)

		isOwned, err := isResourceManaged(kubeclient, un, 5)
		assert.False(t, isOwned)
		assert.Error(t, err)
	})
	t.Run("Not managed", func(t *testing.T) {
		pod1 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
			},
		}
		kubeclient := kube.NewDynamicFakeClient(pod1)

		un := objToUnstructured(pod1)

		isOwned, err := isResourceManaged(kubeclient, un, 5)
		assert.False(t, isOwned)
		assert.NoError(t, err)
	})

	t.Run("Owner reference not found", func(t *testing.T) {
		pod1 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "v1",
						Kind:       "Pod",
						Name:       "pod2",
						UID:        "1234",
					},
				},
			},
		}
		kubeclient := kube.NewDynamicFakeClient(pod1)
		un := objToUnstructured(pod1)
		isOwned, err := isResourceManaged(kubeclient, un, 5)
		assert.False(t, isOwned)
		assert.True(t, errors.IsNotFound(err))
	})

	t.Run("Recursion limit reached", func(t *testing.T) {
		pod1 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "v1",
						Kind:       "Pod",
						Name:       "pod2",
						UID:        "1234",
					},
				},
			},
		}
		pod2 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod2",
				Namespace: "default",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "v1",
						Kind:       "Pod",
						Name:       "pod3",
						UID:        "1234",
					},
				},
			},
		}
		pod3 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod3",
				Namespace: "default",
				Labels: map[string]string{
					"app.kubernetes.io/instance": "some-app",
				},
			},
		}

		kubeclient := kube.NewDynamicFakeClient(pod1, pod2, pod3)

		un := objToUnstructured(pod1)

		isOwned, err := isResourceManaged(kubeclient, un, 3)
		assert.True(t, isOwned)
		assert.NoError(t, err)

		isOwned, err = isResourceManaged(kubeclient, un, 2)
		assert.False(t, isOwned)
		assert.ErrorContains(t, err, "recursion limit reached")
	})
}

func objToUnstructured(obj any) *unstructured.Unstructured {
	unObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: unObj}
}

func Test_processIncomingResourceRequest(t *testing.T) {
	t.Run("Successfully get managed resource", func(t *testing.T) {
		// Create a test pod that is managed by Argo CD
		pod := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				Labels: map[string]string{
					"app.kubernetes.io/instance": "test-app", // This label indicates Argo CD management
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "test-image",
					},
				},
			},
		}

		// Setup test environment
		kubeClient := kube.NewDynamicFakeClient(pod)
		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kubeClient,
			queues:              queue.NewSendRecvQueues(),
			emitter:             event.NewEventSource("test-agent"),
			enableResourceProxy: true,
		}
		require.NoError(t, agent.queues.Create(defaultQueueName))

		// Create resource request event
		reqUUID := "test-uuid"
		resourceReq := &event.ResourceRequest{
			UUID:      reqUUID,
			Name:      "test-pod",
			Namespace: "default",
			GroupVersionResource: v1.GroupVersionResource{
				Group:    "",
				Version:  "v1",
				Resource: "pods",
			},
			Method: http.MethodGet,
		}

		ev := cloudevents.NewEvent()
		ev.SetType(event.GetRequest.String())
		require.NoError(t, ev.SetData(cloudevents.ApplicationJSON, resourceReq))

		// Process the request
		err := agent.processIncomingResourceRequest(event.New(&ev, event.TargetResource))
		require.NoError(t, err)

		// Verify response
		q := agent.queues.SendQ(defaultQueueName)
		require.NotNil(t, q)

		responseEv, shutdown := q.Get()
		assert.False(t, shutdown)
		require.NotNil(t, responseEv)

		// Verify response data
		resp := &event.ResourceResponse{}
		err = responseEv.DataAs(resp)
		require.NoError(t, err)
		assert.Equal(t, reqUUID, resp.UUID)
		assert.Equal(t, 200, resp.Status)

		// Verify resource data
		var responseResource unstructured.Unstructured
		err = json.Unmarshal([]byte(resp.Resource), &responseResource)
		require.NoError(t, err)
		assert.Equal(t, "test-pod", responseResource.GetName())
		assert.Equal(t, "default", responseResource.GetNamespace())
	})

	t.Run("Get unmanaged resource returns error", func(t *testing.T) {
		// Create an unmanaged pod (no Argo CD labels)
		pod := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "unmanaged-pod",
				Namespace: "default",
			},
		}

		// Setup test environment
		kubeClient := kube.NewDynamicFakeClient(pod)
		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kubeClient,
			queues:              queue.NewSendRecvQueues(),
			emitter:             event.NewEventSource("test-agent"),
			enableResourceProxy: true,
		}
		require.NoError(t, agent.queues.Create(defaultQueueName))

		// Create resource request event
		reqUUID := "test-uuid"
		resourceReq := &event.ResourceRequest{
			UUID:      reqUUID,
			Name:      "unmanaged-pod",
			Namespace: "default",
			GroupVersionResource: v1.GroupVersionResource{
				Group:    "",
				Version:  "v1",
				Resource: "pods",
			},
			Method: http.MethodGet,
		}

		ev := cloudevents.NewEvent()
		ev.SetType(event.GetRequest.String())
		require.NoError(t, ev.SetData(cloudevents.ApplicationJSON, resourceReq))

		// Process the request
		err := agent.processIncomingResourceRequest(event.New(&ev, event.TargetResource))
		require.NoError(t, err)

		// Verify error response
		q := agent.queues.SendQ(defaultQueueName)
		require.NotNil(t, q)

		responseEv, shutdown := q.Get()
		assert.False(t, shutdown)
		require.NotNil(t, responseEv)

		resp := &event.ResourceResponse{}
		err = responseEv.DataAs(resp)
		require.NoError(t, err)
		assert.Equal(t, reqUUID, resp.UUID)
		assert.Equal(t, 403, resp.Status) // Should return forbidden for unmanaged resources
	})

	t.Run("Resource not found returns error", func(t *testing.T) {
		// Setup test environment with empty client
		kubeClient := kube.NewDynamicFakeClient()
		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kubeClient,
			queues:              queue.NewSendRecvQueues(),
			emitter:             event.NewEventSource("test-agent"),
			enableResourceProxy: true,
		}
		require.NoError(t, agent.queues.Create(defaultQueueName))

		// Create resource request event
		reqUUID := "test-uuid"
		resourceReq := &event.ResourceRequest{
			UUID:      reqUUID,
			Name:      "nonexistent-pod",
			Namespace: "default",
			GroupVersionResource: v1.GroupVersionResource{
				Group:    "",
				Version:  "v1",
				Resource: "pods",
			},
			Method: http.MethodGet,
		}

		ev := cloudevents.NewEvent()
		ev.SetType(event.GetRequest.String())
		require.NoError(t, ev.SetData(cloudevents.ApplicationJSON, resourceReq))

		// Process the request
		err := agent.processIncomingResourceRequest(event.New(&ev, event.TargetResource))
		require.NoError(t, err)

		// Verify error response
		q := agent.queues.SendQ(defaultQueueName)
		require.NotNil(t, q)

		responseEv, shutdown := q.Get()
		assert.False(t, shutdown)
		require.NotNil(t, responseEv)

		resp := &event.ResourceResponse{}
		err = responseEv.DataAs(resp)
		require.NoError(t, err)
		assert.Equal(t, reqUUID, resp.UUID)
		assert.Equal(t, 404, resp.Status) // Should return not found
	})

	t.Run("Invalid request returns error", func(t *testing.T) {
		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kube.NewDynamicFakeClient(),
			queues:              queue.NewSendRecvQueues(),
			emitter:             event.NewEventSource("test-agent"),
			enableResourceProxy: true,
		}
		require.NoError(t, agent.queues.Create(defaultQueueName))

		// Create invalid event (missing required data)
		ev := cloudevents.NewEvent()
		ev.SetType(event.GetRequest.String())

		// Process the request
		err := agent.processIncomingResourceRequest(event.New(&ev, event.TargetResource))
		assert.NoError(t, err)
	})
}

func Test_getAvailableResources(t *testing.T) {
	t.Run("Error when resource is specified", func(t *testing.T) {
		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kube.NewDynamicFakeClient(),
			enableResourceProxy: true,
		}

		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "pods", // Resource should be empty for API discovery
		}

		_, err := agent.getAvailableResources(context.Background(), gvr, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not authorized to list resources")
	})

	t.Run("Successfully list namespaced resources", func(t *testing.T) {
		// Create test pods
		pod1 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
			},
		}
		pod2 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod2",
				Namespace: "default",
			},
		}

		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kube.NewDynamicFakeClient(pod1, pod2),
			enableResourceProxy: true,
		}

		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "apiresources",
		}

		list, err := agent.getAvailableResources(context.Background(), gvr, "default")
		assert.NoError(t, err)
		assert.NotNil(t, list)
	})

	t.Run("Successfully list cluster-scoped resources", func(t *testing.T) {
		// Create test nodes (cluster-scoped resource)
		node1 := &corev1.Node{
			ObjectMeta: v1.ObjectMeta{
				Name: "node1",
			},
		}
		node2 := &corev1.Node{
			ObjectMeta: v1.ObjectMeta{
				Name: "node2",
			},
		}

		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kube.NewDynamicFakeClient(node1, node2),
			enableResourceProxy: true,
		}

		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "apigroups",
		}

		list, err := agent.getAvailableResources(context.Background(), gvr, "")
		assert.NoError(t, err)
		assert.NotNil(t, list)
	})
}

func Test_processIncomingPostResourceRequest(t *testing.T) {
	type testCase struct {
		name         string
		bodyObj      any
		params       map[string]string
		namespace    string
		expectErr    bool
		expectCreate bool
	}

	tests := []testCase{
		{
			name: "Successfully creates resource",
			bodyObj: &corev1.Pod{
				TypeMeta: v1.TypeMeta{
					Kind: "Pod",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:      "mypod2",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "c", Image: "nginx"},
					},
				},
			},
			params:       map[string]string{},
			namespace:    "default",
			expectErr:    false,
			expectCreate: true,
		},
		{
			name: "Create returns error",
			bodyObj: &corev1.Pod{
				ObjectMeta: v1.ObjectMeta{
					Name:      "mypod",
					Namespace: "nonexistent",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "c", Image: "nginx"},
					},
				},
			},
			params:       map[string]string{},
			namespace:    "nonexistent",
			expectErr:    true,
			expectCreate: false,
		},
		{
			name:         "Fails to unmarshal body",
			bodyObj:      "{invalid-json",
			params:       map[string]string{},
			namespace:    "default",
			expectErr:    true,
			expectCreate: false,
		},
		{
			name: "Passes CreateOptions params",
			bodyObj: &corev1.Pod{
				TypeMeta: v1.TypeMeta{
					Kind: "Pod",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:      "mypod2",
					Namespace: "default",
				},
			},
			params: map[string]string{
				"dryRun":          "All",
				"fieldValidation": "Strict",
				"fieldManager":    "test-manager",
			},
			namespace:    "default",
			expectErr:    false,
			expectCreate: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			var err error
			body, err = json.Marshal(tc.bodyObj)
			require.NoError(t, err)

			kubeClient := kube.NewDynamicFakeClient()
			agent := &Agent{
				context:    context.Background(),
				kubeClient: kubeClient,
			}

			fakeDyn := fake.NewSimpleDynamicClient(runtime.NewScheme())
			if tc.name == "Create returns error" {
				fakeDyn.PrependReactor("create", "pods", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, fmt.Errorf("failed to create pod")
				})
				kubeClient.DynamicClient = fakeDyn
			}
			kubeClient.DynamicClient = fakeDyn

			gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
			req := &event.ResourceRequest{
				Body:      body,
				Params:    tc.params,
				Namespace: tc.namespace,
				Method:    http.MethodPost,
				GroupVersionResource: v1.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "pods",
				},
			}

			res, err := agent.processIncomingPostResourceRequest(context.Background(), req, gvr)
			if tc.expectErr {
				assert.Error(t, err)
				assert.Nil(t, res)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, res)
				if tc.expectCreate {
					assert.Equal(t, "mypod2", res.GetName())
				}
			}
		})
	}
}

func Test_processIncomingPatchResourceRequest(t *testing.T) {
	type testCase struct {
		name          string
		params        map[string]string
		namespace     string
		patchBody     []byte
		setupReactor  func(fakeDyn *fake.FakeDynamicClient)
		expectErr     bool
		expectPatched bool
		forceValue    string
	}

	originalObj := &corev1.Pod{
		ObjectMeta: v1.ObjectMeta{
			Name:      "mypod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "c", Image: "nginx"},
			},
		},
	}
	patch := []byte(`{"metadata":{"labels":{"patched":"true"}}}`)

	tests := []testCase{
		{
			name:          "Successfully patches resource",
			params:        map[string]string{},
			namespace:     "default",
			patchBody:     patch,
			setupReactor:  func(fakeDyn *fake.FakeDynamicClient) {},
			expectErr:     false,
			expectPatched: true,
		},
		{
			name:      "Patch returns error",
			params:    map[string]string{},
			namespace: "default",
			patchBody: patch,
			setupReactor: func(fakeDyn *fake.FakeDynamicClient) {
				fakeDyn.PrependReactor("patch", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, fmt.Errorf("patch failed")
				})
			},
			expectErr:     true,
			expectPatched: false,
		},
		{
			name:          "Invalid force param returns error",
			params:        map[string]string{"force": "notabool"},
			namespace:     "default",
			patchBody:     patch,
			setupReactor:  func(fakeDyn *fake.FakeDynamicClient) {},
			expectErr:     true,
			expectPatched: false,
		},
		{
			name:          "Passes PatchOptions params",
			params:        map[string]string{"dryRun": "All", "fieldValidation": "Strict", "fieldManager": "test-manager", "force": "true"},
			namespace:     "default",
			patchBody:     patch,
			setupReactor:  func(fakeDyn *fake.FakeDynamicClient) {},
			expectErr:     false,
			expectPatched: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kubeClient := kube.NewDynamicFakeClient(originalObj)
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			fakeDyn := fake.NewSimpleDynamicClient(scheme, originalObj)
			tc.setupReactor(fakeDyn)
			kubeClient.DynamicClient = fakeDyn

			agent := &Agent{
				context:    context.Background(),
				kubeClient: kubeClient,
			}

			gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
			req := &event.ResourceRequest{
				Name:      "mypod",
				Body:      tc.patchBody,
				Params:    tc.params,
				Namespace: tc.namespace,
				Method:    http.MethodPatch,
				GroupVersionResource: v1.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "pods",
				},
			}

			res, err := agent.processIncomingPatchResourceRequest(context.Background(), req, gvr)
			if tc.expectErr {
				assert.Error(t, err)
				assert.Nil(t, res)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, res)
				if tc.expectPatched {
					labels := res.GetLabels()
					assert.Equal(t, "true", labels["patched"])
				}
			}
		})
	}
}

func Test_getAvailableAPIs(t *testing.T) {
	t.Run("Successfully get API resources for apps/v1", func(t *testing.T) {
		agent := &Agent{
			context:    context.Background(),
			kubeClient: kube.NewDynamicFakeClient(),
		}

		result, err := agent.getAvailableAPIs(context.Background(), "apps", "v1")
		assert.NoError(t, err)
		assert.NotNil(t, result)

		// The runtime converter preserves the original structure in Object map
		// Check that groupVersion is set correctly
		groupVersion, exists := result.Object["groupVersion"]
		assert.True(t, exists)
		assert.Equal(t, "apps/v1", groupVersion)

		// Check that resources array exists and has content
		resources, exists := result.Object["resources"]
		assert.True(t, exists)
		resourcesList, ok := resources.([]interface{})
		assert.True(t, ok)
		assert.Greater(t, len(resourcesList), 0)

		// Check that the first resource has the expected fields
		firstResource, ok := resourcesList[0].(map[string]interface{})
		assert.True(t, ok)
		assert.Contains(t, firstResource, "name")
		assert.Contains(t, firstResource, "kind")
		assert.Contains(t, firstResource, "verbs")
		assert.Contains(t, firstResource, "namespaced")
		assert.Contains(t, firstResource, "version")
		assert.Equal(t, "v1", firstResource["version"])
	})

	t.Run("Successfully get API resources for core v1", func(t *testing.T) {
		agent := &Agent{
			context:    context.Background(),
			kubeClient: kube.NewDynamicFakeClient(),
		}

		result, err := agent.getAvailableAPIs(context.Background(), "", "v1")
		assert.NoError(t, err)
		assert.NotNil(t, result)

		// Check that groupVersion is set correctly for core API
		groupVersion, exists := result.Object["groupVersion"]
		assert.True(t, exists)
		assert.Equal(t, "v1", groupVersion)

		// Check that resources array exists and has content
		resources, exists := result.Object["resources"]
		assert.True(t, exists)
		resourcesList, ok := resources.([]interface{})
		assert.True(t, ok)
		assert.Greater(t, len(resourcesList), 0)

		// Check that the first resource has the expected fields
		firstResource, ok := resourcesList[0].(map[string]interface{})
		assert.True(t, ok)
		assert.Contains(t, firstResource, "name")
		assert.Contains(t, firstResource, "kind")
		assert.Contains(t, firstResource, "verbs")
		assert.Contains(t, firstResource, "namespaced")
		assert.Contains(t, firstResource, "version")
		assert.Equal(t, "v1", firstResource["version"])
	})

	t.Run("Successfully get all API groups when group and version are empty", func(t *testing.T) {
		agent := &Agent{
			context:    context.Background(),
			kubeClient: kube.NewDynamicFakeClient(),
		}

		result, err := agent.getAvailableAPIs(context.Background(), "", "")
		assert.NoError(t, err)
		assert.NotNil(t, result)

		// Check that groups array exists and has content
		groups, exists := result.Object["groups"]
		assert.True(t, exists)
		groupsList, ok := groups.([]interface{})
		assert.True(t, ok)
		assert.Greater(t, len(groupsList), 0)

		// Check that the first group has the expected fields
		firstGroup, ok := groupsList[0].(map[string]interface{})
		assert.True(t, ok)
		assert.Contains(t, firstGroup, "name")
		assert.Contains(t, firstGroup, "versions")
		assert.Contains(t, firstGroup, "preferredVersion")

		// Check that versions array exists and has content
		versions, exists := firstGroup["versions"]
		assert.True(t, exists)
		versionsList, ok := versions.([]interface{})
		assert.True(t, ok)
		assert.Greater(t, len(versionsList), 0)

		// Check that the first version has the expected fields
		firstVersion, ok := versionsList[0].(map[string]interface{})
		assert.True(t, ok)
		assert.Contains(t, firstVersion, "groupVersion")
		assert.Contains(t, firstVersion, "version")
	})

	t.Run("Returns error for non-existent API group", func(t *testing.T) {
		agent := &Agent{
			context:    context.Background(),
			kubeClient: kube.NewDynamicFakeClient(),
		}

		result, err := agent.getAvailableAPIs(context.Background(), "nonexistent", "v1")
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func Test_processIncomingDeleteResourceRequest(t *testing.T) {
	type testCase struct {
		name         string
		namespace    string
		resourceName string
		deleteBody   []byte
		expectErr    bool
		managedByApp bool
	}

	validDeleteOptions := v1.DeleteOptions{
		GracePeriodSeconds: func() *int64 { v := int64(30); return &v }(),
		PropagationPolicy:  func() *v1.DeletionPropagation { v := v1.DeletePropagationForeground; return &v }(),
	}
	validDeleteBody, _ := json.Marshal(validDeleteOptions)
	invalidDeleteBody := []byte(`{"invalid": json}`)

	// Create a test pod for deletion
	originalObj := &corev1.Pod{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "c", Image: "nginx"},
			},
		},
	}

	tests := []testCase{
		{
			name:         "Successfully deletes resource",
			namespace:    "default",
			resourceName: "test-pod",
			deleteBody:   validDeleteBody,
			expectErr:    false,
			managedByApp: true,
		},
		{
			name:         "Successfully deletes resource with empty delete options",
			namespace:    "default",
			resourceName: "test-pod",
			deleteBody:   []byte(`{}`),
			expectErr:    false,
			managedByApp: true,
		},
		{
			name:         "Returns error for invalid JSON delete options",
			namespace:    "default",
			resourceName: "test-pod",
			deleteBody:   invalidDeleteBody,
			expectErr:    true,
			managedByApp: true,
		},
		{
			name:         "Returns error for unmanaged resource",
			namespace:    "default",
			resourceName: "test-pod",
			deleteBody:   validDeleteBody,
			expectErr:    true,
			managedByApp: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			obj := originalObj.DeepCopy()
			if tc.managedByApp {
				obj.ObjectMeta.Labels = map[string]string{
					"app.kubernetes.io/instance": "some-app",
				}
			}

			kubeClient := kube.NewDynamicFakeClient(obj)
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			fakeDyn := fake.NewSimpleDynamicClient(scheme, obj)
			kubeClient.DynamicClient = fakeDyn

			agent := &Agent{
				context:    context.Background(),
				kubeClient: kubeClient,
			}

			gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

			req := &event.ResourceRequest{
				Name:      tc.resourceName,
				Body:      tc.deleteBody,
				Namespace: tc.namespace,
				Method:    http.MethodDelete,
				GroupVersionResource: v1.GroupVersionResource{
					Group:    gvr.Group,
					Version:  gvr.Version,
					Resource: gvr.Resource,
				},
			}

			err := agent.processIncomingDeleteResourceRequest(context.Background(), req, gvr)

			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if !tc.expectErr {
				_, err := kubeClient.DynamicClient.Resource(gvr).Namespace(tc.namespace).Get(context.Background(), tc.resourceName, v1.GetOptions{})
				assert.Error(t, err)
				assert.True(t, errors.IsNotFound(err))
			}
		})
	}
}

func TestBuildSubresourcePath(t *testing.T) {
	agent := &Agent{}
	
	testCases := []struct {
		name        string
		gvr         schema.GroupVersionResource
		resName     string
		namespace   string
		subresource string
		expected    string
	}{
		{
			name:        "Core API namespaced resource",
			gvr:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			resName:     "my-pod",
			namespace:   "default",
			subresource: "status",
			expected:    "/api/v1/namespaces/default/pods/my-pod/status",
		},
		{
			name:        "Named group API namespaced resource",
			gvr:         schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
			resName:     "my-deployment",
			namespace:   "production",
			subresource: "scale",
			expected:    "/apis/apps/v1/namespaces/production/deployments/my-deployment/scale",
		},
		{
			name:        "Core API cluster-scoped resource",
			gvr:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"},
			resName:     "node1",
			namespace:   "",
			subresource: "status",
			expected:    "/api/v1/nodes/node1/status",
		},
		{
			name:        "Named group API cluster-scoped resource",
			gvr:         schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"},
			resName:     "admin",
			namespace:   "",
			subresource: "status",
			expected:    "/apis/rbac.authorization.k8s.io/v1/clusterroles/admin/status",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := agent.buildSubresourcePath(tc.gvr, tc.resName, tc.namespace, tc.subresource)
			assert.Equal(t, tc.expected, path)
		})
	}
}
