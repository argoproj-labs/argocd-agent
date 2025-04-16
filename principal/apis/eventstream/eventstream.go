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

	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/internal/session"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
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

	eventWriters *event.EventWritersMap

	metrics    *metrics.PrincipalMetrics
	clusterMgr *cluster.Manager
}

type ServerOptions struct {
	MaxStreamDuration time.Duration
	notifyOnConnect   chan types.Agent
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

func WithNotifyOnConnect(notify chan types.Agent) ServerOption {
	return func(o *ServerOptions) {
		o.notifyOnConnect = notify
	}
}

// NewServer returns a new AppStream server instance with the given options
func NewServer(queues queue.QueuePair, eventWriters *event.EventWritersMap, metrics *metrics.PrincipalMetrics, clusterMgr *cluster.Manager, opts ...ServerOption) *Server {
	options := &ServerOptions{}
	for _, o := range opts {
		o(options)
	}
	return &Server{
		queues:       queues,
		options:      options,
		eventWriters: eventWriters,
		metrics:      metrics,
		clusterMgr:   clusterMgr,
	}
}

// newClientConnection returns a new client object to be used to read from and
// send to the subscription stream.
func (s *Server) newClientConnection(ctx context.Context, timeout time.Duration) (*client, error) {
	c := &client{}
	c.wg = &sync.WaitGroup{}

	agentName, err := session.ClientIdFromContext(ctx)
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

	s.clusterMgr.UpdateClusterConnectionInfo(c.agentName, v1alpha1.ConnectionStatusSuccessful, c.start)

	c.logCtx.Info("An agent connected to the subscription stream")
	return c, nil
}

// onDisconnect must be called whenever client c disconnects from the stream
func (s *Server) onDisconnect(c *client) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.end = time.Now()

	s.clusterMgr.UpdateClusterConnectionInfo(c.agentName, v1alpha1.ConnectionStatusFailed, c.end)

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
	streamEvent, err := subs.Recv()
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
	if streamEvent == nil || streamEvent.Event == nil {
		return fmt.Errorf("invalid wire transmission")
	}

	app := &v1alpha1.Application{}
	proj := &v1alpha1.AppProject{}
	resResp := &event.ResourceResponse{}
	incomingEvent, err := format.FromProto(streamEvent.Event)
	if err != nil {
		return fmt.Errorf("could not unserialize event from wire: %w", err)
	}

	logCtx = logCtx.WithFields(logrus.Fields{
		"resource_id":  event.ResourceID(incomingEvent),
		"event_id":     event.EventID(incomingEvent),
		"event_target": incomingEvent.DataSchema(),
		"event_type":   incomingEvent.Type(),
	})

	switch event.Target(incomingEvent) {
	case event.TargetApplication:
		err = incomingEvent.DataAs(app)
	case event.TargetAppProject:
		err = incomingEvent.DataAs(proj)
	case event.TargetResource:
		err = incomingEvent.DataAs(resResp)
	}
	if err != nil {
		return fmt.Errorf("could not unserialize app data from wire: %w", err)
	}
	logCtx.Infof("Received update for application %v", app.QualifiedName())
	q := s.queues.RecvQ(c.agentName)
	if q == nil {
		return fmt.Errorf("panic: no recvq for agent %s", c.agentName)
	}

	if event.Target(incomingEvent) == event.TargetEventAck {
		logCtx.Trace("Received an ACK")
		eventWriter := s.eventWriters.Get(c.agentName)
		if eventWriter == nil {
			return fmt.Errorf("panic: event writer not found for agent %s", c.agentName)
		}
		eventWriter.Remove(incomingEvent)
		logCtx.Trace("Removed the ACK from the event writer")
		return nil
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
	ev, shutdown := queue.GetWithContext(q, c.ctx)
	if shutdown {
		return fmt.Errorf("sendq shutdown in progress")
	}
	if c.ctx.Err() != nil {
		return c.ctx.Err()
	}

	if ev == nil {
		return fmt.Errorf("panic: nil item in queue")
	}
	logCtx.WithField("event_target", ev.DataSchema()).WithField("event_type", ev.Type()).Trace("Grabbed an item")

	mode, err := session.ClientModeFromContext(c.ctx)
	if err != nil {
		return fmt.Errorf("unable to determine agent mode: %w", err)
	}

	if types.AgentModeFromString(mode) != types.AgentModeManaged {
		// Only Update events are valid for unmanaged agents
		if ev.Type() == event.Create.String() || ev.Type() == event.Delete.String() {
			logCtx.WithField("type", ev.Type()).Debug("Discarding event for unmanaged agent")
			return nil
		}
	}

	eventWriter := s.eventWriters.Get(c.agentName)
	if eventWriter == nil {
		return fmt.Errorf("panic: event writer not found for agent %s", c.agentName)
	}

	logCtx.WithFields(logrus.Fields{
		"resource_id": event.ResourceID(ev),
		"event_id":    event.EventID(ev),
		"type":        ev.Type(),
	}).Trace("Adding an event to the event writer")
	eventWriter.Add(ev)

	q.Done(ev)

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

	if s.metrics != nil {
		// increase counter when an agent is connected with principal
		s.metrics.AgentConnected.Inc()

		// store connection time to find average connection time of all agents
		metrics.SetAgentConnectionTime(c.agentName, c.start)
	}

	eventWriter := s.eventWriters.Get(c.agentName)
	if eventWriter != nil {
		eventWriter.UpdateTarget(subs)
	} else {
		eventWriter = event.NewEventWriter(subs)
		s.eventWriters.Add(c.agentName, eventWriter)
	}

	go eventWriter.SendWaitingEvents(c.ctx)

	// Notify to run handlers for the newly connected agent
	if s.options.notifyOnConnect != nil {
		mode, err := session.ClientModeFromContext(c.ctx)
		if err != nil {
			return err
		}
		s.options.notifyOnConnect <- types.NewAgent(c.agentName, mode)
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

				if s.metrics != nil {
					// count no of events received from agent
					s.metrics.EventReceived.Inc()
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

				if s.metrics != nil {
					// count no of events sent to agent
					s.metrics.EventSent.Inc()
				}
			}
		}
	}()

	c.wg.Wait()
	c.logCtx.Info("Closing EventStream")

	if s.metrics != nil {
		// decrease counter when an agent is disconnected with principal
		s.metrics.AgentConnected.Dec()

		// remove connection time of agent
		metrics.DeleteAgentConnectionTime(c.agentName)
	}

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

	agentName, err := session.ClientIdFromContext(ctx)
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
