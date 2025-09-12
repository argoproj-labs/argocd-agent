// Copyright 2025 The argocd-agent Authors
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
	"k8s.io/client-go/rest"
)

func TestResyncOnStart(t *testing.T) {
	a, _ := newAgent(t)
	a.emitter = event.NewEventSource("test")
	a.kubeClient.RestConfig = &rest.Config{}
	logCtx := log()

	t.Run("should return if the agent has already been synced", func(t *testing.T) {
		a.resyncedOnStart = true
		err := a.resyncOnStart(logCtx)
		assert.Nil(t, err)

		sendQ := a.queues.SendQ(defaultQueueName)
		assert.Zero(t, sendQ.Len())
	})

	t.Run("send resource resync request in autonomous mode", func(t *testing.T) {
		a.resyncedOnStart = false
		a.mode = types.AgentModeAutonomous
		err := a.resyncOnStart(logCtx)
		assert.Nil(t, err)

		sendQ := a.queues.SendQ(defaultQueueName)
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		assert.False(t, shutdown)

		assert.Equal(t, event.EventRequestResourceResync.String(), ev.Type())
		assert.True(t, a.resyncedOnStart)
	})

	t.Run("send synced resource list request in managed mode", func(t *testing.T) {
		a.resyncedOnStart = false
		a.mode = types.AgentModeManaged
		err := a.resyncOnStart(logCtx)
		assert.Nil(t, err)

		sendQ := a.queues.SendQ(defaultQueueName)
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		assert.False(t, shutdown)

		assert.Equal(t, event.SyncedResourceList.String(), ev.Type())
		assert.True(t, a.resyncedOnStart)
	})
}
