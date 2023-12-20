package clock

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_StandadClock(t *testing.T) {
	clock := StandardClock()
	var now, cnow time.Time
	// For the unlikely event that now and cnow are being called in another
	// second.
	for {
		now = time.Now()
		cnow = clock.Now()
		if now.Unix() == cnow.Unix() {
			break
		}
	}
}

func Test_SeededClock(t *testing.T) {
	t.Run("Seeded clock", func(t *testing.T) {
		seed, err := time.Parse(time.RFC3339, "2023-12-12T00:01:00Z")
		require.NoError(t, err)
		clock := SeededClock(seed)
		now := clock.Now()
		assert.Equal(t, 2023, now.Year())
		assert.Equal(t, time.Month(12), now.Month())
		assert.Equal(t, 12, now.Day())
	})
}
