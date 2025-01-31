package cmdutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"text/tabwriter"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
)

func Test_MarshalStruct(t *testing.T) {
	type good struct {
		Visible   string `json:"visible" yaml:"visible" text:"Visible"`
		invisible string `json:"invisible" yaml:"invisible" text:"Invisible"`
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

func Test_parseTag(t *testing.T) {
	tc := []struct {
		fieldName string
		tagValue  string
		expName   string
		omitEmpty bool
	}{
		{"Name", "name", "name", false},
		{"Name", "name,omitempty", "name", true},
		{"Name", "name,foobar", "name", false},
		{"Name", "", "Name", false},
	}
	for i, tt := range tc {
		t.Run(fmt.Sprintf("TC_%d", i+1), func(t *testing.T) {
			m := parseTag(tt.fieldName, tt.tagValue)
			assert.GreaterOrEqual(t, len(m), 1)
			assert.Equal(t, tt.expName, m["name"])
			if tt.omitEmpty {
				assert.Equal(t, "omitempty", m["omitempty"])
			}

		})
	}
}

func Test_StructToTabwriter(t *testing.T) {
	t.Run("Write a struct", func(t *testing.T) {
		test := struct {
			SomeField string `text:"Some field"`
		}{
			"Hello",
		}
		bb := &bytes.Buffer{}
		tw := tabwriter.NewWriter(bb, 0, 0, 0, ' ', 0)
		err := StructToTabwriter(test, tw)
		assert.NoError(t, err)
		tw.Flush()
		assert.NotEmpty(t, bb.Bytes())
	})
	t.Run("Write a struct from a pointer", func(t *testing.T) {
		test := &struct {
			SomeField string `text:"Some field"`
		}{
			"Hello",
		}
		bb := &bytes.Buffer{}
		tw := tabwriter.NewWriter(bb, 0, 0, 0, ' ', 0)
		err := StructToTabwriter(test, tw)
		assert.NoError(t, err)
		tw.Flush()
		assert.NotEmpty(t, bb.Bytes())
	})

	t.Run("Write a non-tagged struct", func(t *testing.T) {
		test := &struct {
			SomeField string
		}{
			"Hello",
		}
		bb := &bytes.Buffer{}
		tw := tabwriter.NewWriter(bb, 0, 0, 0, ' ', 0)
		err := StructToTabwriter(test, tw)
		assert.NoError(t, err)
		tw.Flush()
		assert.Empty(t, bb.Bytes())
	})

	t.Run("Write something that is not a struct", func(t *testing.T) {
		test := "Not a struct"
		bb := &bytes.Buffer{}
		tw := tabwriter.NewWriter(bb, 0, 0, 0, ' ', 0)
		err := StructToTabwriter(test, tw)
		assert.ErrorContains(t, err, "expected struct")
		tw.Flush()
		assert.Empty(t, bb.Bytes())
	})

}
