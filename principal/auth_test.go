// Copyright 2025 The argocd-agent Authors
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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"regexp"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/auth/mtls"
	issuermock "github.com/argoproj-labs/argocd-agent/internal/issuer/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/replicationapi"
	"github.com/argoproj-labs/argocd-agent/pkg/ha"
	"github.com/argoproj-labs/argocd-agent/pkg/replication"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	replicationserver "github.com/argoproj-labs/argocd-agent/principal/apis/replication"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func Test_unauthenticated(t *testing.T) {
	t.Run("returns unauthenticated error", func(t *testing.T) {
		ctx, err := unauthenticated()
		assert.Nil(t, ctx)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
		assert.Equal(t, "invalid authentication data", st.Message())
	})
}

func Test_Server_clientCertificateMatches(t *testing.T) {
	tests := []struct {
		name          string
		setupServer   func() *Server
		setupContext  func() context.Context
		clientID      string
		expectedError string
	}{
		{
			name: "client cert matching disabled",
			setupServer: func() *Server {
				return &Server{
					options: &ServerOptions{
						clientCertSubjectMatch: false,
					},
				}
			},
			setupContext: func() context.Context {
				return context.Background()
			},
			clientID:      "test-agent",
			expectedError: "",
		},
		{
			name: "no peer in context",
			setupServer: func() *Server {
				return &Server{
					options: &ServerOptions{
						clientCertSubjectMatch: true,
					},
				}
			},
			setupContext: func() context.Context {
				return context.Background()
			},
			clientID:      "test-agent",
			expectedError: "could not get peer from context",
		},
		{
			name: "no TLS credentials",
			setupServer: func() *Server {
				return &Server{
					options: &ServerOptions{
						clientCertSubjectMatch: true,
					},
				}
			},
			setupContext: func() context.Context {
				p := &peer.Peer{
					Addr:     &mockAddr{},
					AuthInfo: &mockAuthInfo{},
				}
				return peer.NewContext(context.Background(), p)
			},
			clientID:      "test-agent",
			expectedError: "connection requires TLS credentials but has none",
		},
		{
			name: "no verified certificates",
			setupServer: func() *Server {
				return &Server{
					options: &ServerOptions{
						clientCertSubjectMatch: true,
					},
				}
			},
			setupContext: func() context.Context {
				tlsInfo := credentials.TLSInfo{
					State: tls.ConnectionState{
						VerifiedChains: [][]*x509.Certificate{},
					},
				}
				p := &peer.Peer{
					Addr:     &mockAddr{},
					AuthInfo: tlsInfo,
				}
				return peer.NewContext(context.Background(), p)
			},
			clientID:      "test-agent",
			expectedError: "no verified certificates found in TLS cred",
		},
		{
			name: "certificate subject mismatch",
			setupServer: func() *Server {
				return &Server{
					options: &ServerOptions{
						clientCertSubjectMatch: true,
					},
				}
			},
			setupContext: func() context.Context {
				cert := &x509.Certificate{
					Subject: pkix.Name{
						CommonName: "different-agent",
					},
				}
				tlsInfo := credentials.TLSInfo{
					State: tls.ConnectionState{
						VerifiedChains: [][]*x509.Certificate{{cert}},
					},
				}
				p := &peer.Peer{
					Addr:     &mockAddr{},
					AuthInfo: tlsInfo,
				}
				return peer.NewContext(context.Background(), p)
			},
			clientID:      "test-agent",
			expectedError: "the TLS subject 'different-agent' does not match agent name 'test-agent'",
		},
		{
			name: "successful certificate match",
			setupServer: func() *Server {
				return &Server{
					options: &ServerOptions{
						clientCertSubjectMatch: true,
					},
				}
			},
			setupContext: func() context.Context {
				cert := &x509.Certificate{
					Subject: pkix.Name{
						CommonName: "test-agent",
					},
				}
				tlsInfo := credentials.TLSInfo{
					State: tls.ConnectionState{
						VerifiedChains: [][]*x509.Certificate{{cert}},
					},
				}
				p := &peer.Peer{
					Addr:     &mockAddr{},
					AuthInfo: tlsInfo,
				}
				return peer.NewContext(context.Background(), p)
			},
			clientID:      "test-agent",
			expectedError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			ctx := tt.setupContext()

			err := server.clientCertificateMatches(ctx, tt.clientID)

			if tt.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}

func Test_Server_authenticate(t *testing.T) {
	tests := []struct {
		name          string
		setupServer   func() *Server
		setupContext  func() context.Context
		expectedError string
		shouldSucceed bool
	}{
		{
			name: "no metadata in context",
			setupServer: func() *Server {
				return &Server{}
			},
			setupContext: func() context.Context {
				return context.Background()
			},
			expectedError: "invalid authentication data",
			shouldSucceed: false,
		},
		{
			name: "no authorization header",
			setupServer: func() *Server {
				return &Server{}
			},
			setupContext: func() context.Context {
				md := metadata.New(map[string]string{})
				return metadata.NewIncomingContext(context.Background(), md)
			},
			expectedError: "invalid authentication data",
			shouldSucceed: false,
		},
		{
			name: "invalid JWT token",
			setupServer: func() *Server {
				mockIssuer := issuermock.NewIssuer(t)
				mockIssuer.On("ValidateAccessToken", "invalid-token").Return(nil, fmt.Errorf("invalid token"))

				return &Server{
					issuer: mockIssuer,
				}
			},
			setupContext: func() context.Context {
				md := metadata.New(map[string]string{
					"authorization": "invalid-token",
				})
				return metadata.NewIncomingContext(context.Background(), md)
			},
			expectedError: "invalid authentication data",
			shouldSucceed: false,
		},
		{
			name: "token with no subject",
			setupServer: func() *Server {
				mockClaims := &jwt.MapClaims{}
				mockIssuer := issuermock.NewIssuer(t)
				mockIssuer.On("ValidateAccessToken", "valid-token").Return(mockClaims, nil)

				return &Server{
					issuer: mockIssuer,
				}
			},
			setupContext: func() context.Context {
				md := metadata.New(map[string]string{
					"authorization": "valid-token",
				})
				return metadata.NewIncomingContext(context.Background(), md)
			},
			expectedError: "invalid authentication data",
			shouldSucceed: false,
		},
		{
			name: "token with invalid subject JSON",
			setupServer: func() *Server {
				mockClaims := &jwt.MapClaims{
					"sub": "invalid-json",
				}
				mockIssuer := issuermock.NewIssuer(t)
				mockIssuer.On("ValidateAccessToken", "valid-token").Return(mockClaims, nil)

				return &Server{
					issuer: mockIssuer,
				}
			},
			setupContext: func() context.Context {
				md := metadata.New(map[string]string{
					"authorization": "valid-token",
				})
				return metadata.NewIncomingContext(context.Background(), md)
			},
			expectedError: "invalid authentication data",
			shouldSucceed: false,
		},
		{
			name: "agent name matches namespace - should be rejected",
			setupServer: func() *Server {
				agentInfo := auth.AuthSubject{
					ClientID: "argocd",
					Mode:     "managed",
				}
				subjectJSON, _ := json.Marshal(agentInfo)

				mockClaims := &jwt.MapClaims{
					"sub": string(subjectJSON),
				}
				mockIssuer := issuermock.NewIssuer(t)
				mockIssuer.On("ValidateAccessToken", "valid-token").Return(mockClaims, nil)

				return &Server{
					issuer:    mockIssuer,
					namespace: "argocd", // Same as ClientID
				}
			},
			setupContext: func() context.Context {
				md := metadata.New(map[string]string{
					"authorization": "valid-token",
				})
				return metadata.NewIncomingContext(context.Background(), md)
			},
			expectedError: "invalid authentication data",
			shouldSucceed: false,
		},
		{
			name: "agent name different from namespace - should succeed",
			setupServer: func() *Server {
				agentInfo := auth.AuthSubject{
					ClientID: "test-agent",
					Mode:     "managed",
				}
				subjectJSON, _ := json.Marshal(agentInfo)

				mockClaims := &jwt.MapClaims{
					"sub": string(subjectJSON),
				}
				mockIssuer := issuermock.NewIssuer(t)
				mockIssuer.On("ValidateAccessToken", "valid-token").Return(mockClaims, nil)

				queues := queue.NewSendRecvQueues()

				return &Server{
					issuer:    mockIssuer,
					namespace: "argocd", // Different from ClientID
					queues:    queues,
					options: &ServerOptions{
						requireClientCerts: false,
					},
					namespaceMap: make(map[string]types.AgentMode),
				}
			},
			setupContext: func() context.Context {
				md := metadata.New(map[string]string{
					"authorization": "valid-token",
				})
				return metadata.NewIncomingContext(context.Background(), md)
			},
			expectedError: "",
			shouldSucceed: true,
		},
		{
			name: "invalid agent mode",
			setupServer: func() *Server {
				agentInfo := auth.AuthSubject{
					ClientID: "test-agent",
					Mode:     "invalid-mode",
				}
				subjectJSON, _ := json.Marshal(agentInfo)

				mockClaims := &jwt.MapClaims{
					"sub": string(subjectJSON),
				}
				mockIssuer := issuermock.NewIssuer(t)
				mockIssuer.On("ValidateAccessToken", "valid-token").Return(mockClaims, nil)

				queues := queue.NewSendRecvQueues()

				return &Server{
					issuer:    mockIssuer,
					namespace: "argocd",
					queues:    queues,
					options: &ServerOptions{
						requireClientCerts: false,
					},
					namespaceMap: make(map[string]types.AgentMode),
				}
			},
			setupContext: func() context.Context {
				md := metadata.New(map[string]string{
					"authorization": "valid-token",
				})
				return metadata.NewIncomingContext(context.Background(), md)
			},
			expectedError: "invalid authentication data",
			shouldSucceed: false,
		},
		{
			name: "client cert required but validation fails",
			setupServer: func() *Server {
				agentInfo := auth.AuthSubject{
					ClientID: "test-agent",
					Mode:     "managed",
				}
				subjectJSON, _ := json.Marshal(agentInfo)

				mockClaims := &jwt.MapClaims{
					"sub": string(subjectJSON),
				}
				mockIssuer := issuermock.NewIssuer(t)
				mockIssuer.On("ValidateAccessToken", "valid-token").Return(mockClaims, nil)

				return &Server{
					issuer:    mockIssuer,
					namespace: "argocd",
					options: &ServerOptions{
						requireClientCerts:     true,
						clientCertSubjectMatch: true,
					},
				}
			},
			setupContext: func() context.Context {
				md := metadata.New(map[string]string{
					"authorization": "valid-token",
				})
				return metadata.NewIncomingContext(context.Background(), md)
			},
			expectedError: "invalid authentication data",
			shouldSucceed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			ctx := tt.setupContext()

			newCtx, err := server.authenticate(ctx)

			if tt.shouldSucceed {
				assert.NoError(t, err)
				assert.NotNil(t, newCtx)
			} else {
				assert.Error(t, err)
				assert.Nil(t, newCtx)
				if tt.expectedError != "" {
					st, ok := status.FromError(err)
					require.True(t, ok)
					assert.Equal(t, codes.Unauthenticated, st.Code())
					assert.Contains(t, st.Message(), tt.expectedError)
				}
			}
		})
	}
}

func Test_Server_unaryAuthInterceptor(t *testing.T) {
	tests := []struct {
		name           string
		setupServer    func() *Server
		fullMethod     string
		shouldCallAuth bool
		authShouldFail bool
	}{
		{
			name: "method in noauth list - skip authentication",
			setupServer: func() *Server {
				return &Server{
					noauth: map[string]bool{
						"/test.Service/NoAuthMethod": true,
					},
				}
			},
			fullMethod:     "/test.Service/NoAuthMethod",
			shouldCallAuth: false,
			authShouldFail: false,
		},
		{
			name: "RefreshToken in noauth list - skip authentication",
			setupServer: func() *Server {
				return &Server{
					noauth: noAuthEndpoints,
				}
			},
			fullMethod:     "/authapi.Authentication/RefreshToken",
			shouldCallAuth: false,
			authShouldFail: false,
		},
		{
			name: "method not in noauth list - require authentication success",
			setupServer: func() *Server {
				agentInfo := auth.AuthSubject{
					ClientID: "test-agent",
					Mode:     "managed",
				}
				subjectJSON, _ := json.Marshal(agentInfo)

				mockClaims := &jwt.MapClaims{
					"sub": string(subjectJSON),
				}
				mockIssuer := issuermock.NewIssuer(t)
				mockIssuer.On("ValidateAccessToken", "valid-token").Return(mockClaims, nil)

				queues := queue.NewSendRecvQueues()

				return &Server{
					noauth:    map[string]bool{},
					issuer:    mockIssuer,
					namespace: "argocd",
					queues:    queues,
					options: &ServerOptions{
						requireClientCerts: false,
					},
					namespaceMap: make(map[string]types.AgentMode),
				}
			},
			fullMethod:     "/test.Service/AuthMethod",
			shouldCallAuth: true,
			authShouldFail: false,
		},
		{
			name: "method not in noauth list - authentication fails",
			setupServer: func() *Server {
				mockIssuer := issuermock.NewIssuer(t)
				mockIssuer.On("ValidateAccessToken", "invalid-token").Return(nil, fmt.Errorf("invalid token"))

				return &Server{
					noauth: map[string]bool{},
					issuer: mockIssuer,
				}
			},
			fullMethod:     "/test.Service/AuthMethod",
			shouldCallAuth: true,
			authShouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()

			var handlerCalled bool
			handler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			info := &grpc.UnaryServerInfo{
				FullMethod: tt.fullMethod,
			}

			var ctx context.Context
			if tt.shouldCallAuth && !tt.authShouldFail {
				md := metadata.New(map[string]string{
					"authorization": "valid-token",
				})
				ctx = metadata.NewIncomingContext(context.Background(), md)
			} else if tt.shouldCallAuth && tt.authShouldFail {
				md := metadata.New(map[string]string{
					"authorization": "invalid-token",
				})
				ctx = metadata.NewIncomingContext(context.Background(), md)
			} else {
				ctx = context.Background()
			}

			resp, err := server.unaryAuthInterceptor(ctx, "request", info, handler)

			if tt.authShouldFail {
				assert.Error(t, err)
				assert.Nil(t, resp)
				assert.False(t, handlerCalled)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, "response", resp)
				assert.True(t, handlerCalled)
			}
		})
	}
}

func Test_Server_streamAuthInterceptor(t *testing.T) {
	tests := []struct {
		name           string
		setupServer    func() *Server
		fullMethod     string
		shouldCallAuth bool
		authShouldFail bool
	}{
		{
			name: "method in noauth list - skip authentication",
			setupServer: func() *Server {
				return &Server{
					noauth: map[string]bool{
						"/test.Service/NoAuthStream": true,
					},
				}
			},
			fullMethod:     "/test.Service/NoAuthStream",
			shouldCallAuth: false,
			authShouldFail: false,
		},
		{
			name: "method not in noauth list - require authentication success",
			setupServer: func() *Server {
				agentInfo := auth.AuthSubject{
					ClientID: "test-agent",
					Mode:     "managed",
				}
				subjectJSON, _ := json.Marshal(agentInfo)

				mockClaims := &jwt.MapClaims{
					"sub": string(subjectJSON),
				}
				mockIssuer := issuermock.NewIssuer(t)
				mockIssuer.On("ValidateAccessToken", "valid-token").Return(mockClaims, nil)

				queues := queue.NewSendRecvQueues()

				return &Server{
					noauth:    map[string]bool{},
					issuer:    mockIssuer,
					namespace: "argocd",
					queues:    queues,
					options: &ServerOptions{
						requireClientCerts: false,
					},
					namespaceMap: make(map[string]types.AgentMode),
				}
			},
			fullMethod:     "/test.Service/AuthStream",
			shouldCallAuth: true,
			authShouldFail: false,
		},
		{
			name: "method not in noauth list - authentication fails",
			setupServer: func() *Server {
				mockIssuer := issuermock.NewIssuer(t)
				mockIssuer.On("ValidateAccessToken", "invalid-token").Return(nil, fmt.Errorf("invalid token"))

				return &Server{
					noauth: map[string]bool{},
					issuer: mockIssuer,
				}
			},
			fullMethod:     "/test.Service/AuthStream",
			shouldCallAuth: true,
			authShouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()

			var handlerCalled bool
			handler := func(srv any, stream grpc.ServerStream) error {
				handlerCalled = true
				return nil
			}

			info := &grpc.StreamServerInfo{
				FullMethod: tt.fullMethod,
			}

			var ctx context.Context
			if tt.shouldCallAuth && !tt.authShouldFail {
				md := metadata.New(map[string]string{
					"authorization": "valid-token",
				})
				ctx = metadata.NewIncomingContext(context.Background(), md)
			} else if tt.shouldCallAuth && tt.authShouldFail {
				md := metadata.New(map[string]string{
					"authorization": "invalid-token",
				})
				ctx = metadata.NewIncomingContext(context.Background(), md)
			} else {
				ctx = context.Background()
			}

			mockStream := &mockServerStream{ctx: ctx}

			err := server.streamAuthInterceptor("service", mockStream, info, handler)

			if tt.authShouldFail {
				assert.Error(t, err)
				assert.False(t, handlerCalled)
			} else {
				assert.NoError(t, err)
				assert.True(t, handlerCalled)
			}
		})
	}
}

// Mock types for testing

type mockAddr struct{}

func (m *mockAddr) Network() string {
	return "tcp"
}

func (m *mockAddr) String() string {
	return "127.0.0.1:12345"
}

type mockAuthInfo struct{}

func (m *mockAuthInfo) AuthType() string {
	return "mock"
}

type mockServerStream struct {
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

func (m *mockServerStream) SendMsg(msg any) error {
	return nil
}

func (m *mockServerStream) RecvMsg(msg any) error {
	return nil
}

func (m *mockServerStream) SetHeader(metadata.MD) error {
	return nil
}

func (m *mockServerStream) SendHeader(metadata.MD) error {
	return nil
}

func (m *mockServerStream) SetTrailer(metadata.MD) {
}

func newMTLSAuthPeerContext(t *testing.T, commonName string) context.Context {
	t.Helper()
	cert := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: commonName,
		},
	}
	tlsInfo := credentials.TLSInfo{
		State: tls.ConnectionState{
			VerifiedChains: [][]*x509.Certificate{{cert}},
		},
	}
	return peer.NewContext(context.Background(), &peer.Peer{
		Addr:     &mockAddr{},
		AuthInfo: tlsInfo,
	})
}

func newReplicationAuthTestServer(t *testing.T, withMTLS bool, haOpts ...ha.Option) *Server {
	t.Helper()
	authMethods := auth.NewMethods()
	if withMTLS {
		mtlsMethod := mtls.NewMTLSAuthentication(regexp.MustCompile(`CN=([^,]+)`), mtls.IdentitySourceSubject)
		require.NoError(t, authMethods.RegisterMethod(auth.MethodMTLS, mtlsMethod))
	}

	ctrl, err := ha.NewController(context.Background(), haOpts...)
	require.NoError(t, err)

	return &Server{
		noauth:      noAuthEndpoints,
		authMethods: authMethods,
		ha: &HAComponents{
			Controller: ctrl,
		},
	}
}

func Test_Server_authenticateReplication(t *testing.T) {
	unaryMethods := []string{
		"/replicationapi.Replication/GetSnapshot",
		"/replicationapi.Replication/Status",
		"/replicationapi.Replication/ConfirmMissingResources",
	}

	tests := []struct {
		name         string
		withMTLS     bool
		haOpts       []ha.Option
		directAllow  *[]string
		peerCN       string
		wantCode     codes.Code
		wantErrMatch string
	}{
		{
			name:         "omitted allowlist denies ordinary agent",
			withMTLS:     true,
			haOpts:       []ha.Option{ha.WithEnabled(true), ha.WithPreferredRole("primary")},
			peerCN:       "ordinary-agent",
			wantCode:     codes.PermissionDenied,
			wantErrMatch: "allowed replication clients not configured",
		},
		{
			name:         "explicit empty allowlist denies ordinary agent",
			withMTLS:     true,
			haOpts:       []ha.Option{ha.WithEnabled(true), ha.WithPreferredRole("primary"), ha.WithAllowedReplicationClients([]string{})},
			peerCN:       "ordinary-agent",
			wantCode:     codes.PermissionDenied,
			wantErrMatch: "allowed replication clients not configured",
		},
		{
			name:         "whitespace and blank allowlist entries deny ordinary agent",
			withMTLS:     true,
			haOpts:       []ha.Option{ha.WithEnabled(true), ha.WithPreferredRole("primary"), ha.WithAllowedReplicationClients([]string{"", "   ", "\t"})},
			peerCN:       "ordinary-agent",
			wantCode:     codes.PermissionDenied,
			wantErrMatch: "allowed replication clients not configured",
		},
		{
			name:         "raw empty string entries in options deny ordinary agent",
			withMTLS:     true,
			haOpts:       []ha.Option{ha.WithEnabled(true), ha.WithPreferredRole("primary")},
			directAllow:  &[]string{""},
			peerCN:       "ordinary-agent",
			wantCode:     codes.PermissionDenied,
			wantErrMatch: "allowed replication clients not configured",
		},
		{
			name:         "ordinary agent denied when allowlist lists only HA peer",
			withMTLS:     true,
			haOpts:       []ha.Option{ha.WithEnabled(true), ha.WithPreferredRole("primary"), ha.WithAllowedReplicationClients([]string{"ha-replica"})},
			peerCN:       "ordinary-agent",
			wantCode:     codes.PermissionDenied,
			wantErrMatch: "not in allowed replication clients",
		},
		{
			name:     "explicitly listed HA peer is authorized",
			withMTLS: true,
			haOpts:   []ha.Option{ha.WithEnabled(true), ha.WithPreferredRole("primary"), ha.WithAllowedReplicationClients([]string{"ha-replica"})},
			peerCN:   "ha-replica",
			wantCode: codes.OK,
		},
		{
			name:         "missing mTLS method returns Unauthenticated even with allowlist",
			withMTLS:     false,
			haOpts:       []ha.Option{ha.WithEnabled(true), ha.WithPreferredRole("primary"), ha.WithAllowedReplicationClients([]string{"ha-replica"})},
			peerCN:       "ha-replica",
			wantCode:     codes.Unauthenticated,
			wantErrMatch: "no mtls method configured",
		},
		{
			name:         "missing client certificate returns Unauthenticated",
			withMTLS:     true,
			haOpts:       []ha.Option{ha.WithEnabled(true), ha.WithPreferredRole("primary"), ha.WithAllowedReplicationClients([]string{"ha-replica"})},
			peerCN:       "",
			wantCode:     codes.Unauthenticated,
			wantErrMatch: "replication auth failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newReplicationAuthTestServer(t, tt.withMTLS, tt.haOpts...)
			if tt.directAllow != nil {
				srv.ha.Controller.Options().AllowedReplicationClients = *tt.directAllow
			}

			var ctx context.Context
			if tt.peerCN != "" {
				ctx = newMTLSAuthPeerContext(t, tt.peerCN)
			} else {
				ctx = context.Background()
			}

			// Verify unary replication RPCs
			for _, method := range unaryMethods {
				handlerCalled := false
				_, err := srv.unaryAuthInterceptor(ctx, struct{}{}, &grpc.UnaryServerInfo{FullMethod: method}, func(context.Context, any) (any, error) {
					handlerCalled = true
					return "ok", nil
				})
				if tt.wantCode == codes.OK {
					require.NoError(t, err, "method %s", method)
					assert.True(t, handlerCalled, "method %s", method)
				} else {
					require.Error(t, err, "method %s", method)
					assert.False(t, handlerCalled, "method %s", method)
					assert.Equal(t, tt.wantCode, status.Code(err), "method %s: %v", method, err)
					assert.Contains(t, err.Error(), tt.wantErrMatch)
				}
			}

			// Verify streaming replication RPC
			streamCalled := false
			err := srv.streamAuthInterceptor("service", &mockServerStream{ctx: ctx}, &grpc.StreamServerInfo{
				FullMethod:     "/replicationapi.Replication/Subscribe",
				IsServerStream: true,
				IsClientStream: true,
			}, func(any, grpc.ServerStream) error {
				streamCalled = true
				return nil
			})
			if tt.wantCode == codes.OK {
				require.NoError(t, err)
				assert.True(t, streamCalled)
			} else {
				require.Error(t, err)
				assert.False(t, streamCalled)
				assert.Equal(t, tt.wantCode, status.Code(err), err.Error())
				assert.Contains(t, err.Error(), tt.wantErrMatch)
			}
		})
	}
}

type stubReplicationStateProvider struct{}

func (s *stubReplicationStateProvider) GetAllAgentNames() []string   { return nil }
func (s *stubReplicationStateProvider) GetAgentMode(string) string   { return "" }
func (s *stubReplicationStateProvider) IsAgentConnected(string) bool { return false }
func (s *stubReplicationStateProvider) GetAgentResources(string) []replicationserver.ResourceInfo {
	return nil
}
func (s *stubReplicationStateProvider) GetPrincipalResources() []replicationserver.ResourceInfo {
	return nil
}
func (s *stubReplicationStateProvider) ConfirmMissingResources(string, []replicationserver.ResourceInfo) []replicationserver.MissingResourceConfirmation {
	return nil
}

func generateSignedMTLSBundle(t *testing.T, commonName string, ips []net.IP, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(1 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IPAddresses:           ips,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	require.NoError(t, err)

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}
}

func Test_Server_ReplicationAllowlist_EndToEndMTLS(t *testing.T) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(1 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	serverTLSCert := generateSignedMTLSBundle(t, "primary-principal", []net.IP{net.ParseIP("127.0.0.1")}, caCert, caKey)
	agentTLSCert := generateSignedMTLSBundle(t, "ordinary-agent", nil, caCert, caKey)
	peerTLSCert := generateSignedMTLSBundle(t, "ha-replica", nil, caCert, caKey)

	startMTLSReplicationServer := func(t *testing.T, haOpts ...ha.Option) (string, *replicationserver.Server) {
		t.Helper()
		srv := newReplicationAuthTestServer(t, true, haOpts...)
		replSrv := replicationserver.NewServer(replication.NewForwarder(), &stubReplicationStateProvider{})
		srv.ha.ReplicationServer = replSrv

		serverTLS := &tls.Config{
			Certificates: []tls.Certificate{serverTLSCert},
			ClientCAs:    caPool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			MinVersion:   tls.VersionTLS12,
		}
		grpcSrv := grpc.NewServer(
			grpc.Creds(credentials.NewTLS(serverTLS)),
			grpc.ChainUnaryInterceptor(srv.unaryAuthInterceptor),
			grpc.ChainStreamInterceptor(srv.streamAuthInterceptor),
		)
		replicationapi.RegisterReplicationServer(grpcSrv, replSrv)

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() {
			grpcSrv.Stop()
		})
		go func() {
			_ = grpcSrv.Serve(lis)
		}()
		return lis.Addr().String(), replSrv
	}

	dialClient := func(t *testing.T, addr string, clientCert tls.Certificate) replicationapi.ReplicationClient {
		t.Helper()
		clientTLS := &tls.Config{
			Certificates: []tls.Certificate{clientCert},
			RootCAs:      caPool,
			MinVersion:   tls.VersionTLS12,
		}
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = conn.Close()
		})
		return replicationapi.NewReplicationClient(conn)
	}

	t.Run("omitted allowlist denies unary and streaming replication over mTLS", func(t *testing.T) {
		addr, replSrv := startMTLSReplicationServer(t, ha.WithEnabled(true), ha.WithPreferredRole("primary"))
		agentClient := dialClient(t, addr, agentTLSCert)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := agentClient.Status(ctx, &replicationapi.StatusRequest{})
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))

		_, err = agentClient.GetSnapshot(ctx, &replicationapi.SnapshotRequest{})
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))

		_, err = agentClient.ConfirmMissingResources(ctx, &replicationapi.ConfirmMissingResourcesRequest{AgentName: "ordinary-agent"})
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))

		stream, err := agentClient.Subscribe(ctx)
		if err == nil {
			_, err = stream.Recv()
		}
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
		assert.Equal(t, 0, replSrv.ConnectedReplicaCount())
	})

	t.Run("explicit allowlist rejects ordinary agent and allows listed HA peer", func(t *testing.T) {
		addr, replSrv := startMTLSReplicationServer(t,
			ha.WithEnabled(true),
			ha.WithPreferredRole("primary"),
			ha.WithAllowedReplicationClients([]string{"ha-replica"}),
		)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		agentClient := dialClient(t, addr, agentTLSCert)
		_, err := agentClient.GetSnapshot(ctx, &replicationapi.SnapshotRequest{})
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))

		agentStream, err := agentClient.Subscribe(ctx)
		if err == nil {
			_, err = agentStream.Recv()
		}
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))

		peerClient := dialClient(t, addr, peerTLSCert)
		st, err := peerClient.Status(ctx, &replicationapi.StatusRequest{})
		require.NoError(t, err)
		assert.Equal(t, uint64(0), st.CurrentSequenceNum)

		snap, err := peerClient.GetSnapshot(ctx, &replicationapi.SnapshotRequest{})
		require.NoError(t, err)
		require.NotNil(t, snap)

		confirmResp, err := peerClient.ConfirmMissingResources(ctx, &replicationapi.ConfirmMissingResourcesRequest{AgentName: "ordinary-agent"})
		require.NoError(t, err)
		require.NotNil(t, confirmResp)

		peerStream, err := peerClient.Subscribe(ctx)
		require.NoError(t, err)
		require.NoError(t, peerStream.Send(&replicationapi.ReplicationAck{AckedSequenceNum: snap.LastSequenceNum}))

		require.Eventually(t, func() bool {
			return replSrv.ConnectedReplicaCount() == 1
		}, 2*time.Second, 20*time.Millisecond)

		require.NoError(t, peerStream.CloseSend())
		_, recvErr := peerStream.Recv()
		assert.True(t, recvErr == nil || recvErr == io.EOF || status.Code(recvErr) == codes.Canceled || status.Code(recvErr) == codes.DeadlineExceeded)
	})
}
