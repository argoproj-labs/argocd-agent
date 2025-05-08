package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	t.Run("Find manager by owner reference", func(t *testing.T) {
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
				Labels: map[string]string{
					"app.kubernetes.io/instance": "some-app",
				},
			},
		}
		kubeclient := kube.NewDynamicFakeClient(pod1, pod2)

		un := objToUnstructured(pod1)

		isOwned, err := isResourceManaged(kubeclient, un, 5)
		assert.True(t, isOwned)
		assert.NoError(t, err)
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
			context:    context.Background(),
			kubeClient: kubeClient,
			queues:     queue.NewSendRecvQueues(),
			emitter:    event.NewEventSource("test-agent"),
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
			context:    context.Background(),
			kubeClient: kubeClient,
			queues:     queue.NewSendRecvQueues(),
			emitter:    event.NewEventSource("test-agent"),
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
			context:    context.Background(),
			kubeClient: kubeClient,
			queues:     queue.NewSendRecvQueues(),
			emitter:    event.NewEventSource("test-agent"),
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
			context:    context.Background(),
			kubeClient: kube.NewDynamicFakeClient(),
			queues:     queue.NewSendRecvQueues(),
			emitter:    event.NewEventSource("test-agent"),
		}
		require.NoError(t, agent.queues.Create(defaultQueueName))

		// Create invalid event (missing required data)
		ev := cloudevents.NewEvent()
		ev.SetType(event.GetRequest.String())

		// Process the request
		err := agent.processIncomingResourceRequest(event.New(&ev, event.TargetResource))
		assert.Error(t, err) // Should return error for invalid request
	})
}

func Test_getAvailableResources(t *testing.T) {
	t.Run("Error when resource is specified", func(t *testing.T) {
		agent := &Agent{
			context:    context.Background(),
			kubeClient: kube.NewDynamicFakeClient(),
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
			context:    context.Background(),
			kubeClient: kube.NewDynamicFakeClient(pod1, pod2),
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
			context:    context.Background(),
			kubeClient: kube.NewDynamicFakeClient(node1, node2),
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
