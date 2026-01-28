package event

import (
	"context"
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/grpcutil"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
)

type streamWriter interface {
	Send(*eventstreamapi.Event) error
	Context() context.Context
}

// EventWriter keeps track of the latest event for resources and sends them on a given gRPC stream.
// It resends the event with exponential backoff until the event is ACK'd and removed from its list.
type EventWriter struct {
	mu sync.RWMutex

	// key: resource name + UID
	// value: queue of unsent events for a resource
	// - acquire 'lock' before accessing
	unsentEvents map[string]*eventQueue

	// key: resource name + UID
	// value: sent event waiting for ACK
	// - acquire 'lock' before accessing
	sentEvents map[string]*eventMessage

	// target refers to the specified gRPC stream.
	target streamWriter

	log *logrus.Entry
}

type eventMessage struct {
	// when this lock is owned, never attempt to THEN acquire `eventWriter.mu`, as this will lead to a deadlock.
	// If you require `eventWriter.mu`, you must acquire that lock FIRST, before attempting to acquiring `mu` (to avoid deadlock)
	mu sync.RWMutex

	// latest event for a resource
	// - acquire 'lock' before accessing
	event *cloudevents.Event

	// retry sending the event after this time
	retryAfter *time.Time

	// config for exponential backoff
	backoff *wait.Backoff

	// track number of retries attempted
	retryCount int
}

func NewEventWriter(target streamWriter) *EventWriter {
	return &EventWriter{
		unsentEvents: map[string]*eventQueue{},
		sentEvents:   map[string]*eventMessage{},
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

	// Once an app is being deleted, no other updates matter
	if ev.Type() == Delete.String() {
		delete(ew.sentEvents, resID)
		// Clear any existing unsent events and add only the DELETE event
		eq := newEventQueue()
		eq.add(&eventMessage{
			event:   ev,
			backoff: &defaultBackoff,
		})
		ew.unsentEvents[resID] = eq
		logCtx.Trace("cleared all events and added DELETE event")
		return
	}

	// Add to unsent queue with coalescing logic
	eq, exists := ew.unsentEvents[resID]
	if !exists {
		eq = newEventQueue()
		eq.add(&eventMessage{
			event:   ev,
			backoff: &defaultBackoff,
		})
		ew.unsentEvents[resID] = eq
		logCtx.Trace("added a new event to the event writer")
		return
	}

	// The queue's add() coalesces events of the same type.
	eq.add(&eventMessage{
		event:      ev,
		backoff:    &defaultBackoff,
		retryAfter: nil,
	})

	logCtx.Trace("updated an existing event in the event writer")
}

func (ew *EventWriter) Get(resID string) *eventMessage {
	ew.mu.RLock()
	defer ew.mu.RUnlock()

	// First check sent events (for retry)
	if sent, exists := ew.sentEvents[resID]; exists {
		return sent
	}

	// Then check unsent queue
	if eq, exists := ew.unsentEvents[resID]; exists {
		return eq.get()
	}
	return nil
}

func (ew *EventWriter) Remove(ev *cloudevents.Event) {
	ew.mu.Lock()
	defer ew.mu.Unlock()

	// Remove the event only if it matches both the resourceID and eventID.
	resourceID := ResourceID(ev)
	incomingEventID := EventID(ev)

	// First, check and remove from sent events
	if sent, exists := ew.sentEvents[resourceID]; exists {
		sent.mu.RLock()
		sentEventID := EventID(sent.event)
		sent.mu.RUnlock()

		if sentEventID == incomingEventID {
			delete(ew.sentEvents, resourceID)
			return
		}
	}

	// If not in sent events, check unsent queue
	eq, exists := ew.unsentEvents[resourceID]
	if !exists {
		return
	}

	front := eq.get()
	if front == nil {
		return
	}

	front.mu.RLock()
	frontEventID := EventID(front.event)
	front.mu.RUnlock()

	if frontEventID == incomingEventID {
		eq.pop()
		if eq.isEmpty() {
			delete(ew.unsentEvents, resourceID)
		}
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
			// Collect all resource IDs that have either unsent or sent events
			resourceIDs := make([]string, 0, len(ew.unsentEvents)+len(ew.sentEvents))
			for resID := range ew.unsentEvents {
				resourceIDs = append(resourceIDs, resID)
			}
			for resID := range ew.sentEvents {
				// Only add if not already in the list
				if _, exists := ew.unsentEvents[resID]; !exists {
					resourceIDs = append(resourceIDs, resID)
				}
			}
			ew.mu.RUnlock()

			for _, resourceID := range resourceIDs {
				ew.sendEvent(resourceID)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// sendEvent determines whether to retry a sent event or send a new unsent event
func (ew *EventWriter) sendEvent(resID string) {
	// Check if there's a sent event awaiting retry
	ew.mu.RLock()
	sentMsg, hasSent := ew.sentEvents[resID]
	ew.mu.RUnlock()

	if hasSent {
		ew.retrySentEvent(resID, sentMsg)
	} else {
		ew.sendUnsentEvent(resID)
	}
}

// retrySentEvent handles retrying an event that was already sent but not yet acknowledged
func (ew *EventWriter) retrySentEvent(resID string, sentMsg *eventMessage) {
	logCtx := ew.log.WithFields(logrus.Fields{
		"method":      "retrySentEvent",
		"resource_id": resID,
	})

	// Re-verify the event is still in sentEvents
	ew.mu.RLock()
	currentSent, stillExists := ew.sentEvents[resID]
	ew.mu.RUnlock()

	// If event was ACK'd between check and use, skip retry
	if !stillExists || currentSent != sentMsg {
		return
	}

	sentMsg.mu.Lock()

	// Check if it's time to retry
	if sentMsg.retryAfter != nil && sentMsg.retryAfter.After(time.Now()) {
		sentMsg.mu.Unlock()
		return
	}

	logCtx = logCtx.WithFields(logrus.Fields{
		"event_id":     EventID(sentMsg.event),
		"event_target": sentMsg.event.DataSchema(),
		"event_type":   sentMsg.event.Type(),
		"retry_count":  sentMsg.retryCount})

	// Check if we've exhausted retries
	maxRetries := sentMsg.backoff.Steps
	if sentMsg.retryCount >= maxRetries {
		logCtx.Warnf("Event failed after %d retries, giving up to unblock queue", sentMsg.retryCount)
		sentMsg.mu.Unlock()

		// Remove from sentEvents to unblock the queue
		ew.mu.Lock()
		delete(ew.sentEvents, resID)
		ew.mu.Unlock()
		return
	}

	logCtx.Trace("resending an event")

	// Increment retry count and schedule next retry BEFORE attempting send
	// This ensures proper backoff even when the send fails
	sentMsg.retryCount++
	retryAfter := time.Now().Add(sentMsg.backoff.Step())
	sentMsg.retryAfter = &retryAfter

	// Resend the event
	pev, err := format.ToProto(sentMsg.event)
	if err != nil {
		logCtx.Errorf("Could not wire event: %v\n", err)
		sentMsg.mu.Unlock()
		return
	}

	err = ew.target.Send(&eventstreamapi.Event{Event: pev})
	sentMsg.mu.Unlock()

	if err != nil {
		logCtx.Errorf("Error while sending: %v\n", err)
		return
	}

	logCtx.Trace("event sent to target")
}

// scheduleRetry sets the next retry time for an event using exponential backoff
func (ew *EventWriter) scheduleRetry(eventMsg *eventMessage) {
	eventMsg.mu.Lock()
	defer eventMsg.mu.Unlock()
	retryAfter := time.Now().Add(eventMsg.backoff.Step())
	eventMsg.retryAfter = &retryAfter
}

// sendUnsentEvent pops an event from the unsent queue and sends it for the first time
func (ew *EventWriter) sendUnsentEvent(resID string) {
	logCtx := ew.log.WithFields(logrus.Fields{
		"method":      "sendUnsentEvent",
		"resource_id": resID,
	})

	// Pop event from unsent queue and atomically move to sent tracker
	ew.mu.Lock()
	eq, exists := ew.unsentEvents[resID]
	if !exists || eq.isEmpty() {
		ew.mu.Unlock()
		return
	}

	eventMsg := eq.pop()
	if eq.isEmpty() {
		delete(ew.unsentEvents, resID)
	}

	if eventMsg == nil {
		ew.mu.Unlock()
		return
	}

	target := Target(eventMsg.event)
	isFireAndForget := target == TargetEventAck || target == TargetHeartbeat
	if !isFireAndForget {
		// IMPORTANT: Set retryAfter *before* publishing into sentEvents.
		// We can have concurrent SendWaitingEvents loops (e.g. brief overlap during reconnect),
		// and without this, another goroutine can observe retryAfter==nil and immediately retry,
		// causing duplicate sends within the same second.
		//
		// Retry behavior:
		// On success: retry happens if ACK never arrives
		// On failure: retry happens after backoff
		ew.scheduleRetry(eventMsg)
		ew.sentEvents[resID] = eventMsg
	}
	ew.mu.Unlock()

	// Send the event
	eventMsg.mu.Lock()
	logCtx = logCtx.WithFields(logrus.Fields{
		"event_id":     EventID(eventMsg.event),
		"event_target": eventMsg.event.DataSchema(),
		"event_type":   eventMsg.event.Type()})

	pev, err := format.ToProto(eventMsg.event)
	eventMsg.mu.Unlock()

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

	if isFireAndForget {
		logCtx.Trace("Fire-and-forget event (ACK or heartbeat) removed from event writer")
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

// eventQueue is a queue of eventMessages where the items are coalesced by type.
type eventQueue struct {
	mu    sync.RWMutex
	items []*eventMessage
}

func newEventQueue() *eventQueue {
	return &eventQueue{
		items: []*eventMessage{},
	}
}

// add an item to the tail of the queue.
// If the item is the same type as the tail, replace the tail with the new item.
func (eq *eventQueue) add(ev *eventMessage) {
	eq.mu.Lock()
	defer eq.mu.Unlock()

	if len(eq.items) > 0 {
		tail := eq.items[len(eq.items)-1]
		tail.mu.Lock()

		// Replace an older event with a newer one of the same type
		if ev.event.Type() == tail.event.Type() {
			tail.event = ev.event
			tail.backoff = ev.backoff
			tail.retryAfter = ev.retryAfter
			tail.mu.Unlock()
			return
		}
		tail.mu.Unlock()
	}

	eq.items = append(eq.items, ev)
}

// get the first item from the queue.
func (eq *eventQueue) get() *eventMessage {
	eq.mu.RLock()
	defer eq.mu.RUnlock()
	if len(eq.items) == 0 {
		return nil
	}

	return eq.items[0]
}

// pop the first item from the queue.
func (eq *eventQueue) pop() *eventMessage {
	eq.mu.Lock()
	defer eq.mu.Unlock()
	if len(eq.items) == 0 {
		return nil
	}
	item := eq.items[0]
	eq.items = eq.items[1:]
	return item
}

func (eq *eventQueue) isEmpty() bool {
	eq.mu.RLock()
	defer eq.mu.RUnlock()
	return len(eq.items) == 0
}
