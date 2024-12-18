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

package resourceproxy

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"regexp"

	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"k8s.io/client-go/rest"
)

// ResourceProxyOption is an option setting callback function
type ResourceProxyOption func(p *ResourceProxy) error

// podMatcher is the regular expression used to match a request path for pods
const podMatcher = `^/api/v1/namespaces/([^\/]+)/pods/([^\/]+)$`

// logMatcher is the regular expression used to match a request path for logs
const logMatcher = `^/api/v1/namespaces/([^\/]+)/pods/([^\/]+)/log$`

// execMatcher is the regular expression used to match a request path for exec
const execMatcher = `^/api/v1/namespaces/([^\/]+)/pods/([^\/]+)/exec$`

// WithRestConfig configures the proxy to use information from the given REST
// config for connecting to upstream.
func WithRestConfig(config *rest.Config) ResourceProxyOption {
	return func(p *ResourceProxy) error {
		tr, err := tlsutil.TransportFromConfig(config)
		if err != nil {
			return err
		}
		p.upstreamTransport = tr
		u, err := url.Parse(config.Host)
		if err != nil {
			return fmt.Errorf("error parsing upstream server: %w", err)
		}
		p.upstreamAddr = fmt.Sprintf("%s:%s", u.Hostname(), u.Port())
		p.upstreamScheme = u.Scheme
		return nil
	}
}

func WithTLSConfig(t *tls.Config) ResourceProxyOption {
	return func(p *ResourceProxy) error {
		p.tlsConfig = t
		return nil
	}
}

// WithUpstreamTransport sets the transport to use when connecting to the
// upstream API server.
func WithUpstreamTransport(t *http.Transport) ResourceProxyOption {
	return func(p *ResourceProxy) error {
		p.upstreamTransport = t
		return nil
	}
}

// WithUpstreamAddress sets the address for our upstream Kube API
func WithUpstreamAddress(host string, scheme string) ResourceProxyOption {
	return func(p *ResourceProxy) error {
		p.upstreamAddr = host
		p.upstreamScheme = scheme
		return nil
	}
}

// WithRequestMatcher adds a request matcher to the proxy. The handler fn will
// be executed when pattern matches on the request URI's path.
func WithRequestMatcher(pattern string, methods []string, fn HandlerFunc) ResourceProxyOption {
	return func(p *ResourceProxy) error {
		rm, err := matcher(pattern, methods, fn)
		if err != nil {
			return err
		}
		p.interceptors = append(p.interceptors, rm)
		return nil
	}
}

// WithPodMatcher registers a handler for calls to pods
func WithPodMatcher(fn HandlerFunc) ResourceProxyOption {
	return func(p *ResourceProxy) error {
		rm, err := matcher(podMatcher, []string{"namespace", "podname"}, fn)
		if err != nil {
			return err
		}
		p.interceptors = append(p.interceptors, rm)
		return nil
	}
}

// WithLogMatcher registers a handler for calls to pod logs
func WithLogMatcher(fn HandlerFunc) ResourceProxyOption {
	return func(p *ResourceProxy) error {
		rm, err := matcher(logMatcher, []string{"namespace", "podname"}, fn)
		if err != nil {
			return err
		}
		p.interceptors = append(p.interceptors, rm)
		return nil
	}
}

// WithExecMatcher registers a handler for calls to pod exec
func WithExecMatcher(fn HandlerFunc) ResourceProxyOption {
	return func(p *ResourceProxy) error {
		rm, err := matcher(execMatcher, []string{"namespace", "podname"}, fn)
		if err != nil {
			return err
		}
		p.interceptors = append(p.interceptors, rm)
		return nil
	}
}

// matcher creates and returns a new request matcher for the given pattern.
// If the pattern contains submatches, mapping
func matcher(pattern string, methods []string, fn HandlerFunc) (requestMatcher, error) {
	matcher, err := regexp.Compile(pattern)
	if err != nil {
		return requestMatcher{}, err
	}
	rm := requestMatcher{
		pattern: pattern,
		matcher: matcher,
		methods: methods,
		fn:      fn,
	}
	return rm, nil
}

func (rp *ResourceProxy) WithRequestMatcher(pattern string, mapping []string, fn HandlerFunc) error {
	f := WithRequestMatcher(pattern, mapping, fn)
	return f(rp)
}
