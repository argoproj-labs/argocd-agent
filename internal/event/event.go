package event

import (
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
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
	// EvenAppSpecUpdated is an event to update an application's spec field
	EvenAppSpecUpdated
	// EvenAppStatusUpdated is an event to update an application's status field
	EvenAppStatusUpdated
	// EventAppOperationUpdated is an event to update an application's operation field
	EventAppOperationUpdated
)

type Event struct {
	Type           EventType
	Application    *v1alpha1.Application
	AppProject     *v1alpha1.AppProject     // Forward compatibility
	ApplicationSet *v1alpha1.ApplicationSet // Forward compatibility
	Created        *time.Time
	Processed      *time.Time
}

func (et EventType) String() string {
	switch et {
	case EventUnknown:
		return "unknown"
	case EventAppAdded:
		return "add"
	case EventAppDeleted:
		return "delete"
	case EvenAppSpecUpdated:
		return "update_spec"
	case EventAppOperationUpdated:
		return "update_operation"
	case EvenAppStatusUpdated:
		return "update_status"
	default:
		return "unknown"
	}
}
