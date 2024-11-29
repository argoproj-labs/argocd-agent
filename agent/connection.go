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
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/sirupsen/logrus"

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
					err = a.queues.Create(a.remote.ClientID())
					if err != nil {
						log().Warnf("Could not create agent queue pair: %v", err)
					} else {
						a.SetConnected(true)
					}
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
		"module":      "StreamEvent",
		"direction":   "Send",
		"client_addr": grpcutil.AddressFromContext(stream.Context()),
	})

	q := a.queues.SendQ(a.remote.ClientID())
	if q == nil {
		return fmt.Errorf("no send queue found for the remote principal")
	}
	// Get() is blocking until there is at least one item in the
	// queue.
	logCtx.Tracef("Waiting to grab an item from queue as it appears")
	ev, shutdown := q.Get()
	if shutdown {
		logCtx.Tracef("Queue shutdown in progress")
		return nil
	}
	logCtx.Tracef("Grabbed an item")
	if ev == nil {
		// TODO: Is this really the right thing to do?
		return nil
	}

	logCtx.WithField("resource_id", event.ResourceID(ev)).WithField("event_id", event.EventID(ev)).Trace("Adding an event to the event writer")
	a.eventWriter.Add(ev)

	return nil
}

// receiver receives and processes a single event from the event stream. It
// will block until an event has been received, or an error has occurred.
func (a *Agent) receiver(stream eventstreamapi.EventStream_SubscribeClient) error {
	logCtx := log().WithFields(logrus.Fields{
		"module":      "StreamEvent",
		"direction":   "Recv",
		"client_addr": grpcutil.AddressFromContext(stream.Context()),
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
	if err != nil && !event.IsEventDiscarded(err) && !event.IsEventNotAllowed(err) {
		logCtx.WithError(err).Errorf("Unable to process incoming event")
	} else {
		// Send an ACK if the event is processed successfully.
		sendQ := a.queues.SendQ(a.remote.ClientID())
		if sendQ == nil {
			return fmt.Errorf("no send queue found for the remote principal")
		}
		sendQ.Add(a.emitter.ProcessedEvent(event.EventProcessed, ev))
		logCtx.Trace("Sent an ACK for an event")
	}
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
		"module":      "StreamEvent",
		"server_addr": grpcutil.AddressFromContext(stream.Context()),
	})

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
					logCtx.Errorf("Error while sending to stream: %v", err)
				}
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
		}
	}()

	for a.IsConnected() {
		select {
		case <-a.context.Done():
			return nil
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	log().WithField("component", "EventHandler").Info("Stream closed")
	err = a.queues.Delete(a.remote.ClientID(), true)
	if err != nil {
		log().Errorf("Could not remove agent queue: %v", err)
	}

	return nil
}
