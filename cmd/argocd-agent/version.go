package main

import (
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/version"
	"github.com/spf13/cobra"
)

// NewVersionCommand returns a new version command.
func NewVersionCommand() *cobra.Command {
	var (
		short         bool
		versionFormat string
	)

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version of argocd-agent",
		Run: func(cmd *cobra.Command, args []string) {
			v := version.New("argocd-agent")
			if short {
				fmt.Println(v.Version())
			} else {
				switch versionFormat {
				case "text":
					fmt.Println(v.QualifiedVersion())
				case "yaml":
					fmt.Println(v.YAML())
				case "json":
					fmt.Println(v.JSON(true))
				}
			}
		},
	}

	cmd.Flags().BoolVarP(&short, "short", "s", false, "Print the version number only")
	cmd.Flags().StringVarP(&versionFormat, "output", "o", "text", "Output format. One of: text, yaml, json")

	return cmd
}
