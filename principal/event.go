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
	"errors"
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/namedlock"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
	"k8s.io/client-go/util/workqueue"

	cloudevents "github.com/cloudevents/sdk-go/v2"
)

// processRecvQueue processes an entry from the receiver queue, which holds the
// events received by agents. It will trigger updates of resources in the
// server's backend.
func (s *Server) processRecvQueue(ctx context.Context, agentName string, q workqueue.RateLimitingInterface) error {
	i, _ := q.Get()
	ev, ok := i.(*cloudevents.Event)
	if !ok {
		q.Done(i)
		return fmt.Errorf("invalid data in queue: have:%T want:%T", i, ev)
	}

	agentMode := s.agentMode(agentName)
	incoming := &v1alpha1.Application{}
	logCtx := log().WithFields(logrus.Fields{
		"module":   "QueueProcessor",
		"client":   agentName,
		"mode":     agentMode.String(),
		"event":    ev.Type(),
		"incoming": incoming.QualifiedName(),
	})

	logCtx.Debugf("Processing event")
	var err error
	target := event.Target(ev)
	switch target {
	case event.TargetApplication:
		err = s.processApplicationEvent(ctx, agentName, ev)
	case event.TargetAppProject:
	default:
		err = fmt.Errorf("unable to process event with unknown target %s", target)
	}
	q.Done(i)
	return err
}

func (s *Server) processApplicationEvent(ctx context.Context, agentName string, ev *cloudevents.Event) error {
	incoming := &v1alpha1.Application{}
	err := ev.DataAs(incoming)
	if err != nil {
		return err
	}
	agentMode := s.agentMode(agentName)

	logCtx := log().WithFields(logrus.Fields{
		"module":   "QueueProcessor",
		"client":   agentName,
		"mode":     agentMode.String(),
		"event":    ev.Type(),
		"incoming": incoming.QualifiedName(),
	})

	switch ev.Type() {

	// App creation event will only be processed in autonomous mode
	case event.Create.String():
		if agentMode.IsAutonomous() {
			incoming.SetNamespace(agentName)
			_, err := s.appManager.Create(ctx, incoming)
			if err != nil {
				return fmt.Errorf("could not create application %s: %w", incoming.QualifiedName(), err)
			}
		} else {
			logCtx.Debugf("Discarding event, because agent is not in autonomous mode")
			return event.ErrEventDiscarded
		}
	// Spec updates are only allowed in autonomous mode
	case event.SpecUpdate.String():
		var err error
		if agentMode == types.AgentModeAutonomous {
			_, err = s.appManager.UpdateAutonomousApp(ctx, agentName, incoming)
		} else {
			err = fmt.Errorf("event type not allowed when mode is not autonomous")
		}
		if err != nil {
			return fmt.Errorf("could not update application status for %s: %w", incoming.QualifiedName(), err)
		}
		logCtx.Infof("Updated application status %s", incoming.QualifiedName())
	// Status updates are only allowed in managed mode
	case event.StatusUpdate.String():
		var err error
		if agentMode == types.AgentModeManaged {
			_, err = s.appManager.UpdateStatus(ctx, agentName, incoming)
		} else {
			err = fmt.Errorf("event type not allowed when mode is not managed")
		}
		if err != nil {
			return fmt.Errorf("could not update application status for %s: %w", incoming.QualifiedName(), err)
		}
		logCtx.Infof("Updated application spec %s", incoming.QualifiedName())
	// App deletion
	case event.Delete.String():
		var err error
		if agentMode.IsManaged() {
			err = errors.New("event type not allowed when mode is not autonomous")
		} else {
			err = s.appManager.Delete(ctx, agentName, incoming, false)
		}
		if err != nil {
			return fmt.Errorf("could not delete application %s: %w", incoming.QualifiedName(), err)
		}
		logCtx.Infof("Deleted application %s", incoming.QualifiedName())
	default:
		return fmt.Errorf("unable to process event of type %s", ev.Type())
	}

	return nil
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

				go func(agentName string, q workqueue.RateLimitingInterface) {
					defer func() {
						sem.Release(1)
						queueLock.Unlock(agentName)
					}()
					err := s.processRecvQueue(ctx, agentName, q)
					if err != nil {
						logCtx.WithField("client", agentName).WithError(err).Errorf("Could not process agent recveiver queue")
					}
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
