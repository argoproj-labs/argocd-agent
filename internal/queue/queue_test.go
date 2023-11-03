package queue

import (
	"testing"

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
}
