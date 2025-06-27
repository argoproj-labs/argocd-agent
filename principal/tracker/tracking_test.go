package tracker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Tracking(t *testing.T) {

	tracker := Tracker{
		statemap: requestState{
			requests: map[string]*requestWrapper{},
		},
	}

	ch, err := tracker.Track("123", "agent")
	assert.NotNil(t, ch)
	assert.NoError(t, err)
	t.Run("Check that request is tracked", func(t *testing.T) {
		agent, ch := tracker.Tracked("123")
		assert.Equal(t, "agent", agent)
		assert.NotNil(t, ch)
	})
	t.Run("Track existing request", func(t *testing.T) {
		ch, err := tracker.Track("123", "agent")
		assert.Nil(t, ch)
		assert.ErrorContains(t, err, "already tracked")
	})
	t.Run("Stop tracking request", func(t *testing.T) {
		err := tracker.StopTracking("123")
		require.NoError(t, err)
		// Can't stop tracking twice
		err = tracker.StopTracking("123")
		require.Error(t, err)
		// It's now untracked
		agent, ch := tracker.Tracked("123")
		assert.Empty(t, agent)
		assert.Nil(t, ch)
	})
}
