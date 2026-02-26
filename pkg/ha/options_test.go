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
	"context"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAuthMethod struct{}

func (f *fakeAuthMethod) Init() error                                                     { return nil }
func (f *fakeAuthMethod) Authenticate(_ context.Context, _ auth.Credentials) (string, error) { return "test", nil }

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()

	assert.False(t, opts.Enabled)
	assert.Equal(t, RoleReplica, opts.PreferredRole)
	assert.Equal(t, 30*time.Second, opts.FailoverTimeout)
	assert.Equal(t, 8405, opts.AdminPort)
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
		err := WithPeerAddress("peer.example.com:8443")(opts)
		require.NoError(t, err)
		assert.Equal(t, "peer.example.com:8443", opts.PeerAddress)
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

func TestWithAdminPort(t *testing.T) {
	t.Run("valid port", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithAdminPort(9090)(opts)
		require.NoError(t, err)
		assert.Equal(t, 9090, opts.AdminPort)
	})

	t.Run("port zero", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithAdminPort(0)(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "admin port must be between")
	})

	t.Run("negative port", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithAdminPort(-1)(opts)
		require.Error(t, err)
	})

	t.Run("port exceeds max", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithAdminPort(65536)(opts)
		require.Error(t, err)
	})
}

func TestWithAuthMethod(t *testing.T) {
	opts := DefaultOptions()
	m := &fakeAuthMethod{}
	err := WithAuthMethod(m)(opts)
	require.NoError(t, err)
	assert.Equal(t, m, opts.AuthMethod)
}

func TestWithAllowedReplicationClients(t *testing.T) {
	t.Run("set clients", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithAllowedReplicationClients([]string{"region-a", "region-b"})(opts)
		require.NoError(t, err)
		assert.Equal(t, []string{"region-a", "region-b"}, opts.AllowedReplicationClients)
	})

	t.Run("empty list", func(t *testing.T) {
		opts := DefaultOptions()
		err := WithAllowedReplicationClients([]string{})(opts)
		require.NoError(t, err)
		assert.Empty(t, opts.AllowedReplicationClients)
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

	t.Run("enabled without auth method returns error", func(t *testing.T) {
		opts := DefaultOptions()
		opts.Enabled = true
		opts.PreferredRole = RolePrimary
		opts.AuthMethod = nil
		err := opts.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ha-replication-auth is required")
	})

	t.Run("enabled primary without peer address is valid", func(t *testing.T) {
		opts := DefaultOptions()
		opts.Enabled = true
		opts.PreferredRole = RolePrimary
		opts.PeerAddress = ""
		opts.AuthMethod = &fakeAuthMethod{}
		err := opts.Validate()
		require.NoError(t, err)
	})

	t.Run("valid enabled options with peer", func(t *testing.T) {
		opts := DefaultOptions()
		opts.Enabled = true
		opts.PeerAddress = "peer:8443"
		opts.AuthMethod = &fakeAuthMethod{}
		err := opts.Validate()
		require.NoError(t, err)
	})
}
