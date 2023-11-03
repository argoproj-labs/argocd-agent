package principal

import (
	"context"
	"encoding/json"

	middleware "github.com/grpc-ecosystem/go-grpc-middleware/v2"
	"github.com/jannfis/argocd-agent/internal/auth"
	"github.com/jannfis/argocd-agent/pkg/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// authenticate is used as a gRPC interceptor to decide whether a request is
// authenticated or not. If the request is authenticated, authenticate will
// also augment the Context of the request with additional information about
// the client, that can later be evaluated by the server's RPC methods and
// streams.
//
// If the request turns out to be unauthenticated, authenticate will
// return an appropriate error.
func (s *Server) authenticate(ctx context.Context) (context.Context, error) {
	logCtx := log().WithField("module", "AuthHandler")
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "could not get metadata from request")
	}
	jwt, ok := md["authorization"]
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authentication data found")
	}
	claims, err := s.issuer.ValidateAccessToken(jwt[0])
	if err != nil {
		logCtx.Warnf("Error validating token: %v", err)
		return nil, status.Error(codes.Unauthenticated, "invalid authentication data")
	}

	subject, err := claims.GetSubject()
	if err != nil {
		logCtx.Warnf("Could not get subject from token: %v", err)
		return nil, status.Error(codes.Unauthenticated, "invalid authentication data")
	}

	var agentInfo auth.AuthSubject
	err = json.Unmarshal([]byte(subject), &agentInfo)
	if err != nil {
		logCtx.Warnf("Could not unmarshal subject from token: %v", err)
		return nil, status.Error(codes.Unauthenticated, "invalid authentication data")
	}

	// claims at this point is validated and we can propagate values to the
	// context.
	authCtx := context.WithValue(ctx, types.ContextAgentIdentifier, agentInfo.ClientID)
	if !s.queues.HasQueuePair(agentInfo.ClientID) {
		logCtx.Tracef("Creating a new queue pair for client %s", agentInfo.ClientID)
		s.queues.Create(agentInfo.ClientID)
	} else {
		logCtx.Tracef("Reusing existing queue pair for client %s", agentInfo.ClientID)
	}
	mode := types.AgentModeFromString(agentInfo.Mode)
	if mode == types.AgentModeNone {
		return nil, status.Error(codes.Unauthenticated, "invalid operation mode")
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
	return handler(newCtx, nil)
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
