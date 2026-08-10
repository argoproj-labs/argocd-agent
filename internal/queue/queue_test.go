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
	"strconv"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/config"
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
			ev.SetID(strconv.Itoa(i))
			queue.Add(&ev)
		}

		// Since the queue is full, check if the oldest item is popped before adding a new item.
		ev := event.New()
		ev.SetID(strconv.Itoa(defaultMaxQueueSize + 1))
		queue.Add(&ev)
		assert.Equal(t, defaultMaxQueueSize, queue.Len())
		front, _ := queue.Get()
		assert.Equal(t, "2", front.ID())
	})

	t.Run("Ensure that the queue sizes can be configured via environment variable", func(t *testing.T) {
		queueSize := 100
		t.Setenv(config.EnvRecvQueueSize, strconv.Itoa(queueSize))
		t.Setenv(config.EnvSendQueueSize, strconv.Itoa(queueSize+20))
		q := NewSendRecvQueues()
		err := q.Create("agent1")
		assert.NoError(t, err)
		recvQueue := q.RecvQ("agent1")
		sendQueue := q.SendQ("agent1")

		for i := 1; i <= queueSize+20; i++ {
			ev := event.New()
			ev.SetID(strconv.Itoa(i))
			recvQueue.Add(&ev)
			sendQueue.Add(&ev)
		}

		// Since the queue is full, check if the oldest item is popped before adding a new item.
		ev := event.New()
		ev.SetID(strconv.Itoa(queueSize + 1))
		recvQueue.Add(&ev)
		sendQueue.Add(&ev)
		assert.Equal(t, queueSize, recvQueue.Len())
		assert.Equal(t, queueSize+20, sendQueue.Len())
		front, _ := recvQueue.Get()
		assert.Equal(t, "22", front.ID())
		front, _ = sendQueue.Get()
		assert.Equal(t, "2", front.ID())
	})

}
