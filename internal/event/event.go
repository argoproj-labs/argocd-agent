package event

import (
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"

	_ "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"github.com/cloudevents/sdk-go/binding/format/protobuf/v2/pb"
	cloudevents "github.com/cloudevents/sdk-go/v2"
)

type EventType int32

const (
	EventUnknown EventType = iota
	// EventPing is an empty event request to check whether the connection is still intact
	EventPing
	// EventAppAdded is an event to let the peer know about a new application
	EventAppAdded
	// EventAppDeleted is an event to let the peer know about the deletion of an app
	EventAppDeleted
	// EventAppSpecUpdated is an event to update an application's spec field
	EventAppSpecUpdated
	// EventAppStatusUpdated is an event to update an application's status field
	EventAppStatusUpdated
	// EventAppOperationUpdated is an event to update an application's operation field
	EventAppOperationUpdated
)

const cloudEventSpecVersion = "1.0"

const TypePrefix = "io.argoproj.argocd-agent"

const (
	Ping                       = TypePrefix + ".ping"
	Pong                       = TypePrefix + ".pong"
	ApplicationCreated         = TypePrefix + ".application.create"
	ApplicationDeleted         = TypePrefix + ".application.delete"
	ApplicationSpecUpdated     = TypePrefix + ".application.spec-update"
	ApplicationStatusUpdate    = TypePrefix + ".application.status-update"
	ApplicationOperationUpdate = TypePrefix + ".application.operation-update"
)

// type LegacyEvent struct {
// 	Type           EventType
// 	Application    *v1alpha1.Application
// 	AppProject     *v1alpha1.AppProject     // Forward compatibility
// 	ApplicationSet *v1alpha1.ApplicationSet // Forward compatibility
// 	Created        *time.Time
// 	Processed      *time.Time
// 	Event          cloudevents.Event
// }

type Event struct {
	source string
}

func (et EventType) String() string {
	switch et {
	case EventUnknown:
		return "unknown"
	case EventAppAdded:
		return "add"
	case EventAppDeleted:
		return "delete"
	case EventAppSpecUpdated:
		return "update_spec"
	case EventAppOperationUpdated:
		return "update_operation"
	case EventAppStatusUpdated:
		return "update_status"
	default:
		return "unknown"
	}
}

func NewEventEmitter(source string) *Event {
	ev := &Event{}
	ev.source = source
	return ev
}

func foo() {
}

func (ev Event) NewApplicationEvent(evType string, app *v1alpha1.Application) *cloudevents.Event {
	cev := cloudevents.NewEvent()
	cev.SetSource(ev.source)
	cev.SetSpecVersion(cloudEventSpecVersion)
	cev.SetType(evType)
	cev.SetData(cloudevents.ApplicationJSON, app)
	return &cev
}

func ApplicationFromWire(pev *pb.CloudEvent) (*cloudevents.Event, *v1alpha1.Application, error) {
	app := v1alpha1.Application{}
	ev, err := format.FromProto(pev)
	if err != nil {
		return nil, nil, err
	}
	err = ev.DataAs(&app)
	if err != nil {
		return nil, nil, err
	}
	return ev, &app, nil
}
