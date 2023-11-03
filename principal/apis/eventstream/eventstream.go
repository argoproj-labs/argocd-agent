package eventstream

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/jannfis/argocd-agent/internal/event"
	"github.com/jannfis/argocd-agent/internal/queue"
	"github.com/jannfis/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/jannfis/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	eventstreamapi.UnimplementedEventStreamServer

	options *ServerOptions
	queues  queue.QueuePair
}

type ServerOptions struct {
	MaxStreamDuration time.Duration
}

type ServerOption func(o *ServerOptions)

func WithMaxStreamDuration(d time.Duration) ServerOption {
	return func(o *ServerOptions) {
		o.MaxStreamDuration = d
	}
}

// NewServer returns a new AppStream server instance with the given options
func NewServer(queues queue.QueuePair, opts ...ServerOption) *Server {
	options := &ServerOptions{}
	for _, o := range opts {
		o(options)
	}
	return &Server{
		queues:  queues,
		options: options,
	}
}

// agentName gets the agent name from the context ctx. If no agent identifier
// could be found in the context, returns an error.
func agentName(ctx context.Context) (string, error) {
	agentName, ok := ctx.Value(types.ContextAgentIdentifier).(string)
	if !ok {
		return "", fmt.Errorf("invalid context: no agent name")
	}
	return agentName, nil
}

// Subscribe implements a bi-directional stream to exchange application updates
// between the agent and the server.
//
// The connection is kept open until the agent closes it, and the stream tries
// to send updates to the agent as long as possible.
func (s *Server) Subscribe(subs eventstreamapi.EventStream_SubscribeServer) error {
	logCtx := log().WithField("method", "Subscribe")

	var ctx context.Context
	var cancelFn context.CancelFunc

	var wg sync.WaitGroup

	// The stream can have on optional expiry time
	if s.options.MaxStreamDuration > 0 {
		logCtx.Tracef("StreamContext expires in %v", s.options.MaxStreamDuration)
		ctx, cancelFn = context.WithTimeout(subs.Context(), s.options.MaxStreamDuration)
	} else {
		logCtx.Trace("StreamContext does not expire ")
		ctx, cancelFn = context.WithCancel(subs.Context())
	}
	defer cancelFn()

	agentName, err := agentName(ctx)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	logCtx = logCtx.WithField("client", agentName)

	logCtx.Debug("New client connection")

	// We receive events in a dedicated go routine
	wg.Add(1)
	go func() {
		defer func() {
			s.queues.Delete(agentName, true)
			wg.Done()
		}()
		logCtx := logCtx.WithField("direction", "recv")
		logCtx.Trace("Starting event receiver routine")
		for {
			select {
			case <-ctx.Done():
				logCtx.Info("Stopping event receiver routine")
				return
			default:
				logCtx.Tracef("Waiting to receive from channel")
				u, err := subs.Recv()
				if err != nil {
					if err == io.EOF {
						logCtx.Tracef("Remote end hung up")
					} else {
						st, ok := status.FromError(err)
						if ok {
							if st.Code() != codes.DeadlineExceeded && st.Code() != codes.Canceled {
								logCtx.WithError(err).Error("Error receiving application update")
							} else if st.Code() == codes.Canceled {
								logCtx.Trace("Context was canceled")
							}
						} else {
							logCtx.WithError(err).Error("Unknown error")
						}
					}
					cancelFn()
					continue
				}
				logCtx.Infof("Received update for application %v (%p)", u.Application.QualifiedName(), u.Application)
				q := s.queues.RecvQ(agentName)
				if q == nil {
					logCtx.Warnf("I have no receive queue for agent")
					cancelFn()
					continue
				}

				ev := &event.Event{
					Type:        event.EventType(u.Event),
					Application: u.Application,
				}

				q.Add(ev)
			}
		}
	}()

	// We send events in a dedicated go routine
	wg.Add(1)
	go func() {
		defer func() {
			s.queues.Delete(agentName, true)
			wg.Done()
		}()
		logCtx := logCtx.WithFields(logrus.Fields{
			"direction": "send",
			"queue":     agentName,
		})
		logCtx.Tracef("Starting event sender routine")
		for {
			select {
			case <-ctx.Done():
				logCtx.Info("Stopping event sender routine")
				return
			default:
				q := s.queues.SendQ(agentName)
				if q == nil {
					logCtx.Warnf("Send queue for agent disappeared")
					cancelFn()
					continue
				}
				// Get() is blocking until there is at least one item in the
				// queue.
				logCtx.Tracef("Waiting to grab an item from the queue")
				item, shutdown := q.Get()
				if shutdown {
					logCtx.Tracef("Queue shutdown in progress")
					cancelFn()
					continue
				}
				logCtx.Tracef("Grabbed an item")
				if item == nil {
					cancelFn()
					continue
				}

				ev, ok := item.(event.Event)
				if !ok {
					logCtx.Warnf("Invalid data in sendqueue. Want: %T, have %T", event.Event{}, item)
					cancelFn()
					continue
				}

				logCtx.Tracef("Sending an item to the event stream")
				// A Send() on the stream is actually not blocking.
				err := subs.Send(&eventstreamapi.Event{Event: int32(ev.Type), Application: ev.Application})
				// TODO: How to handle errors on send?
				if err != nil {
					status, ok := status.FromError(err)
					if !ok && err != io.EOF {
						logCtx.WithError(err).Error("Error sending data")
						cancelFn()
						continue
					}
					if err == io.EOF || status.Code() == codes.Unavailable {
						logCtx.Debug("Send() returned EOF or codes.Unavailable")
						cancelFn()
						continue
					}
				}
			}
		}
	}()

	wg.Wait()
	logCtx.Info("Closing EventStream")
	return nil
}

// Push implements a client-side stream to receive updates for the client's
// Application resources.
func (s *Server) Push(pushs eventstreamapi.EventStream_PushServer) error {
	logCtx := log().WithField("method", "Push")

	var ctx context.Context
	var cancel context.CancelFunc
	if s.options.MaxStreamDuration > 0 {
		logCtx.Debugf("Setting timeout to %v", s.options.MaxStreamDuration)
		ctx, cancel = context.WithTimeout(pushs.Context(), s.options.MaxStreamDuration)
	} else {
		ctx, cancel = context.WithCancel(pushs.Context())
	}
	defer cancel()

	agentName, err := agentName(ctx)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	logCtx = logCtx.WithField("client", agentName)
	logCtx.Debug("A new client connected to the event stream")

	summary := &eventstreamapi.PushSummary{}

recvloop:
	for {
		u, err := pushs.Recv()
		if err != nil {
			st, ok := status.FromError(err)
			if ok {
				logCtx.Errorf("Error receiving event: %s", st.String())
			} else if err == io.EOF {
				logCtx.Infof("Client disconnected from stream")
			} else {
				logCtx.WithError(err).Errorf("Unexpected error")
			}
			break recvloop
		}
		select {
		case <-ctx.Done():
			logCtx.Infof("Context canceled")
			break recvloop
		default:
			logCtx.Infof("Received update for: %s", u.Application.QualifiedName())
			// In the Push stream, only application updates will be processed.
			// However, depending on configuration, an application update that
			// is observed may result in the creation of this particular app
			// in the server's application backend.
			ev := &event.Event{
				Type:        event.EvenAppStatusUpdated,
				Application: u.Application,
			}
			s.queues.RecvQ(agentName).Add(ev)
			summary.Received += 1
		}
	}

	logCtx.Infof("Sending summary to agent")
	err = pushs.SendAndClose(summary)
	if err != nil {
		logCtx.Errorf("Error sending summary: %v", err)
	}

	return nil
}

func log() *logrus.Entry {
	return logrus.WithField("module", "grpc.AppStream")
}
