// Copyright 2024 The argocd-agent Authors
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

package cmd

import (
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/version"
)

const (
	VersionFormatText        = "text"
	VersionFormatJSONCompact = "json"
	VersionFormatJSONIndent  = "json-indent"
	VersionFormatYAML        = "yaml"
)

func PrintVersion(v *version.Version, format string) {
	switch format {
	case VersionFormatText:
		fmt.Println(v.Version())
	case VersionFormatJSONCompact:
		fmt.Println(v.JSON(false))
	case VersionFormatJSONIndent:
		fmt.Println(v.JSON(true))
	case VersionFormatYAML:
		fmt.Println(v.YAML())
	default:
		fmt.Printf("Warning: Unknown version format '%s', falling back to %s", format, VersionFormatText)
		fmt.Println(v.Version())
	}
}
