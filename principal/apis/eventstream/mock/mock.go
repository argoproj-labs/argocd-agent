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

package mock

import (
	"context"
	"sync/atomic"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"google.golang.org/grpc"
)

// SendHook is a function that will be executed for the Send call in the mock
type SendHook func(s *MockEventServer, sub *eventstreamapi.Event) error

// RecvHook is a function that will be executed for the Recv call in the mock
type RecvHook func(s *MockEventServer) error

// MockEventServer implements a mock for the SubscriptionServer stream
// used for testing.
type MockEventServer struct {
	grpc.ServerStream

	AgentName   string
	NumSent     atomic.Uint32
	NumRecv     atomic.Uint32
	Application v1alpha1.Application
	RecvHooks   []RecvHook
	SendHooks   []SendHook
}

func NewMockEventServer() *MockEventServer {
	return &MockEventServer{AgentName: "default"}
}

func (s *MockEventServer) AddSendHook(hook SendHook) {
	s.SendHooks = append(s.SendHooks, hook)
}

func (s *MockEventServer) AddRecvHook(hook RecvHook) {
	s.RecvHooks = append(s.RecvHooks, hook)
}

func (s *MockEventServer) Context() context.Context {
	if s.AgentName != "" {
		return context.WithValue(context.TODO(), types.ContextAgentIdentifier, s.AgentName)
	} else {
		return context.TODO()
	}
}

func (s *MockEventServer) Send(sub *eventstreamapi.Event) error {
	var err error
	for _, h := range s.SendHooks {
		if err = h(s, sub); err != nil {
			break
		}
	}
	if err == nil {
		s.NumSent.Add(1)
	}
	return err
}

func (s *MockEventServer) Recv() (*eventstreamapi.Event, error) {
	var err error
	for _, h := range s.RecvHooks {
		if err = h(s); err != nil {
			break
		}
	}
	if err == nil {
		s.NumRecv.Add(1)
		wev := event.NewEventSource("foo").ApplicationEvent(event.Create, &s.Application)
		pev, perr := format.ToProto(wev)
		if perr == nil {
			return &eventstreamapi.Event{Event: pev}, nil
		} else {
			return nil, perr
		}
	}

	return nil, err
}
