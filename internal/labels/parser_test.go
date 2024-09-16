package labels

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_StringsToMap(t *testing.T) {
	t.Run("Single valid label", func(t *testing.T) {
		m, err := StringsToMap([]string{"foo=bar"})
		assert.NoError(t, err)
		assert.Equal(t, "bar", m["foo"])
	})
	t.Run("Preserve equal char in values", func(t *testing.T) {
		m, err := StringsToMap([]string{"foo=bar=baz"})
		assert.NoError(t, err)
		assert.Equal(t, "bar=baz", m["foo"])
	})
	t.Run("Multiple valid labels", func(t *testing.T) {
		m, err := StringsToMap([]string{"foo=bar", "bar=foo"})
		assert.NoError(t, err)
		assert.Equal(t, "bar", m["foo"])
		assert.Equal(t, "foo", m["bar"])
	})
	t.Run("Label without value", func(t *testing.T) {
		m, err := StringsToMap([]string{"foo=", "bar=foo"})
		assert.Error(t, err)
		assert.Nil(t, m)
	})
	t.Run("Malformed syntax", func(t *testing.T) {
		m, err := StringsToMap([]string{"foo"})
		assert.Error(t, err)
		assert.Nil(t, m)
	})
}
