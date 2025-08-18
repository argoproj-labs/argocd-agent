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

package principal

import (
	"context"
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/checkpoint"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/argoproj-labs/argocd-agent/internal/namedlock"
	"github.com/argoproj-labs/argocd-agent/internal/resync"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/workqueue"
)

// processRecvQueue processes an entry from the receiver queue, which holds the
// events received by agents. It will trigger updates of resources in the
// server's backend.
func (s *Server) processRecvQueue(ctx context.Context, agentName string, q workqueue.TypedRateLimitingInterface[*cloudevents.Event]) (*cloudevents.Event, error) {
	status := metrics.EventProcessingSuccess
	ev, _ := q.Get()

	logCtx := log().WithFields(logrus.Fields{
		"module":       "QueueProcessor",
		"client":       agentName,
		"event_target": ev.DataSchema(),
		"event_type":   ev.Type(),
		"agent_name":   agentName,
	})

	// Start measuring time for event processing
	cp := checkpoint.NewCheckpoint("process_recv_queue")

	var err error
	target := event.Target(ev)

	// Start checkpoint step
	cp.Start(target.String())

	logCtx.Debugf("Processing event %s", target)

	switch target {
	case event.TargetApplication:
		err = s.processApplicationEvent(ctx, agentName, ev)
	case event.TargetAppProject:
		err = s.processAppProjectEvent(ctx, agentName, ev)
	case event.TargetResource:
		err = s.processResourceEventResponse(ctx, agentName, ev)
	case event.TargetRedis:
		resReq := &event.RedisResponse{}
		err := ev.DataAs(resReq)
		if err == nil {
			logCtx.Infof("processRecvQueue %s %v", resReq.ConnectionUUID, resReq)
		}

		// Process redis responses on their own thread, as they may block
		go func() {
			err = s.processRedisEventResponse(ctx, logCtx, agentName, ev)
			if err != nil {
				logCtx.WithError(err).Error("unable to process redis event response")
			}
		}()

	case event.TargetResourceResync:
		err = s.processIncomingResourceResyncEvent(ctx, agentName, ev)
	default:
		err = fmt.Errorf("unknown target: '%s'", target)
	}

	// Mark event as processed
	q.Done(ev)

	// Stop and log checkpoint information
	cp.End()
	logCtx.Debug(cp.String())

	if s.metrics != nil {
		// ignore EventNotAllowed errors for metrics
		if err != nil {
			if event.IsEventNotAllowed(err) {
				status = metrics.EventProcessingNotAllowed
			} else {
				status = metrics.EventProcessingFail
				s.metrics.PrincipalErrors.WithLabelValues(target.String()).Inc()
			}
		}

		// store time taken by principal to process event in metrics
		s.metrics.EventProcessingTime.WithLabelValues(string(status), agentName, target.String()).Observe(cp.Duration().Seconds())
	}

	return ev, err
}

// processApplicationEvent processes an incoming event that has an application
// target.
func (s *Server) processApplicationEvent(ctx context.Context, agentName string, ev *cloudevents.Event) error {
	incoming := &v1alpha1.Application{}
	err := ev.DataAs(incoming)
	if err != nil {
		return err
	}
	agentMode := s.agentMode(agentName)

	logCtx := log().WithFields(logrus.Fields{
		"module":      "QueueProcessor",
		"client":      agentName,
		"mode":        agentMode.String(),
		"event":       ev.Type(),
		"incoming":    incoming.QualifiedName(),
		"resource_id": event.ResourceID(ev),
		"event_id":    event.EventID(ev),
	})

	// For autonomous agents, we may have to create the appropriate namespace
	// on the control plane on Create or SpecUpdate events.
	if agentMode.IsAutonomous() && (ev.Type() == event.Create.String() || ev.Type() == event.SpecUpdate.String()) {
		if created, err := s.createNamespaceIfNotExist(ctx, agentName); err != nil {
			return fmt.Errorf("could not create namespace %s: %w", agentName, err)
		} else if created {
			logCtx.Infof("Created namespace %s", agentName)
		}

		// AppProjects from the autonomous agents are prefixed with the agent name
		incoming.Spec.Project, err = agentPrefixedProjectName(incoming.Spec.Project, agentName)
		if err != nil {
			return fmt.Errorf("could not prefix project name: %w", err)
		}

		// Set the destination name to the cluster mapping for the agent
		cluster := s.clusterMgr.Mapping(agentName)
		if cluster == nil {
			return fmt.Errorf("cluster mapping not found for agent %s", agentName)
		}
		incoming.Spec.Destination.Name = cluster.Name
		incoming.Spec.Destination.Server = ""
	}

	switch ev.Type() {

	// App creation event will only be processed in autonomous mode
	case event.Create.String():
		if !agentMode.IsAutonomous() {
			logCtx.Debug("Discarding event, because agent is not in autonomous mode")
			return event.ErrEventDiscarded
		}

		incoming.SetNamespace(agentName)
		_, err := s.appManager.Create(ctx, incoming)
		if err != nil {
			if !kerrors.IsAlreadyExists(err) {
				return fmt.Errorf("could not create application %s: %w", incoming.QualifiedName(), err)
			}

			// Update the application if it already exists
			_, err := s.appManager.UpdateAutonomousApp(ctx, agentName, incoming)
			if err != nil {
				return fmt.Errorf("could not update application spec for %s: %w", incoming.QualifiedName(), err)
			}
		}
	// Spec updates are only allowed in autonomous mode
	case event.SpecUpdate.String():
		if !agentMode.IsAutonomous() {
			logCtx.Debug("Discarding event, because agent is not in autonomous mode")
			return event.NewEventNotAllowedErr("event type not allowed when mode is not autonomous")
		}

		_, err := s.appManager.UpdateAutonomousApp(ctx, agentName, incoming)
		if err != nil {
			return fmt.Errorf("could not update application spec for %s: %w", incoming.QualifiedName(), err)
		}
		logCtx.Infof("Updated application spec %s", incoming.QualifiedName())
	// Status updates are only allowed in managed mode
	case event.StatusUpdate.String():
		if !agentMode.IsManaged() {
			logCtx.Debug("Discarding event, because agent is not in managed mode")
			return event.NewEventNotAllowedErr("event type not allowed when mode is not managed")
		}

		_, err := s.appManager.UpdateStatus(ctx, agentName, incoming)
		if err != nil {
			return fmt.Errorf("could not update application status for %s: %w", incoming.QualifiedName(), err)
		}
		logCtx.Infof("Updated application spec %s", incoming.QualifiedName())
	// App deletion
	case event.Delete.String():
		if agentMode.IsAutonomous() {

			deletionPropagation := backend.DeletePropagationForeground
			err = s.appManager.Delete(ctx, agentName, incoming, &deletionPropagation)
			if err != nil {
				if kerrors.IsNotFound(err) {
					return nil
				}
				return fmt.Errorf("could not delete application %s: %w", incoming.QualifiedName(), err)
			}
			logCtx.Infof("Deleted application %s", incoming.QualifiedName())

		} else if agentMode.IsManaged() {

			incoming.SetNamespace(agentName)
			app, err := s.appManager.Get(ctx, incoming.Name, incoming.Namespace)
			if err != nil {
				if kerrors.IsNotFound(err) {
					return nil // ignore not found error: no need to delete app if it doesn't exist
				}
				logCtx.WithError(err).Error("unexpected error when attempting to retrieve deleted Application")
				return err
			}

			// When we receive an 'application deleted' event from a managed agent, it should only cause the principal application to be deleted IF the principal Application is ALSO in the deletion state (deletionTimestamp is non-nil)
			if app.DeletionTimestamp == nil {
				logCtx.Warn("application was detected as deleted from managed agent, even though principal application is not in deletion state")
				return nil
			}

			// Remove the finalizers from the Application when it has been deleted from managed-agent
			if len(app.Finalizers) > 0 {
				_, err := s.appManager.RemoveFinalizers(ctx, app)
				if err != nil {
					logCtx.WithError(err).Error("unexpected error when attempting to update finalizers of deleted Application")
					return err
				}
			}

			deletionPropagation := backend.DeletePropagationForeground
			err = s.appManager.Delete(ctx, agentName, incoming, &deletionPropagation)
			if err != nil {
				if kerrors.IsNotFound(err) {
					return nil
				}
				return fmt.Errorf("could not delete application %s: %w", incoming.QualifiedName(), err)
			}
			logCtx.Infof("Deleted application %s", incoming.QualifiedName())

		} else {
			return fmt.Errorf("unexpected agent mode")
		}
	default:
		return fmt.Errorf("unable to process event of type %s", ev.Type())
	}

	return nil
}

func (s *Server) processAppProjectEvent(ctx context.Context, agentName string, ev *cloudevents.Event) error {
	incoming := &v1alpha1.AppProject{}
	err := ev.DataAs(incoming)
	if err != nil {
		return err
	}
	agentMode := s.agentMode(agentName)

	logCtx := log().WithFields(logrus.Fields{
		"module":      "QueueProcessor",
		"client":      agentName,
		"mode":        agentMode.String(),
		"event":       ev.Type(),
		"incoming":    incoming.Name,
		"resource_id": event.ResourceID(ev),
		"event_id":    event.EventID(ev),
	})

	// AppProjects coming from different autonomous agents could have the same name,
	// so we prefix the project name with the agent name
	if agentMode.IsAutonomous() {
		incoming.Name, err = agentPrefixedProjectName(incoming.Name, agentName)
		if err != nil {
			return fmt.Errorf("could not prefix project name: %w", err)
		}
		// Set the source namespaces to allow the agent's namespace on the principal
		incoming.Spec.SourceNamespaces = []string{agentName}
		// Set all destinations to point to the agent cluster
		for i := range incoming.Spec.Destinations {
			incoming.Spec.Destinations[i].Name = agentName
			incoming.Spec.Destinations[i].Server = "*"
		}
	}

	switch ev.Type() {

	// AppProject creation event will only be processed in autonomous mode
	case event.Create.String():
		if agentMode.IsAutonomous() {
			_, err := s.projectManager.Create(ctx, incoming)
			if err != nil {
				return fmt.Errorf("could not create app-project %s: %w", incoming.Name, err)
			}
		} else {
			logCtx.Debugf("Discarding event, because agent is not in autonomous mode")
			return event.ErrEventDiscarded
		}
	// AppProject spec updates are only allowed in autonomous mode
	case event.SpecUpdate.String():
		if !agentMode.IsAutonomous() {
			logCtx.Debug("Discarding event, because agent is not in autonomous mode")
			return event.NewEventNotAllowedErr("event type not allowed when mode is not autonomous")
		}

		_, err := s.projectManager.UpdateAppProject(ctx, incoming)
		if err != nil {
			return fmt.Errorf("could not update app-project %s: %w", incoming.Name, err)
		}
		logCtx.Infof("Updated appProject %s from agent %s", incoming.Name, agentName)
	// AppProject deletion
	case event.Delete.String():
		if !agentMode.IsAutonomous() {
			return event.NewEventNotAllowedErr("event type not allowed when mode is not autonomous")
		}

		deletionPropagation := backend.DeletePropagationForeground
		err := s.projectManager.Delete(ctx, incoming, &deletionPropagation)
		if err != nil {
			return fmt.Errorf("could not delete app-project %s: %w", incoming.Name, err)
		}
		logCtx.Infof("Deleted app-project %s", incoming.Name)
	default:
		return fmt.Errorf("unable to process event of type %s", ev.Type())
	}

	return nil
}

// processRedisEventResponse proceses (redis) messages received from agents:
// - These messages will be Get responses, initial Subscribe response, and (async) Subscribe notifications
func (s *Server) processRedisEventResponse(ctx context.Context, logCtx *logrus.Entry, agentName string, ev *cloudevents.Event) error {

	resReq := &event.RedisResponse{}
	err := ev.DataAs(resReq)
	if err != nil {
		return err
	}

	if resReq.ConnectionUUID == "" {
		return fmt.Errorf("invalid connectionID seen in redis messsage")
	}

	// Wait up to 30 seconds for event to be handled
	ctx, cancelFunc := context.WithTimeout(ctx, time.Second*30)
	defer cancelFunc()

	logCtx = logCtx.WithField("connectionUUID", resReq.ConnectionUUID).WithField("agent-name", agentName)

	if resReq.Body.PushFromSubscribe != nil {

		logCtx = logCtx.WithField("channelName", resReq.Body.PushFromSubscribe.ChannelName)

		logCtx.Tracef("handling PushFromSubscribe, retrieving tracked channel")

		// PushFromSubscribe is asynchronous, so we can't use event id tracker to find the corresponding channel.
		// - We instead use connection id, which maps directly to the connection from Argo CD
		trAgent, evChan := s.redisProxy.ConnectionIDTracker.Tracked(resReq.ConnectionUUID)
		if evChan == nil {
			logCtx.Debug("connection UUID is no longer in connectionIDTracker, likely the principal connection has closed")
			return nil
		}

		if trAgent != agentName {
			err := fmt.Errorf("agent mismap between event and tracking for PushFromSubscribe: '%s' '%s' '%s'", trAgent, agentName, resReq.ConnectionUUID)
			return err
		}

		logCtx.Tracef("handling PushFromSubscribe, sending message to tracked channel")

		// Forward the event back to the appropriate receiver via connection-specific channel
		select {
		case evChan <- ev:
			err = nil
		case <-ctx.Done():
			err = fmt.Errorf("error waiting for response to be read: %w", ctx.Err())
		}

		if err == nil {
			logCtx.Tracef("handling PushFromSubscribe, sent message to tracked channel")
		} else {
			logCtx.WithError(err).Error("error waiting for response to be read")
		}

		return err
	}

	logCtx = logCtx.WithField("eventID", resReq.UUID)

	// For all other responses, we send the event on the eventIDTracker channel

	// We need to make sure that a) the event is tracked at all, and b) the
	// event is for the currently processed agent.
	trAgent, evChan := s.redisProxy.EventIDTracker.Tracked(resReq.UUID)
	if evChan == nil {
		logCtx.Errorf("redis response not tracked for eventID")
		return fmt.Errorf("redis response not tracked")
	}
	if trAgent != agentName {
		logCtx.Errorf("agent mismap between event and tracking: '%s' '%s'", trAgent, agentName)
		return fmt.Errorf("agent mismap between event and tracking: '%s' '%s'", trAgent, agentName)
	}

	logCtx.Trace("sending message to eventIDTracker channel")

	// Forward the message back to the appropriate reciver via event-specific channel
	select {
	case evChan <- ev:
		err = nil
	case <-ctx.Done():
		err = fmt.Errorf("error waiting for response to be read: %w", ctx.Err())
	}

	logCtx.Trace("sent message to eventIDTracker channel")

	return err
}

// processResourceEventResponse will process a response to a resource request
// event.
func (s *Server) processResourceEventResponse(ctx context.Context, agentName string, ev *cloudevents.Event) error {
	UUID := event.EventID(ev)
	// We need to make sure that a) the event is tracked at all, and b) the
	// event is for the currently processed agent.
	trAgent, evChan := s.resourceProxy.Tracked(UUID)
	if evChan == nil {
		return fmt.Errorf("resource response not tracked")
	}
	if trAgent != agentName {
		return fmt.Errorf("agent mismap between event and tracking")
	}

	// Get the resource response body from the event
	resReq := &event.ResourceResponse{}
	err := ev.DataAs(resReq)
	if err != nil {
		return err
	}

	// There should be someone at the receiving end of the channel to read and
	// process the event. Typically, this will be the resource proxy. However,
	// we will not wait forever for the response to be read.
	select {
	case evChan <- ev:
		err = nil
	case <-ctx.Done():
		err = fmt.Errorf("error waiting for response to be read: %w", ctx.Err())
	}

	return err
}

// processIncomingResourceResyncEvent will handle the incoming resync events from the agent
func (s *Server) processIncomingResourceResyncEvent(ctx context.Context, agentName string, ev *cloudevents.Event) error {
	agentMode := s.agentMode(agentName)
	logCtx := log().WithFields(logrus.Fields{
		"module":      "QueueProcessor",
		"client":      agentName,
		"mode":        agentMode.String(),
		"event":       ev.Type(),
		"resource_id": event.ResourceID(ev),
		"event_id":    event.EventID(ev),
	})

	dynClient, err := dynamic.NewForConfig(s.kubeClient.RestConfig)
	if err != nil {
		return err
	}

	sendQ := s.queues.SendQ(agentName)
	if sendQ == nil {
		return fmt.Errorf("queue not found for agent: %s", agentName)
	}

	resyncHandler := resync.NewRequestHandler(dynClient, sendQ, s.events, s.resources.Get(agentName), logCtx, manager.ManagerRolePrincipal)

	switch ev.Type() {
	case event.SyncedResourceList.String():
		if agentMode != types.AgentModeManaged {
			return fmt.Errorf("principal can only handle SyncedResourceList request in the managed mode")
		}

		incoming := &event.RequestSyncedResourceList{}
		if err := ev.DataAs(incoming); err != nil {
			return err
		}

		return resyncHandler.ProcessSyncedResourceListRequest(agentName, incoming)
	case event.ResponseSyncedResource.String():
		if agentMode != types.AgentModeAutonomous {
			return fmt.Errorf("principal can only handle SyncedResource request in autonomous mode")
		}

		incoming := &event.SyncedResource{}
		if err := ev.DataAs(incoming); err != nil {
			return err
		}

		// Using agentName as the namespace
		incoming.Namespace = agentName

		return resyncHandler.ProcessIncomingSyncedResource(ctx, incoming, agentName)
	case event.EventRequestUpdate.String():
		if agentMode != types.AgentModeManaged {
			return fmt.Errorf("principal can only handle request update in the managed mode")
		}

		incoming := &event.RequestUpdate{}
		if err := ev.DataAs(incoming); err != nil {
			return err
		}

		return resyncHandler.ProcessRequestUpdateEvent(ctx, agentName, incoming)
	case event.EventRequestResourceResync.String():
		if agentMode != types.AgentModeAutonomous {
			return fmt.Errorf("principal can only handle ResourceResync request in autonomous mode")
		}

		incoming := &event.RequestResourceResync{}
		if err := ev.DataAs(incoming); err != nil {
			return err
		}

		return resyncHandler.ProcessIncomingResourceResyncRequest(ctx, agentName)
	default:
		return fmt.Errorf("invalid type of resource resync: %s", ev.Type())
	}
}

// eventProcessor is the main loop to process event from the receiver queue,
// i.e. events coming from the connect agents. It will process events from
// different agents in parallel, but it will not parallelize processing of
// events from the same queue. These latter events need to be processed in
// sequential order, in any case.
func (s *Server) eventProcessor(ctx context.Context) error {
	sem := semaphore.NewWeighted(s.options.eventProcessors)
	queueLock := namedlock.NewNamedLock()
	logCtx := log().WithField("module", "EventProcessor")
	for {
		queuesProcessed := 0
		for _, queueName := range s.queues.Names() {
			select {
			case <-ctx.Done():
				logCtx.Infof("Shutting down event processor")
				return nil
			default:
				// Though unlikely, the agent might have disconnected, and
				// the queue will be gone. In this case, we'll just skip.
				q := s.queues.RecvQ(queueName)
				if q == nil {
					logCtx.Debugf("Queue disappeared -- client probably has disconnected")
					break
				}

				// Since q.Get() is blocking, we want to make sure something is actually
				// in the queue before we try to grab it.
				if q.Len() == 0 {
					break
				}

				// We lock this specific queue, so that we won't process two
				// items of the same queue at the same time. Queues must be
				// processed in FIFO order, always.
				//
				// If it's not possible to get a lock (i.e. a lock is already
				// being held elsewhere), we continue with the next queue.
				if !queueLock.TryLock(queueName) {
					// logCtx.Tracef("Could not acquire queue lock, skipping queue")
					break
				}

				logCtx = logCtx.WithField("queueName", queueName)

				queuesProcessed += 1

				logCtx.Trace("Acquired queue lock")

				err := sem.Acquire(ctx, 1)
				if err != nil {
					logCtx.Tracef("Error acquiring semaphore: %v", err)
					queueLock.Unlock(queueName)
					break
				}

				logCtx.Trace("Acquired semaphore")

				go func(agentName string, q workqueue.TypedRateLimitingInterface[*cloudevents.Event]) {
					defer func() {
						sem.Release(1)
						queueLock.Unlock(agentName)

					}()

					ev, err := s.processRecvQueue(ctx, agentName, q)
					if err != nil {
						logCtx.WithField("client", agentName).WithError(err).Errorf("Could not process agent receiver queue")
						// Don't send an ACK if it is a retryable error.
						if kube.IsRetryableError(err) {
							logCtx.Trace("Skipping ACK for retryable errors")
							return
						}
					}

					// Send an ACK if the event is processed successfully.
					sendQ := s.queues.SendQ(queueName)
					if sendQ == nil {
						logCtx.Debugf("Queue disappeared -- client probably has disconnected")
						return
					}
					logCtx = logCtx.WithFields(logrus.Fields{
						"resource_id": event.ResourceID(ev),
						"event_id":    event.EventID(ev),
						"type":        ev.Type(),
					})

					logCtx.Trace("sending an ACK for an event")
					sendQ.Add(s.events.ProcessedEvent(event.EventProcessed, event.New(ev, event.TargetEventAck)))
				}(queueName, q)
			}
		}
		// Give the CPU a little rest when no agents are connected
		if queuesProcessed == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

}

// StartEventProcessor will start the event processor, which processes items
// from all queues as the items appear in the queues. Processing will be
// performed in parallel, and in the background, until the context ctx is done.
//
// If an error occurs before the processor could be started, it will be
// returned.
func (s *Server) StartEventProcessor(ctx context.Context) error {
	var err error
	go func() {
		log().Infof("Starting event processor")
		err = s.eventProcessor(ctx)
	}()
	return err
}

func (s *Server) createNamespaceIfNotExist(ctx context.Context, name string) (bool, error) {
	if !s.autoNamespaceAllow {
		return false, nil
	}
	if s.autoNamespacePattern != nil {
		if !s.autoNamespacePattern.MatchString(name) {
			return false, nil
		}
	}
	// TODO: Handle namespaces that are currently being deleted in a graceful
	// way.
	_, err := s.kubeClient.Clientset.CoreV1().Namespaces().Get(ctx, name, v1.GetOptions{})
	if err != nil {
		if !kerrors.IsNotFound(err) {
			return false, err
		} else {
			_, err = s.kubeClient.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: v1.ObjectMeta{
					Name:   name,
					Labels: s.autoNamespaceLabels,
				}},
				v1.CreateOptions{})
			return true, err
		}
	}
	return false, nil
}

func agentPrefixedProjectName(project, agent string) (string, error) {
	project = agent + "-" + project
	if len(project) > validation.DNS1123SubdomainMaxLength {
		return "", fmt.Errorf("agent prefixed project name cannot be longer than %d characters: %s",
			validation.DNS1123SubdomainMaxLength, project)
	}
	return project, nil
}
