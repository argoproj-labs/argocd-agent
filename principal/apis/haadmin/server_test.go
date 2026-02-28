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

package haadmin

import (
	"context"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/haadminapi"
	"github.com/argoproj-labs/argocd-agent/pkg/ha"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAuthMethod struct{}

func (f *fakeAuthMethod) Init() error                                                         { return nil }
func (f *fakeAuthMethod) Authenticate(_ context.Context, _ auth.Credentials) (string, error) { return "test", nil }

type fakeStatusProvider struct {
	replicas int
	agents   int
	lag      int64
	seq      uint64
}

func (f *fakeStatusProvider) ConnectedReplicaCount() int { return f.replicas }
func (f *fakeStatusProvider) LastEventTimestamp() int64  { return f.lag }
func (f *fakeStatusProvider) LastSequenceNum() uint64    { return f.seq }
func (f *fakeStatusProvider) ConnectedAgentCount() int   { return f.agents }

func newTestController(t *testing.T, preferred ha.Role, peerAddr string) *ha.Controller {
	t.Helper()
	opts := []ha.Option{
		ha.WithEnabled(true),
		ha.WithPreferredRole(string(preferred)),
		ha.WithAuthMethod(&fakeAuthMethod{}),
	}
	if peerAddr != "" {
		opts = append(opts, ha.WithPeerAddress(peerAddr))
	}
	c, err := ha.NewController(context.Background(), opts...)
	require.NoError(t, err)
	return c
}

func TestStatus(t *testing.T) {
	ctrl := newTestController(t, ha.RolePrimary, "peer:8443")
	sp := &fakeStatusProvider{replicas: 2, lag: 5, seq: 42}
	srv := NewServer(ctrl, sp)

	resp, err := srv.Status(context.Background(), &haadminapi.StatusRequest{})
	require.NoError(t, err)

	assert.Equal(t, string(ha.StateRecovering), resp.State)
	assert.Equal(t, string(ha.RolePrimary), resp.PreferredRole)
	assert.Equal(t, "peer:8443", resp.PeerAddress)
	assert.Equal(t, int32(2), resp.ConnectedReplicas)
	assert.Equal(t, int64(5), resp.LastEventTimestamp)
	assert.Equal(t, uint64(42), resp.LastSequenceNum)
}

func TestPromote(t *testing.T) {
	t.Run("from disconnected with force", func(t *testing.T) {
		ctrl := newTestController(t, ha.RoleReplica, "peer:8443")
		err := ctrl.Start()
		require.NoError(t, err)
		// Replica starts syncing; simulate connection then disconnect to reach disconnected
		ctrl.OnReplicationConnected()
		ctrl.OnReplicationDisconnected()
		assert.Equal(t, ha.StateDisconnected, ctrl.State())

		srv := NewServer(ctrl, nil)
		resp, err := srv.Promote(context.Background(), &haadminapi.PromoteRequest{Force: true})
		require.NoError(t, err)
		assert.Equal(t, string(ha.StateActive), resp.State)
	})

	t.Run("already active", func(t *testing.T) {
		ctrl := newTestController(t, ha.RolePrimary, "peer:8443")
		err := ctrl.Start()
		require.NoError(t, err)
		// Preferred primary, can't reach peer → goes ACTIVE
		assert.Equal(t, ha.StateActive, ctrl.State())

		srv := NewServer(ctrl, nil)
		_, err = srv.Promote(context.Background(), &haadminapi.PromoteRequest{Force: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already active")
	})
}

func TestDemote(t *testing.T) {
	t.Run("from active", func(t *testing.T) {
		ctrl := newTestController(t, ha.RolePrimary, "peer:8443")
		err := ctrl.Start()
		require.NoError(t, err)
		assert.Equal(t, ha.StateActive, ctrl.State())

		srv := NewServer(ctrl, nil)
		resp, err := srv.Demote(context.Background(), &haadminapi.DemoteRequest{})
		require.NoError(t, err)
		assert.Equal(t, string(ha.StateReplicating), resp.State)
	})

	t.Run("from non-active", func(t *testing.T) {
		ctrl := newTestController(t, ha.RoleReplica, "peer:8443")
		err := ctrl.Start()
		require.NoError(t, err)
		// Replica starts in syncing state
		assert.Equal(t, ha.StateSyncing, ctrl.State())

		srv := NewServer(ctrl, nil)
		_, err = srv.Demote(context.Background(), &haadminapi.DemoteRequest{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can only demote from active")
	})
}

func TestNilController(t *testing.T) {
	srv := NewServer(nil, nil)

	_, err := srv.Status(context.Background(), &haadminapi.StatusRequest{})
	require.Error(t, err)

	_, err = srv.Promote(context.Background(), &haadminapi.PromoteRequest{})
	require.Error(t, err)

	_, err = srv.Demote(context.Background(), &haadminapi.DemoteRequest{})
	require.Error(t, err)
}
