package session

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ClientIdFromContext(t *testing.T) {
	t.Run("Successfully extract client ID", func(t *testing.T) {
		ctx := ClientIdToContext(context.Background(), "agent")
		a, err := ClientIdFromContext(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "agent", a)
	})
	t.Run("No client ID in context", func(t *testing.T) {
		a, err := ClientIdFromContext(context.Background())
		assert.ErrorContains(t, err, "no client identifier")
		assert.Empty(t, a)
	})
	t.Run("Invalid client ID in context", func(t *testing.T) {
		ctx := ClientIdToContext(context.Background(), "ag_ent")
		a, err := ClientIdFromContext(ctx)
		assert.ErrorContains(t, err, "invalid client identifier")
		assert.Empty(t, a)
	})
}
