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
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/argoproj-labs/argocd-agent/internal/namedlock"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	})

	logCtx.Debugf("Processing event")

	// Start measuring time for event processing
	cp := checkpoint.NewCheckpoint("process_recv_queue")

	var err error
	target := event.Target(ev)

	// Start checkpoint step
	cp.Start(target.String())

	switch target {
	case event.TargetApplication:
		err = s.processApplicationEvent(ctx, agentName, ev)
	case event.TargetAppProject:
		err = s.processAppProjectEvent(ctx, agentName, ev)
	case event.TargetResource:
		err = s.processResourceEventResponse(ctx, agentName, ev)
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
		if err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("could not create application %s: %w", incoming.QualifiedName(), err)
		}
	// Spec updates are only allowed in autonomous mode
	case event.SpecUpdate.String():
		if !agentMode.IsAutonomous() {
			logCtx.Debug("Discarding event, because agent is not in autonomous mode")
			return event.NewEventNotAllowedErr("event type not allowed when mode is not autonomous")
		}

		_, err := s.appManager.UpdateAutonomousApp(ctx, agentName, incoming)
		if err != nil {
			return fmt.Errorf("could not update application status for %s: %w", incoming.QualifiedName(), err)
		}
		logCtx.Infof("Updated application status %s", incoming.QualifiedName())
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
		if !agentMode.IsAutonomous() {
			logCtx.Debug("Discarding event, because agent is not in autonomous mode")
			return event.NewEventNotAllowedErr("event type not allowed when mode is not autonomous")
		}

		deletionPropagation := backend.DeletePropagationForeground
		err := s.appManager.Delete(ctx, agentName, incoming, &deletionPropagation)
		if err != nil {
			if kerrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("could not delete application %s: %w", incoming.QualifiedName(), err)
		}
		logCtx.Infof("Deleted application %s", incoming.QualifiedName())
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

	switch ev.Type() {

	// AppProject creation event will only be processed in autonomous mode
	case event.Create.String():
		if agentMode.IsAutonomous() {
			incoming.SetNamespace(agentName)
			_, err := s.projectManager.Create(ctx, incoming)
			if err != nil {
				return fmt.Errorf("could not create app-project %s: %w", incoming.Name, err)
			}
		} else {
			logCtx.Debugf("Discarding event, because agent is not in autonomous mode")
			return event.ErrEventDiscarded
		}
	// AppProject deletion
	case event.Delete.String():
		if !agentMode.IsAutonomous() {
			return event.NewEventNotAllowedErr("event type not allowed when mode is not autonomous")
		}

		deletionPropagation := backend.DeletePropagationForeground
		err := s.projectManager.Delete(ctx, agentName, incoming, &deletionPropagation)
		if err != nil {
			return fmt.Errorf("could not delete app-project %s: %w", incoming.Name, err)
		}
		logCtx.Infof("Deleted app-project %s", incoming.Name)
	default:
		return fmt.Errorf("unable to process event of type %s", ev.Type())
	}

	return nil
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
						logCtx.WithField("client", agentName).WithError(err).Errorf("Could not process agent recveiver queue")
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
					logCtx.WithField("resource_id", event.ResourceID(ev)).WithField("event_id", event.EventID(ev)).Trace("sending an ACK for an event")
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
	_, err := s.kubeClient.CoreV1().Namespaces().Get(ctx, name, v1.GetOptions{})
	if err != nil {
		if !kerrors.IsNotFound(err) {
			return false, err
		} else {
			_, err = s.kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
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
