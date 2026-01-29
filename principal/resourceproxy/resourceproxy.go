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

/*
Package resourceproxy implements a very specific, non-general purpose HTTP
proxy server for intercepting a configurable list of calls to the Kubernetes
API.

If a requests matches any of the configured patterns, named fields in the
regexp will be mapped to a params structure and passed to a handler for
further processing.
*/
package resourceproxy

import (
	"context"
	"crypto/tls"
	"fmt"
	golog "log"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/sirupsen/logrus"
)

const (
	defaultIdleTimeout = 5 * time.Second
	defaultReadTimeout = 5 * time.Second
)

// ResourceProxy represents a reverse proxy with interceptors for the Kubernetes
// API. Always Use New to get a new instance.
type ResourceProxy struct {
	// addr specifies the address where this proxy listens on. Must be in the
	// format [address:port].
	addr string
	// tlsConfig is the TLS configuration for the proxy's listener
	tlsConfig *tls.Config

	idleTimeout time.Duration
	readTimeout time.Duration

	// mux is the proxy's HTTP serve mux
	mux *http.ServeMux

	// server is the proxy's HTTP server
	server *http.Server

	// interceptors is a list of interceptors for intercepting requests
	interceptors []requestMatcher

	// state holds state information about requests
	statemap requestState

	// logger is a separate logger from the default one to allow control to the log level of this subsystem
	logger *logging.CentralizedLogger
}

// HandlerFunc is a parameterized HTTP handler function
type HandlerFunc func(w http.ResponseWriter, r *http.Request, params Params)

// requestMatcher holds information for matching requests against
type requestMatcher struct {
	pattern string
	matcher *regexp.Regexp
	methods []string
	fn      HandlerFunc
}

// New returns a new instance of ResourceProxy for connecting to the given upstream
// with the given options. The proxy will listen on host, which must be given
// in the form of [address:port].
//
// The upstream will be defined by the supplied Kubernetes client REST config,
// which needs to hold all properties required to connect to the upstream API.
func New(addr string, options ...ResourceProxyOption) (*ResourceProxy, error) {
	// Just to make sure addr is valid before passing it further down
	_, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid listener definition: %w", err)
	}
	p := &ResourceProxy{
		addr:        addr,
		idleTimeout: defaultIdleTimeout,
		readTimeout: defaultReadTimeout,
	}

	// Options may return error, which aborts instantiation
	for _, o := range options {
		if err := o(p); err != nil {
			return nil, err
		}
	}

	if p.logger == nil {
		p.logger = logging.GetDefaultLogger()
	}

	p.mux = http.NewServeMux()

	// proxyHandler is the main HTTP handler, used to process every request
	p.mux.HandleFunc("/", p.proxyHandler)

	httpLogger := p.log().WriterLevel(logrus.ErrorLevel)

	// Configure the HTTP server
	p.server = &http.Server{
		Addr:              p.addr,
		Handler:           p.mux,
		TLSConfig:         p.tlsConfig,
		ErrorLog:          golog.New(httpLogger, "", 0),
		IdleTimeout:       p.idleTimeout,
		ReadHeaderTimeout: p.readTimeout,
	}

	p.statemap = requestState{requests: make(map[string]*requestWrapper)}
	return p, nil
}

// Start starts the proxy in the background and immediately returns. The caller
// may read an error from the returned channel. The channel will only be
// written to in case of a start-up error, or when the proxy has shut down.
func (rp *ResourceProxy) Start(ctx context.Context) (<-chan error, error) {
	rp.log().Infof("Starting ResourceProxy on %s", rp.addr)
	errCh := make(chan error)
	var l net.Listener
	var err error

	addr, err := netip.ParseAddrPort(rp.addr)
	if err != nil {
		return nil, fmt.Errorf("invalid listener address: %w", err)
	}

	// We support both, IPv4 and IPv6 for the proxy
	var network string
	if addr.Addr().Is4() {
		network = "tcp"
	} else if addr.Addr().Is6() {
		network = "tcp6"
	} else {
		return nil, fmt.Errorf("could not figure out address type for %s", rp.addr)
	}

	// Although we really should only support TLS, we do support plain text
	// connections too. But at least, we print a fat warning in that case.
	if rp.tlsConfig != nil {
		l, err = tls.Listen(network, rp.addr, rp.tlsConfig)
	} else {
		rp.log().Warn("INSECURE: kube-proxy is listening in non-TLS mode")
		l, err = net.Listen(network, rp.addr)
	}
	if err != nil {
		return nil, err
	}

	// Start the HTTP server in the background
	go func() {
		errCh <- rp.server.Serve(l)
	}()

	return errCh, nil
}

// Stop can be used to gracefully shut down the proxy server.
func (rp *ResourceProxy) Stop(ctx context.Context) error {
	return rp.server.Shutdown(ctx)
}

// proxyHandler is a HTTP request handler that inspects incoming requests. By
// default, every request will be passed down to the reverse proxy.
func (rp *ResourceProxy) proxyHandler(w http.ResponseWriter, r *http.Request) {
	rp.log().Debugf("Processing URI %s %s (goroutines:%d)", r.Method, r.RequestURI, runtime.NumGoroutine())

	// Loop through all registered matchers and match them against the request
	// URI's path. First match wins. This is obviously not the most efficient
	// nor performant way to do it, but we need regexp matching with submatch
	// extraction.
	for _, m := range rp.interceptors {
		matches := m.matcher.FindStringSubmatch(r.URL.Path)
		if matches == nil {
			rp.log().Debugf("Request did not match %s %s", r.Method, r.RequestURI)
			continue
		} else {
			validMethod := false
			for _, method := range m.methods {
				if strings.EqualFold(r.Method, method) {
					validMethod = true
				}
			}
			// We must have a callback function defined. Also, method must be
			// allowed.
			if !validMethod || m.fn == nil {
				rp.log().Debugf("Method %s not allowed for URI %s", r.Method, r.RequestURI)
				w.WriteHeader(http.StatusForbidden)
				return
			}

			// uriParams will hold the named matches from the regexp
			uriParams := NewParams()
			for i, name := range m.matcher.SubexpNames() {
				if i != 0 && name != "" {
					uriParams.Set(name, matches[i])
				}
			}

			// Call the handler with our params. The connection will stay open
			// until the handler returns.
			rp.log().Tracef("Executing callback for %v", uriParams)
			m.fn(w, r, uriParams)
			return
		}
	}

	// Finally, if we had no handler match, we don't handle it
	rp.log().Debugf("No interceptor matched %s %s", r.Method, r.RequestURI)
	w.WriteHeader(http.StatusBadRequest)
}

func (rp *ResourceProxy) log() *logrus.Entry {
	return logging.SelectLogger(rp.logger).ModuleLogger("proxy")
}
