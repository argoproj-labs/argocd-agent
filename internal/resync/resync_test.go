// Copyright 2025 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resync

import (
	"context"
	"fmt"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

const (
	testAgentName = "test"
)

func Test_ProcessIncomingSyncedResourceList(t *testing.T) {
	resNum := 10
	handler := createFakeHandler(t)

	t.Run("return nil if there are no resources", func(t *testing.T) {
		incoming := &event.RequestSyncedResourceList{
			Checksum: []byte("test"),
		}

		err := handler.ProcessSyncedResourceListRequest(testAgentName, incoming)
		assert.Nil(t, err)
	})

	t.Run("return nil if checksum matches", func(t *testing.T) {
		for i := 0; i < resNum; i++ {
			name := fmt.Sprintf("test-%d", i)
			testResource := resources.ResourceKey{
				Name:      name,
				Namespace: name,
				Kind:      "Application",
				UID:       name,
			}
			handler.resources.Add(testResource)
		}

		expChecksum := handler.resources.Checksum()

		incoming := &event.RequestSyncedResourceList{
			Checksum: expChecksum,
		}

		err := handler.ProcessSyncedResourceListRequest(testAgentName, incoming)
		assert.Nil(t, err)

		assert.Zero(t, handler.sendQ.Len())
	})

	t.Run("send SyncedResource event for all resources if the checksum doesn't match", func(t *testing.T) {
		testResources := make([]resources.ResourceKey, resNum)
		for i := 0; i < resNum; i++ {
			name := fmt.Sprintf("test-%d", i)
			testResource := resources.ResourceKey{
				Name:      name,
				Namespace: name,
				Kind:      "Application",
				UID:       name,
			}
			handler.resources.Add(testResource)
			testResources[i] = testResource
		}

		incoming := &event.RequestSyncedResourceList{
			Checksum: []byte("random"),
		}

		err := handler.ProcessSyncedResourceListRequest(testAgentName, incoming)
		assert.Nil(t, err)

		assert.Equal(t, handler.sendQ.Len(), resNum)

		for i := 0; i < resNum; i++ {
			ev, shutdown := handler.sendQ.Get()
			assert.False(t, shutdown)
			assert.Equal(t, event.ResponseSyncedResource.String(), ev.Type())
		}
	})
}

func Test_ProcessIncomingSyncedResource(t *testing.T) {
	handler := createFakeHandler(t)

	t.Run("create request update without checksum if resource not found", func(t *testing.T) {
		incoming := &event.SyncedResource{
			Name:      "test-app",
			Namespace: "default",
			Kind:      "Application",
			UID:       "test-uid",
		}

		err := handler.ProcessIncomingSyncedResource(context.Background(), incoming, testAgentName)
		assert.Nil(t, err)

		ev, shutdown := handler.sendQ.Get()
		assert.False(t, shutdown)
		assert.Equal(t, event.EventRequestUpdate.String(), ev.Type())

		got := &event.RequestUpdate{}
		err = ev.DataAs(got)
		assert.Nil(t, err)
		assert.Empty(t, got.Checksum)
	})

	t.Run("create request update with checksum if resource exists", func(t *testing.T) {
		resource := fakeUnresApp()
		resource.SetAnnotations(map[string]string{
			manager.SourceUIDAnnotation: "source-uid",
		})

		gvr, err := getGroupVersionResource("Application")
		assert.Nil(t, err)

		_, err = handler.dynClient.Resource(gvr).Namespace("default").
			Create(context.Background(), resource, v1.CreateOptions{})
		assert.Nil(t, err)

		incoming := &event.SyncedResource{
			Name:      "test-app",
			Namespace: "default",
			Kind:      "Application",
			UID:       "test-uid",
		}

		err = handler.ProcessIncomingSyncedResource(context.Background(), incoming, testAgentName)
		assert.Nil(t, err)

		ev, shutdown := handler.sendQ.Get()
		assert.False(t, shutdown)
		assert.Equal(t, event.EventRequestUpdate.String(), ev.Type())

		got := &event.RequestUpdate{}
		err = ev.DataAs(got)
		assert.Nil(t, err)
		checksum, err := generateSpecChecksum(resource)
		assert.Nil(t, err)
		assert.Equal(t, checksum, got.Checksum)
	})
}

func Test_ProcessIncomingSyncedResource_MissingSourceUID(t *testing.T) {
	gvr, err := getGroupVersionResource("Application")
	require.Nil(t, err)

	newHandlerWithFlag := func(t *testing.T, ignore bool) *RequestHandler {
		t.Helper()
		h := createFakeHandler(t)
		h.WithIgnoreUnmanagedApps(ignore)
		return h
	}

	setupApp := func(t *testing.T, h *RequestHandler, withAnnotation bool) {
		t.Helper()
		resource := fakeUnresApp()
		if withAnnotation {
			resource.SetAnnotations(map[string]string{manager.SourceUIDAnnotation: "source-uid"})
		}
		_, err := h.dynClient.Resource(gvr).Namespace("default").Create(context.Background(), resource, v1.CreateOptions{})
		require.Nil(t, err)
	}

	incoming := &event.SyncedResource{
		Name:      "test-app",
		Namespace: "default",
		Kind:      "Application",
		UID:       "test-uid",
	}

	t.Run("flag disabled: missing source-uid returns wrapped sentinel error", func(t *testing.T) {
		h := newHandlerWithFlag(t, false)
		setupApp(t, h, false)

		err := h.ProcessIncomingSyncedResource(context.Background(), incoming, testAgentName)
		assert.ErrorIs(t, err, ErrSourceUIDNotFound)
		assert.Equal(t, 0, h.sendQ.Len())
	})

	t.Run("flag enabled: missing source-uid suppresses error and sends nothing", func(t *testing.T) {
		h := newHandlerWithFlag(t, true)
		setupApp(t, h, false)

		err := h.ProcessIncomingSyncedResource(context.Background(), incoming, testAgentName)
		assert.Nil(t, err)
		assert.Equal(t, 0, h.sendQ.Len())
	})

	t.Run("flag enabled: present source-uid still sends request update", func(t *testing.T) {
		h := newHandlerWithFlag(t, true)
		setupApp(t, h, true)

		err := h.ProcessIncomingSyncedResource(context.Background(), incoming, testAgentName)
		assert.Nil(t, err)
		assert.Equal(t, 1, h.sendQ.Len())
	})
}

func Test_ProcessIncomingResourceResyncRequest(t *testing.T) {
	handler := createFakeHandler(t)

	t.Run("send request updates for all resources", func(t *testing.T) {
		resource := fakeUnresApp()
		resource.SetAnnotations(map[string]string{
			manager.SourceUIDAnnotation: "source-uid",
		})

		gvr, err := getGroupVersionResource("Application")
		assert.Nil(t, err)

		_, err = handler.dynClient.Resource(gvr).Namespace("default").
			Create(context.Background(), resource, v1.CreateOptions{})
		assert.Nil(t, err)

		handler.resources.Add(resources.ResourceKey{
			Name:      "test-app",
			Namespace: "default",
			Kind:      "Application",
			UID:       "test-uid",
		})

		err = handler.ProcessIncomingResourceResyncRequest(context.Background(), testAgentName)
		assert.Nil(t, err)

		assert.Equal(t, 1, handler.sendQ.Len())
		ev, shutdown := handler.sendQ.Get()
		assert.False(t, shutdown)
		assert.Equal(t, event.EventRequestUpdate.String(), ev.Type())
		got := &event.RequestUpdate{}
		err = ev.DataAs(got)
		assert.Nil(t, err)
		checksum, err := generateSpecChecksum(resource)
		assert.Nil(t, err)
		assert.Equal(t, checksum, got.Checksum)
	})
}

func Test_generateSpecChecksum(t *testing.T) {
	t.Run("generate checksum for valid resource", func(t *testing.T) {
		resource := fakeUnresApp()

		checksum, err := generateSpecChecksum(resource)
		assert.Nil(t, err)
		assert.NotNil(t, checksum)
	})

	t.Run("return error if spec field is missing", func(t *testing.T) {
		resource := fakeUnresApp()
		delete(resource.Object, "spec")

		_, err := generateSpecChecksum(resource)
		assert.NotNil(t, err)
		assert.Equal(t, "spec field not found for resource: test-app", err.Error())
	})

	t.Run("return error if spec field is not a map", func(t *testing.T) {
		resource := fakeUnresApp()
		resource.Object["spec"] = "invalid-spec"

		_, err := generateSpecChecksum(resource)
		assert.NotNil(t, err)
		assert.Equal(t, "unable to convert the spec object of Application:test-app to a map", err.Error())
	})

	t.Run("generate checksum for resource with destination field", func(t *testing.T) {
		resource := fakeUnresApp()
		resource.Object["spec"] = map[string]interface{}{
			"project":     "default",
			"destination": "in-cluster",
		}

		checksum, err := generateSpecChecksum(resource)
		assert.Nil(t, err)
		assert.NotNil(t, checksum)
	})
}

func Test_newRequestUpdateFromObject(t *testing.T) {
	t.Run("return ErrSourceUIDNotFound when annotation missing", func(t *testing.T) {
		resource := fakeUnresApp()

		_, err := newRequestUpdateFromObject(resource, "Application")
		assert.ErrorIs(t, err, ErrSourceUIDNotFound)
	})

	t.Run("return ErrSourceUIDNotFound when annotations nil", func(t *testing.T) {
		resource := fakeUnresApp()
		resource.SetAnnotations(nil)

		_, err := newRequestUpdateFromObject(resource, "Application")
		assert.ErrorIs(t, err, ErrSourceUIDNotFound)
	})

	t.Run("return request update when annotation present", func(t *testing.T) {
		resource := fakeUnresApp()
		resource.SetAnnotations(map[string]string{
			manager.SourceUIDAnnotation: "source-uid-123",
		})

		reqUpdate, err := newRequestUpdateFromObject(resource, "Application")
		assert.Nil(t, err)
		assert.NotNil(t, reqUpdate)
		assert.Equal(t, "test-app", reqUpdate.Name)
		assert.Equal(t, "default", reqUpdate.Namespace)
		assert.Equal(t, "Application", reqUpdate.Kind)
		assert.Equal(t, "source-uid-123", reqUpdate.UID)
		assert.NotEmpty(t, reqUpdate.Checksum)
	})
}

func Test_sendRequestUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("skip resource without source UID annotation when ignore flag enabled", func(t *testing.T) {
		handler := createFakeHandler(t).WithIgnoreUnmanagedApps(true)

		resource := fakeUnresApp()

		gvr, err := getGroupVersionResource("Application")
		assert.Nil(t, err)

		_, err = handler.dynClient.Resource(gvr).Namespace("default").
			Create(ctx, resource, v1.CreateOptions{})
		assert.Nil(t, err)

		err = handler.sendRequestUpdate(ctx, resources.ResourceKey{
			Name:      "test-app",
			Namespace: "default",
			Kind:      "Application",
			UID:       "test-uid",
		})

		assert.Nil(t, err)
		assert.Zero(t, handler.sendQ.Len())
	})

	t.Run("error on resource without source UID annotation when ignore flag disabled", func(t *testing.T) {
		handler := createFakeHandler(t)

		resource := fakeUnresApp()

		gvr, err := getGroupVersionResource("Application")
		assert.Nil(t, err)

		_, err = handler.dynClient.Resource(gvr).Namespace("default").
			Create(ctx, resource, v1.CreateOptions{})
		assert.Nil(t, err)

		err = handler.sendRequestUpdate(ctx, resources.ResourceKey{
			Name:      "test-app",
			Namespace: "default",
			Kind:      "Application",
			UID:       "test-uid",
		})

		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "source UID annotation not found")
	})

	t.Run("send request update for resource with source UID annotation", func(t *testing.T) {
		handler := createFakeHandler(t)

		resource := fakeUnresApp()
		resource.SetAnnotations(map[string]string{
			manager.SourceUIDAnnotation: "source-uid",
		})

		gvr, err := getGroupVersionResource("Application")
		assert.Nil(t, err)

		_, err = handler.dynClient.Resource(gvr).Namespace("default").
			Create(ctx, resource, v1.CreateOptions{})
		assert.Nil(t, err)

		err = handler.sendRequestUpdate(ctx, resources.ResourceKey{
			Name:      "test-app",
			Namespace: "default",
			Kind:      "Application",
			UID:       "test-uid",
		})

		assert.Nil(t, err)
		assert.Equal(t, 1, handler.sendQ.Len())

		ev, shutdown := handler.sendQ.Get()
		assert.False(t, shutdown)
		assert.Equal(t, event.EventRequestUpdate.String(), ev.Type())
	})

	t.Run("return error for non-existent resource", func(t *testing.T) {
		handler := createFakeHandler(t)

		err := handler.sendRequestUpdate(ctx, resources.ResourceKey{
			Name:      "non-existent",
			Namespace: "default",
			Kind:      "Application",
			UID:       "test-uid",
		})

		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "failed to get resource")
	})
}

func Test_SendRequestUpdates(t *testing.T) {
	ctx := context.Background()

	t.Run("skip resources without source UID when ignore flag enabled", func(t *testing.T) {
		handler := createFakeHandler(t).WithIgnoreUnmanagedApps(true)

		gvr, err := getGroupVersionResource("Application")
		assert.Nil(t, err)

		resourceWithAnnotation := fakeUnresApp()
		resourceWithAnnotation.SetName("app-with-annotation")
		resourceWithAnnotation.SetAnnotations(map[string]string{
			manager.SourceUIDAnnotation: "source-uid",
		})
		_, err = handler.dynClient.Resource(gvr).Namespace("default").
			Create(ctx, resourceWithAnnotation, v1.CreateOptions{})
		assert.Nil(t, err)

		resourceWithoutAnnotation := fakeUnresApp()
		resourceWithoutAnnotation.SetName("app-without-annotation")
		_, err = handler.dynClient.Resource(gvr).Namespace("default").
			Create(ctx, resourceWithoutAnnotation, v1.CreateOptions{})
		assert.Nil(t, err)

		handler.resources.Add(resources.ResourceKey{
			Name:      "app-with-annotation",
			Namespace: "default",
			Kind:      "Application",
			UID:       "uid-1",
		})
		handler.resources.Add(resources.ResourceKey{
			Name:      "app-without-annotation",
			Namespace: "default",
			Kind:      "Application",
			UID:       "uid-2",
		})

		handler.SendRequestUpdates(ctx)

		assert.Equal(t, 1, handler.sendQ.Len())

		ev, shutdown := handler.sendQ.Get()
		assert.False(t, shutdown)
		assert.Equal(t, event.EventRequestUpdate.String(), ev.Type())

		got := &event.RequestUpdate{}
		err = ev.DataAs(got)
		assert.Nil(t, err)
		assert.Equal(t, "app-with-annotation", got.Name)
	})

	t.Run("skip all unmanaged resources when ignore flag enabled", func(t *testing.T) {
		handler := createFakeHandler(t).WithIgnoreUnmanagedApps(true)

		gvr, err := getGroupVersionResource("Application")
		assert.Nil(t, err)

		for i := 0; i < 3; i++ {
			resource := fakeUnresApp()
			resource.SetName(fmt.Sprintf("unmanaged-app-%d", i))
			_, err = handler.dynClient.Resource(gvr).Namespace("default").
				Create(ctx, resource, v1.CreateOptions{})
			assert.Nil(t, err)

			handler.resources.Add(resources.ResourceKey{
				Name:      fmt.Sprintf("unmanaged-app-%d", i),
				Namespace: "default",
				Kind:      "Application",
				UID:       fmt.Sprintf("uid-%d", i),
			})
		}

		handler.SendRequestUpdates(ctx)

		assert.Zero(t, handler.sendQ.Len())
	})
}

func Test_ProcessRequestUpdateEvent(t *testing.T) {
	ctx := context.Background()
	handler := createFakeHandler(t)
	handler.namespace = "default"

	t.Run("return nil if resource exists and checksum matches", func(t *testing.T) {
		resource := fakeUnresApp()

		gvr, err := getGroupVersionResource("Application")
		assert.Nil(t, err)

		_, err = handler.dynClient.Resource(gvr).Namespace("default").Create(ctx, resource, v1.CreateOptions{})
		assert.Nil(t, err)

		checksum, err := generateSpecChecksum(resource)
		assert.Nil(t, err)

		reqUpdate := &event.RequestUpdate{
			Name:      "test-app",
			Namespace: "default",
			Kind:      "Application",
			Checksum:  checksum,
		}

		err = handler.ProcessRequestUpdateEvent(ctx, testAgentName, reqUpdate)
		assert.Nil(t, err)
		assert.Zero(t, handler.sendQ.Len())

		err = handler.dynClient.Resource(gvr).Namespace("default").Delete(ctx, resource.GetName(), v1.DeleteOptions{})
		assert.Nil(t, err)
	})

	t.Run("send delete event if resource does not exist", func(t *testing.T) {
		reqUpdate := &event.RequestUpdate{
			Name:      "non-existent-app",
			Namespace: "default",
			Kind:      "Application",
		}

		err := handler.ProcessRequestUpdateEvent(ctx, testAgentName, reqUpdate)
		assert.Nil(t, err)

		ev, shutdown := handler.sendQ.Get()
		assert.False(t, shutdown)
		assert.Equal(t, event.Delete.String(), ev.Type())
	})

	t.Run("send spec update event if checksum does not match", func(t *testing.T) {
		resource := fakeUnresApp()

		gvr, err := getGroupVersionResource("Application")
		assert.Nil(t, err)

		_, err = handler.dynClient.Resource(gvr).Namespace("default").Create(ctx, resource, v1.CreateOptions{})
		assert.Nil(t, err)

		reqUpdate := &event.RequestUpdate{
			Name:      "test-app",
			Namespace: "default",
			Kind:      "Application",
			Checksum:  []byte("invalid-checksum"),
		}

		err = handler.ProcessRequestUpdateEvent(ctx, testAgentName, reqUpdate)
		assert.Nil(t, err)

		ev, shutdown := handler.sendQ.Get()
		assert.False(t, shutdown)
		assert.Equal(t, event.SpecUpdate.String(), ev.Type())

		err = handler.dynClient.Resource(gvr).Namespace("default").Delete(ctx, resource.GetName(), v1.DeleteOptions{})
		assert.Nil(t, err)
	})
}

func createFakeHandler(t *testing.T) *RequestHandler {
	evs := event.NewEventSource("test")

	agentName := "test"
	queues := queue.NewSendRecvQueues()

	err := queues.Create(agentName)
	assert.Nil(t, err)

	dynClient := fake.NewSimpleDynamicClient(&runtime.Scheme{})
	res := resources.NewResources()
	return NewRequestHandler(dynClient, queues.SendQ(agentName), evs, res, logrus.NewEntry(logrus.New()), manager.ManagerRoleAgent, "argocd")
}

func fakeUnresApp() *unstructured.Unstructured {
	resource := &unstructured.Unstructured{}
	resource.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "argoproj.io",
		Version: "v1alpha1",
		Kind:    "Application",
	})
	resource.SetName("test-app")
	resource.SetNamespace("default")
	resource.SetUID("test-uid")
	resource.Object["spec"] = map[string]interface{}{
		"project": "default",
	}
	return resource
}
