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
	"regexp"

	"github.com/argoproj-labs/argocd-agent/internal/logging"
)

// ResourceProxyOption is an option setting callback function
type ResourceProxyOption func(p *ResourceProxy) error

// WithTLSConfig sets the TLS configuration used for the proxy's listener
func WithTLSConfig(t *tls.Config) ResourceProxyOption {
	return func(p *ResourceProxy) error {
		p.tlsConfig = t
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

// WithLogger sets the logger for the proxy to what is passed in. If this option is not provided
// the New function will set it to the default logger
func (rp *ResourceProxy) WithLogger(logger *logging.CentralizedLogger) ResourceProxyOption {
	return func(p *ResourceProxy) error {
		p.logger = logger
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
