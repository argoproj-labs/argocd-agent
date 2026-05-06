package event

import (
	"context"
	"errors"
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

const (
	// maxEventRetries is the maximum number of times an event will be retried before giving up.
	maxEventRetries = 12
)

type streamWriter interface {
	Send(*eventstreamapi.Event) error
	Context() context.Context
}

// SendErrorReason classifies an error returned from streamWriter.Send so
// callers (and metrics) can distinguish a dead transport from a transient
// failure.
type SendErrorReason string

const (
	SendErrorTransportClosing SendErrorReason = "transport-closing"
	SendErrorContextCanceled  SendErrorReason = "context-canceled"
	SendErrorOther            SendErrorReason = "other"
)

// SendErrorObserver is invoked synchronously after every Send() failure on
// the underlying stream. The Subscribe handler installs one of these to
// detect a permanently dead target and trigger a reconnect.
type SendErrorObserver func(reason SendErrorReason, err error)

// TargetLease is an opaque token returned by NewEventWriter and UpdateTarget.
// Pass it to SendWaitingEvents to bind the send loop to exactly that stream;
// the loop exits when a subsequent UpdateTarget advances the internal
// generation and this lease becomes stale.
type TargetLease struct{ gen uint64 }

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

	// targetGen is bumped every time UpdateTarget swaps the stream. A
	// pending Send goroutine that captured an older generation must NOT
	// surface its error to the current observer — that error belongs to
	// a stream the current Subscribe owner does not control.
	// - acquire 'mu' (read or write) before accessing
	targetGen uint64

	// agentName is the name of the agent for which this EventWriter is responsible.
	agentName string

	// onSendError is invoked after every Send() failure. May be nil.
	// - acquire 'mu' (read or write) before accessing
	onSendError SendErrorObserver

	// testHookAfterUnlock is called in sendUnsentEvent after ew.mu is released
	// and before snapshotTargetForGen. Nil in production; set in tests to
	// inject a synchronization point that exercises the post-pop gen-advance race.
	testHookAfterUnlock func()

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

// NewEventWriter creates a new EventWriter for the given target stream.
// If you create an EventWriter targeting the principal, an empty agentName
// should be used. The returned TargetLease must be passed to SendWaitingEvents
// to bind that loop to this specific stream.
func NewEventWriter(agentName string, target streamWriter) (*EventWriter, TargetLease) {
	const initialGen uint64 = 1
	ew := &EventWriter{
		unsentEvents: map[string]*eventQueue{},
		sentEvents:   map[string]*eventMessage{},
		target:       target,
		targetGen:    initialGen,
		agentName:    agentName,
		log:          logging.GetDefaultLogger().ModuleLogger("EventWriter").WithField(logfields.ClientAddr, grpcutil.AddressFromContext(target.Context())).WithField(logfields.Agent, agentName),
	}
	return ew, TargetLease{initialGen}
}

// UpdateTarget swaps in a new stream. Returns a TargetLease the caller must
// pass to SendWaitingEvents to bind that loop to this stream; the loop exits
// once a later UpdateTarget supersedes it.
//
// Clears the send-error observer — the new owner must call
// SetSendErrorObserver after UpdateTarget. Resets retry timers so queued
// events are resent immediately on the fresh stream.
func (ew *EventWriter) UpdateTarget(target streamWriter) TargetLease {
	ew.mu.Lock()
	defer ew.mu.Unlock()
	ew.target = target
	ew.targetGen++
	ew.log = logging.GetDefaultLogger().ModuleLogger("EventWriter").
		WithField(logfields.ClientAddr, grpcutil.AddressFromContext(target.Context())).
		WithField(logfields.Agent, ew.agentName)
	ew.onSendError = nil
	// Reset retry timers so events in sentEvents are retried immediately on the new connection.
	// This is important for reconnection scenarios where the old connection died
	// and ACKs will never arrive, so we want to give events a fresh chance on the new stream.
	now := time.Now()
	for _, msg := range ew.sentEvents {
		msg.mu.Lock()
		msg.retryAfter = &now
		msg.mu.Unlock()
	}
	return TargetLease{ew.targetGen}
}

// snapshotTargetForGen returns the current target if its generation still
// matches the lease. Returns ok=false when a newer UpdateTarget has taken
// over; callers must not Send in that case.
func (ew *EventWriter) snapshotTargetForGen(lease TargetLease) (streamWriter, bool) {
	ew.mu.RLock()
	defer ew.mu.RUnlock()
	if ew.targetGen != lease.gen {
		return nil, false
	}
	return ew.target, true
}

// SetSendErrorObserver installs a callback fired after every Send() failure.
// Pass nil to clear. Intended for the Subscribe handler to detect a dead
// target and tear down the stream.
func (ew *EventWriter) SetSendErrorObserver(fn SendErrorObserver) {
	ew.mu.Lock()
	defer ew.mu.Unlock()
	ew.onSendError = fn
}

// Depth returns the total number of events currently held in the writer
// (sent waiting for ACK + unsent queued). Cheap enough to call from a
// metrics scrape loop.
func (ew *EventWriter) Depth() int {
	ew.mu.RLock()
	defer ew.mu.RUnlock()

	depth := len(ew.sentEvents)
	for _, eq := range ew.unsentEvents {
		eq.mu.RLock()
		depth += len(eq.items)
		eq.mu.RUnlock()
	}
	return depth
}

// classifySendError maps a Send() error to a SendErrorReason for metrics
// labels and observer dispatch.
func classifySendError(err error) SendErrorReason {
	if err == nil {
		return SendErrorOther
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return SendErrorContextCanceled
	}
	if grpcutil.NeedReconnectOnError(err) {
		// NeedReconnectOnError covers Unavailable / Canceled / EOF / RST_STREAM —
		// all of which mean the underlying transport is gone for this stream.
		return SendErrorTransportClosing
	}
	return SendErrorOther
}

// notifySendError fires the registered observer (if any). Must be called
// without ew.mu held to avoid re-entrant deadlocks if the observer touches
// the EventWriter. Drops the notification when the lease is stale so a slow
// Send goroutine from a replaced stream cannot tear down the current owner.
func (ew *EventWriter) notifySendError(lease TargetLease, err error) {
	ew.mu.RLock()
	if ew.targetGen != lease.gen {
		ew.mu.RUnlock()
		return
	}
	fn := ew.onSendError
	ew.mu.RUnlock()
	if fn == nil {
		return
	}
	fn(classifySendError(err), err)
}

func (ew *EventWriter) Add(ev *cloudevents.Event) {
	resID := ResourceID(ev)
	ew.mu.Lock()
	defer ew.mu.Unlock()

	logCtx := ew.log.WithFields(logrus.Fields{
		"resource_id": ResourceID(ev),
		"event_id":    EventID(ev),
		"type":        ev.Type(),
	})

	defaultBackoff := wait.Backoff{
		Steps:    maxEventRetries,
		Duration: 1 * time.Second,
		Factor:   1.5,
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
//
// The lease binds this loop to a specific stream generation. When UpdateTarget
// is called the generation advances and the loop exits.
func (ew *EventWriter) SendWaitingEvents(ctx context.Context, lease TargetLease) {
	ew.mu.RLock()
	logCtx := ew.log
	ew.mu.RUnlock()

	logCtx.Info("Starting event writer")
	for {
		select {
		case <-ctx.Done():
			logCtx.Info("Shutting down event writer")
			return
		default:
			ew.mu.RLock()
			if ew.targetGen != lease.gen {
				ew.mu.RUnlock()
				logCtx.Info("Target generation advanced; stopping event writer")
				return
			}
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
				ew.sendEvent(resourceID, lease)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// sendEvent determines whether to retry a sent event or send a new unsent event
func (ew *EventWriter) sendEvent(resID string, lease TargetLease) {
	// Check if there's a sent event awaiting retry
	ew.mu.RLock()
	sentMsg, hasSent := ew.sentEvents[resID]
	ew.mu.RUnlock()

	if hasSent {
		ew.retrySentEvent(resID, sentMsg, lease)
	} else {
		ew.sendUnsentEvent(resID, lease)
	}
}

// retrySentEvent handles retrying an event that was already sent but not yet acknowledged.
//
// Lock order invariant: ew.mu must be acquired before sentMsg.mu, never the
// reverse — UpdateTarget holds ew.mu while ranging over sentEvents and
// locking each msg.mu. Snapshot (stream, gen) under ew.mu.RLock before
// touching sentMsg.mu to preserve that order.
func (ew *EventWriter) retrySentEvent(resID string, sentMsg *eventMessage, lease TargetLease) {
	ew.mu.RLock()
	logCtx := ew.log.WithFields(logrus.Fields{
		"method":      "retrySentEvent",
		"resource_id": resID,
	})
	// Re-verify the event is still in sentEvents
	currentSent, stillExists := ew.sentEvents[resID]
	stream := ew.target
	currentGen := ew.targetGen
	ew.mu.RUnlock()

	// If event was ACK'd between check and use, skip retry
	if !stillExists || currentSent != sentMsg {
		return
	}
	if currentGen != lease.gen {
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
		"retry_count":  sentMsg.retryCount,
	})

	// Check if we've exhausted retries
	if sentMsg.retryCount >= maxEventRetries {
		logCtx.Warnf("Event failed after %d retries, giving up to unblock queue", sentMsg.retryCount)
		sentMsg.mu.Unlock()
		// Remove from sentEvents to unblock the queue. ew.mu is taken
		// AFTER sentMsg.mu was released, preserving lock order.
		ew.mu.Lock()
		delete(ew.sentEvents, resID)
		ew.mu.Unlock()
		return
	}

	logCtx.Trace("resending an event")

	// Increment retry count and schedule next retry BEFORE attempting send
	// This ensures proper backoff even when the send fails.
	sentMsg.retryCount++
	retryAfter := time.Now().Add(sentMsg.backoff.Step())
	sentMsg.retryAfter = &retryAfter

	// Resend the event
	pev, err := format.ToProto(sentMsg.event)
	sentMsg.mu.Unlock()

	if err != nil {
		logCtx.Errorf("Could not wire event: %v\n", err)
		return
	}

	err = stream.Send(&eventstreamapi.Event{Event: pev})
	if err != nil {
		logCtx.Errorf("Error while sending: %v\n", err)
		ew.notifySendError(lease, err)
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
func (ew *EventWriter) sendUnsentEvent(resID string, lease TargetLease) {
	ew.mu.Lock()
	logCtx := ew.log.WithFields(logrus.Fields{
		"method":      "sendUnsentEvent",
		"resource_id": resID,
	})

	if ew.targetGen != lease.gen {
		ew.mu.Unlock()
		return
	}

	// Pop event from unsent queue and atomically move to sent tracker
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

	isFireAndForget := Target(eventMsg.event) == TargetEventAck || Target(eventMsg.event) == TargetHeartbeat

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

	if ew.testHookAfterUnlock != nil {
		ew.testHookAfterUnlock()
	}

	// Send the event
	eventMsg.mu.Lock()
	logCtx = logCtx.WithFields(logrus.Fields{
		"event_id":     EventID(eventMsg.event),
		"event_target": eventMsg.event.DataSchema(),
		"event_type":   eventMsg.event.Type(),
	})

	if !isFireAndForget {
		SetSentAt(eventMsg.event)
	}

	pev, err := format.ToProto(eventMsg.event)
	eventMsg.mu.Unlock()

	if err != nil {
		logCtx.Errorf("Could not wire event: %v\n", err)
		return
	}

	// A Send() on the stream is actually not blocking. If a successor took
	// over the stream while we were preparing this event, snapshotTargetForGen
	// returns false and we re-enqueue fire-and-forget events rather than drop them.
	stream, ok := ew.snapshotTargetForGen(lease)
	if !ok {
		// Gen advanced between pop and here. Non-fire-and-forget events are
		// safe: they landed in sentEvents above and the new owner retries them.
		// Fire-and-forget events (ACKs, heartbeats) have no such safety net;
		// push back to the front of the unsent queue for the new owner.
		if isFireAndForget {
			ew.mu.Lock()
			if ew.unsentEvents[resID] == nil {
				ew.unsentEvents[resID] = newEventQueue()
			}
			ew.unsentEvents[resID].pushFront(eventMsg)
			ew.mu.Unlock()
		}
		return
	}
	err = stream.Send(&eventstreamapi.Event{Event: pev})
	if err != nil {
		logCtx.Errorf("Error while sending: %v\n", err)
		ew.notifySendError(lease, err)
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

// pushFront prepends an item to the front of the queue without dedup.
// Used to restore a popped fire-and-forget event when the target gen changed.
func (eq *eventQueue) pushFront(ev *eventMessage) {
	eq.mu.Lock()
	defer eq.mu.Unlock()
	eq.items = append([]*eventMessage{ev}, eq.items...)
}
