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
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_WithInformerSyncTimeout(t *testing.T) {
	s := &Server{options: &ServerOptions{}}
	err := WithInformerSyncTimeout(5 * time.Second)(s)
	assert.NoError(t, err)
	assert.Equal(t, 5*time.Second, s.options.informerSyncTimeout)
}

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

func Test_WithTLSCipherSuites(t *testing.T) {
	t.Run("Single valid cipher suite", func(t *testing.T) {
		cs := tls.CipherSuites()[0]
		s := &Server{options: &ServerOptions{}}
		err := WithTLSCipherSuites([]string{cs.Name})(s)
		assert.NoError(t, err)
		assert.Equal(t, []uint16{cs.ID}, s.options.tlsCiphers)
	})

	t.Run("Multiple valid cipher suites", func(t *testing.T) {
		ciphers := tls.CipherSuites()
		if len(ciphers) >= 2 {
			s := &Server{options: &ServerOptions{}}
			err := WithTLSCipherSuites([]string{ciphers[0].Name, ciphers[1].Name})(s)
			assert.NoError(t, err)
			assert.Equal(t, []uint16{ciphers[0].ID, ciphers[1].ID}, s.options.tlsCiphers)
		}
	})

	t.Run("Empty cipher suites", func(t *testing.T) {
		s := &Server{options: &ServerOptions{}}
		err := WithTLSCipherSuites([]string{})(s)
		assert.NoError(t, err)
		assert.Nil(t, s.options.tlsCiphers)
	})

	t.Run("Invalid cipher suite", func(t *testing.T) {
		s := &Server{options: &ServerOptions{}}
		err := WithTLSCipherSuites([]string{"cowabunga"})(s)
		assert.Error(t, err)
		assert.Nil(t, s.options.tlsCiphers)
	})

	t.Run("Mix of valid and invalid cipher suites", func(t *testing.T) {
		cs := tls.CipherSuites()[0]
		s := &Server{options: &ServerOptions{}}
		err := WithTLSCipherSuites([]string{cs.Name, "invalid"})(s)
		assert.Error(t, err)
	})
}

func Test_WithMinimumTLSVersion(t *testing.T) {
	t.Run("All valid minimum TLS versions", func(t *testing.T) {
		versions := map[string]uint16{
			"tls1.1": tls.VersionTLS11,
			"tls1.2": tls.VersionTLS12,
			"tls1.3": tls.VersionTLS13,
		}
		for k, v := range versions {
			s := &Server{options: &ServerOptions{}}
			err := WithMinimumTLSVersion(k)(s)
			assert.NoError(t, err)
			assert.Equal(t, v, s.options.tlsMinVersion)
		}
	})

	t.Run("Invalid minimum TLS versions", func(t *testing.T) {
		for _, v := range []string{"tls1.0", "ssl3.0", "invalid", "tls"} {
			s := &Server{options: &ServerOptions{}}
			err := WithMinimumTLSVersion(v)(s)
			assert.Error(t, err)
			assert.Equal(t, uint16(0), s.options.tlsMinVersion)
		}
	})
}

func Test_WithMaximumTLSVersion(t *testing.T) {
	t.Run("All valid maximum TLS versions", func(t *testing.T) {
		versions := map[string]uint16{
			"tls1.1": tls.VersionTLS11,
			"tls1.2": tls.VersionTLS12,
			"tls1.3": tls.VersionTLS13,
		}
		for k, v := range versions {
			s := &Server{options: &ServerOptions{}}
			err := WithMaximumTLSVersion(k)(s)
			assert.NoError(t, err)
			assert.Equal(t, v, s.options.tlsMaxVersion)
		}
	})

	t.Run("Invalid maximum TLS versions", func(t *testing.T) {
		for _, v := range []string{"tls1.0", "ssl3.0", "invalid", "tls"} {
			s := &Server{options: &ServerOptions{}}
			err := WithMaximumTLSVersion(v)(s)
			assert.Error(t, err)
			assert.Equal(t, uint16(0), s.options.tlsMaxVersion)
		}
	})
}

func Test_WithRedisProxyDisabled(t *testing.T) {
	s := &Server{options: &ServerOptions{}}
	err := WithRedisProxyDisabled()(s)
	assert.NoError(t, err)
	assert.True(t, s.options.redisProxyDisabled)
}
