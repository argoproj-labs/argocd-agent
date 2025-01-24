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

package queue

import (
	"testing"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/stretchr/testify/assert"
)

func Test_Queue(t *testing.T) {
	t.Run("Initialize new queue pair", func(t *testing.T) {
		q := NewSendRecvQueues()
		assert.Equal(t, 0, q.Len())
		assert.False(t, q.HasQueuePair("agent1"))
		assert.Nil(t, q.RecvQ("agent1"))
		assert.Nil(t, q.SendQ("agent1"))
	})

	t.Run("Add a new named queue pair", func(t *testing.T) {
		q := NewSendRecvQueues()
		err := q.Create("agent1")
		assert.NoError(t, err)
		assert.Equal(t, 1, q.Len())
		assert.True(t, q.HasQueuePair("agent1"))
		assert.NotNil(t, q.RecvQ("agent1"))
		assert.NotNil(t, q.SendQ("agent1"))
	})

	t.Run("Queue pair must not be added twice", func(t *testing.T) {
		q := NewSendRecvQueues()
		err := q.Create("agent1")
		assert.NoError(t, err)
		err = q.Create("agent1")
		assert.Error(t, err)
	})

	t.Run("Delete queue pair", func(t *testing.T) {
		q := NewSendRecvQueues()
		err := q.Create("agent1")
		assert.NoError(t, err)
		err = q.Delete("agent1", true)
		assert.NoError(t, err)
		err = q.Delete("agent1", true)
		assert.Error(t, err)
	})

	t.Run("Ensure that the max queue size is respected", func(t *testing.T) {
		q := NewSendRecvQueues()
		err := q.Create("agent1")
		assert.NoError(t, err)
		queue := q.RecvQ("agent1")

		for i := 1; i <= defaultMaxQueueSize; i++ {
			ev := event.New()
			queue.Add(&ev)
		}

		// Since the queue is full, check if it is emptied before adding a new item.
		queue.Add(&event.Event{})
		assert.Equal(t, 1, queue.Len())
	})
}
