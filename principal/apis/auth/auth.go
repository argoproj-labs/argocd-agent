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

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/issuer"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/internal/version"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/authapi"
	"github.com/argoproj-labs/argocd-agent/principal/registration"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	authapi.UnimplementedAuthenticationServer
	authMethods *auth.Methods
	issuer      issuer.Issuer
	options     *ServerOptions
	queues      *queue.SendRecvQueues

	agentRegistrationManager *registration.AgentRegistrationManager

	// principalVersion is the version of the principal, used for handshake validation
	principalVersion string
}

const (
	accessTokenValidity     = 5 * time.Minute
	refreshTokenValidity    = 24 * time.Hour
	refreshTokenAutoRefresh = 10 * time.Minute
)

const (
	authFailedMessage = "authentication failed"
)

var errAuthenticationFailed = status.Error(codes.Unauthenticated, authFailedMessage)

type ServerOptions struct {
	agentRegistrationManager *registration.AgentRegistrationManager
}

type ServerOption func(o *ServerOptions) error

// NewServer creates a new instance of an authentication server with the given
// authentication methods and options.
func NewServer(queues *queue.SendRecvQueues, authMethods *auth.Methods, iss issuer.Issuer, opts ...ServerOption) (*Server, error) {
	s := &Server{}
	s.options = &ServerOptions{}
	if authMethods != nil {
		s.authMethods = authMethods
	} else {
		s.authMethods = auth.NewMethods()
	}
	s.queues = queues
	s.issuer = iss
	for _, o := range opts {
		err := o(s.options)
		if err != nil {
			return nil, err
		}
	}

	s.agentRegistrationManager = s.options.agentRegistrationManager
	s.principalVersion = version.New("argocd-agent").Version()
	return s, nil
}

func (s *Server) issueTokens(subject *auth.AuthSubject, refresh bool) (accessToken string, refreshToken string, err error) {
	subj, err := json.Marshal(subject)
	if err != nil {
		return "", "", fmt.Errorf("could not render subject to JSON: %w", err)
	}
	accessToken, err = s.issuer.IssueAccessToken(string(subj), accessTokenValidity)
	if err != nil {
		return "", "", status.Error(codes.Internal, "unable to generate a token")
	}
	if refresh {
		refreshToken, err = s.issuer.IssueRefreshToken(string(subj), refreshTokenValidity)
		if err != nil {
			return "", "", status.Error(codes.Internal, "unable to generate a token")
		}
	}
	return accessToken, refreshToken, nil
}

// Authenticate provides an authz endpoint for the Server. The client is
// supposed to specify the authentication method and the credentials to use.
//
// A Server may support one or more authentication methods, and if the authz
// request succeeds, a JWT will be issued to the client.
//
// This method also performs version handshake validation. The agent must send
// its version number, and if it doesn't match the principal's version exactly,
// the authentication will be rejected.
func (s *Server) Authenticate(ctx context.Context, ar *authapi.AuthRequest) (*authapi.AuthResponse, error) {
	logCtx := log().WithField("method", "Authenticate").WithField("authmethod", ar.Method)

	switch ar.Mode {
	case "managed", "autonomous":
		break
	default:
		return nil, fmt.Errorf("unknown or missing operation mode: '%s'", ar.Mode)
	}
	am := s.authMethods.Method(ar.Method)
	if am == nil {
		logCtx.Info("unknown authentication method")
		return nil, errAuthenticationFailed
	}
	clientID, err := am.Authenticate(ctx, ar.Credentials)
	if clientID == "" || err != nil {
		logCtx.WithError(err).WithField("client", clientID).Info("client authentication failed")
		return nil, errAuthenticationFailed
	}

	agentVersion := ar.Version

	if agentVersion == "" {
		logCtx.Warn("Agent did not provide version information")
		return nil, status.Error(codes.InvalidArgument, "agent version is required")
	}

	if agentVersion != s.principalVersion {
		logCtx.Warnf("Version mismatch: rejecting connection (agent: %s, principal: %s)", agentVersion, s.principalVersion)
		return nil, status.Errorf(codes.FailedPrecondition, "version mismatch")
	}

	logCtx.WithField("client", clientID).WithField("agent_version", agentVersion).Info("client authentication successful")

	// If self agent registration is enabled, register the agent and create cluster secret if it doesn't exist
	if s.agentRegistrationManager != nil && s.agentRegistrationManager.IsSelfAgentRegistrationEnabled() {
		if err := s.agentRegistrationManager.RegisterAgent(ctx, clientID); err != nil {
			logCtx.WithError(err).WithField("client", clientID).Error("Failed to register agent")
			return nil, errAuthenticationFailed
		}
	}

	subject := &auth.AuthSubject{ClientID: clientID, Mode: ar.Mode}
	accessToken, refreshToken, err := s.issueTokens(subject, true)
	if err != nil {
		logCtx.WithError(err).Warnf("Unable to generate token")
		return nil, errAuthenticationFailed
	}
	if !s.queues.HasQueuePair(clientID) {
		err = s.queues.Create(clientID)
		if err != nil {
			return nil, err
		}
	}
	return &authapi.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Version:      s.principalVersion,
	}, nil
}

// RefreshToken issues a new access token when the client presents a valid
// refresh token. If the refresh token is only valid for 10 minutes or less,
// a new refresh token will be issued as well.
func (s *Server) RefreshToken(ctx context.Context, r *authapi.RefreshTokenRequest) (*authapi.AuthResponse, error) {
	logCtx := log().WithField("method", "RefreshToken")
	if r.RefreshToken == "" {
		logCtx.Warn("No refresh token supplied")
		return nil, errAuthenticationFailed
	}

	c, err := s.issuer.ValidateRefreshToken(r.RefreshToken)
	if err != nil {
		logCtx.WithError(err).Warnf("Could not validate refresh token")
		return nil, errAuthenticationFailed
	}

	// We need the subject of the refresh token to issue a new one
	subj, err := c.GetSubject()
	if err != nil {
		logCtx.WithError(err).Warnf("Could not get subject from refresh token")
		return nil, errAuthenticationFailed
	}
	subject := &auth.AuthSubject{}
	err = json.Unmarshal([]byte(subj), subject)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal subject: %w", err)
	}

	// We only want to issue a new refresh token when the old one is close to
	// expiry.
	exp, err := c.GetExpirationTime()
	if err != nil {
		logCtx.WithError(err).Warnf("Could not get exp from refresh token")
		return nil, errAuthenticationFailed
	}

	// Only issue a new refresh token when the old one is close to expiry
	refresh := time.Until(exp.Time) < refreshTokenAutoRefresh

	accessToken, refreshToken, err := s.issueTokens(subject, refresh)
	if err != nil {
		logCtx.WithError(err).WithField("refresh", refresh).Warnf("Could not issue a new token")
		return nil, errAuthenticationFailed
	}
	return &authapi.AuthResponse{AccessToken: accessToken, RefreshToken: refreshToken}, nil
}

func log() *logrus.Entry {
	return logging.GetDefaultLogger().ModuleLogger("grpc.AuthenticationServer")
}
