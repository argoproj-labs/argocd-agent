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
	"encoding/json"
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/grpcutil"
	"github.com/argoproj-labs/argocd-agent/internal/session"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	middleware "github.com/grpc-ecosystem/go-grpc-middleware/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// clientCertificateMatches checks whether the client certificate credentials
func (s *Server) clientCertificateMatches(ctx context.Context, match string) error {
	logCtx := log().WithField("client_addr", grpcutil.AddressFromContext(ctx))
	if !s.options.clientCertSubjectMatch {
		logCtx.Debug("No client cert subject matching requested")
		return nil
	}
	// Skip TLS checks in plaintext mode (e.g., when running behind Istio)
	if s.options.insecurePlaintext {
		logCtx.Debug("Skipping client cert matching in plaintext mode")
		return nil
	}
	c, ok := peer.FromContext(ctx)
	if !ok {
		return fmt.Errorf("could not get peer from context")
	}
	tls, ok := c.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return fmt.Errorf("connection requires TLS credentials but has none")
	}
	if len(tls.State.VerifiedChains) < 1 {
		return fmt.Errorf("no verified certificates found in TLS cred")
	}
	cn := tls.State.VerifiedChains[0][0].Subject.CommonName
	if match != cn {
		return fmt.Errorf("the TLS subject '%s' does not match agent name '%s'", cn, match)
	}

	logCtx.WithField("client_name", cn).Infof("Successful match of client cert subject '%s'", cn)

	// Subject has been matched
	return nil
}

// unauthenticated is a wrapper function to return a gRPC unauthenticated
// response to the caller.
func unauthenticated() (context.Context, error) {
	return nil, status.Error(codes.Unauthenticated, "invalid authentication data")
}

// authenticate is used as a gRPC interceptor to decide whether a request is
// authenticated or not. If the request is authenticated, authenticate will
// also augment the Context of the request with additional information about
// the client, that can later be evaluated by the server's RPC methods and
// streams.
//
// If the request turns out to be unauthenticated, authenticate will
// return an appropriate error.
func (s *Server) authenticate(ctx context.Context) (context.Context, error) {
	logCtx := log().WithField("module", "AuthHandler").WithField("client", grpcutil.AddressFromContext(ctx))
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		logCtx.Error("No metadata in incoming request")
		return unauthenticated()
	}
	jwt, ok := md["authorization"]
	if !ok {
		logCtx.Error("No authorization header in request")
		return unauthenticated()
	}
	claims, err := s.issuer.ValidateAccessToken(jwt[0])
	if err != nil {
		logCtx.Warnf("Error validating token: %v", err)
		return unauthenticated()
	}

	subject, err := claims.GetSubject()
	if err != nil {
		logCtx.Warnf("Could not get subject from token: %v", err)
		return unauthenticated()
	}

	var agentInfo auth.AuthSubject
	err = json.Unmarshal([]byte(subject), &agentInfo)
	if err != nil {
		logCtx.Warnf("Could not unmarshal subject from token: %v", err)
		return unauthenticated()
	}

	// Reject agents that use the same name as the Argo CD installation namespace
	if agentInfo.ClientID == s.namespace {
		logCtx.Warnf("Agent name '%s' is not allowed as it matches the Argo CD installation namespace. Please use a different agent name.", agentInfo.ClientID)
		return unauthenticated()
	}

	// If we require client certificates, we enforce any potential rules for
	// the certificate here, instead of at time the connection is made.
	if s.options.requireClientCerts {
		if err := s.clientCertificateMatches(ctx, agentInfo.ClientID); err != nil {
			logCtx.Errorf("could not match TLS certificate: %v", err)
			return unauthenticated()
		}
		logCtx.Infof("Matched client cert subject to agent name")
	}

	// claims at this point is validated and we can propagate values to the
	// context.
	authCtx := session.ClientInfoToContext(ctx, agentInfo.ClientID, agentInfo.Mode)

	if !s.queues.HasQueuePair(agentInfo.ClientID) {
		logCtx.Tracef("Creating a new queue pair for client %s", agentInfo.ClientID)
		if err := s.queues.Create(agentInfo.ClientID); err != nil {
			logCtx.Errorf("Cannot authenticate client: Can't create agent queue: %v", err)
			return nil, status.Error(codes.Internal, "internal server error")
		}
	} else {
		logCtx.Tracef("Reusing existing queue pair for client %s", agentInfo.ClientID)
	}
	mode := types.AgentModeFromString(agentInfo.Mode)
	if mode == types.AgentModeUnknown {
		logCtx.Warnf("Client requested invalid operation mode: %s", agentInfo.Mode)
		return unauthenticated()
	}
	s.setAgentMode(agentInfo.ClientID, mode)
	logCtx.WithField("client", agentInfo.ClientID).WithField("mode", agentInfo.Mode).Tracef("Client passed authentication")
	return authCtx, nil
}

// unaryAuthInterceptor is a server interceptor for unary gRPC requests.
//
// It enforces authentication on incoming gRPC calls according to settings of
// Server s. If the called method is in the list of unauthenticated endpoints,
// authentication is skipped.
func (s *Server) unaryAuthInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if _, ok := s.noauth[info.FullMethod]; ok {
		return handler(ctx, req)
	}
	newCtx, err := s.authenticate(ctx)
	if err != nil {
		return nil, err
	}
	return handler(newCtx, req)
}

// streamAuthInterceptor is a server interceptor for streaming gRPC requests.
//
// It enforces authentication on incoming gRPC calls according to settings of
// Server s. If the called method is in the list of unauthenticated endpoints,
// authentication is skipped.
func (s *Server) streamAuthInterceptor(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if _, ok := s.noauth[info.FullMethod]; ok {
		return handler(srv, stream)
	}
	newCtx, err := s.authenticate(stream.Context())
	if err != nil {
		return err
	}
	wrapped := middleware.WrapServerStream(stream)
	wrapped.WrappedContext = newCtx
	return handler(srv, wrapped)
}
