package cmd

import (
	"fmt"

	"github.com/jannfis/argocd-agent/internal/version"
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
