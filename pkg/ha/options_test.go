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

package ha

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()

	assert.False(t, opts.Enabled)
	assert.Equal(t, RoleReplica, opts.PreferredRole)
	assert.Equal(t, 30*time.Second, opts.FailoverTimeout)
	assert.Equal(t, 8404, opts.ReplicationPort)
	assert.Equal(t, 1*time.Second, opts.ReconnectBackoffInitial)
	assert.Equal(t, 30*time.Second, opts.ReconnectBackoffMax)
	assert.Equal(t, 1.5, opts.ReconnectBackoffFactor)
}

func TestWithEnabled(t *testing.T) {
	opts := DefaultOptions()
	err := WithEnabled(true)(opts)
	require.NoError(t, err)
	assert.True(t, opts.Enabled)
}

func TestWithPreferredRole(t *testing.T) {
	t.Run("primary", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithPreferredRole("primary")(opts)
		require.NoError(t, err)
		assert.Equal(t, RolePrimary, opts.PreferredRole)
	})

	t.Run("replica", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithPreferredRole("replica")(opts)
		require.NoError(t, err)
		assert.Equal(t, RoleReplica, opts.PreferredRole)
	})

	t.Run("invalid", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithPreferredRole("invalid")(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown role")
	})
}

func TestWithPeerAddress(t *testing.T) {
	t.Run("valid address", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithPeerAddress("peer.example.com:8404")(opts)
		require.NoError(t, err)
		assert.Equal(t, "peer.example.com:8404", opts.PeerAddress)
	})

	t.Run("empty address", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithPeerAddress("")(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})
}

func TestWithFailoverTimeout(t *testing.T) {
	t.Run("valid timeout", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithFailoverTimeout(60 * time.Second)(opts)
		require.NoError(t, err)
		assert.Equal(t, 60*time.Second, opts.FailoverTimeout)
	})

	t.Run("negative timeout", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithFailoverTimeout(-1 * time.Second)(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be negative")
	})
}

func TestWithReplicationPort(t *testing.T) {
	t.Run("valid port", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithReplicationPort(8405)(opts)
		require.NoError(t, err)
		assert.Equal(t, 8405, opts.ReplicationPort)
	})

	t.Run("port too low", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithReplicationPort(0)(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be between 1 and 65535")
	})

	t.Run("port too high", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithReplicationPort(65536)(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be between 1 and 65535")
	})
}

func TestWithTLSConfig(t *testing.T) {
	opts := DefaultOptions()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	err := WithTLSConfig(tlsConfig)(opts)
	require.NoError(t, err)
	assert.Equal(t, tlsConfig, opts.TLSConfig)
}

func TestWithReconnectBackoff(t *testing.T) {
	t.Run("valid backoff", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithReconnectBackoff(2*time.Second, 60*time.Second, 2.0)(opts)
		require.NoError(t, err)
		assert.Equal(t, 2*time.Second, opts.ReconnectBackoffInitial)
		assert.Equal(t, 60*time.Second, opts.ReconnectBackoffMax)
		assert.Equal(t, 2.0, opts.ReconnectBackoffFactor)
	})

	t.Run("negative initial", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithReconnectBackoff(-1*time.Second, 60*time.Second, 2.0)(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "initial backoff cannot be negative")
	})

	t.Run("max less than initial", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithReconnectBackoff(60*time.Second, 30*time.Second, 2.0)(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max backoff cannot be less than initial")
	})

	t.Run("factor less than 1", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithReconnectBackoff(1*time.Second, 60*time.Second, 0.5)(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "backoff factor must be at least 1.0")
	})
}

func TestOptionsValidate(t *testing.T) {
	t.Run("disabled is always valid", func(t *testing.T) {
		opts := DefaultOptions()
		opts.Enabled = false
		err := opts.Validate()
		require.NoError(t, err)
	})

	t.Run("enabled replica requires peer address", func(t *testing.T) {
		opts := DefaultOptions()
		opts.Enabled = true
		opts.PreferredRole = RoleReplica
		opts.PeerAddress = ""
		err := opts.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "peer address is required for replica")
	})

	t.Run("enabled primary without peer address is valid", func(t *testing.T) {
		opts := DefaultOptions()
		opts.Enabled = true
		opts.PreferredRole = RolePrimary
		opts.PeerAddress = ""
		err := opts.Validate()
		require.NoError(t, err)
	})

	t.Run("valid enabled options with peer", func(t *testing.T) {
		opts := DefaultOptions()
		opts.Enabled = true
		opts.PeerAddress = "peer:8404"
		err := opts.Validate()
		require.NoError(t, err)
	})
}
