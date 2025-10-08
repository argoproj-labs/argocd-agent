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
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_WithPort(t *testing.T) {
	ports := []struct {
		port  int
		valid bool
	}{
		{1, true},
		{0, true},
		{-1, false},
		{65535, true},
		{65536, false},
	}

	for _, tt := range ports {
		s := &Server{options: &ServerOptions{}}
		err := WithListenerPort(tt.port)(s)
		if tt.valid {
			assert.NoErrorf(t, err, "port %d should be valid", tt.port)
			assert.Equal(t, tt.port, s.options.port)
		} else {
			assert.Errorf(t, err, "port %d should be invalid", tt.port)
			assert.Equal(t, 0, s.options.port)
		}
	}
}

func Test_WithListenerAddress(t *testing.T) {
	s := &Server{options: &ServerOptions{}}
	err := WithListenerAddress("127.0.0.1")(s)
	assert.NoError(t, err)
	assert.Equal(t, "127.0.0.1", s.options.address)
}

func Test_WithTLSCipherSuite(t *testing.T) {
	t.Run("All valid cipher suites", func(t *testing.T) {
		for _, cs := range tls.CipherSuites() {
			s := &Server{options: &ServerOptions{}}
			err := WithTLSCipherSuite(cs.Name)(s)
			assert.NoError(t, err)
			assert.Equal(t, cs, s.options.tlsCiphers)
		}
	})

	t.Run("Invalid cipher suite", func(t *testing.T) {
		s := &Server{options: &ServerOptions{}}
		err := WithTLSCipherSuite("cowabunga")(s)
		assert.Error(t, err)
		assert.Nil(t, s.options.tlsCiphers)
	})
}

func Test_WithMinimumTLSVersion(t *testing.T) {
	t.Run("All valid minimum cipher suites", func(t *testing.T) {
		for k, v := range supportedTLSVersion {
			s := &Server{options: &ServerOptions{}}
			err := WithMinimumTLSVersion(k)(s)
			assert.NoError(t, err)
			assert.Equal(t, v, s.options.tlsMinVersion)
		}
	})

	t.Run("Invalid minimum cipher suites", func(t *testing.T) {
		for _, v := range []string{"tls1.0", "ssl3.0", "invalid", "tls"} {
			s := &Server{options: &ServerOptions{}}
			err := WithMinimumTLSVersion(v)(s)
			assert.Error(t, err)
			assert.Equal(t, 0, s.options.tlsMinVersion)
		}
	})
}

func Test_WithRedisProxyDisabled(t *testing.T) {
	s := &Server{options: &ServerOptions{}}
	err := WithRedisProxyDisabled()(s)
	assert.NoError(t, err)
	assert.True(t, s.options.redisProxyDisabled)
}
