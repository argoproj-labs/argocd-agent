package cmdutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v2"
)

// MarshalStruct marshals any tagged struct in the output format given.
// Formats supported are json, yaml and text. Struct fields to be
// marshaled must be exported and properly tagged.
func MarshalStruct(s any, outputFormat string) ([]byte, error) {
	var out []byte
	var err error
	switch strings.ToLower(outputFormat) {
	case "json":
		out, err = json.MarshalIndent(s, "", " ")
		out = append(out, '\n')
	case "yaml":
		out, err = yaml.Marshal(s)
	case "text":
		bb := &bytes.Buffer{}
		tw := tabwriter.NewWriter(bb, 0, 0, 2, ' ', 0)
		err = StructToTabwriter(s, tw)
		tw.Flush()
		out = bb.Bytes()
	default:
		err = fmt.Errorf("unknown output format: %s", outputFormat)
	}
	return out, err
}
