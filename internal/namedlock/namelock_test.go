package namedlock

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_Lock(t *testing.T) {
	nl := NewNamedLock()
	assert.True(t, nl.TryLock("foo"))
	assert.False(t, nl.TryLock("foo"))
	assert.True(t, nl.TryLock("bar"))
	testch := make(chan struct{})
	go func() {
		// We have no assertions here, other than being able to acquire locks
		nl.Unlock("foo")
		nl.Unlock("bar")
		nl.RLock("foo")
		nl.RLock("foo")
		assert.False(t, nl.TryLock("foo"))
		nl.RUnlock("foo")
		nl.RUnlock("foo")
		nl.Lock("foo")
		assert.False(t, nl.TryRLock("foo"))
		nl.Unlock("foo")
		assert.True(t, nl.TryRLock("foo"))
		nl.RUnlock("foo")
		close(testch)
	}()
	timeout := time.NewTimer(1 * time.Second)
	for {
		select {
		case <-testch:
			return
		case <-timeout.C:
			t.Fatal("timeout reached")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
