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
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	issuermock "github.com/argoproj-labs/argocd-agent/internal/issuer/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
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
