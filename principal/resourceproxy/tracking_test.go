package resourceproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Tracking(t *testing.T) {
	rp, err := New("127.0.0.1:0")
	require.NoError(t, err)
	ch, err := rp.Track("123", "agent")
	assert.NotNil(t, ch)
	assert.NoError(t, err)
	t.Run("Check that request is tracked", func(t *testing.T) {
		agent, ch := rp.Tracked("123")
		assert.Equal(t, "agent", agent)
		assert.NotNil(t, ch)
	})
	t.Run("Track existing request", func(t *testing.T) {
		ch, err := rp.Track("123", "agent")
		assert.Nil(t, ch)
		assert.ErrorContains(t, err, "already tracked")
	})
	t.Run("Stop tracking request", func(t *testing.T) {
		err := rp.StopTracking("123")
		require.NoError(t, err)
		// Can't stop tracking twice
		err = rp.StopTracking("123")
		require.Error(t, err)
		// It's now untracked
		agent, ch := rp.Tracked("123")
		assert.Empty(t, agent)
		assert.Nil(t, ch)
	})
}
