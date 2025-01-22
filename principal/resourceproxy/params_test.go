package resourceproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_Params_Set(t *testing.T) {
	p := NewParams()
	p.Set("some", "value")
	assert.Equal(t, "value", p["some"])
	assert.Empty(t, p["value"])
}

func Test_Params_Get(t *testing.T) {
	p := NewParams()
	p.Set("some", "value")
	assert.Equal(t, "value", p.Get("some"))
	assert.Empty(t, p.Get("value"))
}

func Test_Params_Has(t *testing.T) {
	p := NewParams()
	p.Set("some", "value")
	assert.True(t, p.Has("some"))
	assert.False(t, p.Has("value"))
}
