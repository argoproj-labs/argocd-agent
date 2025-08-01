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

package event

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/grpcutil"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"github.com/cloudevents/sdk-go/binding/format/protobuf/v2/pb"
	cloudevents "github.com/cloudevents/sdk-go/v2"
)

const cloudEventSpecVersion = "1.0"

type EventType string
type EventTarget string

const TypePrefix = "io.argoproj.argocd-agent.event"

// Supported EventTypes that are sent agent <-> principal. Note that not every
// EventType is supported by every EventTarget.
const (
	Ping                       EventType = TypePrefix + ".ping"
	Pong                       EventType = TypePrefix + ".pong"
	Create                     EventType = TypePrefix + ".create"
	Delete                     EventType = TypePrefix + ".delete"
	Update                     EventType = TypePrefix + ".update"
	SpecUpdate                 EventType = TypePrefix + ".spec-update"
	StatusUpdate               EventType = TypePrefix + ".status-update"
	OperationUpdate            EventType = TypePrefix + ".operation-update"
	EventProcessed             EventType = TypePrefix + ".processed"
	GetRequest                 EventType = TypePrefix + ".get"
	GetResponse                EventType = TypePrefix + ".response"
	RedisGenericRequest        EventType = TypePrefix + ".redis-request"
	RedisGenericResponse       EventType = TypePrefix + ".redis-response"
	SyncedResourceList         EventType = TypePrefix + ".request-synced-resource-list"
	ResponseSyncedResource     EventType = TypePrefix + ".response-synced-resource"
	EventRequestUpdate         EventType = TypePrefix + ".request-update"
	EventRequestResourceResync EventType = TypePrefix + ".request-resource-resync"
)

const (
	TargetUnknown        EventTarget = "unknown"
	TargetApplication    EventTarget = "application"
	TargetAppProject     EventTarget = "appproject"
	TargetEventAck       EventTarget = "eventProcessed"
	TargetResource       EventTarget = "resource"
	TargetRedis          EventTarget = "redis"
	TargetResourceResync EventTarget = "resourceResync"
)

const (
	resourceID string = "resourceid"
	eventID    string = "eventid"
)

var (
	ErrEventDiscarded    error = errors.New("event discarded")
	ErrEventNotAllowed   error = errors.New("event not allowed in this agent mode")
	ErrEventNotSupported error = errors.New("event not supported by target")
	ErrEventUnknown      error = errors.New("unknown event")
)

func (t EventType) String() string {
	return string(t)
}

func (t EventTarget) String() string {
	return string(t)
}

// EventSource is a utility to construct new 'cloudevents.Event' events for a given 'source'
type EventSource struct {
	source string
}

// Event is the 'on the wire' representation of an event, and is parsed by from protobuf via FromWire
type Event struct {
	event  *cloudevents.Event
	target EventTarget
}

func New(ev *cloudevents.Event, target EventTarget) *Event {
	return &Event{
		event:  ev,
		target: target,
	}
}

func (ev *Event) SetEvent(e *cloudevents.Event) {
	ev.event = e
}

func NewEventSource(source string) *EventSource {
	ev := &EventSource{}
	ev.source = source
	return ev
}

func IsEventDiscarded(err error) bool {
	return errors.Is(err, ErrEventDiscarded)
}

func IsEventNotAllowed(err error) bool {
	return errors.Is(err, ErrEventNotAllowed)
}

func NewEventNotAllowedErr(format string, a ...any) error {
	return fmt.Errorf("%w: %w", ErrEventNotAllowed, fmt.Errorf(format, a...))
}

func NewEventDiscardedErr(format string, a ...any) error {
	return fmt.Errorf("%w: %w", ErrEventDiscarded, fmt.Errorf(format, a...))
}

func (evs EventSource) ApplicationEvent(evType EventType, app *v1alpha1.Application) *cloudevents.Event {
	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(evType.String())
	cev.SetExtension(eventID, createEventID(app.ObjectMeta))
	cev.SetExtension(resourceID, createResourceID(app.ObjectMeta))
	cev.SetDataSchema(TargetApplication.String())
	// TODO: Handle this error situation?
	_ = cev.SetData(cloudevents.ApplicationJSON, app)
	return &cev
}

func createResourceID(res v1.ObjectMeta) string {
	return fmt.Sprintf("%s_%s", res.Name, res.UID)
}

func createEventID(res v1.ObjectMeta) string {
	return fmt.Sprintf("%s_%s_%s", res.Name, res.UID, res.ResourceVersion)
}

func (evs EventSource) AppProjectEvent(evType EventType, appProject *v1alpha1.AppProject) *cloudevents.Event {
	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(evType.String())
	cev.SetExtension(eventID, createEventID(appProject.ObjectMeta))
	cev.SetExtension(resourceID, createResourceID(appProject.ObjectMeta))
	cev.SetDataSchema(TargetAppProject.String())
	// TODO: Handle this error situation?
	_ = cev.SetData(cloudevents.ApplicationJSON, appProject)
	return &cev
}

type RedisRequest struct {
	UUID           string           `json:"uuid"`
	ConnectionUUID string           `json:"connectionUuid"`
	Body           RedisCommandBody `json:"body"`
}

type RedisCommandBody struct {
	Get       *RedisCommandBodyGet       `json:"get,omitempty"`
	Subscribe *RedisCommandBodySubscribe `json:"subscribe,omitempty"`
	Ping      *RedisCommandBodyPing      `json:"ping,omitempty"`
}

type RedisCommandBodyGet struct {
	Key string `json:"key"`
}

type RedisCommandBodySubscribe struct {
	ChannelName string `json:"channel"`
}

type RedisCommandBodyPing struct {
}

type RedisResponse struct {
	UUID           string            `json:"uuid"`
	ConnectionUUID string            `json:"connectionUuid"`
	Body           RedisResponseBody `json:"body"`
}

type RedisResponseBody struct {
	Get               *RedisResponseBodyGet               `json:"get,omitempty"`
	SubscribeResponse *RedisResponseBodySubscribeResponse `json:"subscribeResponse,omitempty"`
	PushFromSubscribe *RedisResponseBodyPushFromSubscribe `json:"pushFromSubscribe,omitempty"`
	Pong              *RedisResponseBodyPong              `json:"pong,omitempty"`
}

type RedisResponseBodySubscribeResponse struct {
	Error string `json:"error"`
}

type RedisResponseBodyGet struct {
	Bytes    []byte `json:"bytes"`
	CacheHit bool   `json:"cacheHit"`
	Error    string `json:"error"`
}

type RedisResponseBodyPushFromSubscribe struct {
	ChannelName string `json:"channelName"`
}

type RedisResponseBodyPong struct {
}

// ResourceRequest is an event that holds a request for a resource. It is
// usually emitted from the resource proxy, and is sent from the principal
// to an agent.
type ResourceRequest struct {
	// A unique UUID for this resource request. This unique ID will also be
	// used in the request response, so we can map responses to requests.
	// Note that this does neither correlate to event ID nor the Kubernetes
	// resource's ID.
	UUID string `json:"uuid"`
	// Namespace of the requested resource
	Namespace string `json:"namespace,omitempty"`
	// Name of the requested resource
	Name string `json:"name"`
	// HTTP method of the request
	Method string `json:"method,omitempty"`
	// Body for write requests e.g. POST, PATCH, etc.
	Body []byte `json:"body,omitempty"`
	// Parameters from the HTTP request
	Params map[string]string `json:"params,omitempty"`
	// The group and version of the requested resource
	v1.GroupVersionResource
}

// IsEmpty returns true if the resource request is empty
func (r *ResourceRequest) IsEmpty() bool {
	return r.Group == "" && r.Version == "" && r.Resource == "" && r.Name == "" && r.Namespace == ""
}

// IsList returns true if the resource request is a list request
func (r *ResourceRequest) IsList() bool {
	return r.Resource == "" && r.Name == ""
}

// IsResource returns true if the resource request is a resource request
func (r *ResourceRequest) IsResource() bool {
	return r.Resource != "" && r.Name != ""
}

// IsClusterScoped returns true if the resource request is a cluster-scoped request
func (r *ResourceRequest) IsClusterScoped() bool {
	return r.Namespace == ""
}

// IsNamespaced returns true if the resource request is a namespaced request
func (r *ResourceRequest) IsNamespaced() bool {
	return r.Namespace != ""
}

// IsValid returns true if the resource request is valid
func (r *ResourceRequest) IsValid() bool {
	return r.IsResource() || r.IsList()
}

// ResourceResponse is an event that holds the response to a resource request.
// It is usually sent by an agent to the princiapl in response to a prior
// resource request.
type ResourceResponse struct {
	// UUID is the unique ID of the request this response is targeted at
	UUID string `json:"uuid"`
	// Status contains the HTTP status of the request
	Status int `json:"status"`
	// Resource is the body of the requested resource
	Resource string `json:"resource,omitempty"`
}

// HTTPStatusFromError tries to derive a HTTP status code from the error err.
// If err is non-nil and the code cannot be derived, it will be set to HTTP 500
// InternalServerError. If err is nil, HTTP 200 OK will be returned.
func HTTPStatusFromError(err error) int {
	if err != nil {
		if status, ok := err.(apierrors.APIStatus); ok || errors.As(err, &status) {
			return int(status.Status().Code)
		} else {
			return http.StatusInternalServerError
		}
	}
	return http.StatusOK
}

func (evs EventSource) NewRedisRequestEvent(connectionUUID string, body RedisCommandBody) (*cloudevents.Event, error) {
	reqUUID := uuid.NewString()
	rr := &RedisRequest{
		UUID:           reqUUID,
		ConnectionUUID: connectionUUID,
		Body:           body,
	}
	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(RedisGenericRequest.String())
	cev.SetDataSchema(TargetRedis.String())
	cev.SetExtension(resourceID, reqUUID)
	cev.SetExtension(eventID, reqUUID)
	err := cev.SetData(cloudevents.ApplicationJSON, rr)
	return &cev, err
}

func (evs EventSource) NewRedisResponseEvent(reqUUID string, connectionUUID string, body RedisResponseBody) *cloudevents.Event {
	resUUID := uuid.NewString()
	rr := &RedisResponse{
		UUID:           reqUUID,
		ConnectionUUID: connectionUUID,
		Body:           body,
	}
	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(RedisGenericResponse.String())
	cev.SetDataSchema(TargetRedis.String())
	cev.SetExtension(resourceID, resUUID)
	// eventid must be set to the requested resource's uuid
	cev.SetExtension(eventID, reqUUID)
	_ = cev.SetData(cloudevents.ApplicationJSON, rr)
	return &cev
}

// NewResourceRequestEvent creates a cloud event for requesting a resource from
// an agent. The resource will be specified by the GroupVersionResource id in
// gvr, and its name and namespace. If namespace is empty, the request will
// be for a cluster-scoped resource.
//
// The event's resource ID and event ID will be set to a randomly generated
// UUID, because we'll have
func (evs EventSource) NewResourceRequestEvent(gvr v1.GroupVersionResource, namespace, name, method string, body []byte, params map[string]string) (*cloudevents.Event, error) {
	reqUUID := uuid.NewString()
	rr := &ResourceRequest{
		UUID:                 reqUUID,
		Namespace:            namespace,
		Name:                 name,
		GroupVersionResource: gvr,
		Method:               method,
		Body:                 body,
		Params:               params,
	}
	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(method)
	cev.SetDataSchema(TargetResource.String())
	cev.SetExtension(resourceID, reqUUID)
	cev.SetExtension(eventID, reqUUID)
	err := cev.SetData(cloudevents.ApplicationJSON, rr)
	return &cev, err
}

func (evs EventSource) NewResourceResponseEvent(reqUUID string, status int, data string) *cloudevents.Event {
	resUUID := uuid.NewString()
	rr := &ResourceResponse{
		UUID:     reqUUID,
		Status:   status,
		Resource: data,
	}
	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(GetResponse.String())
	cev.SetDataSchema(TargetResource.String())
	cev.SetExtension(resourceID, resUUID)
	// eventid must be set to the requested resource's uuid
	cev.SetExtension(eventID, reqUUID)
	_ = cev.SetData(cloudevents.ApplicationJSON, rr)
	return &cev
}

func (evs EventSource) ProcessedEvent(evType EventType, ev *Event) *cloudevents.Event {
	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(evType.String())

	for k, v := range ev.event.Extensions() {
		cev.SetExtension(k, v)
	}

	cev.SetDataSchema(TargetEventAck.String())
	return &cev
}

// RequestSyncedResourceList is sent by a peer to the source when the peer process restarts.
// Peer sends a checksum of all the resource keys to the source to detect orphaned resources.
// Managed mode: Sent from Agent to Principal
// Autonomous mode: Sent from Principal to Agent
type RequestSyncedResourceList struct {
	// checksum of all the resource keys managed by that component
	Checksum []byte `json:"checksum"`
}

func (evs EventSource) RequestSyncedResourceListEvent(checksum []byte) (*cloudevents.Event, error) {
	reqUUID := uuid.NewString()
	req := &RequestSyncedResourceList{
		Checksum: checksum,
	}

	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(SyncedResourceList.String())
	cev.SetDataSchema(TargetResourceResync.String())
	cev.SetExtension(resourceID, reqUUID)
	cev.SetExtension(eventID, reqUUID)

	err := cev.SetData(cloudevents.ApplicationJSON, req)
	return &cev, err
}

// SyncedResource is a response to the SyncedResourceList request and is sent for each resource managed by the source.
// On receiving the SyncedResourceList request, the source will calculate the checksum of all its resource keys.
// If the checksum doesn't match, the source will send the SyncedResource event for each resource.
type SyncedResource struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	UID       string `json:"uid"`
	Kind      string `json:"kind"`
}

func (evs EventSource) SyncedResourceEvent(resourceKey resources.ResourceKey) (*cloudevents.Event, error) {
	reqUUID := uuid.NewString()
	syncedResource := SyncedResource{
		Name:      resourceKey.Name,
		Namespace: resourceKey.Namespace,
		UID:       resourceKey.UID,
		Kind:      resourceKey.Kind,
	}

	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(ResponseSyncedResource.String())
	cev.SetDataSchema(TargetResourceResync.String())
	cev.SetExtension(resourceID, reqUUID)
	cev.SetExtension(eventID, reqUUID)

	err := cev.SetData(cloudevents.ApplicationJSON, syncedResource)
	return &cev, err
}

// RequestUpdate is used to request the latest content of a resource and is
// sent from a peer to the source of truth. The peer calculates the spec checksum
// of a resource and sends it in the RequestUpdate event. The source will calculate
// the spec checksum on its side and sends a SpecUpdate event if the checksums don't match.
type RequestUpdate struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	UID       string `json:"uid"`
	Kind      string `json:"kind"`
	Checksum  []byte `json:"checksum"`
}

func NewRequestUpdate(name, namespace, kind, uid string, checksum []byte) *RequestUpdate {
	return &RequestUpdate{
		Name:      name,
		Namespace: namespace,
		UID:       uid,
		Kind:      kind,
		Checksum:  checksum,
	}
}

func (evs EventSource) RequestUpdateEvent(reqUpdate *RequestUpdate) (*cloudevents.Event, error) {
	reqUUID := uuid.NewString()
	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(EventRequestUpdate.String())
	cev.SetDataSchema(TargetResourceResync.String())
	cev.SetExtension(resourceID, reqUUID)
	cev.SetExtension(eventID, reqUUID)

	err := cev.SetData(cloudevents.ApplicationJSON, reqUpdate)
	return &cev, err
}

// RequestResourceResync is sent by the source to a peer when the source process restarts.
// It informs the peer that the source process restarted and it might be out of sync with the source.
type RequestResourceResync struct {
}

func (evs EventSource) RequestResourceResyncEvent() (*cloudevents.Event, error) {
	reqUUID := uuid.NewString()
	req := &RequestResourceResync{}

	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(EventRequestResourceResync.String())
	cev.SetDataSchema(TargetResourceResync.String())
	cev.SetExtension(resourceID, reqUUID)
	cev.SetExtension(eventID, reqUUID)

	err := cev.SetData(cloudevents.ApplicationJSON, req)
	return &cev, err
}

// FromWire validates an event from the wire in protobuf format, converts it
// into an Event object and returns it. If the event on the wire is invalid,
// or could not be converted for another reason, FromWire returns an error.
func FromWire(pev *pb.CloudEvent) (*Event, error) {
	raw, err := format.FromProto(pev)
	if err != nil {
		return nil, err
	}
	ev := &Event{}
	var target EventTarget
	if ev.target = Target(raw); ev.target == "" {
		return nil, fmt.Errorf("unknown event target FromWire: %s / %v", target, *raw)
	}
	ev.event = raw
	return ev, nil
}

func Target(raw *cloudevents.Event) EventTarget {
	switch raw.DataSchema() {
	case TargetApplication.String():
		return TargetApplication
	case TargetAppProject.String():
		return TargetAppProject
	case TargetResource.String():
		return TargetResource
	case TargetEventAck.String():
		return TargetEventAck
	case TargetResourceResync.String():
		return TargetResourceResync
	case TargetRedis.String():
		return TargetRedis
	}
	return ""
}

func (ev Event) Target() EventTarget {
	return ev.target
}

func (ev Event) Type() EventType {
	return EventType(ev.event.Type())
}

func (ev Event) Application() (*v1alpha1.Application, error) {
	app := &v1alpha1.Application{}
	err := ev.event.DataAs(app)
	return app, err
}

func (ev Event) ResourceID() string {
	return ResourceID(ev.event)
}

func (ev Event) EventID() string {
	return EventID(ev.event)
}

func ResourceID(ev *cloudevents.Event) string {
	id, ok := ev.Extensions()[resourceID].(string)
	if ok {
		return id
	}

	return ""
}

func EventID(ev *cloudevents.Event) string {
	id, ok := ev.Extensions()[eventID].(string)
	if ok {
		return id
	}

	return ""
}

func (ev Event) AppProject() (*v1alpha1.AppProject, error) {
	proj := &v1alpha1.AppProject{}
	err := ev.event.DataAs(proj)
	return proj, err
}

// ResourceRequest gets the resource request payload from an event
func (ev Event) RedisRequest() (*RedisRequest, error) {
	req := &RedisRequest{}
	err := ev.event.DataAs(req)
	return req, err
}

// ResourceRequest gets the resource request payload from an event
func (ev Event) ResourceRequest() (*ResourceRequest, error) {
	req := &ResourceRequest{}
	err := ev.event.DataAs(req)
	return req, err
}

// ResourceResponse gets the resource response payload from an event
func (ev Event) ResourceResponse() (*ResourceResponse, error) {
	resp := &ResourceResponse{}
	err := ev.event.DataAs(resp)
	return resp, err
}

func (ev Event) RequestSyncedResourceList() (*RequestSyncedResourceList, error) {
	syncedResourceList := &RequestSyncedResourceList{}
	err := ev.event.DataAs(syncedResourceList)
	return syncedResourceList, err
}

func (ev Event) SyncedResource() (*SyncedResource, error) {
	syncedResource := &SyncedResource{}
	err := ev.event.DataAs(syncedResource)
	return syncedResource, err
}

func (ev Event) RequestResourceResync() (*RequestResourceResync, error) {
	resResync := &RequestResourceResync{}
	err := ev.event.DataAs(resResync)
	return resResync, err
}

func (ev Event) RequestUpdate() (*RequestUpdate, error) {
	reqUpdate := &RequestUpdate{}
	err := ev.event.DataAs(reqUpdate)
	return reqUpdate, err
}

type streamWriter interface {
	Send(*eventstreamapi.Event) error
	Context() context.Context
}

// EventWriter keeps track of the latest event for resources and sends them on a given gRPC stream.
// It resends the event with exponential backoff until the event is ACK'd and removed from its list.
type EventWriter struct {
	mu sync.RWMutex

	// key: resource name + UID
	// value: latest event for a resource
	// - acquire 'lock' before accessing
	latestEvents map[string]*eventMessage

	// target refers to the specified gRPC stream.
	target streamWriter

	log *logrus.Entry
}

type eventMessage struct {
	mu sync.RWMutex

	// latest event for a resource
	// - acquire 'lock' before accessing
	event *cloudevents.Event

	// retry sending the event after this time
	retryAfter *time.Time

	// config for exponential backoff
	backoff *wait.Backoff
}

func NewEventWriter(target streamWriter) *EventWriter {
	return &EventWriter{
		latestEvents: map[string]*eventMessage{},
		target:       target,
		log:          logging.ModuleLogger("EventWriter").WithField(logfields.ClientAddr, grpcutil.AddressFromContext(target.Context())),
	}
}

func (ew *EventWriter) UpdateTarget(target streamWriter) {
	ew.mu.Lock()
	defer ew.mu.Unlock()
	ew.target = target
}

func (ew *EventWriter) Add(ev *cloudevents.Event) {
	resID := ResourceID(ev)
	logCtx := ew.log.WithFields(logrus.Fields{
		"resource_id": ResourceID(ev),
		"event_id":    EventID(ev),
		"type":        ev.Type(),
	})

	ew.mu.Lock()
	defer ew.mu.Unlock()

	defaultBackoff := wait.Backoff{
		Steps:    5,
		Duration: 5 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
	}

	if resID == "" {
		logCtx.Error("resID was empty")
		return
	}

	eventMsg, exists := ew.latestEvents[resID]
	if !exists {
		ew.latestEvents[resID] = &eventMessage{
			event:   ev,
			backoff: &defaultBackoff,
		}
		logCtx.Trace("added a new event to the event writer")
		return
	}

	// Replace the old event and reset the backoff.
	eventMsg.mu.Lock()
	eventMsg.event = ev
	eventMsg.backoff = &defaultBackoff
	eventMsg.retryAfter = nil
	eventMsg.mu.Unlock()
	logCtx.Trace("updated an existing event in the event writer")
}

func (ew *EventWriter) Get(resID string) *eventMessage {
	ew.mu.RLock()
	defer ew.mu.RUnlock()
	return ew.latestEvents[resID]
}

func (ew *EventWriter) Remove(ev *cloudevents.Event) {
	ew.mu.Lock()
	defer ew.mu.Unlock()

	// Remove the event only if it matches both the resourceID and eventID.
	resourceID := ResourceID(ev)
	latestEvent, exists := ew.latestEvents[resourceID]
	if !exists {
		return
	}

	latestEvent.mu.RLock()
	latestEventID := EventID(latestEvent.event)
	latestEvent.mu.RUnlock()

	if latestEventID == EventID(ev) {
		delete(ew.latestEvents, resourceID)
	}
}

// SendWaitingEvents will periodically send the events waiting in the EventWriter.
// Note: This function will never return unless the context is done, and therefore
// should be started in a separate goroutine.
func (ew *EventWriter) SendWaitingEvents(ctx context.Context) {

	logCtx := ew.log

	logCtx.Info("Starting event writer")
	for {
		select {
		case <-ctx.Done():
			logCtx.Info("Shutting down event writer")
			return
		default:
			ew.mu.RLock()
			resourceIDs := make([]string, 0, len(ew.latestEvents))
			for resID := range ew.latestEvents {
				resourceIDs = append(resourceIDs, resID)
			}
			ew.mu.RUnlock()

			for _, resourceID := range resourceIDs {
				ew.sendEvent(resourceID)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (ew *EventWriter) sendEvent(resID string) {
	logCtx := ew.log.WithFields(logrus.Fields{
		"resource_id": resID,
	})

	// Check if the event is already ACK'd.
	eventMsg := ew.Get(resID)
	if eventMsg == nil {
		logCtx.Trace("event is not found, perhaps it is already ACK'd")
		return
	}

	logCtx = logCtx.WithFields(logrus.Fields{
		"event_id":     EventID(eventMsg.event),
		"event_target": eventMsg.event.DataSchema(),
		"event_type":   eventMsg.event.Type()})

	isACKRemoved := false

	eventMsg.mu.Lock()
	defer func() {
		// Check if the mu is already unlocked while removing the ACK.
		if !isACKRemoved {
			eventMsg.mu.Unlock()
		}
	}()

	// Check if it is time to resend the event.
	if eventMsg.retryAfter != nil {
		if eventMsg.retryAfter.After(time.Now()) {
			return
		}
		logCtx.Trace("resending an event")
	}

	defer func() {
		// Update the retryAfter for resending the event again
		retryAfter := time.Now().Add(eventMsg.backoff.Step())
		eventMsg.retryAfter = &retryAfter
	}()

	// Resend the event since it is not ACK'd.
	pev, err := format.ToProto(eventMsg.event)
	if err != nil {
		logCtx.Errorf("Could not wire event: %v\n", err)
		return
	}

	// A Send() on the stream is actually not blocking.
	err = ew.target.Send(&eventstreamapi.Event{Event: pev})
	if err != nil {
		logCtx.Errorf("Error while sending: %v\n", err)
		return
	}

	logCtx.Trace("event sent to target")

	// We don't have to wait for an ACK if the current event is ACK. So, remove it from the EventWriter.
	if Target(eventMsg.event) == TargetEventAck {
		eventMsg.mu.Unlock()
		ew.Remove(eventMsg.event)
		logCtx.Trace("ACK is removed from the event writer")
		isACKRemoved = true
	}
}

// eventWritersMap provides a thread-safe way to manage event writers.
type EventWritersMap struct {
	mu sync.RWMutex

	// key: AgentName
	// value: EventWriter for that agent
	// - acquire 'lock' before accessing
	eventWriters map[string]*EventWriter
}

func NewEventWritersMap() *EventWritersMap {
	return &EventWritersMap{
		eventWriters: make(map[string]*EventWriter),
	}
}

func (ewm *EventWritersMap) Get(agentName string) *EventWriter {
	ewm.mu.RLock()
	defer ewm.mu.RUnlock()

	eventWriter, exists := ewm.eventWriters[agentName]
	if exists {
		return eventWriter
	}

	return nil
}

func (ewm *EventWritersMap) Add(agentName string, eventWriter *EventWriter) {
	ewm.mu.Lock()
	defer ewm.mu.Unlock()

	ewm.eventWriters[agentName] = eventWriter
}

func (ewm *EventWritersMap) Remove(agentName string) {
	ewm.mu.Lock()
	defer ewm.mu.Unlock()

	delete(ewm.eventWriters, agentName)
}
