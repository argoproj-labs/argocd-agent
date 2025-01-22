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

If a requests matches any of the configured patterns,
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

	"github.com/sirupsen/logrus"
)

const defaultIdleTimeout = 5 * time.Second
const defaultReadTimeout = 5 * time.Second

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

	// If not upstream host was set, we use the default
	// if p.upstreamAddr == "" {
	// 	p.upstreamAddr = defaultUpstreamHost
	// }

	// If not upstream scheme was set, we use the default
	// if p.upstreamScheme == "" {
	// 	p.upstreamScheme = defaultUpstreamScheme
	// }

	p.mux = http.NewServeMux()

	// Configure the reverse proxy that proxies most of the requests to our
	// upstream Kube API. Anything that's not explicitly handled otherwise
	// will be routed to the upstream as-is.
	// p.proxy = &httputil.ReverseProxy{
	// 	ErrorLog: nil,
	// 	Director: func(r *http.Request) {
	// 		r.URL.Host = p.upstreamAddr
	// 		r.URL.Scheme = p.upstreamScheme
	// 	},
	// 	Transport: p.upstreamTransport,
	// 	ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
	// 		log().WithError(err).Errorf("Could not connect to upstream '%s://%s'", p.upstreamScheme, p.upstreamAddr)
	// 		w.WriteHeader(http.StatusBadGateway)
	// 	},
	// }

	// proxyHandler is the main HTTP handler, used to process every request
	p.mux.HandleFunc("/", p.proxyHandler)

	httpLogger := log().WriterLevel(logrus.ErrorLevel)

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
func (p *ResourceProxy) Start(ctx context.Context) (<-chan error, error) {
	log().Infof("Starting ResourceProxy on %s", p.addr)
	errCh := make(chan error)
	var l net.Listener
	var err error

	addr, err := netip.ParseAddrPort(p.addr)
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
		return nil, fmt.Errorf("could not figure out address type for %s", p.addr)
	}

	// Although we really should only support TLS, we do support plain text
	// connections too. But at least, we print a fat warning in that case.
	if p.tlsConfig != nil {
		l, err = tls.Listen(network, p.addr, p.tlsConfig)
	} else {
		log().Warn("INSECURE: kube-proxy is listening in non-TLS mode")
		l, err = net.Listen(network, p.addr)
	}
	if err != nil {
		return nil, err
	}

	// Start the HTTP server in the background
	go func() {
		errCh <- p.server.Serve(l)
	}()

	return errCh, nil
}

// Stop can be used to gracefully shut down the proxy server.
func (p *ResourceProxy) Stop(ctx context.Context) error {
	return p.server.Shutdown(ctx)
}

// proxyHandler is a HTTP request handler that inspects incoming requests. By
// default, every request will be passed down to the reverse proxy.
func (p *ResourceProxy) proxyHandler(w http.ResponseWriter, r *http.Request) {
	log().Debugf("Processing URI %s %s (goroutines:%d)", r.Method, r.RequestURI, runtime.NumGoroutine())

	// Loop through all registered matchers and match them against the request
	// URI's path. First match wins. This is obviously not the most efficient
	// nor performant way to do it, but we need regexp matching with submatch
	// extraction.
	for _, m := range p.interceptors {
		matches := m.matcher.FindStringSubmatch(r.URL.Path)
		if matches == nil {
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
			log().Tracef("Executing callback for %v", uriParams)
			m.fn(w, r, uriParams)
			return
		}
	}

	// Finally, if we had no handler match, we don't handle it
	log().Debugf("No interceptor matched %s %s", r.Method, r.RequestURI)
	w.WriteHeader(http.StatusBadRequest)
}

func log() *logrus.Entry {
	return logrus.WithField("module", "proxy")
}
