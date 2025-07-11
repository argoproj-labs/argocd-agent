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

package resync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cloudevent "github.com/cloudevents/sdk-go/v2/event"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/workqueue"
)

// RequestHandler handles all the resync requests that are exchanged when the agent/principal process restarts.
// Depending on the agent mode, the sync messages are common to both the agent and the principal.
type RequestHandler struct {
	dynClient dynamic.Interface

	sendQ workqueue.TypedRateLimitingInterface[*cloudevent.Event]

	events *event.EventSource

	resources *resources.Resources

	log *logrus.Entry
}

func NewRequestHandler(dynClient dynamic.Interface, queue workqueue.TypedRateLimitingInterface[*cloudevent.Event], events *event.EventSource, resources *resources.Resources, log *logrus.Entry) *RequestHandler {
	return &RequestHandler{
		dynClient: dynClient,
		sendQ:     queue,
		events:    events,
		log:       log,
		resources: resources,
	}
}

func (r *RequestHandler) ProcessSyncedResourceListRequest(agentName string, req *event.RequestSyncedResourceList) error {
	r.log.Trace("Received a request for synced resource list event")

	if r.resources == nil || r.resources.Len() == 0 {
		r.log.Trace("No resources found for this agent. Skip sending synced resources")
		return nil
	}

	checksum := r.resources.Checksum()
	if bytes.Equal(req.Checksum, checksum) {
		r.log.Info("Agent and Principal checksums match. Skip sending synced resources")
		return nil
	}

	// At this stage we know that the agent and the principal are out of sync.
	// We need to send synced resources to the agent.

	r.log.Info("Agent and Principal checksums don't match, sending synced resource event for each resource")

	resources := r.resources.GetAll()
	for _, res := range resources {
		ev, err := r.events.SyncedResourceEvent(res)
		if err != nil {
			return fmt.Errorf("failed to create synced resource event: %w", err)
		}

		r.log.WithField("name", res.Name).WithField("kind", res.Kind).Trace("Sent synced resource event")
		r.sendQ.Add(ev)
	}

	return nil
}

func (r *RequestHandler) ProcessIncomingSyncedResource(ctx context.Context, incoming *event.SyncedResource, agentID string) error {
	logCtx := r.log.WithFields(logrus.Fields{
		"kind":      incoming.Kind,
		"namespace": incoming.Namespace,
		"name":      incoming.Name,
		"uid":       incoming.UID,
	})

	logCtx.Trace("Received a synced resource event")

	var reqUpdate *event.RequestUpdate

	gvr, err := getGroupVersionResource(incoming.Kind)
	if err != nil {
		return err
	}

	// Check if the given resource exists locally
	resClient := r.dynClient.Resource(gvr)
	res, err := resClient.Namespace(incoming.Namespace).Get(ctx, incoming.Name, v1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		// The incoming app is not found locally
		reqUpdate = event.NewRequestUpdate(incoming.Name, incoming.Namespace, incoming.Kind, "", nil)
	} else {
		reqUpdate, err = newRequestUpdateFromObject(res)
		if err != nil {
			return fmt.Errorf("failed to construct a request update for resource: %s", res.GetName())
		}
	}

	reqUpdateEvent, err := r.events.RequestUpdateEvent(reqUpdate)
	if err != nil {
		return err
	}

	r.sendQ.Add(reqUpdateEvent)
	logCtx.Trace("Sent a request update event after processing the synced resource request")

	return nil
}

func (r *RequestHandler) ProcessIncomingResourceResyncRequest(ctx context.Context, queueID string) error {
	r.log.Trace("Received a request for resource resync")

	resources := r.resources.GetAll()
	for _, resource := range resources {
		if err := r.sendRequestUpdate(ctx, resource); err != nil {
			r.log.Errorf("failed to send request update for resource %s: %v", resource.Name, err)
			continue
		}
	}

	return nil
}

func (r *RequestHandler) SendRequestUpdates(ctx context.Context) {
	resources := r.resources.GetAll()
	for _, resource := range resources {
		if err := r.sendRequestUpdate(ctx, resource); err != nil {
			r.log.Errorf("failed to send request update for resource %s: %v", resource.Name, err)
			continue
		}
	}
}

func (r *RequestHandler) sendRequestUpdate(ctx context.Context, resource resources.ResourceKey) error {
	gvr, err := getGroupVersionResource(resource.Kind)
	if err != nil {
		return err
	}

	resClient := r.dynClient.Resource(gvr)
	res, err := resClient.Namespace(resource.Namespace).Get(ctx, resource.Name, v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get resource: %v", err)
	}

	reqUpdate, err := newRequestUpdateFromObject(res)
	if err != nil {
		return fmt.Errorf("failed to construct a request update from resource %s: %w", resource.Name, err)
	}

	ev, err := r.events.RequestUpdateEvent(reqUpdate)
	if err != nil {
		return fmt.Errorf("failed to create request update event: %w", err)
	}

	r.sendQ.Add(ev)
	r.log.WithField("kind", resource.Kind).WithField("name", resource.Name).Trace("Sent a request update event")
	return nil
}

func (r *RequestHandler) ProcessRequestUpdateEvent(ctx context.Context, agentName string, reqUpdate *event.RequestUpdate) error {
	logCtx := r.log.WithFields(logrus.Fields{
		"name":      reqUpdate.Name,
		"kind":      reqUpdate.Kind,
		"namespace": reqUpdate.Namespace,
	})

	logCtx.Trace("Received a request for the resource update event")

	gvr, err := getGroupVersionResource(reqUpdate.Kind)
	if err != nil {
		return err
	}

	// Check if the given resource exists locally
	resClient := r.dynClient.Resource(gvr)
	res, err := resClient.Namespace(reqUpdate.Namespace).Get(ctx, reqUpdate.Name, v1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		// The resource doesn't exist on the source. So, send a delete event to remove the orphaned resource from the peer.
		return r.handleDeletedResource(logCtx, reqUpdate)
	}

	// The resource exists on the source. Compare the checksum and check if we need to send a SpecUpdate event.
	checksum, err := generateSpecChecksum(res)
	if err != nil {
		return fmt.Errorf("failed to generate checksum for resource %s/%s: %w", res.GetKind(), res.GetName(), err)
	}

	// Do nothing if the incoming spec and the existing spec matches
	if bytes.Equal(checksum[:], reqUpdate.Checksum) {
		logCtx.Trace("Checksum of the incoming request update matches with the local resource")
		return nil
	}

	logCtx.Trace("Checksums do not match. Sending a specUpdate event")

	return r.handleUpdatedResource(logCtx, reqUpdate, res)
}

func (r *RequestHandler) handleUpdatedResource(logCtx *logrus.Entry, reqUpdate *event.RequestUpdate, res *unstructured.Unstructured) error {
	resBytes, err := res.MarshalJSON()
	if err != nil {
		return err
	}

	switch reqUpdate.Kind {
	case "Application":
		app := &v1alpha1.Application{}
		err := json.Unmarshal(resBytes, app)
		if err != nil {
			return err
		}

		ev := r.events.ApplicationEvent(event.SpecUpdate, app)
		logCtx.Trace("Sending a request to update the application")
		r.sendQ.Add(ev)

	case "AppProject":
		appProject := &v1alpha1.AppProject{}
		err := json.Unmarshal(resBytes, appProject)
		if err != nil {
			return err
		}

		ev := r.events.AppProjectEvent(event.SpecUpdate, appProject)
		logCtx.Trace("Sending a request to update the appProject")
		r.sendQ.Add(ev)
	default:
		return fmt.Errorf("unknown resource Kind: %s", reqUpdate.Kind)
	}

	return nil
}

func (r *RequestHandler) handleDeletedResource(logCtx *logrus.Entry, reqUpdate *event.RequestUpdate) error {
	switch reqUpdate.Kind {
	case "Application":
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      reqUpdate.Name,
				Namespace: reqUpdate.Namespace,
			},
		}

		ev := r.events.ApplicationEvent(event.Delete, app)
		logCtx.Trace("Sending a request to delete the orphaned application")
		r.sendQ.Add(ev)
		return nil

	case "AppProject":
		appProject := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      reqUpdate.Name,
				Namespace: reqUpdate.Namespace,
			},
		}
		ev := r.events.AppProjectEvent(event.Delete, appProject)
		logCtx.Trace("Sending a request to delete the orphaned appProject")
		r.sendQ.Add(ev)
		return nil
	default:
		return fmt.Errorf("unknown resource Kind: %s", reqUpdate.Kind)
	}
}

func newRequestUpdateFromObject(res *unstructured.Unstructured) (*event.RequestUpdate, error) {
	// RequestUpdate is always sent by the peer. So, the object must have the source UID annotation
	annotations := res.GetAnnotations()
	sourceUID, ok := annotations[manager.SourceUIDAnnotation]
	if !ok {
		return nil, fmt.Errorf("source UID annotation not found for resource: %s", res.GetName())
	}

	checksum, err := generateSpecChecksum(res)
	if err != nil {
		return nil, fmt.Errorf("failed to generate checksum for resource %s/%s: %w", res.GetKind(), res.GetName(), err)
	}

	reqUpdate := event.NewRequestUpdate(res.GetName(), res.GetNamespace(), res.GetKind(), sourceUID, checksum[:])
	return reqUpdate, nil
}

func getGroupVersionResource(kind string) (schema.GroupVersionResource, error) {
	switch kind {
	case "Application":
		return schema.GroupVersionResource{
			Group:    "argoproj.io",
			Resource: "applications",
			Version:  "v1alpha1",
		}, nil
	case "AppProject":
		return schema.GroupVersionResource{
			Group:    "argoproj.io",
			Resource: "appprojects",
			Version:  "v1alpha1",
		}, nil
	default:
		return schema.GroupVersionResource{}, fmt.Errorf("unexpected Kind: %s", kind)
	}
}

func generateSpecChecksum(resObj *unstructured.Unstructured) ([]byte, error) {
	res := resObj.DeepCopy()

	resSpec, ok := res.Object["spec"]
	if !ok {
		return nil, fmt.Errorf("spec field not found for resource: %s", res.GetName())
	}

	specMap, ok := resSpec.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unable to convert the spec object of %s:%s to a map", res.GetKind(), res.GetName())
	}

	// The destination field will be modified to "in-cluster" by the agent.
	// Principal and the agent checksums will differ if we don't remove the destination field.
	delete(specMap, "destination")

	specBytes, err := json.Marshal(resSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal application spec: %v", err)
	}

	checksum := sha256.Sum256(specBytes)

	return checksum[:], nil
}
