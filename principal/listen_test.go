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
	"crypto/tls"
	"fmt"
	"path"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/auth/userpass"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/authapi"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/versionapi"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	fakecerts "github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"k8s.io/apimachinery/pkg/util/wait"
)

// type slowedClient struct{}

// func (c *slowedClient) RoundTrip(r *http.Request) (*http.Response, error) {
// 	t := &http.Transport{
// 		TLSClientConfig: &tls.Config{
// 			InsecureSkipVerify: true,
// 		},
// 	}
// 	return t.RoundTrip(r)
// }

func Test_parseAddress(t *testing.T) {
	tc := []struct {
		address string
		host    string
		port    int
		valid   bool
	}{
		{"127.0.0.1:8080", "127.0.0.1", 8080, true},
		{"[::1]:8080", "[::1]", 8080, true},
		{"127.0.0.1:203201", "", 0, false},
		{"[some]:host]:8080", "", 0, false},
	}

	for _, tt := range tc {
		host, port, err := parseAddress(tt.address)
		if !tt.valid {
			assert.Error(t, err)
			assert.Equal(t, tt.host, host)
			assert.Equal(t, tt.port, port)
		} else {
			assert.NoError(t, err)
			assert.Equal(t, tt.host, host)
			assert.Equal(t, tt.port, port)
		}
	}
}

func Test_Listen(t *testing.T) {
	tempDir := t.TempDir()
	templ := certTempl
	fakecerts.WriteSelfSignedCert(t, "rsa", path.Join(tempDir, "test-cert"), templ)
	t.Run("Auto-select port for listener", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
			WithTLSKeyPairFromPath(path.Join(tempDir, "test-cert.crt"), path.Join(tempDir, "test-cert.key")),
			WithListenerPort(0),
			WithGeneratedTokenSigningKey(),
			WithListenerAddress("127.0.0.1"),
		)
		require.NoError(t, err)
		err = s.Listen(context.Background(), wait.Backoff{Duration: 100 * time.Millisecond, Steps: 2})
		require.NoError(t, err)
		defer s.listener.l.Close()
		assert.Equal(t, "127.0.0.1", s.listener.host)
		assert.NotZero(t, s.listener.port)
	})

	t.Run("Listen on privileged port", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
			WithTLSKeyPairFromPath(path.Join(tempDir, "test-cert.crt"), path.Join(tempDir, "test-cert.key")),
			WithGeneratedTokenSigningKey(),
			WithListenerPort(443),
			WithListenerAddress("127.0.0.1"),
		)
		require.NoError(t, err)
		err = s.Listen(context.Background(), wait.Backoff{Duration: 100 * time.Millisecond, Steps: 2})
		require.Error(t, err)
		assert.Nil(t, s.listener)
	})

}

func grpcDialer(t *testing.T, s *Server) *grpc.ClientConn {
	t.Helper()
	tlsC := &tls.Config{InsecureSkipVerify: true}
	creds := credentials.NewTLS(tlsC)
	conn, err := grpc.Dial(fmt.Sprintf("%s:%d", s.listener.host, s.listener.port),
		grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	return conn
}

func userPass(t *testing.T, kv ...string) *userpass.UserPassAuthentication {
	if len(kv)%2 != 0 {
		t.Fatalf("kv must have pairs of 2")
	}
	up := userpass.NewUserPassAuthentication("")
	for i := 0; i < len(kv); i = i + 2 {
		up.UpsertUser(kv[i], kv[i+1])
	}

	return up
}

func creds(username, password string) auth.Credentials {
	m := make(map[string]string)
	m[userpass.ClientIDField] = username
	m[userpass.ClientSecretField] = password
	return m
}

func Test_Serve(t *testing.T) {
	tempDir := t.TempDir()
	templ := certTempl
	fakecerts.WriteSelfSignedCert(t, "rsa", path.Join(tempDir, "test-cert"), templ)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s, err := NewServer(ctx, kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
		WithTLSKeyPairFromPath(path.Join(tempDir, "test-cert.crt"), path.Join(tempDir, "test-cert.key")),
		WithGeneratedTokenSigningKey(),
		WithListenerPort(0),
		WithListenerAddress("127.0.0.1"),
		WithShutDownGracePeriod(2*time.Second),
		WithGRPC(true),
		WithInformerSyncTimeout(5*time.Second),
	)
	require.NoError(t, err)
	errch := make(chan error)

	err = s.Start(ctx, errch)
	require.NoError(t, err)

	// Create and register authentication method
	up := userPass(t, "hello", "world")
	s.authMethods.RegisterMethod("userpass", up)

	conn := grpcDialer(t, s)
	defer conn.Close()
	authC := authapi.NewAuthenticationClient(conn)
	a, err := authC.Authenticate(
		context.Background(),
		&authapi.AuthRequest{Method: "userpass", Credentials: creds("hello", "world"), Mode: types.AgentModeAutonomous.String()},
	)
	require.NoError(t, err)
	require.NotNil(t, a)
	versionC := versionapi.NewVersionClient(conn)
	v, err := versionC.Version(context.Background(), &versionapi.VersionRequest{})
	require.NoError(t, err)
	assert.Equal(t, v.Version, s.version.QualifiedVersion())
	err = s.Shutdown()
	assert.NoError(t, err)
}
