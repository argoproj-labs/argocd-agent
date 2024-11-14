package checkpoint

import (
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_CheckpointInitialization(t *testing.T) {
	t.Run("Checkpoint has name properly set", func(t *testing.T) {
		cp := NewCheckpoint("bar")
		assert.Equal(t, "bar", cp.Name())
	})
	t.Run("Checkpoint returns duration of 0 when not run", func(t *testing.T) {
		cp := NewCheckpoint("bar")
		assert.Equal(t, time.Duration(0), cp.Duration())
	})
	t.Run("Checkpoint has no steps after initialization", func(t *testing.T) {
		cp := NewCheckpoint("bar")
		assert.Equal(t, 0, cp.NumSteps())
	})
}

func newSeed(t *testing.T) time.Time {
	seed, err := time.Parse(time.RFC3339, "2023-12-12T00:01:00Z")
	require.NoError(t, err)
	return seed
}

func Test_CheckpointMeasure(t *testing.T) {
	t.Run("It measures a single step", func(t *testing.T) {
		cl := clock.SeededClock(newSeed(t))
		cp := NewCheckpoint("bar", cl)
		cp.Start("step1")
		cl.At(cl.Now().Add(1 * time.Second))
		cp.End()
		assert.Equal(t, 1, cp.NumSteps())
		assert.Equal(t, 1*time.Second, cp.Duration())
	})
	t.Run("It measures multiple steps", func(t *testing.T) {
		cl := clock.SeededClock(newSeed(t))
		cp := NewCheckpoint("bar", cl)
		cp.Start("step1")
		cl.At(cl.Now().Add(1 * time.Second))
		cp.Start("step2")
		cl.At(cl.Now().Add(1 * time.Second))
		cp.End()
		assert.Equal(t, 2, cp.NumSteps())
		assert.Equal(t, 2*time.Second, cp.Duration())
	})
	t.Run("Steps can be measured independently", func(t *testing.T) {
		cl := clock.SeededClock(newSeed(t))
		cp := NewCheckpoint("bar", cl)
		cp.Start("step1")
		cl.At(cl.Now().Add(1 * time.Second))
		cp.End()
		steps := cp.Steps()
		assert.Len(t, steps, 1)
		assert.Equal(t, 1*time.Second, steps[0].Duration())
		assert.Equal(t, 1*time.Second, cp.Duration())
	})
	t.Run("Unfinished step returns 0", func(t *testing.T) {
		s := Step{}
		assert.Equal(t, time.Duration(0), s.Duration())
	})
}

func Test_CheckpointOutput(t *testing.T) {
	t.Run("It generates a usable string", func(t *testing.T) {
		cl := clock.SeededClock(newSeed(t))
		cp := NewCheckpoint("bar", cl)
		cp.Start("step1")
		cl.At(cl.Now().Add(1 * time.Second))
		cp.Start("step2")
		cl.At(cl.Now().Add(1 * time.Second))
		cp.End()
		s := cp.String()
		assert.Contains(t, s, "checkpoint bar duration=2.000s")
		assert.Contains(t, s, "step1=1.000s")
		assert.Contains(t, s, "step2=1.000s")
	})
}
