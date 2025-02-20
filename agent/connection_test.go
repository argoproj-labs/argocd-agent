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

package agent

import (
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/stretchr/testify/assert"
)

func TestResyncOnStart(t *testing.T) {
	a := newAgent(t)
	a.emitter = event.NewEventSource("test")
	logCtx := log()

	err := a.queues.Create(a.remote.ClientID())
	assert.Nil(t, err)

	t.Run("should return if the agent has already been synced", func(t *testing.T) {
		a.resyncedOnStart = true
		err := a.resyncOnStart(logCtx)
		assert.Nil(t, err)

		sendQ := a.queues.SendQ(a.remote.ClientID())
		assert.Zero(t, sendQ.Len())
	})

	t.Run("send request entity resync in autonomous mode", func(t *testing.T) {
		a.resyncedOnStart = false
		a.mode = types.AgentModeAutonomous
		err := a.resyncOnStart(logCtx)
		assert.Nil(t, err)

		sendQ := a.queues.SendQ(a.remote.ClientID())
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		assert.False(t, shutdown)

		assert.Equal(t, event.EventRequestEntityResync.String(), ev.Type())
		assert.True(t, a.resyncedOnStart)
	})

	t.Run("send basic entity list in managed mode", func(t *testing.T) {
		a.resyncedOnStart = false
		a.mode = types.AgentModeManaged
		err := a.resyncOnStart(logCtx)
		assert.Nil(t, err)

		sendQ := a.queues.SendQ(a.remote.ClientID())
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		assert.False(t, shutdown)

		assert.Equal(t, event.RequestBasicEntity.String(), ev.Type())
		assert.True(t, a.resyncedOnStart)
	})
}
