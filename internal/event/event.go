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
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
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
	Ping            EventType = TypePrefix + ".ping"
	Pong            EventType = TypePrefix + ".pong"
	Create          EventType = TypePrefix + ".create"
	Delete          EventType = TypePrefix + ".delete"
	Update          EventType = TypePrefix + ".update"
	SpecUpdate      EventType = TypePrefix + ".spec-update"
	StatusUpdate    EventType = TypePrefix + ".status-update"
	OperationUpdate EventType = TypePrefix + ".operation-update"
	EventProcessed  EventType = TypePrefix + ".processed"
	GetRequest      EventType = TypePrefix + ".get"
	GetResponse     EventType = TypePrefix + ".response"
)

const (
	TargetUnknown     EventTarget = "unknown"
	TargetApplication EventTarget = "application"
	TargetAppProject  EventTarget = "appproject"
	TargetEventAck    EventTarget = "eventProcessed"
	TargetResource    EventTarget = "resource"
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
	// The group and version of the requested resource
	v1.GroupVersionResource
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

func HttpStatusFromError(err error) int {
	if status, ok := err.(apierrors.APIStatus); ok || errors.As(err, &status) {
		return int(status.Status().Code)
	}
	return http.StatusOK
}

// NewResourceRequestEvent creates a cloud event for requesting a resource from
// an agent. The resource will be specified by the GroupVersionResource id in
// gvr, and its name and namespace. If namespace is empty, the request will
// be for a cluster-scoped resource.
//
// The event's resource ID and event ID will be set to a randomly generated
// UUID, because we'll have
func (evs EventSource) NewResourceRequestEvent(gvr v1.GroupVersionResource, namespace string, name string) (*cloudevents.Event, error) {
	reqUUID := uuid.NewString()
	rr := &ResourceRequest{
		UUID:                 reqUUID,
		Namespace:            namespace,
		Name:                 name,
		GroupVersionResource: gvr,
	}
	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(GetRequest.String())
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
		return nil, fmt.Errorf("unknown event target: %s", target)
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
		log: logrus.WithFields(logrus.Fields{
			"module":      "EventWriter",
			"client_addr": grpcutil.AddressFromContext(target.Context()),
		}),
	}
}

func (ew *EventWriter) Add(ev *cloudevents.Event) {
	resID := ResourceID(ev)
	logCtx := ew.log.WithFields(logrus.Fields{
		"resource_id": ResourceID(ev),
		"event_id":    EventID(ev),
	})

	ew.mu.Lock()
	defer ew.mu.Unlock()

	defaultBackoff := wait.Backoff{
		Steps:    5,
		Duration: 5 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
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
	ew.log.Info("Starting event writer")
	for {
		select {
		case <-ctx.Done():
			ew.log.Info("Shutting down event writer")
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
	// Check if the event is already ACK'd.
	eventMsg := ew.Get(resID)
	if eventMsg == nil {
		ew.log.WithField("resource_id", resID).Trace("event is not found, perhaps it is already ACK'd")
		return
	}

	isACKRemoved := false

	eventMsg.mu.Lock()
	defer func() {
		// Check if the mu is already unlocked while removing the ACK.
		if !isACKRemoved {
			eventMsg.mu.Unlock()
		}
	}()

	logCtx := ew.log.WithFields(logrus.Fields{
		"resource_id":  resID,
		"event_id":     EventID(eventMsg.event),
		"event_target": eventMsg.event.DataSchema(),
		"event_type":   eventMsg.event.Type(),
	})

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
