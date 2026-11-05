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

package profile

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestMux returns a mux with the profiler registered, gated by isEnabled.
func newTestMux(isEnabled func() bool) *http.ServeMux {
	mux := http.NewServeMux()
	NewPprofServer("localhost:8080", isEnabled).RegisterProfiler(mux)
	return mux
}

func get(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestPprofGate(t *testing.T) {
	// Every registered endpoint must be gated. "profile" and "trace" are
	// omitted: both block for a duration, which is not useful in a unit test.
	paths := []string{
		"/debug/pprof/",
		"/debug/pprof/heap",
		"/debug/pprof/goroutine",
		"/debug/pprof/allocs",
		"/debug/pprof/cmdline",
		"/debug/pprof/symbol",
	}

	t.Run("returns 403 when pprof disabled", func(t *testing.T) {
		mux := newTestMux(func() bool { return false })

		for _, path := range paths {
			rec := get(mux, path)
			assert.Equal(t, http.StatusForbidden, rec.Code, "path %s", path)
			assert.Contains(t, rec.Body.String(), "pprof is disabled", "path %s", path)
		}
	})

	t.Run("serves profiles when pprof enabled", func(t *testing.T) {
		mux := newTestMux(func() bool { return true })

		for _, path := range paths {
			rec := get(mux, path)
			assert.Equal(t, http.StatusOK, rec.Code, "path %s", path)
			assert.NotContains(t, rec.Body.String(), "pprof is disabled", "path %s", path)
		}
	})

	t.Run("reflects runtime toggle", func(t *testing.T) {
		var isEnabled atomic.Bool
		mux := newTestMux(isEnabled.Load)

		// Toggle on via a simulated ConfigMap update.
		isEnabled.Store(true)
		assert.Equal(t, http.StatusOK, get(mux, "/debug/pprof/").Code)

		// Toggle back off.
		isEnabled.Store(false)
		assert.Equal(t, http.StatusForbidden, get(mux, "/debug/pprof/").Code)
	})
}

func TestPprofServerStartBindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	s := NewPprofServer(ln.Addr().String(), func() bool { return true })
	err = s.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to listen")
}
