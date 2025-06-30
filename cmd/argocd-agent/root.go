package main

import (
	"os"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/spf13/cobra"
)

// NewRootCommand returns a new root command.
func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "argocd-agent",
		Short: "Run argocd-agent components",
	}

	rootCmd.AddCommand(NewAgentRunCommand())
	rootCmd.AddCommand(NewPrincipalRunCommand())
	rootCmd.AddCommand(NewVersionCommand())

	return rootCmd
}

func main() {
	cmdutil.InitLogging()
	cmd := NewRootCommand()
	err := cmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}
