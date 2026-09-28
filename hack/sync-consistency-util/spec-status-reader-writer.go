package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"time"

	v1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// eventState is shared state of the application between multiple goroutines
type eventState struct {
	mutex sync.RWMutex

	// nextEventId is a monotonically increasing id for every event created within the process
	// - Acquire mutex before reading/writing
	nextEventId int

	// allEvents: actions taken (spec/status updates on source of truth) and observed (by watch, on peer)
	// - Acquire mutex before reading/writing
	allEvents []*applicationEvent

	// appNameList contains the current list of Applications that should exist on control plane (source of truth for Applications)
	// - Key is application name, value is not used.
	// - When an app is created/deleted on principal, it will be immediately added/remove from this map
	// - Acquire mutex before reading/writing
	appNameList map[string]bool
}

func run() error {

	state := &eventState{
		nextEventId: 1,
		allEvents:   make([]*applicationEvent, 0),
		appNameList: make(map[string]bool),
	}

	controlPlaneClient, err := getK8sClientByContextName("vcluster-control-plane", clientQPS, clientBurst, disableClientRateLimiter)
	if err != nil {
		return err
	}

	managedAgentClient, err := getK8sClientByContextName("vcluster-agent-managed", clientQPS, clientBurst, disableClientRateLimiter)
	if err != nil {
		return err
	}

	var deletedApps map[string]bool

	fmt.Println("* Pre-startup")
	if err := disableApplicationController(managedAgentClient); err != nil {
		return err
	}

	if deletedApps, err = deleteOldApplicationsOnStartup(controlPlaneClient, managedAgentClient); err != nil {
		return err
	}
	fmt.Println("* Startup complete.")

	fmt.Println("* Reader/writer threads started.")

	writerCtx, writerCancelFunc := context.WithCancel(context.Background())

	readerCtx := context.Background() // We currently don't need to cancel reader.

	go func() {
		time.Sleep(timeToRun)
		fmt.Println("* Cancelling context, then waiting 10 seconds")
		writerCancelFunc()
		time.Sleep(10 * time.Second)

		validateEventualConsistencyOfEventList(state, deletedApps)
		exitSuccess("eventual consistency check complete")
	}()

	go func() {
		if err := startManagedAgentSpecWatchLog(readerCtx, managedAgentClient, state); err != nil {
			fmt.Println(err)
			exit("spec watch error")
		}
	}()

	go func() {
		if err := startControlPlaneStatusWatchLog(readerCtx, controlPlaneClient, state); err != nil {
			fmt.Println(err)
			exit("control plane watch error")
		}
	}()

	go func() {
		if err := startControlPlaneSpecWriter(writerCtx, controlPlaneClient, state); err != nil {
			if writerCtx.Err() != nil {
				return
			}
			fmt.Println(err)
			exit("spec writer error")
		}
	}()

	go func() {
		if err := startManagedAgentStatusWriter(writerCtx, managedAgentClient, state); err != nil {
			if writerCtx.Err() != nil {
				return
			}
			fmt.Println(err)
			exit("status writer error")
		}
	}()

	return nil

}

// startControlPlaneSpecWriter: Each 'round', randomly create/modify/delete an application. If an action is not possible (for example, attempting to delete an application when # of applications is below minimum), skip and try again
// - supports ctx cancellation
// - unexpected error are fatal
func startControlPlaneSpecWriter(ctx context.Context, controlPlaneClient client.Client, state *eventState) error {

	for round := 0; ; round++ {

		roll := rand.IntN(100)

		switch {
		case roll < specWriterCreatePercent:

			state.mutex.RLock()
			if len(state.appNameList) > maxConcurrentApps {
				state.mutex.RUnlock()
				continue
			}
			state.mutex.RUnlock()

			// A) create a new Application
			name := fmt.Sprintf("event-generator-app-%d-%s", round, randString(24))
			app := &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "agent-managed",
				},
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL: fmt.Sprintf("https://fakegithub.com/example/%s.git", randString(64)), // This won't actually be resolved by argocd
						Path:    ".",
					},
					Destination: v1alpha1.ApplicationDestination{
						Name:      "agent-managed",
						Namespace: "default", // time.Sleep(time.Second)
					},
					Project: "default",
				},
			}

			newAppEvent := applicationEvent{
				appName:         name,
				eventType:       watch.Added,
				mode:            modeSource,
				direction:       direction_SpecWriter,
				repoURL:         app.Spec.Source.RepoURL,
				resourceVersion: app.ResourceVersion,
			}

			state.initEvent(&newAppEvent)

			if err := controlPlaneClient.Create(ctx, app); err != nil && ctx.Err() == nil {
				return fmt.Errorf("round %d: create application %q: %w", round, name, err)
			}

			newAppEvent.afterContextCancel = ctx.Err() != nil

			// We still record event even if Create returned 'Context Cancelled' error, as in some cases the error is still returned even though the actual Create succeeded

			state.mutex.Lock()
			state.appNameList[name] = true
			state.mutex.Unlock()

			state.recordEvent(newAppEvent)

		case roll < specWriterCreatePercent+specWriterUpdatePercent:

			state.mutex.RLock()
			if len(state.appNameList) == 0 {
				state.mutex.RUnlock()
				continue
			}
			name := randomKeyFromAppNameMap(state.appNameList)
			state.mutex.RUnlock()

			// B) patch (update) an existing Application

			newRepoURL := fmt.Sprintf("https://fakegithub.com/example/%s.git", randString(64))
			var app v1alpha1.Application
			if err := controlPlaneClient.Get(ctx, types.NamespacedName{Namespace: "agent-managed", Name: name}, &app); err != nil {
				return fmt.Errorf("round %d: get application %q for modify: %w", round, name, err)
			}

			patch := client.MergeFrom(app.DeepCopy())
			if app.Spec.Source == nil {
				app.Spec.Source = &v1alpha1.ApplicationSource{}
			}
			app.Spec.Source.RepoURL = newRepoURL

			newAppEvent := applicationEvent{
				appName:         app.Name,
				eventType:       watch.Modified,
				mode:            modeSource,
				direction:       direction_SpecWriter,
				repoURL:         app.Spec.Source.RepoURL,
				resourceVersion: app.ResourceVersion,
			}
			state.initEvent(&newAppEvent)

			if err := controlPlaneClient.Patch(ctx, &app, patch); err != nil && ctx.Err() == nil {
				return fmt.Errorf("round %d: patch application %q: %w", round, name, err)
			}

			newAppEvent.afterContextCancel = ctx.Err() != nil

			// We still record event even if Patch returned 'Context Cancelled' error, as in some cases the error is still returned even though the actual Patch succeeded

			state.recordEvent(newAppEvent)

		default:

			state.mutex.Lock()
			if len(state.appNameList) < minConcurrentAppsToCreate {
				state.mutex.Unlock()
				continue
			}
			name := randomKeyFromAppNameMap(state.appNameList)
			delete(state.appNameList, name)
			state.mutex.Unlock()

			// C) Delete a random Application

			var app v1alpha1.Application
			if err := controlPlaneClient.Get(ctx, types.NamespacedName{Namespace: "agent-managed", Name: name}, &app); err != nil {
				return fmt.Errorf("round %d: get application %q for delete: %w", round, name, err)
			}

			newAppEvent := applicationEvent{
				appName:         app.Name,
				eventType:       watch.Deleted,
				mode:            modeSource,
				direction:       direction_SpecWriter,
				repoURL:         app.Spec.Source.RepoURL,
				resourceVersion: app.ResourceVersion,
			}
			state.initEvent(&newAppEvent)

			if err := controlPlaneClient.Delete(ctx, &app); err != nil && ctx.Err() == nil {
				return fmt.Errorf("round %d: delete application %q: %w", round, name, err)
			}

			newAppEvent.afterContextCancel = ctx.Err() != nil

			// We still record event even if Delete returned 'Context Cancelled' error, as in some cases the error is still returned even though the actual Delete succeeded

			state.recordEvent(newAppEvent)
		}
	}
}

// startManagedAgentStatusWriter: Each 'round', randomly modify .status of an existing application. If an action is not possible (for example, attempting to modify an Application that was recently deleted), skip and try again
// - supports ctx cancellation
// - unexpected error are NOT fatal
func startManagedAgentStatusWriter(ctx context.Context, managedAgentClient client.Client, state *eventState) error {

	for round := 0; ; round++ {

		state.mutex.RLock()
		if len(state.appNameList) <= 10 {
			state.mutex.RUnlock()
			time.Sleep(200 * time.Millisecond) // wait a few moments at  startup where list is still empty
			continue
		}
		name := randomKeyFromAppNameMap(state.appNameList)
		state.mutex.RUnlock()

		var app v1alpha1.Application

		if err := managedAgentClient.Get(ctx, types.NamespacedName{Namespace: "argocd-managed", Name: name}, &app); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// We can safely ignore the case where the Application can't be found: likely it was recently created on control plane but not yet synced to agent. If this is not the case, it will be detected as a failure at the end.
			continue
		}

		// Patch .status.sync.revision with a new random value
		// - There is nothing special about this field; I'm just using it as _a_ place I can stick random values. Any other field would work as well.
		patch := client.MergeFrom(app.DeepCopy())
		randStringVal := randString(64)

		app.Status.Sync.Revision = randStringVal

		newAppEvent := applicationEvent{
			appName:         app.Name,
			mode:            modeSource,
			eventType:       watch.Modified,
			direction:       direction_StatusWriter,
			statusValue:     app.Status.Sync.Revision,
			resourceVersion: app.ResourceVersion,
		}
		state.initEvent(&newAppEvent)

		if err := managedAgentClient.Patch(ctx, &app, patch); err != nil && ctx.Err() == nil {
			// non-fatal, and not a bug: this can occassionally happen if we managed agent deletes an application AFTER we have picked it to patch
			fmt.Println("err on status update B:", app.Status.Sync.Revision, err)
			continue
		}
		newAppEvent.afterContextCancel = ctx.Err() != nil

		// We still record event even if Patch returned 'Context Cancelled' error, as in some cases the error is still returned even though the patch succeeded

		state.recordEvent(newAppEvent)

	}
}

// startManagedAgentSpecWatchLog watches for Application .spec change events (or Application creates/deletes) on managed-agent cluster, and stores those events in eventState list
// - We treat watch errors as fatal; FWIW I've never seen these fail when running against vcluster
func startManagedAgentSpecWatchLog(ctx context.Context, managedAgentClient client.WithWatch, state *eventState) error {
	watcher, err := managedAgentClient.Watch(ctx, &v1alpha1.ApplicationList{}, client.InNamespace("argocd-managed"))
	if err != nil {
		return fmt.Errorf("start watch: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				exit("Channel closed by client or network drop")
				// Channel closed by client or network drop
				return nil
			}

			switch event.Type {
			case watch.Error:
				// The API server sends a *metav1.Status on watch error
				if status, ok := event.Object.(*metav1.Status); ok {

					if status.Code == http.StatusGone || status.Reason == metav1.StatusReasonExpired {
						exit(fmt.Sprintf("gone or expired %v", status))
					} else {
						exit(fmt.Sprintf("other status %v", status))
					}
				} else {
					exit("unexpected cast error on status")
				}

			case watch.Added, watch.Modified, watch.Deleted:
				app, ok := event.Object.(*v1alpha1.Application)
				if !ok {
					exit("unexpected cast error")
					continue
				}

				appEvent := applicationEvent{
					appName:         app.Name,
					eventType:       event.Type,
					mode:            modeDestination,
					direction:       direction_SpecWatcher,
					repoURL:         app.Spec.Source.RepoURL,
					resourceVersion: app.ResourceVersion,
				}
				state.initEvent(&appEvent)
				state.recordEvent(appEvent)
			default:
				exit("unexpected type:" + string(event.Type))
			}
		}
	}

}

// startControlPlaneStatusWatchLog watches for .status change events on Applications, and logs them to eventState list.
// - we treat watch errors as fatal: FWIW I'm never seen these fail (at least on vcluster dev-env)
func startControlPlaneStatusWatchLog(ctx context.Context, controlPlaneClient client.WithWatch, state *eventState) error {
	watcher, err := controlPlaneClient.Watch(ctx, &v1alpha1.ApplicationList{}, client.InNamespace("agent-managed"))
	if err != nil {
		return fmt.Errorf("start watch: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				exit("Channel closed by client or network drop")
				return nil
			}

			switch event.Type {
			case watch.Error:
				// The API server sends a *metav1.Status on watch error
				if status, ok := event.Object.(*metav1.Status); ok {

					if status.Code == http.StatusGone || status.Reason == metav1.StatusReasonExpired {
						exit(fmt.Sprintf("gone or expired %v", status))
					} else {
						exit(fmt.Sprintf("other status %v", status))
					}
				} else {
					exit("unexpected cast error on status")
				}

			case watch.Added, watch.Modified, watch.Deleted:
				app, ok := event.Object.(*v1alpha1.Application)
				if !ok {
					exit("unexpected cast error")
					continue
				}
				appEvent := applicationEvent{
					appName:         app.Name,
					eventType:       event.Type,
					mode:            modeDestination,
					direction:       direction_StatusWatcher,
					statusValue:     app.Status.Sync.Revision,
					resourceVersion: app.ResourceVersion,
				}
				state.initEvent(&appEvent)
				state.recordEvent(appEvent)
			default:
				exit("unexpected type:" + string(event.Type))
			}
		}
	}
}

func validateEventualConsistencyOfEventList(state *eventState, appsDeletedOnStartup map[string]bool) {

	numSourceEvents := 0
	numDestEvents := 0

	state.mutex.Lock()
	listEvents := make([]*applicationEvent, len(state.allEvents))
	for i, e := range state.allEvents {
		clone := *e
		listEvents[i] = &clone
	}
	state.mutex.Unlock()

	// evaluation start time is the time at which this function was called. We use this value to selectively ignore events that occured close to this time, since they may not have had a chance to fully propagate principal <-> agent.
	evaluationStartTime := time.Now()

	// key: name of application
	// value: list of events where event.appName = key
	mapAppNameToEvents := map[string][]*applicationEvent{}
	for _, e := range listEvents {

		if appsDeletedOnStartup[e.appName] {
			continue
		}
		mapAppNameToEvents[e.appName] = append(mapAppNameToEvents[e.appName], e)

		switch e.mode {
		case modeSource:
			numSourceEvents++
		case modeDestination:
			numDestEvents++
		default:
			exit("Unrecognized mode:" + string(e.mode))
		}
	}

	// For each Application resource (name) we saw, evaluate: check spec/status updates match between source of truth and peer
	for _, appEvents := range mapAppNameToEvents {
		validateEventualConsistencyOfSpecUpdates(appEvents, evaluationStartTime)
		validateEventualConsistencyOfStatusUpdates(appEvents, evaluationStartTime)
	}

	fmt.Println("* All matched.", len(listEvents), "events. (", numSourceEvents, " source events /", numDestEvents, " dest events)")
}

// validateEventualConsistencyOfSpecUpdates verifies that updates match to .spec field of Application on principal are synced to .spec field on Application on managed-agent
func validateEventualConsistencyOfSpecUpdates(eventsParam []*applicationEvent, timeOfEvaluation time.Time) {

	sourceEvents := []*applicationEvent{}      // events on principal
	destinationEvents := []*applicationEvent{} // events on managed-agent
	for idx := range eventsParam {
		e := eventsParam[idx]

		// We only care about spec writes/and spec watch events
		if e.direction != direction_SpecWriter && e.direction != direction_SpecWatcher {
			continue
		}

		switch e.mode {
		case modeSource:
			sourceEvents = append(sourceEvents, e)
		case modeDestination:
			destinationEvents = append(destinationEvents, e)
		}
	}

	destinationEvents = deduplicateEvents(destinationEvents, func(e *applicationEvent) string {
		// Don't de-duplicate deletes, but DO de-deduplicate add/update
		eventType := e.eventType
		if eventType == watch.Added || eventType == watch.Modified {
			eventType = "added-or-modified"
		}
		return e.repoURL + "|" + string(eventType)
	})

	allSourceEvents := make([]*applicationEvent, len(sourceEvents))
	copy(allSourceEvents, sourceEvents)

	innerOutputEventList := func() {
		fmt.Println("--------")
		fmt.Println("* Evaluating consistency of spec updates:")
		outputEventList(string(modeSource), allSourceEvents, true)
		fmt.Println()
		outputEventList(string(modeDestination), destinationEvents, true)
	}

	if outputVerboseText {
		innerOutputEventList()
	}

	// For each .spec update on managed-agent, ensure there is a corresponding entry on principal (but must be a match IN ORDER)
	for _, destinationEvent := range destinationEvents {

		match := false

	outer:
		for x, sourceEvent := range sourceEvents {

			if sourceEvent.eventType != destinationEvent.eventType {
				continue
			}

			if sourceEvent.eventType == watch.Deleted {
				// For delete events, there is no guarantee that the repo URL will match. So we only remove the FIRST delete we've seen.
				sourceEvents = sourceEvents[x+1:]

				// Uncomment for additional debuging of this check:

				// fmt.Println("Deleted triggered on ", sourceEvent.string(), len(sourceEvents))
				// for _, se := range sourceEvents {
				// 	fmt.Println("-", se.string())
				// }
				// fmt.Println("DEST: ", destinationEvent.string())
				// fmt.Println()
				// fmt.Println()

				match = true
				break outer

			} else if sourceEvent.repoURL != "" && sourceEvent.repoURL == destinationEvent.repoURL && destinationEvent.eventId > sourceEvent.eventId {

				sourceEvents = sourceEvents[x+1:]

				// Uncomment for additional debuging of this check:

				// fmt.Println("Next event found ", sourceEvent.string(), len(sourceEvents))
				// for _, se := range sourceEvents {
				// 	fmt.Println("-", se.string())
				// }
				// fmt.Println("DEST:", destinationEvent.string())
				// fmt.Println()
				// fmt.Println()

				match = true
				break outer
			}
		}

		if !match {
			// No match, output state and fail.
			if !outputVerboseText {
				innerOutputEventList()
			}

			exit(fmt.Sprintf("No match on: %v (len source events: %d)", destinationEvent.string(), len(sourceEvents)))
		}
	}

	newSourceEvents := filterSourceEventsNearDelete(allSourceEvents)

	innerOutputEventList = func() {
		fmt.Println("--------")
		fmt.Println("* Evaluating consistency of spec updates:")
		outputEventList(string(modeSource), newSourceEvents, true)
		fmt.Println()
		outputEventList(string(modeDestination), destinationEvents, true)
	}

	validateMaxPropagationDelay(newSourceEvents, destinationEvents, timeOfEvaluation, innerOutputEventList)
}

// filterSourceEventsNearDelete filters out add/modify source events that occur within the propagation delay window of a delete event.
// The current behaviour of argocd-agent is that when event writer receives a DELETE, it will (short circuit the sending of and) remove any other (add/updates) events that were also waiting to be sent in the event writer.
// We thus can't expect any other events to be received within X seconds of a DELETE (where X is propagation delay), so we remove them.
func filterSourceEventsNearDelete(allSourceEventsParam []*applicationEvent) []*applicationEvent {

	result := []*applicationEvent{}

	allSourceEventsReversed := make([]*applicationEvent, len(allSourceEventsParam))
	copy(allSourceEventsReversed, allSourceEventsParam)

	slices.Reverse(allSourceEventsReversed)

	atLeastOneAddOrModifySeen := false

	var timeOfLastDelete *time.Time
	for idx := range allSourceEventsReversed {
		e := allSourceEventsReversed[idx]

		// For Deletes, add them unmodified, but keep track of the most recent one that occurred
		if e.eventType == watch.Deleted {
			timeOfLastDelete = &e.dateTime
			result = append(result, e)
			continue
		}

		if timeOfLastDelete != nil {
			// If we've already seen a delete, then we need to remove (ignore) non-delete events X seconds before that

			ignoreEventsAfter := timeOfLastDelete.Add(-1 * maxPropagationDelay)

			if e.dateTime.After(ignoreEventsAfter) {
				continue
			}

			atLeastOneAddOrModifySeen = true
			// Add/Modify event is outside of the X second window of a delete, so add it as is (e.g. don't filter it out)
		}

		result = append(result, e)
	}

	if timeOfLastDelete != nil && !atLeastOneAddOrModifySeen && len(result) > 0 {
		// It's possible that after we filter out all of the add/modify events that occured within X seconds of a delete, that there are now 0 add/modifies events left.
		// In this case, all the remains is a delete.
		// But, by definition, you cannot delete any entity that was never created to begin with.
		// So in this case, we remove the delete as well.

		if len(result) == 1 {
			result = []*applicationEvent{}
		} else {
			for _, e := range result {
				fmt.Println("Unexpected:", e.string())
			}
			exit("unexpected number of events in new source events list: expected only a single delete")
		}
	}

	// Put the result back into oldest first order (ascending by time)
	slices.Reverse(result)

	return result
}

// validateMaxPropagationDelay verifies that if an event A occurs on source at time X, that ANY corresponding destination event must be received by at most time X+15 seconds (e.g. for a propagation delay of 15)
// - We don't verify that the specific event A was received in X seconds, because it may have been removed by de-duplication
// - We assume sourceEvents/destinationEvents are of the same type (both must be .spec events xor .status events)
func validateMaxPropagationDelay(sourceEvents []*applicationEvent, destinationEvents []*applicationEvent, timeOfEvaluation time.Time, outputEventList func()) {

	if !maxPropagationDelayCheck { // Skip if check is disabled in main.go
		return
	}

	ignoreSourceAfter := timeOfEvaluation.Add(maxPropagationDelay * -1)

	for _, sourceEvent := range sourceEvents {

		if sourceEvent.dateTime.After(ignoreSourceAfter) {
			continue
		}

		if sourceEvent.afterContextCancel { // Skip source events that occured after writer context was cancelled. For events in this state, it means we aren't sure whether the goroutine actually wrote that event to the source cluster (or if it was cancelled before)
			continue
		}

		match := false

		for _, destinationEvent := range destinationEvents {

			diff := destinationEvent.dateTime.Sub(sourceEvent.dateTime)

			if diff >= 0*time.Second && diff < maxPropagationDelay {
				match = true
				break
			}

		}

		if !match {
			if !outputVerboseText {
				outputEventList()
			}

			exit(fmt.Sprintf("Source event was not handled within expected timeframe: %v, timeOfEvaluation is: %v", sourceEvent.string(), timeOfEvaluation))
		}
	}
}

// Extract add/delete events from source events (that is, events originating from spec writer on control plane cluster)
func extractLifecycleEventsFromSourceEvents(events []*applicationEvent, outputEventList func()) []*applicationEvent {

	deleteAndAddEvents := []*applicationEvent{}

	for idx := range events {
		e := events[idx]

		if e.mode != modeSource { // We only care about source events here
			continue
		}

		if e.eventType == watch.Deleted || e.eventType == watch.Added { // We are only interested in lifecycle events

			if e.eventType == watch.Deleted {
				// Verify that delete has a previous corresponding add; by definition you can't delete something that wasn't previously created.

				lastItemIndex := len(deleteAndAddEvents) - 1
				if lastItemIndex < 0 {
					outputEventList()
					exit(fmt.Sprintf("No matching add for delete: no previous event: %s", e.string()))
				}
				lastItem := deleteAndAddEvents[lastItemIndex]

				if lastItem.eventType != watch.Added {
					outputEventList()
					exit(fmt.Sprintf("no matching add for delete: last item is not an add: %s", e.string()))
				}
			}

			if e.eventType == watch.Added {
				// Verify that the add does not have a previous add event: by definition you cannot create something which already exists (without an intervening delete)
				lastItemIndex := len(deleteAndAddEvents) - 1
				if lastItemIndex >= 0 {
					lastItem := deleteAndAddEvents[lastItemIndex]
					if lastItem.eventType == watch.Added {
						outputEventList()
						exit(fmt.Sprintf("double add seen: %s", e.string()))
					}
				}
			}

			deleteAndAddEvents = append(deleteAndAddEvents, e)
		}
	}
	return deleteAndAddEvents
}

// validateEventualConsistencyOfStatusUpdates verifies that updates to .status field of Application on managed-agent are synced to .status field of Application on principal
func validateEventualConsistencyOfStatusUpdates(eventsParam []*applicationEvent, timeOfEvaluation time.Time) {

	// Split events into source/destination values, and filter out any events with repoURL values
	sourceEvents := []*applicationEvent{}
	destinationEvents := []*applicationEvent{}
	{
		for idx := range eventsParam {
			e := eventsParam[idx]

			// We only care about spec write events and spec watch events
			if e.direction != direction_StatusWriter && e.direction != direction_StatusWatcher {
				continue
			}

			// There is a brief moment after an Application is created where it doesn't have a status value, but we can still receive status-watcher MODIFY events for that Application (due to a spec write by other goroutine). We can safely ignore these cases.
			if e.statusValue == "" {
				continue
			}

			switch e.mode {
			case modeSource:
				sourceEvents = append(sourceEvents, e)
			case modeDestination:
				destinationEvents = append(destinationEvents, e)
			}
		}
	}

	// Filter out status value dupes: ensure that a particular status value is reported at most once
	destinationEvents = deduplicateEvents(destinationEvents, func(e *applicationEvent) string { return e.statusValue })

	internalOutputEventList := func() {
		fmt.Println("--------")
		fmt.Println("* Evaluating consistency of status updates:")
		outputEventList(string(modeSource), sourceEvents, false)
		fmt.Println()
		outputEventList(string(modeDestination), destinationEvents, false)
	}

	if outputVerboseText {
		internalOutputEventList()
	}

	allSourceEvents := make([]*applicationEvent, len(sourceEvents))
	copy(allSourceEvents, sourceEvents)

	// For each .status update event seen via watch on principal (destination event), verify it has a corresponding action on writer on managed-agent (source event), and that the corresponding action event is in chronological order (consistent event ordering between source and destination)
	{
		for _, destinationEvent := range destinationEvents {

			match := false

			if destinationEvent.eventType == watch.Added { // Status updates will never be reported via an add
				continue
			}

		outer:
			for x, sourceEvent := range sourceEvents {

				if sourceEvent.eventType != destinationEvent.eventType {
					continue
				}

				if sourceEvent.eventType == watch.Deleted {
					// For delete events, there is no guarantee that the status value will match. So we only remove the FIRST delete we've seen.
					sourceEvents = sourceEvents[x+1:]

					match = true
					break outer

				} else if sourceEvent.statusValue != "" && sourceEvent.statusValue == destinationEvent.statusValue && destinationEvent.eventId > sourceEvent.eventId {

					sourceEvents = sourceEvents[x+1:]

					match = true
					break outer
				}
			}

			if !match {
				// No match, output state and fail.
				if !outputVerboseText {
					internalOutputEventList()
				}
				exit(fmt.Sprintf("No match on: %v", destinationEvent.string()))
			}
		}
	}

	// Before we validate that events are propogated from source -> destination within max propagation delay, we must filter out source status updates that occur after (or briefly before) source delete events (filter them out of sourceEvents)
	{
		// If Application is deleted at time=X, we shouldn't look for any status updates (on source) that were made before time-15 seconds (where 15 seconds is max propagation delay)
		//
		// This helps avoid this race condition:
		// - 1) t=0: managed agent> set status update on application A
		// - 2) t=0: principal> delete application A
		// - 3) t=1: managed agent receives delete event from 2), and deletes application A.
		// - 4) (principal never record status update from 1, because the Application that had its status updated no longer exists.)
		// The above behaviour is correct and expected (within eventual consistency), thus we should not flag it as a failure.
		// - To avoid flagging it as a failure, we filter out status event updates that occur close to a DELETE
		internalOutputEventList := func() {
			fmt.Println("--------")
			var appName string
			if len(eventsParam) > 0 {
				appName = eventsParam[0].appName
			}
			fmt.Println("--------")
			fmt.Println("raw repo events:")
			outputEventList(appName, eventsParam, true)
			fmt.Println()
			fmt.Println("raw status events:")
			outputEventList(appName, eventsParam, false)
		}

		lifecycleEventsFromSourceEvents := extractLifecycleEventsFromSourceEvents(eventsParam, internalOutputEventList)

		for idx := range lifecycleEventsFromSourceEvents {

			lifecycleEvent := lifecycleEventsFromSourceEvents[idx]

			if lifecycleEvent.eventType != watch.Deleted {
				continue
			}

			var nextLifecycleEventInList *applicationEvent
			if idx+1 < len(lifecycleEventsFromSourceEvents) {
				nextLifecycleEventInList = lifecycleEventsFromSourceEvents[idx+1]
			}

			if nextLifecycleEventInList == nil {
				// This is the final delete for this Application: there are no add/delete events after this one

				// Remove any status update sources events that occur after (time of delete event - max propagation delay period) from a delete
				allSourceEvents = slices.DeleteFunc(allSourceEvents, func(e *applicationEvent) bool {

					ignoreAfter := lifecycleEvent.dateTime.Add(-1 * maxPropagationDelay)
					res := e.dateTime.After(ignoreAfter)
					return res
				})

			} else {
				if nextLifecycleEventInList.eventType != watch.Added {
					internalOutputEventList()
					exit("Unexpected event: we expect only an add after a delete")
				}

				// There is NOT the final delete: there is an add after this
				internalOutputEventList()
				exit("This is not currently implemented.")
			}
		}
	}

	// Validate that events are propogated from source -> destination within timely fashion
	validateMaxPropagationDelay(allSourceEvents, destinationEvents, timeOfEvaluation, internalOutputEventList)

}
