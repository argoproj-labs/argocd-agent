// Copyright 2026 The argocd-agent Authors
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

package kube

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	conf "github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// defaultServerA is the server of the "ctx-a" context in the kubeconfig
// written by most of the tests, which never talk to it.
const defaultServerA = "https://a.example.com:6443"

// writeKubeconfig writes a kubeconfig with two contexts, "ctx-a" (current) and
// "ctx-b", each with its own server and namespace, and returns its path.
// Context "ctx-b" always points to b.example.com.
func writeKubeconfig(t *testing.T, serverA string) string {
	t.Helper()
	servers := map[string]string{"a": serverA, "b": "https://b.example.com:6443"}
	cfg := clientcmdapi.NewConfig()
	for name, server := range servers {
		cfg.Clusters["cluster-"+name] = &clientcmdapi.Cluster{Server: server, InsecureSkipTLSVerify: true}
		cfg.AuthInfos["user-"+name] = &clientcmdapi.AuthInfo{Token: "token-" + name}
		cfg.Contexts["ctx-"+name] = &clientcmdapi.Context{
			Cluster:   "cluster-" + name,
			AuthInfo:  "user-" + name,
			Namespace: "ns-" + name,
		}
	}
	cfg.CurrentContext = "ctx-a"
	path := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, clientcmd.WriteToFile(*cfg, path))
	return path
}

func Test_NewKubernetesClientFromConfig(t *testing.T) {
	// The QPS and burst defaults only apply when the variables are not set, so
	// make sure the environment of whoever runs the tests does not leak in.
	// t.Setenv restores the original values when the test ends.
	for _, key := range []string{conf.EnvKubeQPS, conf.EnvKubeBurst} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}

	t.Run("builds every client from the kubeconfig", func(t *testing.T) {
		// Record what reaches the server as "<method> <path> <authorization>".
		var (
			mu       sync.Mutex
			requests []string
		)
		// client-go does not send credentials over plain HTTP, hence TLS.
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			requests = append(requests, fmt.Sprintf("%s %s %s", r.Method, r.URL.Path, r.Header.Get("Authorization")))
			w.WriteHeader(http.StatusNotFound)
		}))
		t.Cleanup(srv.Close)
		ctx := context.WithValue(context.Background(), struct{}{}, "marker")

		kc, err := NewKubernetesClientFromConfig(ctx, "", writeKubeconfig(t, srv.URL), "")

		require.NoError(t, err)
		assert.Equal(t, ctx, kc.Context)
		require.NotNil(t, kc.RestConfig)
		assert.Equal(t, srv.URL, kc.RestConfig.Host)
		require.NotNil(t, kc.Clientset)
		require.NotNil(t, kc.ApplicationsClientset)
		require.NotNil(t, kc.DynamicClient)

		// Each client must talk to the server of the kubeconfig and use its
		// credentials. The server answers 404 to everything; that the answer
		// makes it back is checked, what it says is not the point.
		_, err = kc.Clientset.CoreV1().Namespaces().Get(ctx, "a-namespace", metav1.GetOptions{})
		require.True(t, kerrors.IsNotFound(err), "core client: %v", err)
		_, err = kc.ApplicationsClientset.ArgoprojV1alpha1().Applications("argocd").Get(ctx, "an-app", metav1.GetOptions{})
		require.True(t, kerrors.IsNotFound(err), "applications client: %v", err)
		_, err = kc.DynamicClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "secrets"}).
			Namespace("argocd").Get(ctx, "a-secret", metav1.GetOptions{})
		require.True(t, kerrors.IsNotFound(err), "dynamic client: %v", err)

		mu.Lock()
		defer mu.Unlock()
		assert.ElementsMatch(t, []string{
			"GET /api/v1/namespaces/a-namespace Bearer token-a",
			"GET /apis/argoproj.io/v1alpha1/namespaces/argocd/applications/an-app Bearer token-a",
			"GET /api/v1/namespaces/argocd/secrets/a-secret Bearer token-a",
		}, requests)
	})

	t.Run("an empty namespace falls back to the one of the context", func(t *testing.T) {
		tests := []struct {
			name        string
			kubecontext string
			want        string
		}{
			{name: "current context", kubecontext: "", want: "ns-a"},
			{name: "overridden context", kubecontext: "ctx-b", want: "ns-b"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				kc, err := NewKubernetesClientFromConfig(context.Background(), "", writeKubeconfig(t, defaultServerA), tt.kubecontext)

				require.NoError(t, err)
				assert.Equal(t, tt.want, kc.Namespace)
			})
		}
	})

	t.Run("an explicit namespace wins over the one of the context", func(t *testing.T) {
		kc, err := NewKubernetesClientFromConfig(context.Background(), "argocd", writeKubeconfig(t, defaultServerA), "")

		require.NoError(t, err)
		assert.Equal(t, "argocd", kc.Namespace)
	})

	t.Run("QPS and burst have defaults", func(t *testing.T) {
		kc, err := NewKubernetesClientFromConfig(context.Background(), "", writeKubeconfig(t, defaultServerA), "")

		require.NoError(t, err)
		assert.Equal(t, float32(100), kc.RestConfig.QPS)
		assert.Equal(t, 300, kc.RestConfig.Burst)
	})

	t.Run("QPS and burst can be set through the environment", func(t *testing.T) {
		t.Setenv(conf.EnvKubeQPS, "7")
		t.Setenv(conf.EnvKubeBurst, "11")

		kc, err := NewKubernetesClientFromConfig(context.Background(), "", writeKubeconfig(t, defaultServerA), "")

		require.NoError(t, err)
		assert.Equal(t, float32(7), kc.RestConfig.QPS)
		assert.Equal(t, 11, kc.RestConfig.Burst)
	})

	t.Run("an unknown context is an error", func(t *testing.T) {
		kc, err := NewKubernetesClientFromConfig(context.Background(), "", writeKubeconfig(t, defaultServerA), "does-not-exist")

		require.ErrorContains(t, err, "does-not-exist")
		assert.Nil(t, kc)
	})
}

func Test_NewRestConfig(t *testing.T) {
	t.Run("uses the current context of the kubeconfig", func(t *testing.T) {
		cfg, err := NewRestConfig(writeKubeconfig(t, defaultServerA), "")

		require.NoError(t, err)
		assert.Equal(t, "https://a.example.com:6443", cfg.Host)
		assert.Equal(t, "token-a", cfg.BearerToken)
	})

	t.Run("a given context overrides the current one", func(t *testing.T) {
		cfg, err := NewRestConfig(writeKubeconfig(t, defaultServerA), "ctx-b")

		require.NoError(t, err)
		assert.Equal(t, "https://b.example.com:6443", cfg.Host)
		assert.Equal(t, "token-b", cfg.BearerToken)
	})

	t.Run("an unknown context is an error", func(t *testing.T) {
		cfg, err := NewRestConfig(writeKubeconfig(t, defaultServerA), "does-not-exist")

		require.ErrorContains(t, err, "does-not-exist")
		assert.Nil(t, cfg)
	})

	t.Run("a missing kubeconfig file is an error", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")

		cfg, err := NewRestConfig(missing, "")

		require.ErrorContains(t, err, missing)
		assert.Nil(t, cfg)
	})
}

// resetErr is an error that is only an ECONNRESET as far as errors.Is is
// concerned, its message gives nothing away.
type resetErr struct{}

func (resetErr) Error() string { return "boom" }

func (resetErr) Is(target error) bool { return target == syscall.ECONNRESET }

func Test_IsRetryableError(t *testing.T) {
	gr := schema.GroupResource{Resource: "secrets"}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},

		// Errors reported by the API server
		{name: "internal error", err: kerrors.NewInternalError(errors.New("boom")), want: true},
		{name: "invalid", err: kerrors.NewInvalid(schema.GroupKind{Kind: "Secret"}, "s", nil), want: true},
		{name: "too many requests", err: kerrors.NewTooManyRequests("slow down", 1), want: true},
		{name: "server timeout", err: kerrors.NewServerTimeout(gr, "get", 1), want: true},
		{name: "service unavailable", err: kerrors.NewServiceUnavailable("down"), want: true},
		{name: "timeout", err: kerrors.NewTimeoutError("slow", 1), want: true},
		{name: "unexpected object", err: &kerrors.UnexpectedObjectError{}, want: true},
		{
			name: "unexpected server response",
			err: &kerrors.StatusError{ErrStatus: metav1.Status{
				Reason: metav1.StatusReasonUnknown,
				Details: &metav1.StatusDetails{
					Causes: []metav1.StatusCause{{Type: metav1.CauseTypeUnexpectedServerResponse}},
				},
			}},
			want: true,
		},
		{name: "not found", err: kerrors.NewNotFound(gr, "s"), want: false},
		{name: "forbidden", err: kerrors.NewForbidden(gr, "s", errors.New("no access")), want: false},
		{name: "forbidden by quota", err: kerrors.NewForbidden(gr, "s", errors.New("exceeded quota: compute")), want: true},
		{name: "quota message without being forbidden", err: errors.New("exceeded quota: compute"), want: false},
		{
			name: "resource quota message without being a conflict",
			err:  errors.New("Operation cannot be fulfilled on resourcequota \"q\": modified"),
			want: false,
		},
		{name: "conflict", err: kerrors.NewConflict(gr, "s", errors.New("modified")), want: false},
		{
			name: "conflict on a resource quota",
			err:  kerrors.NewConflict(schema.GroupResource{Resource: "resourcequota"}, "q", errors.New("modified")),
			want: true,
		},

		// An object that is being deleted can no longer change, so retrying cannot help
		// (these would be retryable as internal errors if the check came later)
		{
			name: "immutable deletionTimestamp",
			err:  kerrors.NewInternalError(errors.New("metadata.deletionTimestamp: field is immutable")),
			want: false,
		},
		{
			name: "immutable deletionGracePeriodSeconds",
			err:  kerrors.NewInternalError(errors.New("metadata.deletionGracePeriodSeconds: field is immutable")),
			want: false,
		},
		{
			name: "deletionTimestamp mentioned without being immutable",
			err:  kerrors.NewInternalError(errors.New("deletionTimestamp is set")),
			want: true,
		},

		// Network errors
		{name: "operation error", err: &net.OpError{Op: "dial", Err: errors.New("refused")}, want: true},
		{name: "dns error", err: &net.DNSError{Err: "no such host", Name: "x"}, want: true},
		{name: "unknown network", err: net.UnknownNetworkError("tcp9"), want: true},
		{name: "operation error wrapped in another error", err: fmt.Errorf("get: %w", &net.OpError{Op: "dial"}), want: true},
		{name: "ECONNRESET errno (also matched by its message)", err: syscall.ECONNRESET, want: true},
		// The errno is also caught by the "connection reset by peer" message, so
		// this one only reaches the errors.Is check.
		{name: "ECONNRESET found through errors.Is", err: resetErr{}, want: true},
		{
			name: "url error closed by the remote host",
			err:  &url.Error{Op: "Get", URL: "https://x", Err: errors.New("Connection closed by foreign host")},
			want: true,
		},
		{
			name: "url error for another reason",
			err:  &url.Error{Op: "Get", URL: "https://x", Err: errors.New("unsupported protocol scheme")},
			want: false,
		},

		// Errors that only show in the message
		{name: "TLS handshake timeout", err: errors.New("net/http: TLS handshake timeout"), want: true},
		{name: "i/o timeout", err: errors.New("read tcp: i/o timeout"), want: true},
		{name: "connection timed out", err: errors.New("dial tcp: connection timed out"), want: true},
		{name: "connection reset by peer", err: errors.New("read: connection reset by peer"), want: true},
		{name: "HTTP/2 GOAWAY", err: errors.New("http2: server sent GOAWAY and closed the connection"), want: true},
		// The ProcessState of these is nil, which is fine for Error().
		{name: "exit error with a timeout on stderr", err: &exec.ExitError{Stderr: []byte("i/o timeout")}, want: true},
		{name: "exit error without a known cause", err: &exec.ExitError{Stderr: []byte("permission denied")}, want: false},
		{name: "unrelated error", err: errors.New("something else"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsRetryableError(tt.err))
		})
	}
}
