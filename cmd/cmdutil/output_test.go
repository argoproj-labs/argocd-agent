package cmdutil

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
)

func Test_MarshalStruct(t *testing.T) {
	type good struct {
		Visible   string `json:"visible" yaml:"visible" text:"Visible"`
		invisible string `json:"visible" yaml:"visible" text:"Visible"`
	}
	t.Run("Marshal to JSON", func(t *testing.T) {
		s := &good{"visible", "invisible"}
		out := &good{}
		in, err := MarshalStruct(s, "json")
		assert.NoError(t, err)
		err = json.Unmarshal(in, out)
		assert.NoError(t, err)
		assert.Equal(t, s.Visible, out.Visible)
		assert.Empty(t, out.invisible)
	})
	t.Run("Marshal to YAML", func(t *testing.T) {
		s := &good{"visible", "invisible"}
		out := &good{}
		in, err := MarshalStruct(s, "yaml")
		assert.NoError(t, err)
		err = yaml.Unmarshal(in, out)
		assert.NoError(t, err)
		assert.Equal(t, s.Visible, out.Visible)
		assert.Empty(t, out.invisible)
	})
	t.Run("Marshal to text", func(t *testing.T) {
		s := &good{"visible", "invisible"}
		in, err := MarshalStruct(s, "text")
		assert.NoError(t, err)
		assert.Regexp(t, `^Visible:\s+visible\n$`, string(in))
	})

	t.Run("Marshal to unknown", func(t *testing.T) {
		s := &good{"visible", "invisible"}
		in, err := MarshalStruct(s, "unknown")
		assert.ErrorContains(t, err, "unknown output")
		assert.Empty(t, in)
	})
}
