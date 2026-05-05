package agent

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// JGW-TODO: Add a new logger for these log messages via cmd/cmdutil/log's ParseLogLevels et al

// JGW-TODO: Document the algorithm at the top of this file
// - the informer should never be blocked
// - at most one runner for a given resource instance at a time
// - if there is also a runner running, we should not start a new one, instead we should keep waiting.

// Opportunity window: If the opportunity window is open, it means any update events received will be processed immediately. If the opportunity window is closed, this means an update event was previously processed within the last X seconds (e.g. 10), and we are now waiting up to X seconds for the next opportunity to process that update event.

// - As the SharedInformer docs note:  "For a given SharedInformer and client, the notifications are delivered sequentially.  For a given SharedInformer, client, and object ID, the notifications are delivered in order."

const (
	// informerEventBufferLogResourceID is the structured logging key name containing resource id of the k8s object being processed
	informerEventBufferLogResourceID = "resourceID"
)

// JGW-TODO: Add comment

type informerEventBuffer[T runtime.Object] struct {

	// instanceMap maintains the state for each instance (e.g. individual k8s object)
	// key: namespace/name
	// value: current state for the given resource instance
	// - acquire lock before accessing instanceMap
	// - acquire bufferedResourceState.fieldLock before accessing fields of child objects
	instanceMap map[string]*bufferedResourceState[T]

	// lock should be acquired before reading/writing 'instanceMap'.
	// - This lock should be lightweight, and never acquired for e.g. I/O operations
	lock sync.Mutex

	// the following constants are immutable once set (lock ownership is thus not required)
	addHandlerFnToCall          func(obj T)
	updateHandlerFnToCall       func(oldObj T, newObj T)
	deleteHandlerFnToCall       func(obj T)
	minTimeBetweenUpdateFnCalls time.Duration
}

func newInformerEventBuffer[T runtime.Object](addFnToCall func(obj T), updateFnToCall func(oldObj T, newObj T), deleteFnToCall func(obj T), minTimeBetweenFnCalls time.Duration) *informerEventBuffer[T] {

	res := &informerEventBuffer[T]{
		instanceMap:                 make(map[string]*bufferedResourceState[T]),
		addHandlerFnToCall:          addFnToCall,
		updateHandlerFnToCall:       updateFnToCall,
		deleteHandlerFnToCall:       deleteFnToCall,
		minTimeBetweenUpdateFnCalls: minTimeBetweenFnCalls,
	}

	logEntry := res.logEntry("") // "" as resourceID not known at this point

	logEntry.Debugf("Started informerEventBuffer with minTimeBetweenFnCalls: '%s'", minTimeBetweenFnCalls.String())

	// Start the goroutine for scheduling function calls for throttled events
	go informerEventBufferEventLoop(res, logEntry)

	return res
}

// When we receive an resource update event from K8s informer: if we are not able to process that update event immediately (e.g. because we previously processed another update event too recently), then the values from that update event will be stored in this struct until it is time to process them.
// - Ensure 'lock' is owned in 'informerEventBuffer' before accessing the fields in this struct
// - Note: only objects from update calls are stored here: add and delete informer events are never buffered, and thus not stored. (However, runnerLock still must be acquired when running them)
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
	fieldLock sync.Mutex

	// runnerLock:
	// - Should be acquired when the an informer handler function is running, and release it when the function has completed. This includes add/delete informer events.
	// - DO NOT acquire runnerLock if fieldLock is already owned. If you need to own runnerLock, acquire it first.
	runnerLock sync.Mutex
}

func (buffer *informerEventBuffer[T]) receiveAddInformerEvent(objParam T) {

	// Deep-copy so we never retain or mutate the informer cache object.
	copied := objParam.DeepCopyObject()
	tcopy, ok := copied.(T)
	if !ok {
		buffer.logEntry("").Error("unable to cast object")
		return
	}

	resourceID, err := objectResourceMapKey(copied)
	if err != nil {
		buffer.logEntry("").Errorf("unable to extract resource ID from object: %v", err)
		return
	}

	logEntry := buffer.logEntry(resourceID)
	logEntry.Debugf("received addInformerEvent")

	// Retrieve bufferedResourceState from map
	var resourceState *bufferedResourceState[T]
	{
		var exists bool
		buffer.lock.Lock()
		resourceState, exists = buffer.instanceMap[resourceID]

		if !exists {
			logEntry.Debug("first time seeing event for resource")

			// We have not sent an event for this resource previously, add it to the map

			resourceState = &bufferedResourceState[T]{
				oldObjectForUpdateFunc: *new(T), // empty
				newObjectForUpdateFunc: *new(T), // empty
			}
			buffer.instanceMap[resourceID] = resourceState
		}

		buffer.lock.Unlock()
	}

	// Acquire the lock to ensure there is not another runner running.
	// - For 'add' events, we block the informer until the add event is processed.
	{
		resourceState.runnerLock.Lock()
		defer resourceState.runnerLock.Unlock()

		// We have received an add event for the resource, so any previous update events are now stale: we clear them here.
		{
			resourceState.fieldLock.Lock()

			resourceState.oldObjectForUpdateFunc = *new(T) // reset to nil
			resourceState.newObjectForUpdateFunc = *new(T) // reset to nil
			resourceState.nextOpportunityToCallUpdateFn = time.Time{}

			resourceState.fieldLock.Unlock()
		}

		// Call the function with the object
		logEntry.Debug("calling addHandlerFnToCall")
		buffer.addHandlerFnToCall(tcopy)
		logEntry.Debug("done calling addHandlerFnToCall")
	}

}

func (buffer *informerEventBuffer[T]) receiveUpdateInformerEvent(oldParam T, newParam T) {

	// Deep-copy so we never retain or mutate the informer cache object.
	oldCopied := oldParam.DeepCopyObject()
	old, ok := oldCopied.(T)
	if !ok {
		buffer.logEntry("").Error("unable to cast 'old' object")
		return
	}

	newCopied := newParam.DeepCopyObject()
	new, ok := newCopied.(T)
	if !ok {
		buffer.logEntry("").Error("unable to cast 'new' object")
		return
	}

	resourceID, err := objectResourceMapKey(old)
	if err != nil {
		buffer.logEntry("").Errorf("unable to extract resource ID from object: %v", err)
		return
	}

	logEntry := buffer.logEntry(resourceID)

	logEntry.Debug("received updateInformerEvent")

	var resourceState *bufferedResourceState[T]
	{
		buffer.lock.Lock()
		var exists bool
		resourceState, exists = buffer.instanceMap[resourceID]

		if !exists {
			logEntry.Debug("first time seeing update event for resource")

			// We have not sent an event for this resource previously, add it to the map

			resourceState = &bufferedResourceState[T]{
				oldObjectForUpdateFunc: old,
				newObjectForUpdateFunc: new,
			}
			buffer.instanceMap[resourceID] = resourceState

		}

		buffer.lock.Unlock()
	}

	resourceState.fieldLock.Lock()

	if !objectSlotEmpty(resourceState.oldObjectForUpdateFunc) {
		logEntry.Debug("replaced old update object value with new value")
	}
	// Replace whatever value was previously present (which is now stale) with latest received version of the objects
	resourceState.oldObjectForUpdateFunc = old
	resourceState.newObjectForUpdateFunc = new

	// isOpportunityWindowOpen := time.Now().After(resourceState.nextOpportunityToCallUpdateFn)
	resourceState.fieldLock.Unlock()

	// if isOpportunityWindowOpen {

	// If the opportunity window has opened (more than X amount of time has elapsed since we sent the last message, where X is minTimeBetweenSends)
	// logEntry.Debug("opportunity window is open")

	// logging.GetDefaultLogger().
	buffer.attemptToCallFuncAndResetOpportunityWindow(resourceState, logEntry)

	// } else {
	// 	// The opportunity window has not yet opened, so we merely return until it is time: the go-routine will ensure it is scheduled appropriately.
	// 	logEntry.Debugf("opportunity window is not yet open: %v", resourceState.nextOpportunityToCallUpdateFn)
	// }

}

func (buffer *informerEventBuffer[T]) receiveDeleteInformerEvent(objParam T) {
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

	logEntry := buffer.logEntry(resourceID)
	logEntry.Debugf("received deleteInformerEvent")

	// Retrieve bufferedResourceState from map
	var resourceState *bufferedResourceState[T]
	{
		buffer.lock.Lock()
		var exists bool
		resourceState, exists = buffer.instanceMap[resourceID]

		if !exists {
			logEntry.Debug("first time seeing event for resource")

			// We have not sent an event for this resource previously, add it to the map

			resourceState = &bufferedResourceState[T]{
				oldObjectForUpdateFunc: *new(T), // empty
				newObjectForUpdateFunc: *new(T), // empty
			}
			buffer.instanceMap[resourceID] = resourceState
		}

		buffer.lock.Unlock()
	}

	// Acquire the runner lock to ensure there is not another runner running.
	// - For 'delete' events, we block the informer until the delete event is processed.
	{
		resourceState.runnerLock.Lock()
		defer resourceState.runnerLock.Unlock()

		buffer.lock.Lock()
		delete(buffer.instanceMap, resourceID)
		buffer.lock.Unlock()

		// {
		// 	// Acquire the lock to ensure there is not another running:
		// 	resourceState.fieldLock.Lock()

		// 	// We have received a delete event for the resource, so any previous update events are now stale: we clear them here.
		// 	resourceState.oldObjectForUpdateFunc = *new(T) // reset to nil
		// 	resourceState.newObjectForUpdateFunc = *new(T) // reset to nil
		// 	resourceState.nextOpportunityToCallUpdateFn = time.Time{}

		// 	resourceState.fieldLock.Unlock()
		// }

		logEntry.Debugf("calling delete function from go-routine") // JGW-TODO

		// Call the function with the object

		logEntry.Debug("calling deleteHandlerFnToCall")
		buffer.deleteHandlerFnToCall(tObj)
		logEntry.Debug("done calling deleteHandlerFnToCall")

	}

}

// informerEventBufferEventLoop runs in the background and waits for an opportunity window to open for resources that have a waiting update event. When the window opens, the update event is sent for processing.
func informerEventBufferEventLoop[T runtime.Object](buffer *informerEventBuffer[T], logEntry *logrus.Entry) {

	for {

		// First we retrieve the resources states for any resources with an update event waiting
		resourceStates := []*bufferedResourceState[T]{}
		{
			buffer.lock.Lock()

			for _, resourceState := range buffer.instanceMap {

				if objectSlotEmpty(resourceState.oldObjectForUpdateFunc) {
					// No objects waiting? No work to do.
					continue
				}

				resourceStates = append(resourceStates, resourceState)
			}
			buffer.lock.Unlock()
		}

		// Next we attempt to process the waiting update events for any resources whose opportunity window is open
		for idx := range resourceStates {
			resourceState := resourceStates[idx]

			resourceState.fieldLock.Lock()

			resourceID, err := objectResourceMapKey(resourceState.oldObjectForUpdateFunc)
			resourceState.fieldLock.Unlock()

			if err != nil {
				buffer.logEntry("").Errorf("unable to extract resource ID from object: %v", err)
				continue
			}

			logEntry := logEntry.WithField(informerEventBufferLogResourceID, resourceID)

			// logEntry.Debug("calling update function handler from go-routine")
			buffer.attemptToCallFuncAndResetOpportunityWindow(resourceState, logEntry)
		}

		// JGW-TODO: Rather than a go routine that is always running and a time.Sleep(...), we could instead keep a timer channel that corresponds to the next most recent scheduled event, for each resource
		time.Sleep(time.Millisecond * 100)
	}
}

// no locks should be owned when calling: the function will acquire the locks it needs
func (buffer *informerEventBuffer[T]) attemptToCallFuncAndResetOpportunityWindow(resourceState *bufferedResourceState[T], logEntry *logrus.Entry) {

	// Attempt to acquire the runner lock; if we can't, it means we are already processing this resource in another goroutine.
	runnerLockAcquired := resourceState.runnerLock.TryLock()
	if !runnerLockAcquired {
		// If we can't acquire: return to try again later

		// logEntry.Debugf("unable to acquire handler func runner lock, will try again")

		// resourceState.fieldLock.Lock()
		// if time.Now().After(resourceState.nextOpportunityToCallUpdateFn) {
		// 	resourceState.nextOpportunityToCallUpdateFn = time.Now().Add(time.Second)
		// }
		// resourceState.fieldLock.Unlock()
		return
	}

	// From this point, we've acquired the runner lock

	// Determine if there is still work to be done, if yes, do it
	var oldObjectToSend T
	var newObjectToSend T
	{
		// Lock to retrieve the current state of the resourceState: it may have been modified by another thread.
		resourceState.fieldLock.Lock()

		if objectSlotEmpty(resourceState.oldObjectForUpdateFunc) {
			resourceState.fieldLock.Unlock()
			resourceState.runnerLock.Unlock()
			// After we acquired the lock, we discovered no object is waiting, so just return.
			return
		}

		isOpportunityWindowOpen := time.Now().After(resourceState.nextOpportunityToCallUpdateFn)
		if !isOpportunityWindowOpen {
			resourceState.fieldLock.Unlock()
			resourceState.runnerLock.Unlock()
			// The opportunity window has not yet opened, so we merely return until it is time: the go-routine will ensure it is scheduled appropriately.
			// logEntry.Debugf("opportunity window is not yet open: %v", resourceState.nextOpportunityToCallUpdateFn)
			return
		}

		// If the opportunity window has opened (more than X amount of time has elapsed since we sent the last message, where X is minTimeBetweenSends), we can process the event

		oldObjectToSend = resourceState.oldObjectForUpdateFunc
		newObjectToSend = resourceState.newObjectForUpdateFunc

		// Since we are handling the current value, clear it from the state
		resourceState.oldObjectForUpdateFunc = *new(T) // reset to nil
		resourceState.newObjectForUpdateFunc = *new(T) // reset to nil
		resourceState.nextOpportunityToCallUpdateFn = time.Now().Add(buffer.minTimeBetweenUpdateFnCalls)
		logEntry.Debugf("nextOpportunityToCallUpdateFn updated to '%v'", resourceState.nextOpportunityToCallUpdateFn)

		resourceState.fieldLock.Unlock()
	}

	// We now start a new goroutine to perform the actual function call (so as not to block the calling function)
	// Note that we transfer ownership of the runner lock to the goroutine.
	go func() {
		defer resourceState.runnerLock.Unlock()
		logEntry.Debug("calling updateFnToCall")
		buffer.updateHandlerFnToCall(oldObjectToSend, newObjectToSend)
		logEntry.Debug("done calling updateFnToCall")
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
	// fmt.Sprintf("%s", nn.String()), nil
	// return fmt.Sprintf("%s_%s", nn.String(), string(acc.GetUID())), nil
}

func (buffer *informerEventBuffer[T]) logEntry(resourceID string) *logrus.Entry {

	res := log().WithField(logfields.Component, "informerEventBuffer")

	if resourceID != "" {
		res = res.WithField(informerEventBufferLogResourceID, resourceID)
	}

	return res
}
