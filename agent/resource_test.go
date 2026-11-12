package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/event/targets"
	agentkube "github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
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
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func Test_isResourceOwnedByApp(t *testing.T) {
	labelTrackingReader := newTestTrackingReader(v1alpha1.TrackingMethodLabel)

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

		isOwned, err := isResourceManaged(kubeclient, un, 5, labelTrackingReader)
		assert.True(t, isOwned)
		assert.NoError(t, err)
	})
	t.Run("Find manager by annotation", func(t *testing.T) {
		annotationTrackingReader := newTestTrackingReader(v1alpha1.TrackingMethodAnnotation)
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

		isOwned, err := isResourceManaged(kubeclient, un, 5, annotationTrackingReader)
		assert.True(t, isOwned)
		assert.NoError(t, err)
	})
	t.Run("Find manager by custom label key", func(t *testing.T) {
		customLabelTrackingReader := newTestTrackingReaderWithLabelKey(v1alpha1.TrackingMethodLabel, "argocd.argoproj.io/instance")
		pod1 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
				Labels: map[string]string{
					"argocd.argoproj.io/instance": "some-app",
				},
			},
		}
		kubeclient := kube.NewDynamicFakeClient(pod1)

		un := objToUnstructured(pod1)

		isOwned, err := isResourceManaged(kubeclient, un, 5, customLabelTrackingReader)
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

		isOwned, err := isResourceManaged(kubeclient, un, 5, labelTrackingReader)
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

		isOwned, err := isResourceManaged(kubeclient, un, 5, labelTrackingReader)
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

		isOwned, err := isResourceManaged(kubeclient, un, 5, labelTrackingReader)
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

		isOwned, err := isResourceManaged(kubeclient, un, 5, labelTrackingReader)
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
		isOwned, err := isResourceManaged(kubeclient, un, 5, labelTrackingReader)
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

		isOwned, err := isResourceManaged(kubeclient, un, 3, labelTrackingReader)
		assert.True(t, isOwned)
		assert.NoError(t, err)

		isOwned, err = isResourceManaged(kubeclient, un, 2, labelTrackingReader)
		assert.False(t, isOwned)
		assert.ErrorContains(t, err, "recursion limit reached")
	})

	// Test different tracking methods
	t.Run("Label-only tracking method", func(t *testing.T) {
		labelOnlyReader := newTestTrackingReader(v1alpha1.TrackingMethodLabel)

		// Should be tracked, has label
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
		isOwned, err := isResourceManaged(kubeclient, un, 5, labelOnlyReader)
		assert.True(t, isOwned)
		assert.NoError(t, err)

		// Should not be tracked, only has annotation.
		pod2 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod2",
				Namespace: "default",
				Annotations: map[string]string{
					"argocd.argoproj.io/tracking-id": "some-app",
				},
			},
		}
		kubeclient2 := kube.NewDynamicFakeClient(pod2)
		un2 := objToUnstructured(pod2)
		isOwned, err = isResourceManaged(kubeclient2, un2, 5, labelOnlyReader)
		assert.False(t, isOwned)
		assert.NoError(t, err)
	})

	t.Run("Annotation-only tracking method", func(t *testing.T) {
		annotationOnlyReader := newTestTrackingReader(v1alpha1.TrackingMethodAnnotation)

		// Should be tracked, has annotation
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
		isOwned, err := isResourceManaged(kubeclient, un, 5, annotationOnlyReader)
		assert.True(t, isOwned)
		assert.NoError(t, err)

		// Should not be tracked, only has label
		pod2 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod2",
				Namespace: "default",
				Labels: map[string]string{
					"app.kubernetes.io/instance": "some-app",
				},
			},
		}
		kubeclient2 := kube.NewDynamicFakeClient(pod2)
		un2 := objToUnstructured(pod2)
		isOwned, err = isResourceManaged(kubeclient2, un2, 5, annotationOnlyReader)
		assert.False(t, isOwned)
		assert.NoError(t, err)
	})

	t.Run("Annotation+Label tracking method", func(t *testing.T) {
		annotationAndLabelReader := newTestTrackingReader(v1alpha1.TrackingMethodAnnotationAndLabel)

		// Should be tracked, has both annotation AND label
		pod1 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
				Annotations: map[string]string{
					"argocd.argoproj.io/tracking-id": "some-app",
				},
				Labels: map[string]string{
					"app.kubernetes.io/instance": "some-app",
				},
			},
		}
		kubeclient := kube.NewDynamicFakeClient(pod1)
		un := objToUnstructured(pod1)
		isOwned, err := isResourceManaged(kubeclient, un, 5, annotationAndLabelReader)
		assert.True(t, isOwned)
		assert.NoError(t, err)

		// Should be tracked, annotation+label uses the annotation for tracking.
		pod2 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod2",
				Namespace: "default",
				Annotations: map[string]string{
					"argocd.argoproj.io/tracking-id": "some-app",
				},
			},
		}
		kubeclient2 := kube.NewDynamicFakeClient(pod2)
		un2 := objToUnstructured(pod2)
		isOwned, err = isResourceManaged(kubeclient2, un2, 5, annotationAndLabelReader)
		assert.True(t, isOwned)
		assert.NoError(t, err)

		// Should not be tracked, only has label.
		pod3 := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "pod3",
				Namespace: "default",
				Labels: map[string]string{
					"app.kubernetes.io/instance": "some-app",
				},
			},
		}
		kubeclient3 := kube.NewDynamicFakeClient(pod3)
		un3 := objToUnstructured(pod3)
		isOwned, err = isResourceManaged(kubeclient3, un3, 5, annotationAndLabelReader)
		assert.False(t, isOwned)
		assert.NoError(t, err)
	})

	t.Run("OLM ClusterServiceVersion resolves via olm.operatorGroup annotation", func(t *testing.T) {
		og := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1",
			"kind":       "OperatorGroup",
			"metadata": map[string]any{
				"name":      "my-operator",
				"namespace": "operators",
				"labels": map[string]any{
					"app.kubernetes.io/instance": "some-app",
				},
			},
		}}
		csv := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "ClusterServiceVersion",
			"metadata": map[string]any{
				"name":      "my-operator.v1.0.0",
				"namespace": "operators",
				"annotations": map[string]any{
					"olm.operatorGroup": "my-operator",
				},
			},
		}}
		kubeclient := newOLMFakeClient(og, csv)

		isOwned, err := isResourceManaged(kubeclient, csv, 5, labelTrackingReader)
		assert.True(t, isOwned)
		assert.NoError(t, err)
	})

	t.Run("OLM descendant chain resolves through ClusterServiceVersion", func(t *testing.T) {
		og := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1",
			"kind":       "OperatorGroup",
			"metadata": map[string]any{
				"name":      "my-operator",
				"namespace": "operators",
				"labels": map[string]any{
					"app.kubernetes.io/instance": "some-app",
				},
			},
		}}
		csv := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "ClusterServiceVersion",
			"metadata": map[string]any{
				"name":      "my-operator.v1.0.0",
				"namespace": "operators",
				"annotations": map[string]any{
					"olm.operatorGroup": "my-operator",
				},
			},
		}}
		depl := &appsv1.Deployment{
			ObjectMeta: v1.ObjectMeta{
				Name:      "my-operator-controller",
				Namespace: "operators",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "operators.coreos.com/v1alpha1",
						Kind:       "ClusterServiceVersion",
						Name:       "my-operator.v1.0.0",
					},
				},
			},
		}
		pod := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      "my-operator-pod",
				Namespace: "operators",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "my-operator-controller",
					},
				},
			},
		}
		kubeclient := newOLMFakeClient(og, csv, depl, pod)

		isOwned, err := isResourceManaged(kubeclient, objToUnstructured(pod), 5, labelTrackingReader)
		assert.True(t, isOwned)
		assert.NoError(t, err)
	})

	t.Run("OLM ClusterServiceVersion without annotation is not managed", func(t *testing.T) {
		csv := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "ClusterServiceVersion",
			"metadata": map[string]any{
				"name":      "my-operator.v1.0.0",
				"namespace": "operators",
			},
		}}
		kubeclient := newOLMFakeClient(csv)

		isOwned, err := isResourceManaged(kubeclient, csv, 5, labelTrackingReader)
		assert.False(t, isOwned)
		assert.NoError(t, err)
	})

	t.Run("OLM ClusterServiceVersion with missing OperatorGroup returns error", func(t *testing.T) {
		csv := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "ClusterServiceVersion",
			"metadata": map[string]any{
				"name":      "my-operator.v1.0.0",
				"namespace": "operators",
				"annotations": map[string]any{
					"olm.operatorGroup": "missing-operator",
				},
			},
		}}
		kubeclient := newOLMFakeClient(csv)

		isOwned, err := isResourceManaged(kubeclient, csv, 5, labelTrackingReader)
		assert.False(t, isOwned)
		assert.True(t, errors.IsNotFound(err))
	})
}

func objToUnstructured(obj any) *unstructured.Unstructured {
	unObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: unObj}
}

func newOLMFakeClient(objects ...runtime.Object) *agentkube.KubernetesClient {
	c := kube.NewDynamicFakeClient(objects...)
	fakeDiscovery := c.Clientset.Discovery().(*fakediscovery.FakeDiscovery)
	olmResourceList := &v1.APIResourceList{
		GroupVersion: "operators.coreos.com/v1",
		APIResources: []v1.APIResource{
			{Group: "operators.coreos.com", Version: "v1", Name: "operatorgroups", SingularName: "operatorgroup", Namespaced: true, Kind: "OperatorGroup", Verbs: []string{"get", "list", "watch"}},
		},
	}
	olmAlphaResourceList := &v1.APIResourceList{
		GroupVersion: "operators.coreos.com/v1alpha1",
		APIResources: []v1.APIResource{
			{Group: "operators.coreos.com", Version: "v1alpha1", Name: "clusterserviceversions", SingularName: "clusterserviceversion", Namespaced: true, Kind: "ClusterServiceVersion", Verbs: []string{"get", "list", "watch"}},
		},
	}
	fakeDiscovery.Resources = append(fakeDiscovery.Resources, olmResourceList, olmAlphaResourceList)
	return c
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
			trackingReader:      newTestTrackingReader(v1alpha1.TrackingMethodLabel),
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
		err := agent.processIncomingResourceRequest(event.New(&ev, targets.Resource))
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
			trackingReader:      newTestTrackingReader(""),
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
		err := agent.processIncomingResourceRequest(event.New(&ev, targets.Resource))
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
			trackingReader:      newTestTrackingReader(""),
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
		err := agent.processIncomingResourceRequest(event.New(&ev, targets.Resource))
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
		kubeClient := kube.NewDynamicFakeClient()
		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kubeClient,
			queues:              queue.NewSendRecvQueues(),
			emitter:             event.NewEventSource("test-agent"),
			enableResourceProxy: true,
			trackingReader:      newTestTrackingReader(""),
		}
		require.NoError(t, agent.queues.Create(defaultQueueName))

		// Create invalid event (missing required data)
		ev := cloudevents.NewEvent()
		ev.SetType(event.GetRequest.String())

		// Process the request
		err := agent.processIncomingResourceRequest(event.New(&ev, targets.Resource))
		assert.NoError(t, err)
	})
}

func Test_getAvailableResources(t *testing.T) {
	t.Run("Error when resource is specified", func(t *testing.T) {
		kubeClient := kube.NewDynamicFakeClient()
		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kubeClient,
			enableResourceProxy: true,
			trackingReader:      newTestTrackingReader(""),
		}

		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "pods", // Resource should be empty for API discovery
		}

		_, err := agent.getAvailableResources(context.Background(), agent.kubeClient, gvr, "", nil)
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

		kubeClient := kube.NewDynamicFakeClient(pod1, pod2)
		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kubeClient,
			enableResourceProxy: true,
			trackingReader:      newTestTrackingReader(""),
		}

		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "apiresources",
		}

		list, err := agent.getAvailableResources(context.Background(), agent.kubeClient, gvr, "default", nil)
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

		kubeClient := kube.NewDynamicFakeClient(node1, node2)
		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kubeClient,
			enableResourceProxy: true,
			trackingReader:      newTestTrackingReader(""),
		}

		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "apigroups",
		}

		list, err := agent.getAvailableResources(context.Background(), agent.kubeClient, gvr, "", nil)
		assert.NoError(t, err)
		assert.NotNil(t, list)
	})

	t.Run("Passes field and label selectors to list call", func(t *testing.T) {
		kubeClient := kube.NewDynamicFakeClient()
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		fakeDyn := fake.NewSimpleDynamicClient(scheme)

		var capturedListRestrictions k8stesting.ListRestrictions
		fakeDyn.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
			listAction := action.(k8stesting.ListAction)
			capturedListRestrictions = listAction.GetListRestrictions()
			return true, &unstructured.UnstructuredList{}, nil
		})
		kubeClient.DynamicClient = fakeDyn

		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kubeClient,
			enableResourceProxy: true,
			trackingReader:      newTestTrackingReader(""),
		}

		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "events",
		}

		params := map[string]string{
			"fieldSelector": "involvedObject.name=my-pod,involvedObject.namespace=default",
			"labelSelector": "app=test",
		}

		_, err := agent.getAvailableResources(context.Background(), agent.kubeClient, gvr, "default", params)
		assert.NoError(t, err)
		assert.Equal(t, "involvedObject.name=my-pod,involvedObject.namespace=default", capturedListRestrictions.Fields.String())
		assert.Equal(t, "app=test", capturedListRestrictions.Labels.String())
	})

	t.Run("Passes limit and continue to list call", func(t *testing.T) {
		kubeClient := kube.NewDynamicFakeClient()
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		fakeDyn := fake.NewSimpleDynamicClient(scheme)

		var capturedAction k8stesting.ListAction
		fakeDyn.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
			capturedAction = action.(k8stesting.ListAction)
			return true, &unstructured.UnstructuredList{}, nil
		})
		kubeClient.DynamicClient = fakeDyn

		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kubeClient,
			enableResourceProxy: true,
			trackingReader:      newTestTrackingReader(""),
		}

		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "events",
		}

		params := map[string]string{
			"limit":    "100",
			"continue": "abc123",
		}

		_, err := agent.getAvailableResources(context.Background(), agent.kubeClient, gvr, "default", params)
		assert.NoError(t, err)
		assert.NotNil(t, capturedAction)
	})

	t.Run("Nil params does not panic", func(t *testing.T) {
		kubeClient := kube.NewDynamicFakeClient()
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		fakeDyn := fake.NewSimpleDynamicClient(scheme)
		kubeClient.DynamicClient = fakeDyn

		agent := &Agent{
			context:             context.Background(),
			kubeClient:          kubeClient,
			enableResourceProxy: true,
			trackingReader:      newTestTrackingReader(""),
		}

		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "events",
		}

		_, err := agent.getAvailableResources(context.Background(), agent.kubeClient, gvr, "default", nil)
		assert.NoError(t, err)
	})
}

func Test_isKnownListOption(t *testing.T) {
	knownOptions := []string{
		"fieldSelector",
		"labelSelector",
		"resourceVersion",
		"continue",
		"limit",
	}
	for _, opt := range knownOptions {
		t.Run("Known option: "+opt, func(t *testing.T) {
			assert.True(t, isKnownListOption(opt))
		})
	}

	unknownOptions := []string{
		"watch",
		"timeout",
		"allowWatchBookmarks",
		"dryRun",
		"fieldManager",
		"force",
		"",
		"FieldSelector",
		"FIELDSELECTOR",
		"field_selector",
		"unknown",
	}
	for _, opt := range unknownOptions {
		t.Run("Unknown option: "+opt, func(t *testing.T) {
			assert.False(t, isKnownListOption(opt))
		})
	}
}

func Test_listOptionsFromParams(t *testing.T) {
	t.Run("Nil params returns empty ListOptions", func(t *testing.T) {
		opts := listOptionsFromParams(nil)
		assert.Equal(t, v1.ListOptions{}, opts)
	})

	t.Run("Empty params returns empty ListOptions", func(t *testing.T) {
		opts := listOptionsFromParams(map[string]string{})
		assert.Equal(t, v1.ListOptions{}, opts)
	})

	t.Run("All supported params are mapped", func(t *testing.T) {
		params := map[string]string{
			"fieldSelector":   "metadata.name=foo",
			"labelSelector":   "app=bar",
			"resourceVersion": "12345",
			"continue":        "token-abc",
			"limit":           "500",
		}
		opts := listOptionsFromParams(params)
		assert.Equal(t, "metadata.name=foo", opts.FieldSelector)
		assert.Equal(t, "app=bar", opts.LabelSelector)
		assert.Equal(t, "12345", opts.ResourceVersion)
		assert.Equal(t, "token-abc", opts.Continue)
		assert.Equal(t, int64(500), opts.Limit)
	})

	t.Run("Invalid limit is ignored", func(t *testing.T) {
		params := map[string]string{
			"limit": "not-a-number",
		}
		opts := listOptionsFromParams(params)
		assert.Equal(t, int64(0), opts.Limit)
	})

	t.Run("Unknown params are ignored", func(t *testing.T) {
		params := map[string]string{
			"unknown":       "value",
			"fieldSelector": "status.phase=Running",
		}
		opts := listOptionsFromParams(params)
		assert.Equal(t, "status.phase=Running", opts.FieldSelector)
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

			res, err := agent.processIncomingPostResourceRequest(context.Background(), agent.kubeClient, req, gvr)
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
		managedByApp  bool
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
			managedByApp:  true,
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
			managedByApp:  true,
		},
		{
			name:          "Invalid force param returns error",
			params:        map[string]string{"force": "notabool"},
			namespace:     "default",
			patchBody:     patch,
			setupReactor:  func(fakeDyn *fake.FakeDynamicClient) {},
			expectErr:     true,
			expectPatched: false,
			managedByApp:  true,
		},
		{
			name:          "Passes PatchOptions params",
			params:        map[string]string{"dryRun": "All", "fieldValidation": "Strict", "fieldManager": "test-manager", "force": "true"},
			namespace:     "default",
			patchBody:     patch,
			setupReactor:  func(fakeDyn *fake.FakeDynamicClient) {},
			expectErr:     false,
			expectPatched: true,
			managedByApp:  true,
		},
		{
			name:          "Returns error on unmanaged resource",
			params:        map[string]string{},
			namespace:     "default",
			patchBody:     patch,
			setupReactor:  func(fakeDyn *fake.FakeDynamicClient) {},
			expectErr:     true,
			expectPatched: false,
			managedByApp:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			obj := originalObj.DeepCopy()
			if tc.managedByApp {
				obj.ObjectMeta.Labels = map[string]string{
					"app.kubernetes.io/instance": "test-app",
				}
			}

			kubeClient := kube.NewDynamicFakeClient(obj)
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			fakeDyn := fake.NewSimpleDynamicClient(scheme, obj)
			tc.setupReactor(fakeDyn)
			kubeClient.DynamicClient = fakeDyn

			agent := &Agent{
				context:        context.Background(),
				kubeClient:     kubeClient,
				trackingReader: newTestTrackingReader(v1alpha1.TrackingMethodLabel),
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

			res, err := agent.processIncomingPatchResourceRequest(context.Background(), agent.kubeClient, req, gvr)
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

		result, err := agent.getAvailableAPIs(context.Background(), agent.kubeClient, "apps", "v1")
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
		resourcesList, ok := resources.([]any)
		assert.True(t, ok)
		assert.Greater(t, len(resourcesList), 0)

		// Check that the first resource has the expected fields
		firstResource, ok := resourcesList[0].(map[string]any)
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

		result, err := agent.getAvailableAPIs(context.Background(), agent.kubeClient, "", "v1")
		assert.NoError(t, err)
		assert.NotNil(t, result)

		// Check that groupVersion is set correctly for core API
		groupVersion, exists := result.Object["groupVersion"]
		assert.True(t, exists)
		assert.Equal(t, "v1", groupVersion)

		// Check that resources array exists and has content
		resources, exists := result.Object["resources"]
		assert.True(t, exists)
		resourcesList, ok := resources.([]any)
		assert.True(t, ok)
		assert.Greater(t, len(resourcesList), 0)

		// Check that the first resource has the expected fields
		firstResource, ok := resourcesList[0].(map[string]any)
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

		result, err := agent.getAvailableAPIs(context.Background(), agent.kubeClient, "", "")
		assert.NoError(t, err)
		assert.NotNil(t, result)

		// Check that groups array exists and has content
		groups, exists := result.Object["groups"]
		assert.True(t, exists)
		groupsList, ok := groups.([]any)
		assert.True(t, ok)
		assert.Greater(t, len(groupsList), 0)

		// Check that the first group has the expected fields
		firstGroup, ok := groupsList[0].(map[string]any)
		assert.True(t, ok)
		assert.Contains(t, firstGroup, "name")
		assert.Contains(t, firstGroup, "versions")
		assert.Contains(t, firstGroup, "preferredVersion")

		// Check that versions array exists and has content
		versions, exists := firstGroup["versions"]
		assert.True(t, exists)
		versionsList, ok := versions.([]any)
		assert.True(t, ok)
		assert.Greater(t, len(versionsList), 0)

		// Check that the first version has the expected fields
		firstVersion, ok := versionsList[0].(map[string]any)
		assert.True(t, ok)
		assert.Contains(t, firstVersion, "groupVersion")
		assert.Contains(t, firstVersion, "version")
	})

	t.Run("Returns error for non-existent API group", func(t *testing.T) {
		agent := &Agent{
			context:    context.Background(),
			kubeClient: kube.NewDynamicFakeClient(),
		}

		result, err := agent.getAvailableAPIs(context.Background(), agent.kubeClient, "nonexistent", "v1")
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
				context:        context.Background(),
				kubeClient:     kubeClient,
				trackingReader: newTestTrackingReader(v1alpha1.TrackingMethodLabel),
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

			err := agent.processIncomingDeleteResourceRequest(context.Background(), agent.kubeClient, req, gvr)

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
			path, err := agent.buildSubresourcePath(tc.gvr, tc.resName, tc.namespace, tc.subresource)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, path)
		})
	}
}

func TestBuildSubresourcePathValidation(t *testing.T) {
	agent := &Agent{}
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

	testCases := []struct {
		name        string
		resName     string
		namespace   string
		subresource string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Path traversal in subresource",
			resName:     "valid-name",
			namespace:   "default",
			subresource: "../../etc/passwd",
			expectError: true,
			errorMsg:    "subresource contains invalid path traversal sequence",
		},
		{
			name:        "Path separator in resource name",
			resName:     "invalid/name",
			namespace:   "default",
			subresource: "status",
			expectError: true,
			errorMsg:    "resource name contains invalid path separator",
		},
		{
			name:        "Path traversal in namespace",
			resName:     "valid-name",
			namespace:   "../system",
			subresource: "status",
			expectError: true,
			errorMsg:    "namespace contains invalid path traversal sequence",
		},
		{
			name:        "Empty resource name",
			resName:     "",
			namespace:   "default",
			subresource: "status",
			expectError: true,
			errorMsg:    "resource name cannot be empty",
		},
		{
			name:        "Empty subresource",
			resName:     "valid-name",
			namespace:   "default",
			subresource: "",
			expectError: true,
			errorMsg:    "subresource cannot be empty",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path, err := agent.buildSubresourcePath(gvr, tc.resName, tc.namespace, tc.subresource)
			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.errorMsg)
				assert.Empty(t, path)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, path)
			}
		})
	}
}

// apiRequest is a request that reached the fake Kubernetes API server.
type apiRequest struct {
	method      string
	path        string
	impersonate string
}

// fakeKubeAPI is a Kubernetes API server that records who each request was
// made as. It serves a pod that carries no tracking label but is owned by a
// ReplicaSet that does, so deciding whether the pod is managed takes the agent
// a discovery request and an owner lookup in addition to the pod request.
type fakeKubeAPI struct {
	mu       sync.Mutex
	requests []apiRequest
	denyGet  string
	server   *httptest.Server
}

func newFakeKubeAPI(t *testing.T) *fakeKubeAPI {
	t.Helper()
	api := &fakeKubeAPI{}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		api.requests = append(api.requests, apiRequest{r.Method, r.URL.Path, r.Header.Get("Impersonate-User")})
		denied := api.denyGet != "" && r.Method == http.MethodGet && r.Header.Get("Impersonate-User") == api.denyGet
		api.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case denied:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Forbidden","code":403,"message":"forbidden"}`))
		case r.URL.Path == "/api":
			_, _ = w.Write([]byte(`{"kind":"APIVersions","versions":["v1"]}`))
		case r.URL.Path == "/apis":
			_, _ = w.Write([]byte(`{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`))
		case r.URL.Path == "/api/v1":
			_, _ = w.Write([]byte(`{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[]}`))
		case r.URL.Path == "/apis/batch/v1":
			_, _ = w.Write([]byte(`{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"batch/v1","resources":[]}`))
		case r.URL.Path == "/apis/apps/v1":
			_, _ = w.Write([]byte(`{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"apps/v1","resources":[{"name":"replicasets","kind":"ReplicaSet","namespaced":true,"verbs":["get"]}]}`))
		case strings.Contains(r.URL.Path, "/replicasets/"):
			_, _ = w.Write([]byte(`{"apiVersion":"apps/v1","kind":"ReplicaSet","metadata":{"name":"rs-1","namespace":"default","labels":{"app.kubernetes.io/instance":"test-app"}}}`))
		case strings.HasSuffix(r.URL.Path, "/events"):
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"EventList","metadata":{},"items":[]}`))
		case r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Success"}`))
		default:
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"test-pod","namespace":"default","ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"rs-1","uid":"u1"}]}}`))
		}
	}))
	t.Cleanup(api.server.Close)
	return api
}

// denyReadsAs makes the API refuse every GET made on behalf of user.
func (api *fakeKubeAPI) denyReadsAs(user string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.denyGet = user
}

func (api *fakeKubeAPI) seen() []apiRequest {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]apiRequest(nil), api.requests...)
}

func (api *fakeKubeAPI) kubeClient(t *testing.T) *agentkube.KubernetesClient {
	t.Helper()
	cfg := &rest.Config{Host: api.server.URL}
	clientset, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)
	dyn, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err)
	kc := agentkube.NewKubernetesClient(context.Background(), clientset, nil, "argocd")
	kc.DynamicClient = dyn
	kc.RestConfig = cfg
	return kc
}

func Test_processIncomingResourceRequest_impersonation(t *testing.T) {
	const user = "system:serviceaccount:default:deployer"
	pods := v1.GroupVersionResource{Version: "v1", Resource: "pods"}
	events := v1.GroupVersionResource{Version: "v1", Resource: "events"}

	operations := []struct {
		name string
		req  event.ResourceRequest
		// managedCheck is true if the agent decides whether the target is
		// managed by Argo CD before acting on it.
		managedCheck bool
	}{
		{"get resource", event.ResourceRequest{Method: http.MethodGet, Name: "test-pod", Namespace: "default", GroupVersionResource: pods}, true},
		{"get subresource", event.ResourceRequest{Method: http.MethodGet, Name: "test-pod", Namespace: "default", Subresource: "status", GroupVersionResource: pods}, true},
		{"list events", event.ResourceRequest{Method: http.MethodGet, Namespace: "default", GroupVersionResource: events}, false},
		{"list API groups", event.ResourceRequest{Method: http.MethodGet}, false},
		{"list APIs of a version", event.ResourceRequest{Method: http.MethodGet, GroupVersionResource: v1.GroupVersionResource{Version: "v1"}}, false},
		{"list APIs of a group version", event.ResourceRequest{Method: http.MethodGet, GroupVersionResource: v1.GroupVersionResource{Group: "batch", Version: "v1"}}, false},
		{"create resource", event.ResourceRequest{Method: http.MethodPost, Namespace: "default", Body: []byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"new-pod"}}`), GroupVersionResource: pods}, false},
		{"post subresource", event.ResourceRequest{Method: http.MethodPost, Name: "test-pod", Namespace: "default", Subresource: "eviction", Body: []byte(`{}`), GroupVersionResource: pods}, true},
		{"patch resource", event.ResourceRequest{Method: http.MethodPatch, Name: "test-pod", Namespace: "default", Body: []byte(`{"metadata":{"labels":{"a":"b"}}}`), GroupVersionResource: pods}, true},
		{"patch subresource", event.ResourceRequest{Method: http.MethodPatch, Name: "test-pod", Namespace: "default", Subresource: "status", Body: []byte(`{}`), GroupVersionResource: pods}, true},
		{"delete resource", event.ResourceRequest{Method: http.MethodDelete, Name: "test-pod", Namespace: "default", Body: []byte(`{}`), GroupVersionResource: pods}, true},
	}

	// process runs the resource request through the agent and returns the HTTP
	// status the agent reports back to the principal.
	process := func(t *testing.T, a *Agent, rreq event.ResourceRequest) int {
		t.Helper()
		rreq.UUID = "test-uuid"
		ev := cloudevents.NewEvent()
		ev.SetType(event.GetRequest.String())
		require.NoError(t, ev.SetData(cloudevents.ApplicationJSON, &rreq))
		require.NoError(t, a.processIncomingResourceRequest(event.New(&ev, targets.Resource)))

		sendq := a.queues.SendQ(defaultQueueName)
		require.Eventually(t, func() bool { return sendq.Len() > 0 }, 5*time.Second, 5*time.Millisecond, "agent emitted no response")
		responseEv, shutdown := sendq.Get()
		require.False(t, shutdown)
		resp := &event.ResourceResponse{}
		require.NoError(t, responseEv.DataAs(resp))
		require.Equal(t, "test-uuid", resp.UUID)
		return resp.Status
	}

	newTestAgent := func(t *testing.T, kc *agentkube.KubernetesClient, impersonation bool) *Agent {
		t.Helper()
		a := &Agent{
			context:                          context.Background(),
			kubeClient:                       kc,
			queues:                           queue.NewSendRecvQueues(),
			emitter:                          event.NewEventSource("test-agent"),
			enableResourceProxy:              true,
			enableResourceProxyImpersonation: impersonation,
			trackingReader:                   newTestTrackingReader(v1alpha1.TrackingMethodLabel),
		}
		require.NoError(t, a.queues.Create(defaultQueueName))
		return a
	}

	for _, op := range operations {
		t.Run(op.name+": the user's requests are made as the service account, the agent's own lookups are not", func(t *testing.T) {
			api := newFakeKubeAPI(t)
			a := newTestAgent(t, api.kubeClient(t), true)
			rreq := op.req
			rreq.Impersonate = user

			assert.Equal(t, http.StatusOK, process(t, a, rreq))

			var userRequests, agentRequests int
			for _, r := range api.seen() {
				if r.path == "/apis/apps/v1" || strings.Contains(r.path, "/replicasets/") {
					agentRequests++
					assert.Empty(t, r.impersonate, "%s %s is the agent's own ownership lookup", r.method, r.path)
					continue
				}
				userRequests++
				assert.Equal(t, user, r.impersonate, "%s %s was made on behalf of the user", r.method, r.path)
			}
			assert.Positive(t, userRequests, "no request on behalf of the user reached the API")
			if op.managedCheck {
				assert.Positive(t, agentRequests, "the managed check must run with the agent's identity")
			}
		})

		t.Run(op.name+": without a requested identity the agent uses its own", func(t *testing.T) {
			for _, impersonation := range []bool{true, false} {
				api := newFakeKubeAPI(t)
				a := newTestAgent(t, api.kubeClient(t), impersonation)

				assert.Equal(t, http.StatusOK, process(t, a, op.req))

				require.NotEmpty(t, api.seen())
				for _, r := range api.seen() {
					assert.Empty(t, r.impersonate, "%s %s with impersonation=%v", r.method, r.path, impersonation)
				}
			}
		})

		t.Run(op.name+": refused when impersonation is not enabled on the agent", func(t *testing.T) {
			api := newFakeKubeAPI(t)
			a := newTestAgent(t, api.kubeClient(t), false)
			rreq := op.req
			rreq.Impersonate = user

			assert.Equal(t, http.StatusForbidden, process(t, a, rreq))
			assert.Empty(t, api.seen(), "a refused request must not reach the API")
		})

		t.Run(op.name+": refused when the identity is not a service account", func(t *testing.T) {
			api := newFakeKubeAPI(t)
			a := newTestAgent(t, api.kubeClient(t), true)
			rreq := op.req
			rreq.Impersonate = "system:masters"

			assert.Equal(t, http.StatusForbidden, process(t, a, rreq))
			assert.Empty(t, api.seen(), "a refused request must not reach the API")
		})
	}

	for _, op := range operations {
		if !op.managedCheck {
			continue
		}
		t.Run(op.name+": a read denied to the service account stops the request", func(t *testing.T) {
			api := newFakeKubeAPI(t)
			api.denyReadsAs(user)
			a := newTestAgent(t, api.kubeClient(t), true)
			rreq := op.req
			rreq.Impersonate = user

			assert.Equal(t, http.StatusForbidden, process(t, a, rreq))
			for _, r := range api.seen() {
				assert.Equal(t, http.MethodGet, r.method, "%s %s must not be sent after the read was denied", r.method, r.path)
			}
		})
	}

	t.Run("fails closed when the agent has no REST config to impersonate with", func(t *testing.T) {
		a := newTestAgent(t, kube.NewDynamicFakeClient(), true)
		rreq := operations[0].req
		rreq.Impersonate = user

		kc, err := a.kubeClientFor(&rreq)
		assert.Nil(t, kc)
		require.Error(t, err)
		assert.True(t, errors.IsInternalError(err), "got %v", err)
		assert.ErrorContains(t, err, "REST configuration")
		assert.Equal(t, http.StatusInternalServerError, process(t, a, rreq))
	})
}
