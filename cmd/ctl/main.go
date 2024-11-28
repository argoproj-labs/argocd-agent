package main

import (
	"fmt"
	"os"

	"github.com/argoproj-labs/argocd-agent/internal/version"
	"github.com/spf13/cobra"
)

var globalOpts *GlobalFlags = &GlobalFlags{}

var cliDescription string = `
A CLI to manage configuration for the principal component of argocd-agent.

It lets you create, modify and inspect various aspects of the configuration,
such as credentials, cryptographic tokens, resource proxy settings and more.

Please note that this CLI may generate assets that are not suitable for any
kind of production environments.
`

func NewRootCommand() *cobra.Command {
	command := &cobra.Command{
		Short: "Inspect and manipulate argocd-agent configuration",
		Long:  cliDescription,
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Usage()
			os.Exit(1)
		},
	}
	configGroup := &cobra.Group{ID: "config", Title: "Configuration"}
	command.AddGroup(configGroup)
	command.AddCommand(NewAgentCommand())
	command.AddCommand(NewCACommand())
	command.AddCommand(NewVersionCommand())
	addGlobalFlags(command, globalOpts)
	return command
}

func NewVersionCommand() *cobra.Command {
	var (
		indent bool
	)
	command := &cobra.Command{
		Use:   "version",
		Short: "Display the version of argocd-agent-ctl",
		Run: func(cmd *cobra.Command, args []string) {
			v := version.New("argocd-agent", "ctl")
			fmt.Println(v.JSON(indent))
		},
	}
	command.Flags().BoolVarP(&indent, "indent", "i", false, "Display indented JSON")
	return command
}

type GlobalFlags struct {
	context   string
	namespace string
}

func addGlobalFlags(command *cobra.Command, opts *GlobalFlags) {
	command.PersistentFlags().StringVarP(&opts.context, "context", "x", "", "The Kubernetes context to operate in")
	command.PersistentFlags().StringVarP(&opts.namespace, "namespace", "n", "argocd", "The Kubernetes namespace to operate in")
}

func main() {
	cmd := NewRootCommand()
	cmd.Execute()
}
