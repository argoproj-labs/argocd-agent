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

package agent

import (
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/grpcutil"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/resync"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/dynamic"

	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
)

func (a *Agent) maintainConnection() error {
	go func() {
		var err error
		for {
			if !a.IsConnected() {
				err = a.remote.Connect(a.context, false)
				if err != nil {
					log().Warnf("Could not connect to %s: %v", a.remote.Addr(), err)
				} else {
					if !a.queues.HasQueuePair(defaultQueueName) {
						err = a.queues.Create(defaultQueueName)
						if err != nil {
							log().Warnf("Could not create agent queue pair: %v", err)
							continue
						}
					}
					a.SetConnected(true)
				}
			} else {
				err = a.handleStreamEvents()
				if err != nil {
					log().Warnf("Error while handling stream events: %v", err)
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	return nil
}

func (a *Agent) sender(stream eventstreamapi.EventStream_SubscribeClient) error {
	logCtx := log().WithFields(logrus.Fields{
		logfields.Module:     "StreamEvent",
		logfields.Direction:  "Send",
		logfields.ClientAddr: grpcutil.AddressFromContext(stream.Context()),
	})

	q := a.queues.SendQ(defaultQueueName)
	if q == nil {
		return fmt.Errorf("no send queue found for the default queue pair")
	}
	// Get() is blocking until there is at least one item in the
	// queue.
	logCtx.Tracef("Waiting to grab an item from queue as it appears")
	ev, shutdown := q.Get()
	if shutdown {
		logCtx.Tracef("Queue shutdown in progress")
		return nil
	}
	logCtx.Trace("Grabbed an item")
	if ev == nil {
		// TODO: Is this really the right thing to do?
		return nil
	}
	logCtx = logCtx.WithFields(logrus.Fields{
		"event_target": ev.DataSchema(),
		"event_type":   ev.Type(),
		"resource_id":  event.ResourceID(ev),
		"event_id":     event.EventID(ev),
	})
	logCtx.Trace("Adding an event to the event writer")
	a.eventWriter.Add(ev)

	return nil
}

// receiver receives and processes a single event from the event stream. It
// will block until an event has been received, or an error has occurred.
func (a *Agent) receiver(stream eventstreamapi.EventStream_SubscribeClient) error {
	logCtx := log().WithFields(logrus.Fields{
		logfields.Module:     "StreamEvent",
		logfields.Direction:  "Recv",
		logfields.ClientAddr: grpcutil.AddressFromContext(stream.Context()),
	})
	rcvd, err := stream.Recv()
	if err != nil {
		if grpcutil.NeedReconnectOnError(err) {
			return err
		} else {
			logCtx.Errorf("Error while receiving: %v", err)
			return nil
		}
	}
	ev, err := event.FromWire(rcvd.Event)
	if err != nil {
		logCtx.Errorf("Could not unwrap event: %v", err)
		return nil
	}

	logCtx = logCtx.WithFields(logrus.Fields{
		"resource_id": ev.ResourceID(),
		"event_id":    ev.EventID(),
		"type":        ev.Type(),
	})

	logCtx.Debugf("Received a new event from stream")

	if ev.Target() == event.TargetEventAck {
		logCtx.Trace("Received an ACK for an event")
		rawEvent, err := format.FromProto(rcvd.Event)
		if err != nil {
			return err
		}
		a.eventWriter.Remove(rawEvent)
		logCtx.Trace("Removed an event from the event writer")
		return nil
	}

	err = a.processIncomingEvent(ev)
	if err != nil {
		logCtx.WithError(err).Errorf("Unable to process incoming event")
		// Don't send an ACK if it is a retryable error.
		if kube.IsRetryableError(err) {
			logCtx.Trace("Skipping ACK for retryable errors")
			return nil
		}
	}

	// Send an ACK if the event is processed successfully.
	sendQ := a.queues.SendQ(defaultQueueName)
	if sendQ == nil {
		return fmt.Errorf("no send queue found for the default queue pair")
	}
	sendQ.Add(a.emitter.ProcessedEvent(event.EventProcessed, ev))
	logCtx.Trace("Sent an ACK for an event")

	return nil
}

func (a *Agent) handleStreamEvents() error {
	conn := a.remote.Conn()
	client := eventstreamapi.NewEventStreamClient(conn)
	stream, err := client.Subscribe(a.context)
	if err != nil {
		return err
	}

	a.eventWriter = event.NewEventWriter(stream)
	go a.eventWriter.SendWaitingEvents(a.context)

	logCtx := log().WithFields(logrus.Fields{
		logfields.Module:     "StreamEvent",
		logfields.ServerAddr: grpcutil.AddressFromContext(stream.Context()),
	})

	if err := a.resyncOnStart(logCtx); err != nil {
		logCtx.Errorf("failed to resync the agent on startup: %v", err)
	}

	// Receive events from the subscription stream
	go func() {
		logCtx := logCtx.WithFields(logrus.Fields{
			"direction": "recv",
		})
		logCtx.Info("Starting to receive events from event stream")
		var err error
		// Continuously retrieve events from the event stream 'inbox' and process them, while the stream is connected
		for a.IsConnected() && err == nil {
			err = a.receiver(stream)
			if err != nil {
				if grpcutil.NeedReconnectOnError(err) {
					a.SetConnected(false)
				} else {
					logCtx.Errorf("Error while receiving from stream: %v", err)
				}
			}

			if a.metrics != nil {
				// count no of events received from principal
				a.metrics.EventReceived.Inc()
			}
		}
	}()

	// Send events in the sendq to the event stream
	go func() {
		logCtx := logCtx.WithFields(logrus.Fields{
			"direction": "send",
		})
		logCtx.Info("Starting to send events to event stream")
		var err error
		// Continuously read events from the 'outbox', and send them to principal, while the stream is connected
		for a.IsConnected() && err == nil {
			err = a.sender(stream)
			if err != nil {
				if grpcutil.NeedReconnectOnError(err) {
					a.SetConnected(false)
				} else {
					logCtx.Errorf("Error while sending to stream: %v", err)
				}
			}

			if a.metrics != nil {
				// count no of events sent to principal
				a.metrics.EventSent.Inc()
			}
		}
	}()

	// Send heartbeat (ping) events at regular intervals to keep the stream alive.
	// This is necessary for service meshes like Istio that timeout idle connections.
	// Heartbeats are sent directly on the stream (bypassing EventWriter) because they
	// are fire-and-forget and don't need ACK tracking or retry logic.
	if a.options.heartbeatInterval > 0 {
		go func() {
			logCtx := logCtx.WithFields(logrus.Fields{
				"direction": "heartbeat",
			})
			logCtx.Infof("Starting heartbeat sender with interval %v", a.options.heartbeatInterval)
			ticker := time.NewTicker(a.options.heartbeatInterval)
			defer ticker.Stop()

			for a.IsConnected() {
				select {
				case <-a.context.Done():
					logCtx.Debug("Heartbeat sender stopped due to context cancellation")
					return
				case <-ticker.C:
					// Create and send ping event directly on stream (bypass EventWriter, fire & forget)
					pingEvent := a.emitter.HeartbeatEvent(event.Ping)
					pev, err := format.ToProto(pingEvent)
					if err != nil {
						logCtx.Errorf("Failed to encode heartbeat: %v", err)
						continue
					}
					if err := stream.Send(&eventstreamapi.Event{Event: pev}); err != nil {
						logCtx.Warnf("Failed to send heartbeat: %v", err)
						// Don't return on error - connection state handled by other goroutines
					} else {
						logCtx.Trace("Sent heartbeat ping")
					}
				}
			}
			logCtx.Debug("Heartbeat sender stopped")
		}()
	}

	for a.IsConnected() {
		select {
		case <-a.context.Done():
			return nil
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	log().WithField(logfields.Component, "EventHandler").Info("Stream closed")

	return nil
}

func (a *Agent) resyncOnStart(logCtx *logrus.Entry) error {
	if a.resyncedOnStart {
		return nil
	}

	sendQ := a.queues.SendQ(defaultQueueName)
	if sendQ == nil {
		return fmt.Errorf("no send queue found for the default queue pair")
	}

	// Agent is the source of truth in autonomous mode. So, the agent must request resource
	// resync with the principal
	if a.mode == types.AgentModeAutonomous {
		ev, err := a.emitter.RequestResourceResyncEvent()
		if err != nil {
			return fmt.Errorf("failed to create RequestResourceResync event: %w", err)
		}

		sendQ.Add(ev)
		logCtx.Trace("Sent a request for resource resync")
	} else {
		logCtx.Trace("Checking if the agent is out of sync with the principal")

		// Principal is the source of truth in the managed mode. Agent should request the latest content
		// from the Principal to detect any updates on the agent side.
		dynClient, err := dynamic.NewForConfig(a.kubeClient.RestConfig)
		if err != nil {
			return err
		}

		resyncHandler := resync.NewRequestHandler(dynClient, sendQ, a.emitter, a.resources, logCtx, manager.ManagerRoleAgent, a.namespace)
		go resyncHandler.SendRequestUpdates(a.context)

		// Agent should request SyncedResourceList from the principal to detect deleted
		// resources on the agent side.
		checksum := a.resources.Checksum()

		// send the checksum to the principal
		ev, err := a.emitter.RequestSyncedResourceListEvent(checksum)
		if err != nil {
			return fmt.Errorf("failed to create synced resource list event: %v", err)
		}

		sendQ.Add(ev)
		logCtx.Trace("Sent a request for SyncedResourceList")
	}
	a.resyncedOnStart = true
	return nil
}
