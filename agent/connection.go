package agent

import (
	"io"
	"time"

	"github.com/jannfis/argocd-agent/internal/event"
	"github.com/jannfis/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (a *Agent) maintainConnection() error {
	go func() {
		var err error
		for {
			if !a.connected.Load() {
				err = a.remote.Connect(a.context, false)
				if err != nil {
					log().Warnf("Could not connect to %s: %v", a.remote.Addr(), err)
				} else {
					err = a.queues.Create(a.remote.ClientID())
					if err != nil {
						log().Warnf("Could not create agent queue pair: %v", err)
					} else {
						a.connected.Store(true)
					}
				}
			} else {
				a.handleStreamEvents()
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	return nil
}

func (a *Agent) handleStreamEvents() error {
	conn := a.remote.Conn()
	client := eventstreamapi.NewEventStreamClient(conn)
	stream, err := client.Subscribe(a.context)
	if err != nil {
		return err
	}
	syncCh := make(chan struct{})

	// Receive events from the subscription stream
	go func() {
		logCtx := log().WithFields(logrus.Fields{
			"module":    "StreamEvent",
			"direction": "Recv",
		})
		logCtx.Info("Starting to receive events from event stream")
		for a.connected.Load() {
			ev, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					close(syncCh)
					return
				}
				time.Sleep(100 * time.Millisecond)
				continue
			}
			logCtx.Debugf("Received a new event from stream")
			switch event.EventType(ev.Event) {
			case event.EventAppAdded:
				_, err := a.createApplication(ev.Application)
				if err != nil {
					logCtx.Errorf("Error creating application: %v", err)
				}
			case event.EvenAppSpecUpdated:
				_, err = a.updateApplication(ev.Application)
				if err != nil {
					logCtx.Errorf("Error updating application: %v", err)
				}
			case event.EventAppDeleted:
				err = a.deleteApplication(ev.Application)
				if err != nil {
					logCtx.Errorf("Error deleting application: %v", err)
				}
			default:
				logCtx.Warnf("Received an unknown event: %d. Protocol mismatch?", ev.Event)
			}
		}
	}()

	// Send events in the sendq to the event stream
	go func() {
		logCtx := log().WithFields(logrus.Fields{
			"module":    "StreamEvent",
			"direction": "Send",
		})
		logCtx.Info("Starting to send events to event stream")
		for a.connected.Load() {
			select {
			case <-a.context.Done():
				logCtx.Info("Context canceled")
				return
			default:
				q := a.queues.SendQ(a.remote.ClientID())
				if q == nil {
					logCtx.Warnf("I have no send queue for the remote server")
					return
				}
				// Get() is blocking until there is at least one item in the
				// queue.
				logCtx.Tracef("Waiting to grab an item from queue as it appears")
				item, shutdown := q.Get()
				if shutdown {
					logCtx.Tracef("Queue shutdown in progress")
					return
				}
				logCtx.Tracef("Grabbed an item")
				if item == nil {
					return
				}

				ev, ok := item.(event.Event)
				if !ok {
					logCtx.Warnf("invalid data in sendqueue")
					continue
				}

				logCtx.Tracef("Sending an item to the event stream")
				// A Send() on the stream is actually not blocking.
				err := stream.Send(&eventstreamapi.Event{Event: int32(ev.Type), Application: ev.Application})
				// TODO: How to handle errors on send?
				if err != nil {
					status, ok := status.FromError(err)
					if !ok {
						logCtx.Errorf("Error sending data: %v", err)
						continue
					}
					if status.Code() == codes.Unavailable {
						logCtx.Info("Agent has closed the connection during send, closing send loop")
						a.cancelFn()
						return
					}
				}
			}
		}

	}()

	for {
		select {
		case <-a.context.Done():
			return nil
		case <-syncCh:
			log().WithField("componet", "EventHandller").Info("Stream closed")
			return nil
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}
