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

package eventstream

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	cloudevents "github.com/cloudevents/sdk-go/v2"
)

var _ eventstreamapi.EventStreamServer = &Server{}

// Server:
// - Reads Application CR events from GRPC stream and writes them to the relevant agent receive queue (the 'inbox') in 'queues' for processing (see recvFunc)
// - Reads Application CR events from agent send queue in 'queues' (the 'outbox'), and writes to GRPC stream (see sendFunc)
//
// Server implements the pkg/api/grpc/evenstreamapi/EventStreamServer interface
//
// Server is only used by principal.
type Server struct {
	eventstreamapi.UnimplementedEventStreamServer

	options *ServerOptions
	queues  queue.QueuePair
}

type ServerOptions struct {
	MaxStreamDuration time.Duration
}

type ServerOption func(o *ServerOptions)

type client struct {
	ctx       context.Context
	cancelFn  context.CancelFunc
	logCtx    *logrus.Entry
	agentName string
	wg        *sync.WaitGroup
	start     time.Time
	// lock must be owned before read/writing to 'end' var
	end  time.Time
	lock sync.RWMutex
}

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

// newClientConnection returns a new client object to be used to read from and
// send to the subscription stream.
func (s *Server) newClientConnection(ctx context.Context, timeout time.Duration) (*client, error) {
	c := &client{}
	c.wg = &sync.WaitGroup{}

	agentName, err := agentName(ctx)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	c.agentName = agentName

	c.logCtx = logrus.WithFields(logrus.Fields{
		"method": "Subscribe",
		"client": c.agentName,
	})

	// The stream can have on optional expiry time
	if timeout > 0 {
		c.logCtx.Tracef("StreamContext expires in %v", timeout)
		c.ctx, c.cancelFn = context.WithTimeout(ctx, timeout)
	} else {
		c.logCtx.Trace("StreamContext does not expire ")
		c.ctx, c.cancelFn = context.WithCancel(ctx)
	}
	c.start = time.Now()
	c.logCtx.Info("An agent connected to the subscription stream")
	return c, nil
}

// agentName gets the agent name from the context ctx. If no agent identifier
// could be found in the context, returns an error.
func agentName(ctx context.Context) (string, error) {
	agentName, ok := ctx.Value(types.ContextAgentIdentifier).(string)
	if !ok {
		return "", fmt.Errorf("invalid context: no agent name")
	}
	// TODO: check agentName for validity
	return agentName, nil
}

// onDisconnect must be called whenever client c disconnects from the stream
func (s *Server) onDisconnect(c *client) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.end = time.Now()
	if s.queues.HasQueuePair(c.agentName) {
		err := s.queues.Delete(c.agentName, true)
		if err != nil {
			log().Warnf("Could not delete agent queue %s: %v", c.agentName, err)
		}
	}
	c.wg.Done()
}

// recvFunc retrieves exactly one message from the client c on the event stream
// sub. The function will block until a message is available on the stream.
//
// This function must NOT be called concurrently.
//
// On success, it adds the message in cloudevents format to the internal event
// queue for further processing and returns nil.
//
// Any error that occurs during receive or processing will be returned to the
// caller.
func (s *Server) recvFunc(c *client, subs eventstreamapi.EventStream_SubscribeServer) error {
	logCtx := c.logCtx.WithField("direction", "recv")
	logCtx.Tracef("Waiting to receive from channel")
	event, err := subs.Recv()
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
		return err
	}
	if event == nil || event.Event == nil {
		return fmt.Errorf("invalid wire transmission")
	}
	app := &v1alpha1.Application{}
	incomingEvent, err := format.FromProto(event.Event)
	if err != nil {
		return fmt.Errorf("could not unserialize event from wire: %w", err)
	}
	err = incomingEvent.DataAs(app)
	if err != nil {
		return fmt.Errorf("could not unserialize app data from wire: %w", err)
	}
	logCtx.Infof("Received update for application %v", app.QualifiedName())
	q := s.queues.RecvQ(c.agentName)
	if q == nil {
		return fmt.Errorf("panic: no recvq for agent %s", c.agentName)
	}

	q.Add(incomingEvent)
	return nil
}

// sendFunc gets the next event from the internal event queue, transforms it
// into wire format and sends it via the eventstream sub to client c. The
// function will block until there is an event on the internal event queue.
//
// This function must NOT be called concurrently.
//
// If an error occurs, it will be returned to the caller. Otherwise, nil is
// returned.
func (s *Server) sendFunc(c *client, subs eventstreamapi.EventStream_SubscribeServer) error {
	logCtx := c.logCtx.WithField("direction", "send")
	q := s.queues.SendQ(c.agentName)
	if q == nil {
		return fmt.Errorf("panic: no sendq for agent %s", c.agentName)
	}

	// Get() is blocking until there is at least one item in the
	// queue.
	logCtx.Tracef("Waiting to grab an item from the queue")
	item, shutdown := q.Get()
	if shutdown {
		return fmt.Errorf("sendq shutdown in progress")
	}
	logCtx.Tracef("Grabbed an item")
	if item == nil {
		return fmt.Errorf("panic: nil item in queue")
	}

	ev, ok := item.(*cloudevents.Event)
	if !ok {
		return fmt.Errorf("panic: invalid data in sendqueue: want: %T, have %T", cloudevents.Event{}, item)
	}

	prEv, err := format.ToProto(ev)
	if err != nil {
		return fmt.Errorf("panic: could not serialize event to wire format: %v", err)
	}

	q.Done(item)

	logCtx.Tracef("Sending an item to the event stream")

	// A Send() on the stream is actually not blocking.
	err = subs.Send(&eventstreamapi.Event{Event: prEv})
	if err != nil {
		status, ok := status.FromError(err)
		if !ok && err != io.EOF {
			return fmt.Errorf("error sending data to stream: %w", err)
		}
		if err == io.EOF || status.Code() == codes.Unavailable {
			return fmt.Errorf("remote hung up while sending data to stream: %w", err)
		}
	}

	return nil
}

// Subscribe implements a bi-directional stream to exchange application updates
// between the agent and the server.
//
// The connection is kept open until the agent closes it, and the stream tries
// to send updates to the agent as long as possible.
//
// Subscribe is called by GRPC machinery.
func (s *Server) Subscribe(subs eventstreamapi.EventStream_SubscribeServer) error {
	c, err := s.newClientConnection(subs.Context(), s.options.MaxStreamDuration)
	if err != nil {
		return err
	}

	// We receive events in a dedicated go routine
	c.wg.Add(1)
	go func() {
		defer s.onDisconnect(c)
		c.logCtx.Trace("Starting event receiver routine")
		for {
			select {
			case <-c.ctx.Done():
				c.logCtx.Info("Stopping event receiver routine")
				return
			default:
				err := s.recvFunc(c, subs)
				if err != nil {
					c.logCtx.Infof("Receiver disconnected: %v", err)
					c.cancelFn()
				}
			}
		}
	}()

	// We send events in a dedicated go routine
	c.wg.Add(1)
	go func() {
		defer s.onDisconnect(c)
		c.logCtx.Tracef("Starting event sender routine")
		for {
			select {
			case <-c.ctx.Done():
				c.logCtx.Info("Stopping event sender routine")
				return
			default:
				err := s.sendFunc(c, subs)
				if err != nil {
					c.logCtx.Infof("Send: %v", err)
					c.cancelFn()
				}

			}
		}
	}()

	c.wg.Wait()
	c.logCtx.Info("Closing EventStream")
	return nil
}

// Push implements a client-side stream to receive updates for the client's
// Application resources.
// Push is called by GRPC machinery.
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
			// In the Push stream, only application updates will be processed.
			// However, depending on configuration, an application update that
			// is observed may result in the creation of this particular app
			// in the server's application backend.
			ev, err := format.FromProto(u.Event)
			if err != nil {
				logCtx.Errorf("Could not deserialize event from wire: %v", err)
				continue
			}
			app := &v1alpha1.Application{}
			err = ev.DataAs(app)
			if err != nil {
				logCtx.Errorf("Could not deserialize application from event: %v", err)
				continue
			}
			logCtx.Infof("Received update for: %s", app.QualifiedName())
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
