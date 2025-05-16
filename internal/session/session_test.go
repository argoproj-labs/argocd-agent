package session

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ClientInfoInContext(t *testing.T) {
	t.Run("Successfully extract client ID and mode", func(t *testing.T) {
		ctx := ClientInfoToContext(context.Background(), "agent", "managed")
		a, err := ClientIDFromContext(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "agent", a)
		m, err := ClientModeFromContext(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "managed", m)
	})
	t.Run("No client ID in context", func(t *testing.T) {
		a, err := ClientIDFromContext(context.Background())
		assert.ErrorContains(t, err, "no client identifier")
		assert.Empty(t, a)
	})
	t.Run("No client mode in context", func(t *testing.T) {
		a, err := ClientModeFromContext(context.Background())
		assert.ErrorContains(t, err, "no client mode found in context")
		assert.Empty(t, a)
	})
	t.Run("Invalid client ID in context", func(t *testing.T) {
		ctx := ClientInfoToContext(context.Background(), "ag_ent", "managed")
		a, err := ClientIDFromContext(ctx)
		assert.ErrorContains(t, err, "invalid client identifier")
		assert.Empty(t, a)
	})
}
