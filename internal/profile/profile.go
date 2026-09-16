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
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/sirupsen/logrus"
)

// isPprofEnabledFunc is a function that returns true if pprof is enabled.
// It is used to gate access to the pprof endpoints.
type isPprofEnabledFunc func() bool

// PprofServer is a HTTP server that serves pprof endpoints.
type PprofServer struct {
	addr      string
	logger    *logrus.Entry
	isEnabled isPprofEnabledFunc
}

func NewPprofServer(addr string, isEnabled isPprofEnabledFunc) *PprofServer {
	return &PprofServer{
		addr:      addr,
		logger:    logrus.WithField("module", "pprofServer"),
		isEnabled: isEnabled,
	}
}

func (s *PprofServer) Start(ctx context.Context) error {
	if s.addr == "" {
		return fmt.Errorf("pprof server address is not set")
	}

	mux := http.NewServeMux()
	s.RegisterProfiler(mux)

	srv := &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	go func() {
		s.logger.Infof("Starting pprof server on %s", srv.Addr)
		// ErrServerClosed is the expected result of Shutdown below.
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.WithError(err).Error("pprof server terminated")
		}
	}()

	go func() {
		<-ctx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			s.logger.WithError(err).Error("pprof server shutdown failed")
			return
		}
		s.logger.Infof("pprof server on %s shutdown", srv.Addr)
	}()

	return nil
}

// RegisterProfiler adds the pprof endpoints to mux. Every handler is gated, so
// registering them is safe even while pprof is disabled.
func (s *PprofServer) RegisterProfiler(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", s.pprofGate(pprof.Index))
	mux.HandleFunc("/debug/pprof/cmdline", s.pprofGate(pprof.Cmdline))
	mux.HandleFunc("/debug/pprof/profile", s.pprofGate(pprof.Profile))
	mux.HandleFunc("/debug/pprof/symbol", s.pprofGate(pprof.Symbol))
	mux.HandleFunc("/debug/pprof/trace", s.pprofGate(pprof.Trace))
}

// pprofGate allows a request only while pprof is enabled, otherwise returns 403.
func (s *PprofServer) pprofGate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isEnabled() {
			http.Error(w, "pprof is disabled, set \"pprof.enabled\" in the settings ConfigMap to enable it", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}
}
