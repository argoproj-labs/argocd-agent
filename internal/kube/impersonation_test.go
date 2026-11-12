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
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/argoproj/argo-cd/v3/pkg/client/clientset/versioned"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func Test_ParseServiceAccountUser(t *testing.T) {
	tests := []struct {
		name      string
		user      string
		namespace string
		sa        string
		wantErr   bool
	}{
		{name: "valid", user: "system:serviceaccount:tenant-a:deployer", namespace: "tenant-a", sa: "deployer"},
		{name: "name with dots and dashes", user: "system:serviceaccount:ns1:my-sa.v2", namespace: "ns1", sa: "my-sa.v2"},
		{name: "empty", user: "", wantErr: true},
		{name: "plain user", user: "admin", wantErr: true},
		{name: "cluster admin group style", user: "system:masters", wantErr: true},
		{name: "node user", user: "system:node:worker-1", wantErr: true},
		{name: "missing name", user: "system:serviceaccount:tenant-a", wantErr: true},
		{name: "empty name", user: "system:serviceaccount:tenant-a:", wantErr: true},
		{name: "empty namespace", user: "system:serviceaccount::deployer", wantErr: true},
		{name: "extra component", user: "system:serviceaccount:tenant-a:deployer:extra", wantErr: true},
		{name: "wrong prefix", user: "system:serviceaccounts:tenant-a:deployer", wantErr: true},
		{name: "uppercase namespace", user: "system:serviceaccount:Tenant:deployer", wantErr: true},
		{name: "underscore in name", user: "system:serviceaccount:tenant-a:bad_name", wantErr: true},
		{name: "namespace with dot", user: "system:serviceaccount:tenant.a:deployer", wantErr: true},
		{name: "leading space", user: " system:serviceaccount:tenant-a:deployer", wantErr: true},
		{name: "newline injected", user: "system:serviceaccount:tenant-a:deployer\nImpersonate-Group: system:masters", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns, sa, err := ParseServiceAccountUser(tt.user)
			if tt.wantErr {
				require.Error(t, err)
				assert.Empty(t, ns)
				assert.Empty(t, sa)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.namespace, ns)
			assert.Equal(t, tt.sa, sa)
		})
	}
}

// requestRecorder is a fake Kubernetes API server that records the
// impersonation headers of every request it receives.
type requestRecorder struct {
	mu       sync.Mutex
	requests []http.Header
	server   *httptest.Server
}

func newRequestRecorder(t *testing.T) *requestRecorder {
	t.Helper()
	rr := &requestRecorder{}
	rr.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rr.mu.Lock()
		rr.requests = append(rr.requests, r.Header.Clone())
		rr.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"p","namespace":"ns"}}`))
	}))
	t.Cleanup(rr.server.Close)
	return rr
}

// last returns the headers of the most recent request, failing the test if no
// request was recorded since the previous call to reset.
func (rr *requestRecorder) last(t *testing.T) http.Header {
	t.Helper()
	rr.mu.Lock()
	defer rr.mu.Unlock()
	require.NotEmpty(t, rr.requests, "no request reached the API server")
	return rr.requests[len(rr.requests)-1]
}

func (rr *requestRecorder) reset() {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.requests = nil
}

func newClientFor(t *testing.T, cfg *rest.Config) *KubernetesClient {
	t.Helper()
	clientset, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)
	dyn, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err)
	apps, err := versioned.NewForConfig(cfg)
	require.NoError(t, err)
	kc := NewKubernetesClient(context.Background(), clientset, apps, "argocd")
	kc.DynamicClient = dyn
	kc.RestConfig = cfg
	return kc
}

func Test_KubernetesClient_Impersonate(t *testing.T) {
	const user = "system:serviceaccount:tenant-a:deployer"
	podGVR := schema.GroupVersionResource{Version: "v1", Resource: "pods"}

	t.Run("every client of the copy that is built impersonates the user", func(t *testing.T) {
		rr := newRequestRecorder(t)
		kc := newClientFor(t, &rest.Config{Host: rr.server.URL})

		imp, err := kc.Impersonate(user)
		require.NoError(t, err)

		_, _ = imp.Clientset.CoreV1().Pods("ns").Get(context.Background(), "p", v1.GetOptions{})
		assert.Equal(t, user, rr.last(t).Get("Impersonate-User"), "typed clientset")

		rr.reset()
		_, _ = imp.DynamicClient.Resource(podGVR).Namespace("ns").Get(context.Background(), "p", v1.GetOptions{})
		assert.Equal(t, user, rr.last(t).Get("Impersonate-User"), "dynamic client")

		assert.Nil(t, imp.ApplicationsClientset, "unused, and must not be left behind as the agent's own")

		rr.reset()
		req := imp.Clientset.Discovery().RESTClient().Get().AbsPath("/api/v1/namespaces/ns/pods/p")
		_, _ = req.DoRaw(context.Background())
		assert.Equal(t, user, rr.last(t).Get("Impersonate-User"), "discovery REST client")

		assert.Equal(t, user, imp.RestConfig.Impersonate.UserName)
		assert.Equal(t, kc.Namespace, imp.Namespace)
		assert.Equal(t, kc.Context, imp.Context)
	})

	t.Run("the original client is left untouched", func(t *testing.T) {
		rr := newRequestRecorder(t)
		kc := newClientFor(t, &rest.Config{Host: rr.server.URL})

		_, err := kc.Impersonate(user)
		require.NoError(t, err)

		assert.Empty(t, kc.RestConfig.Impersonate.UserName)
		_, _ = kc.Clientset.CoreV1().Pods("ns").Get(context.Background(), "p", v1.GetOptions{})
		assert.Empty(t, rr.last(t).Get("Impersonate-User"))
		rr.reset()
		_, _ = kc.DynamicClient.Resource(podGVR).Namespace("ns").Get(context.Background(), "p", v1.GetOptions{})
		assert.Empty(t, rr.last(t).Get("Impersonate-User"))
	})

	t.Run("impersonation already present in the base config is replaced", func(t *testing.T) {
		rr := newRequestRecorder(t)
		kc := newClientFor(t, &rest.Config{
			Host: rr.server.URL,
			Impersonate: rest.ImpersonationConfig{
				UserName: "system:serviceaccount:other:other",
				Groups:   []string{"system:masters"},
				UID:      "1234",
				Extra:    map[string][]string{"scopes": {"all"}},
			},
		})

		imp, err := kc.Impersonate(user)
		require.NoError(t, err)

		_, _ = imp.Clientset.CoreV1().Pods("ns").Get(context.Background(), "p", v1.GetOptions{})
		h := rr.last(t)
		assert.Equal(t, user, h.Get("Impersonate-User"))
		assert.Empty(t, h.Values("Impersonate-Group"))
		assert.Empty(t, h.Get("Impersonate-Uid"))
		assert.Empty(t, h.Get("Impersonate-Extra-Scopes"))
	})

	t.Run("concurrent calls each impersonate their own user", func(t *testing.T) {
		rr := newRequestRecorder(t)
		kc := newClientFor(t, &rest.Config{Host: rr.server.URL})

		const workers = 8
		users := make([]string, workers)
		for i := range users {
			users[i] = fmt.Sprintf("system:serviceaccount:tenant-%d:deployer", i)
		}

		var wg sync.WaitGroup
		for _, u := range users {
			wg.Add(1)
			go func() {
				defer wg.Done()
				imp, err := kc.Impersonate(u)
				if !assert.NoError(t, err) {
					return
				}
				_, _ = imp.Clientset.CoreV1().Pods("ns").Get(context.Background(), "p", v1.GetOptions{})
			}()
		}
		wg.Wait()

		seen := map[string]int{}
		rr.mu.Lock()
		for _, h := range rr.requests {
			seen[h.Get("Impersonate-User")]++
		}
		rr.mu.Unlock()
		for _, u := range users {
			assert.Equal(t, 1, seen[u], "exactly one request as %s", u)
		}
		assert.Empty(t, kc.RestConfig.Impersonate.UserName, "the shared client must stay untouched")
	})

	t.Run("invalid user is refused and no client is returned", func(t *testing.T) {
		rr := newRequestRecorder(t)
		kc := newClientFor(t, &rest.Config{Host: rr.server.URL})

		imp, err := kc.Impersonate("system:masters")
		require.Error(t, err)
		assert.Nil(t, imp)
	})

	t.Run("missing REST config is refused", func(t *testing.T) {
		kc := NewKubernetesClient(context.Background(), nil, nil, "argocd")

		imp, err := kc.Impersonate(user)
		require.Error(t, err)
		assert.Nil(t, imp)
	})
}
