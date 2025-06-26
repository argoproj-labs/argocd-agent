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

package main

import (
	"fmt"
	"os"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/env"
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
			_ = cmd.Usage()
			os.Exit(1)
		},
	}
	configGroup := &cobra.Group{ID: "config", Title: "Configuration"}
	command.AddGroup(configGroup)
	command.AddCommand(NewAgentCommand())
	command.AddCommand(NewPKICommand())
	command.AddCommand(NewJWTCommand())
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
			v := version.New("argocd-agent")
			fmt.Println(v.JSON(indent))
		},
	}
	command.Flags().BoolVarP(&indent, "indent", "i", false, "Display indented JSON")
	return command
}

type GlobalFlags struct {
	principalContext   string
	principalNamespace string
	agentContext       string
	agentNamespace     string
}

func addGlobalFlags(command *cobra.Command, opts *GlobalFlags) {
	command.PersistentFlags().StringVar(&opts.principalContext, "principal-context", env.StringWithDefault("ARGOCD_AGENT_PRINCIPAL_CONTEXT", nil, ""), "The Kubernetes context of the principal")
	command.PersistentFlags().StringVar(&opts.principalNamespace, "principal-namespace", env.StringWithDefault("ARGOCD_AGENT_PRINCIPAL_NAMESPACE", nil, "argocd"), "The Kubernetes namespace the principal is installed in")
	command.PersistentFlags().StringVar(&opts.agentContext, "agent-context", env.StringWithDefault("ARGOCD_AGENT_AGENT_CONTEXT", nil, ""), "The Kubernetes context of the agent")
	command.PersistentFlags().StringVar(&opts.agentNamespace, "agent-namespace", env.StringWithDefault("ARGOCD_AGENT_AGENT_NAMESPACE", nil, "argocd"), "The Kubernetes namespace the agent is installed in")
}

func main() {
	cmd := NewRootCommand()
	if err := cmd.Execute(); err != nil {
		cmdutil.Fatal("Error executing CLI: %v", err)
	}
}
