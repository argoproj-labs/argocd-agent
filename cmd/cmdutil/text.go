// Copyright 2025 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmdutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v2"
)

// parseTag parses a struct field's tag and returns it tokens in a map.
// The returned map will contain at least the key "name". If the tag is
// empty, its value will be fieldName.
func parseTag(fieldName string, tag string) map[string]string {
	m := make(map[string]string)
	if tag == "" {
		m["name"] = fieldName
		return m
	}
	tt := strings.Split(tag, ",")
	m["name"] = tt[0]
	for i := 1; i < len(tt); i++ {
		if tt[i] == "omitempty" {
			m["omitempty"] = "omitempty"
		}
	}
	return m
}

// StructToTabwriter takes any struct s and produces a formatted text output
// using the tabwriter tw. The fields in struct s to render must be tagged
// properly with a "text" tag, and they must be exported.
//
// This function will not flush the tabwriter's writer, so the caller is
// expected to do that after this function returns.
//
// An error will be returned if the data type passed as s was unexpected.
func StructToTabwriter(s any, tw *tabwriter.Writer) error {
	t := reflect.TypeOf(s)
	v := reflect.ValueOf(s)
	if t.Kind() == reflect.Pointer {
		t = v.Elem().Type()
		v = v.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("expected struct, got %s", t.Kind())
	}
	for i := 0; i < t.NumField(); i++ {
		if !t.Field(i).IsExported() {
			continue
		}
		s := t.Field(i).Tag.Get("text")
		if s == "" {
			continue
		}
		tag := parseTag(t.Field(i).Name, s)
		fmt.Fprintf(tw, "%s:\t%v\n", tag["name"], v.Field(i).Interface())
	}
	return nil
}

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
