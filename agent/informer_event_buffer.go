package agent

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/clock"
	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

const (
	// informerEventBufferLogResourceID is the structured logging key name containing resource id of the k8s object being processed
	informerEventBufferLogResourceID = "resourceID"
)

// informerEventBuffer is an optional, configurable 'informer event buffer' layer that sits between the K8s informer and the Argo CD agent `Application` resource informers event handlers.
// - It only buffers and de-duplicates Application update events (e.g. not add/delete)
// - Does not touch other resource types (AppProject, etc)
//
// This new layer ensures that in an X second period (for example, 10 second), the Argo CD agent 'update' event handler functions will be called at most once every X seconds.
//
// This is an example of how the algorithm works:
// - @0 seconds) Application informer informs us of an Application Event (for example, .status change)
//   - Agent's Application update informer handler is immediately called. (Since we have not called the informer handler within the last 10 seconds)
//   - We set the 'next available opportunity to send event' window to @10 seconds.
//
// - @1 second) Application informer informs us of another Application Event
//   - We DON'T call the Application informer handler.
//   - We instead put this event at the top of the buffer.
//
// - @6 seconds) Application informer informs us of another Appliation Event
//   - Same as above, we don't call the Appliation infomer handler.
//   - This event replaces the event from @1 second in the buffer (since the one at @1 second is now stale).
//
// - @10 seconds) We detect that the 10 seconds has elaped, and send the message from @6 seconds.
// - @20 seconds) Application informer informs us of an Application Event (for example, .status change)
//   - Since we have not seen any events for the last 10 seconds, we call Application informer handler immediately.
//
// This ensures that principal (downstream for Application .status update) is not overloaded by large numbers of Application .status changes at once. For example, as can occur on initial deploy of an Application, or less commonly, when Argo CD has been misconfigured such that it is battling another controller for ownership of a resource.
// The semantics of this new layer preserve the same semantics as SharedInformer:
//
// - As the SharedInformer docs note: "For a given SharedInformer and client, the notifications are delivered sequentially. For a given SharedInformer, client, and object ID, the notifications are delivered in order."
// - When informer calls us, we return quickly (the PR implementation is always equal to or faster than current implementation)
// - Each Application instance will be processed by at most 1 goroutine (e.g. no concurrent processing of different events from same Application entity)
// - Resources ordering same as SharedInformer: creates will be processed first, then updates (in chronological order, never concurrently), then delete.
//   - BUT: If we see a 'delete', it will short circuit any remaining updates that are still to process.
//
// Opportunity window: If the opportunity window is open, it means any update events received will be processed immediately. If the opportunity window is closed, this means an update event was previously processed within the last X seconds (e.g. 10), and we are now waiting up to X seconds for the next opportunity to process that update event.
type informerEventBuffer[T runtime.Object] struct {

	// instanceMap maintains the state for each instance (e.g. individual k8s object)
	// key: namespace/name
	// value: current state for the given resource instance
	// - acquire lock before accessing instanceMap
	// - acquire bufferedResourceState.fieldLock before accessing fields of child objects
	instanceMap map[string]*bufferedResourceState[T]

	// lock should be acquired before reading/writing 'instanceMap'.
	// - This lock should be lightweight, and never be acquired while performing e.g. I/O operations
	lock sync.Mutex

	// -----
	// all of the following constants are immutable once set (lock ownership is thus not required):

	// addHandlerFnToCall is pointer to function which handles 'add' events from informer
	addHandlerFnToCall func(obj T)
	// updateHandlerFnToCall is pointer to function which handles 'update' events from informer
	updateHandlerFnToCall func(oldObj T, newObj T)
	// deleteHandlerFnToCall is pointer to function which handles 'delete' events from informer
	deleteHandlerFnToCall func(obj T)

	// minTimeBetweenUpdateFnCalls is the minimum amount of time to wait after a previous informer update event is processed before another one is processed. See detailed description of this behaviour, above.
	minTimeBetweenUpdateFnCalls time.Duration

	// clock abstracts time operations so tests can control time without sleeping
	clock clock.Clock

	// eventLoopInterval is the polling interval of the background event loop goroutine
	eventLoopInterval time.Duration

	// logFn is the base logging function, but code within this file should instead call 'informerEventBuffer.logEntry(...)'
	logFn func() *logrus.Entry
}

// newInformerEventBuffer creates a new informerEventBuffer. Only a single informerEventBuffer object will exist (e.g. for a single agent OS process).
// eventLoopInterval controls the polling interval of the background event loop goroutine.
func newInformerEventBuffer[T runtime.Object](addFnToCall func(obj T), updateFnToCall func(oldObj T, newObj T), deleteFnToCall func(obj T), minTimeBetweenFnCalls time.Duration, clk clock.Clock, eventLoopInterval time.Duration, logFn func() *logrus.Entry) *informerEventBuffer[T] {

	res := &informerEventBuffer[T]{
		instanceMap:                 make(map[string]*bufferedResourceState[T]),
		addHandlerFnToCall:          addFnToCall,
		updateHandlerFnToCall:       updateFnToCall,
		deleteHandlerFnToCall:       deleteFnToCall,
		minTimeBetweenUpdateFnCalls: minTimeBetweenFnCalls,
		clock:                       clk,
		eventLoopInterval:           eventLoopInterval,
		logFn:                       logFn,
	}

	logEntry := res.logEntry("") // "" as resourceID not known at this point

	logEntry.Debugf("Started informerEventBuffer with minTimeBetweenFnCalls: '%s'", minTimeBetweenFnCalls.String())

	// Start the goroutine for scheduling function calls for throttled events
	// - a single instance of this goroutine exists per agent (os process)
	go informerEventBufferEventLoop(res, logEntry)

	return res
}

// When we receive a resource update event from K8s informer: if we are not able to process that update event immediately (e.g. because we previously processed another update event too recently), then the values from that update event will be stored in this struct until it is time to process them.
// - Ensure 'lock' is owned in 'informerEventBuffer' before accessing the fields in this struct
// - Note: only objects from update calls are stored here: add and delete informer events are never buffered, and thus not stored. (However, runnerLock still must be acquired when running add/update/delete, to prevent overlap between update and add/delete)
type bufferedResourceState[T runtime.Object] struct {

	// These objects are the objects that are waiting to be passed to update handler function. Variables may be nil (empty) if no object is currently waiting.
	// Note: only objects from update calls are stored in the fields: add and delete informer events are never buffered.
	oldObjectForUpdateFunc T
	newObjectForUpdateFunc T

	// nextOpportunityToCallUpdateFn is the next point in time at which we are free to call the update callback fn.
	// - This will always be ('time since update function was last called' + 'minTimeBetweenFnCalls'), except for the case where the function has not been previously called (which will instead be instantaneous).
	nextOpportunityToCallUpdateFn time.Time

	// lock should be acquired:
	// - When reading/writing any of the above fields
	// - This lock should be lightweight, and never be acquired while performing e.g. I/O operations (that is what runnerLock is for)
	// - No new locks should be acquired while the lock is owned. (It's fine to own other locks FIRST, then acquire fieldLock (we do this often); but it's not fine to acquire fieldLock, THEN attempt to acquire another lock)
	fieldLock sync.Mutex

	// runnerLock:
	// - Should be acquired when the an informer handler function is running, and release it when the function has completed. This includes add/delete informer events.
	// - DO NOT acquire runnerLock if fieldLock is already owned. If you need to own runnerLock, acquire it first.
	runnerLock sync.Mutex
}

// receiveAddInformerEvent is called by informer add handler when an 'add' event is seen for a K8s object
func (buffer *informerEventBuffer[T]) receiveAddInformerEvent(objParam T) {

	buffer.handleAddOrDeleteInformerEvent(true /* (true for 'add')*/, objParam)

}

// receiveUpdateInformerEvent is called by informer update handler when an 'update' event is seen for a K8s object
func (buffer *informerEventBuffer[T]) receiveUpdateInformerEvent(oldParam T, newParam T) {

	// Deep-copy so we never retain or mutate the informer cache object.
	oldCopied := oldParam.DeepCopyObject()
	oldObj, ok := oldCopied.(T)
	if !ok {
		buffer.logEntry("").Error("unable to cast 'old' object")
		return
	}

	newCopied := newParam.DeepCopyObject()
	newObj, ok := newCopied.(T)
	if !ok {
		buffer.logEntry("").Error("unable to cast 'new' object")
		return
	}

	resourceID, err := objectResourceMapKey(oldObj)
	if err != nil {
		buffer.logEntry("").Errorf("unable to extract resource ID from object: %v", err)
		return
	}

	logEntry := buffer.logEntry(resourceID)

	logEntry.Trace("received updateInformerEvent")

	resourceStateCreated := false

	// Get (or initialize) current resource state from map
	var resourceState *bufferedResourceState[T]
	{
		buffer.lock.Lock()
		var exists bool
		resourceState, exists = buffer.instanceMap[resourceID]

		if !exists {

			resourceState = &bufferedResourceState[T]{
				oldObjectForUpdateFunc: oldObj,
				newObjectForUpdateFunc: newObj,
			}
			buffer.instanceMap[resourceID] = resourceState
			resourceStateCreated = true
		}

		buffer.lock.Unlock()
	}

	if !resourceStateCreated {
		resourceState.fieldLock.Lock()

		if !objectSlotEmpty(resourceState.oldObjectForUpdateFunc) {
			logEntry.Trace("replaced old update object value with new value")
		}

		// Replace whatever value was previously present (which is now stale) with latest received version of the object
		resourceState.oldObjectForUpdateFunc = oldObj
		resourceState.newObjectForUpdateFunc = newObj

		resourceState.fieldLock.Unlock()
	}

	logEntry = logEntry.WithField("calledFrom", "receiveUpdateInformerEvent")

	// Unlike with add/delete events (where we block until runner lock is acquired), for update events we only ATTEMPT to acquire runner lock: for update, if the runner lock could not be acquired, we exit and instead rely on the event loop to process the event at next opportunity.
	// - We do this only for update, because only update events are buffered (by the logic in this file)
	// - Create/delete events are infrequent (since they are lifecycle events, rather than state update events), so buffering them is less useful (and thus not implemented)
	buffer.attemptToCallUpdateFuncAndResetOpportunityWindow(resourceState, logEntry)

}

// receiveDeleteInformerEvent is called by informer delete handler when an 'delete' event is seen for a K8s object
func (buffer *informerEventBuffer[T]) receiveDeleteInformerEvent(objParam T) {
	buffer.handleAddOrDeleteInformerEvent(false /* 'false' for delete*/, objParam)
}

// handleAddOrDeleteInformerEvent handles either add/delete informer events
// - How they are handled is exactly the same between add/delete, with the only difference being which function is called at the end (e.g. either 'buffer.addHandlerFnToCall(tObj)' for add, or 'buffer.deleteHandlerFnToCall(tObj)' for delete), plus log statement differences
// - This function will never be called while 'receiveUpdateInformerEvent' is running on another thread (and vice versa). This is guaranteed by informer. (BUT, note that the update logic can still be running if 'attemptToCallUpdateFuncAndResetOpportunityWindow' owns runner lock)
func (buffer *informerEventBuffer[T]) handleAddOrDeleteInformerEvent(isAdd bool, objParam T) {
	// Deep-copy so we never retain or mutate the informer cache object.
	copied := objParam.DeepCopyObject()

	tObj, ok := copied.(T)
	if !ok {
		buffer.logEntry("").Error("unable to cast object")
		return
	}

	resourceID, err := objectResourceMapKey(tObj)
	if err != nil {
		buffer.logEntry("").Errorf("unable to extract resource ID from object: %v", err)
		return
	}

	operationNameForLog := "add" // string is for logging purposes only
	if !isAdd {
		operationNameForLog = "delete"
	}

	logEntry := buffer.logEntry(resourceID)
	logEntry.Tracef("received %sInformerEvent", operationNameForLog)

	// Retrieve (or initialize to empty) bufferedResourceState from map
	var resourceState *bufferedResourceState[T]
	buffer.lock.Lock()
	resourceState = buffer.instanceMap[resourceID]
	buffer.lock.Unlock()

	if resourceState != nil {

		// Acquire the runner lock to ensure there is not an update runner running.
		// - For 'add/delete' events, we block the informer until the add/delete event is processed (rather than using 'tryLock' like with update)
		resourceState.runnerLock.Lock()
		defer resourceState.runnerLock.Unlock()

		// Invalidate the resourceState, since it is necessarily stale.
		// - This also helps ensure that, in informerEventBufferEventLoop, if there are any bufferedResourceState in the thread local slice, that they are not processed (since we know they are now stale).
		resourceState.fieldLock.Lock()
		resourceState.clearToEmpty()
		resourceState.fieldLock.Unlock()

	}
	// Otherwise, if resourceState is nil: No resourceState to lock on, and update informer function can't be called while this function is running (as per informer logic), so no need to create runner lock or hold it

	logEntry.Tracef("calling %sHandlerFnToCall", operationNameForLog)
	if isAdd {
		buffer.addHandlerFnToCall(tObj)
	} else {
		buffer.deleteHandlerFnToCall(tObj)
	}

	logEntry.Tracef("done calling %sHandlerFnToCall", operationNameForLog)

	// Since:
	// - Within this function, we can be sure there are no newer update informer events waiting (since informer guarantees that calls are not concurrent for a given resource)
	// - We are sure that no update events are waiting in the go rountine event loop, because we cleared them above.
	// - no update runners are running,
	//  We can thus safely delete the entry from the map
	buffer.lock.Lock()
	delete(buffer.instanceMap, resourceID)
	buffer.lock.Unlock()

}

// informerEventBufferEventLoop runs in the background and waits for an opportunity window to open for resources that have a waiting update event. When the window opens, the update event is sent for processing.
// - There will only exist a single instance of this goroutine/function (e.g. for a single agent OS process)
func informerEventBufferEventLoop[T runtime.Object](buffer *informerEventBuffer[T], logEntry *logrus.Entry) {

	for {

		// First we retrieve the resources states for any resources with an update event waiting
		resourceStatesWithWaitingEvents := []*bufferedResourceState[T]{}
		{

			buffer.lock.Lock()

			for _, resourceState := range buffer.instanceMap {

				resourceState.fieldLock.Lock()
				objectSlotIsEmpty := objectSlotEmpty(resourceState.oldObjectForUpdateFunc)
				resourceState.fieldLock.Unlock()

				if objectSlotIsEmpty {
					// No objects waiting? No work to do.

					// Note: You may be tempted to 'delete(buffer.instanceMap, mapKey)' here. It's not necessary, and significantly increases the complexity of the logic in this file.

					continue
				}

				resourceStatesWithWaitingEvents = append(resourceStatesWithWaitingEvents, resourceState)
			}

			buffer.lock.Unlock()
		}

		// Next, we attempt to process our thread local copy of the waiting update events for any resources whose opportunity window is open
		for idx := range resourceStatesWithWaitingEvents {
			resourceState := resourceStatesWithWaitingEvents[idx]

			resourceState.fieldLock.Lock()

			if objectSlotEmpty(resourceState.oldObjectForUpdateFunc) {
				// If object was emptied (for example, due to a create/delete event on the same k8s object that was processed) after we retrieved it from map, then no work is needed.
				resourceState.fieldLock.Unlock()
				continue
			}

			// Retrieve the resourceID so we can add it to the log context
			resourceID, err := objectResourceMapKey(resourceState.oldObjectForUpdateFunc)
			resourceState.fieldLock.Unlock()
			if err != nil {
				buffer.logEntry("").Errorf("unable to extract resource ID from object: %v", err)
				continue
			}
			logEntry := logEntry.WithField(informerEventBufferLogResourceID, resourceID).WithField("calledFrom", "informerEventBufferEventLoop")

			// Finally, (attempt) to call the update function
			buffer.attemptToCallUpdateFuncAndResetOpportunityWindow(resourceState, logEntry)
		}

		// At present we are spacing out processing of the waiting update events by 100ms, however, a useful future enhancement might be:
		// - Rather than a go routine that is always running and a time.Sleep(...) periodically, we could instead keep a timer that corresponds to the next most recent scheduled event, for each resource.
		time.Sleep(buffer.eventLoopInterval)
	}
}

// attemptToCallUpdateFuncAndResetOpportunityWindow will acquire the required locks, remove the object from queue, then call update informer function on that object
// Note:
// - no locks should be owned when calling: the function will acquire the locks it needs
func (buffer *informerEventBuffer[T]) attemptToCallUpdateFuncAndResetOpportunityWindow(resourceState *bufferedResourceState[T], logEntry *logrus.Entry) {

	// Attempt to acquire the runner lock; if we can't, it means we are already processing this resource in another goroutine.
	// - We acquire it first, because it is mostly likely of the locks to be contended. If we can't acquire it, there is no point in doing any other work (or acquiring any another locks).
	runnerLockAcquired := resourceState.runnerLock.TryLock()
	if !runnerLockAcquired {
		// If we can't acquire: return to try again later via event loop (or triggered via another update event)
		return
	}

	// From this point, we've acquired the runner lock ------
	// This ensures that:
	// - no delete/add event handler functions are being called for this application namespace/name
	// - no other update handler functions are being called for this application namespace/name

	// Next, we will determine if there is still work to be done, if yes, do it on a separate goroutine (so as not to block the event loop)

	var oldObjectToSend T
	var newObjectToSend T
	{
		// Lock to retrieve the current state of the resourceState: it may have been modified by another thread.
		resourceState.fieldLock.Lock()

		if objectSlotEmpty(resourceState.oldObjectForUpdateFunc) {
			// After we acquired the lock, we discovered no object is waiting, so just return.
			resourceState.fieldLock.Unlock()
			resourceState.runnerLock.Unlock()
			return
		}

		isOpportunityWindowOpen := buffer.clock.Now().After(resourceState.nextOpportunityToCallUpdateFn)
		if !isOpportunityWindowOpen {
			// We have an object, but the opportunity window has not yet opened, so we merely return until it is time: the go-routine will ensure it is scheduled appropriately.
			resourceState.fieldLock.Unlock()
			resourceState.runnerLock.Unlock()
			return
		}

		// If the opportunity window has opened (more than X amount of time has elapsed since we sent the last message, where X is minTimeBetweenSends), we can process the event

		// Make a local copy of the update func values, then clear them from the queue

		oldObjectToSend = resourceState.oldObjectForUpdateFunc
		newObjectToSend = resourceState.newObjectForUpdateFunc

		resourceState.clearToEmpty()
		resourceState.nextOpportunityToCallUpdateFn = buffer.clock.Now().Add(buffer.minTimeBetweenUpdateFnCalls)
		logEntry.Tracef("nextOpportunityToCallUpdateFn updated to '%v'", resourceState.nextOpportunityToCallUpdateFn)

		resourceState.fieldLock.Unlock()
	}

	// We now start a new goroutine to perform the actual function call (so as not to block the calling function)
	// - Note that we transfer ownership of the runner lock to the goroutine.
	go func() {
		defer resourceState.runnerLock.Unlock()
		defer func() {
			if r := recover(); r != nil {
				logEntry.WithField("panic", r).Error("panic calling updateHandlerFnToCall")
			}
		}()

		logEntry.Trace("calling updateFnToCall")
		buffer.updateHandlerFnToCall(oldObjectToSend, newObjectToSend)
		logEntry.Trace("done calling updateFnToCall")
	}()
}

// objectSlotEmpty reports whether a queued object should be treated as absent.
// Uses reflection so T only needs runtime.Object (pointer-backed API types are typical).
func objectSlotEmpty[T runtime.Object](v T) bool {
	return reflect.ValueOf(v).IsZero()
}

// objectResourceMapKey returns a stable key for a Kubernetes API object using its
// metadata (namespace/name). Works for any type that implements metav1.Object
// (including Application, AppProject, etc.).
// - We do not use UID, since sharedinformer does not use UID, and our aim is to match its behaviour.
func objectResourceMapKey(obj runtime.Object) (string, error) {
	acc, err := meta.Accessor(obj)
	if err != nil {
		return "", fmt.Errorf("get object metadata: %w", err)
	}

	nn := types.NamespacedName{Namespace: acc.GetNamespace(), Name: acc.GetName()}
	return nn.String(), nil
}

// logEntry will generate a *logrus.Entry customized for logging 'informer event buffer' events
// - The verbosity of log statements logged using this function can be customized independently via parameter, via e.g. '--log-level informer-event-buffer=error'
// - resourceID can be passed if it known, otherwise ""
func (buffer *informerEventBuffer[T]) logEntry(resourceID string) *logrus.Entry {

	res := buffer.logFn().WithField(logfields.Component, "informerEventBuffer")
	if resourceID != "" {
		res = res.WithField(informerEventBufferLogResourceID, resourceID)
	}

	return res
}

// clearToEmpty clears any existing objects from the bufferedResourceState
// - Ensure fieldLock is owned before calling clearToEmpty
func (brs *bufferedResourceState[T]) clearToEmpty() {
	brs.oldObjectForUpdateFunc = *new(T) // reset to nil
	brs.newObjectForUpdateFunc = *new(T) // reset to nil
	brs.nextOpportunityToCallUpdateFn = time.Time{}
}
