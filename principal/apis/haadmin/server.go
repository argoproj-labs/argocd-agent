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

package haadmin

import (
	"context"

	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/haadminapi"
	"github.com/argoproj-labs/argocd-agent/pkg/ha"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StatusProvider supplies replication metrics to the status RPC
type StatusProvider interface {
	ConnectedReplicaCount() int
	LastEventTimestamp() int64
	LastSequenceNum() uint64
	ConnectedAgentCount() int
}

// Server implements the HAAdmin gRPC service
type Server struct {
	haadminapi.UnimplementedHAAdminServer

	controller     *ha.Controller
	statusProvider StatusProvider
}

// NewServer creates a new HAAdmin gRPC server
func NewServer(controller *ha.Controller, sp StatusProvider) *Server {
	return &Server{
		controller:     controller,
		statusProvider: sp,
	}
}

func (s *Server) Status(_ context.Context, _ *haadminapi.StatusRequest) (*haadminapi.HAStatusResponse, error) {
	if s.controller == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "HA not configured")
	}

	st := s.controller.GetStatus()
	resp := &haadminapi.HAStatusResponse{
		State:         string(st.State),
		PreferredRole: string(st.PreferredRole),
		PeerAddress:   st.PeerAddress,
		PeerReachable: st.PeerReachable,
		PeerState:     string(st.PeerState),
	}

	if s.statusProvider != nil {
		resp.ConnectedReplicas = int32(s.statusProvider.ConnectedReplicaCount())
		resp.LastEventTimestamp = s.statusProvider.LastEventTimestamp()
		resp.LastSequenceNum = s.statusProvider.LastSequenceNum()
		resp.ConnectedAgents = int32(s.statusProvider.ConnectedAgentCount())
	}

	return resp, nil
}

func (s *Server) Promote(ctx context.Context, req *haadminapi.PromoteRequest) (*haadminapi.PromoteResponse, error) {
	if s.controller == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "HA not configured")
	}

	if err := s.controller.Promote(ctx, req.Force); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%s", err)
	}

	return &haadminapi.PromoteResponse{
		State: string(s.controller.State()),
	}, nil
}

func (s *Server) Demote(ctx context.Context, _ *haadminapi.DemoteRequest) (*haadminapi.DemoteResponse, error) {
	if s.controller == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "HA not configured")
	}

	if err := s.controller.Demote(ctx); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%s", err)
	}

	return &haadminapi.DemoteResponse{
		State: string(s.controller.State()),
	}, nil
}
