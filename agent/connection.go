package agent

import (
	"fmt"
	"time"

	"github.com/jannfis/argocd-agent/internal/event"
	"github.com/jannfis/argocd-agent/internal/grpcutil"
	"github.com/jannfis/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/sirupsen/logrus"

	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	cloudevents "github.com/cloudevents/sdk-go/v2"
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
	item, shutdown := q.Get()
	if shutdown {
		logCtx.Tracef("Queue shutdown in progress")
		return nil
	}
	logCtx.Tracef("Grabbed an item")
	if item == nil {
		// FIXME: Is this really the right thing to do?
		return nil
	}

	ev, ok := item.(*cloudevents.Event)
	if !ok {
		logCtx.Warnf("invalid data in sendqueue")
		return nil
	}

	logCtx.Tracef("Sending an item to the event stream")

	pev, err := format.ToProto(ev)
	if err != nil {
		logCtx.Warnf("Could not wire event: %v", err)
		return nil
	}

	err = stream.Send(&eventstreamapi.Event{Event: pev})
	if err != nil {
		if grpcutil.NeedReconnectOnError(err) {
			return err
		} else {
			logCtx.Infof("Error while sending: %v", err)
			return nil
		}
	}

	return nil
}

// receiver receives and processes a single event from the event stream. It
// will block until an event has been received, or an error has occured.
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
			logCtx.Infof("Error while receiving: %v", err)
			return nil
		}
	}
	ev, err := event.FromWire(rcvd.Event)
	if err != nil {
		logCtx.Errorf("Could not unwrap event: %v", err)
		return nil
	}
	logCtx.Debugf("Received a new event from stream")
	err = a.processIncomingEvent(ev)
	if err != nil {
		logCtx.WithError(err).Errorf("Unable to process incoming event")
	}
	// switch ev.Type() {
	// case event.Create:
	// 	_, err := a.createApplication(incomingApp)
	// 	if err != nil {
	// 		logCtx.Errorf("Error creating application: %v", err)
	// 	}
	// case event.SpecUpdate:
	// 	_, err = a.updateApplication(incomingApp)
	// 	if err != nil {
	// 		logCtx.Errorf("Error updating application: %v", err)
	// 	}
	// case event.Delete:
	// 	err = a.deleteApplication(incomingApp)
	// 	if err != nil {
	// 		logCtx.Errorf("Error deleting application: %v", err)
	// 	}
	// default:
	// 	logCtx.Warnf("Received an unknown event: %s. Protocol mismatch?", ev.Type())
	// }

	return nil
}

func (a *Agent) handleStreamEvents() error {
	conn := a.remote.Conn()
	client := eventstreamapi.NewEventStreamClient(conn)
	stream, err := client.Subscribe(a.context)
	if err != nil {
		return err
	}
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
