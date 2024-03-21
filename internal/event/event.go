package event

import (
	"fmt"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"

	_ "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"github.com/cloudevents/sdk-go/binding/format/protobuf/v2/pb"
	cloudevents "github.com/cloudevents/sdk-go/v2"
)

const cloudEventSpecVersion = "1.0"

type EventType string
type EventTarget string

const TypePrefix = "io.argoproj.argocd-agent.event"

const (
	Ping            EventType = TypePrefix + ".ping"
	Pong            EventType = TypePrefix + ".pong"
	Create          EventType = TypePrefix + ".create"
	Delete          EventType = TypePrefix + ".delete"
	Update          EventType = TypePrefix + ".update"
	SpecUpdate      EventType = TypePrefix + ".spec-update"
	StatusUpdate    EventType = TypePrefix + ".status-update"
	OperationUpdate EventType = TypePrefix + ".operation-update"
)

const (
	TargetUnknown     EventTarget = "unknown"
	TargetApplication EventTarget = "application"
	TargetAppProject  EventTarget = "appproject"
)

func (t EventType) String() string {
	return string(t)
}

func (t EventTarget) String() string {
	return string(t)
}

type EventSource struct {
	source string
}

type Event struct {
	event  *cloudevents.Event
	target EventTarget
}

func NewEventSource(source string) *EventSource {
	ev := &EventSource{}
	ev.source = source
	return ev
}

func (evs EventSource) NewApplicationEvent(evType EventType, app *v1alpha1.Application) *cloudevents.Event {
	cev := cloudevents.NewEvent()
	cev.SetSource(evs.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(evType.String())
	cev.SetDataSchema(TargetApplication.String())
	// TODO: Handle this error situation?
	_ = cev.SetData(cloudevents.ApplicationJSON, app)
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

func (ev Event) AppProject() (*v1alpha1.AppProject, error) {
	proj := &v1alpha1.AppProject{}
	err := ev.event.DataAs(proj)
	return proj, err
}
