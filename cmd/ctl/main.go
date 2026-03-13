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
		Use:   "argocd-agentctl",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Usage()
			os.Exit(1)
		},
	}
	configGroup := &cobra.Group{ID: "config", Title: "Configuration"}
	command.AddGroup(configGroup)
	command.AddCommand(NewAgentCommand())
	command.AddCommand(NewConfigCommand())
	command.AddCommand(NewCheckConfigCommand())
	command.AddCommand(NewPKICommand())
	command.AddCommand(NewJWTCommand())
	command.AddCommand(NewVersionCommand())
	addGlobalFlags(command, globalOpts)

	command.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		skip := map[string]bool{"argocd-agentctl version": true, "argocd-agentctl config create": true}
		if skip[cmd.CommandPath()] {
			return
		}
		principalCfg, agentCfg = loadLocalConfig()
	}

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

// Global Flags related to what clusters to target
// The order of which are used for the commands should be as followed
// Principal: (Highest Priority) Context and Namespace flags -> Principal flag (config lookup) -> Default principal in config -> Current context selected in kubeconfig(Lowest Priority)
// Agent: (Highest Priority) Context and namespace flags -> Agent flag (config lookup) -> Current context selected in kubeconfig (Lowest Priority)
type GlobalFlags struct {
	principal          string // Symbolic name for a principal in the config file
	principalContext   string // Kube context for principal
	principalNamespace string // Namespace where the principal component is installed on target cluster
	agent              string // Symbolic name for an agent in the config file
	agentContext       string // Kube context for agent
	agentNamespace     string // Namespace where the agent component is installed on target cluster
	configPath         string // Path to the local config for the ctl
}

func addGlobalFlags(command *cobra.Command, opts *GlobalFlags) {
	command.PersistentFlags().StringVar(&opts.principalContext, "principal-context", env.StringWithDefault("ARGOCD_AGENT_PRINCIPAL_CONTEXT", nil, ""), "The Kubernetes context of the principal")
	command.PersistentFlags().StringVar(&opts.principalNamespace, "principal-namespace", env.StringWithDefault("ARGOCD_AGENT_PRINCIPAL_NAMESPACE", nil, "argocd"), "The Kubernetes namespace the principal is installed in")
	command.PersistentFlags().StringVar(&opts.agentContext, "agent-context", env.StringWithDefault("ARGOCD_AGENT_AGENT_CONTEXT", nil, ""), "The Kubernetes context of the agent")
	command.PersistentFlags().StringVar(&opts.agentNamespace, "agent-namespace", env.StringWithDefault("ARGOCD_AGENT_AGENT_NAMESPACE", nil, "argocd"), "The Kubernetes namespace the agent is installed in")
	command.PersistentFlags().StringVar(&opts.principal, "principal", "", "The symbolic name of a principal in the config file")
	command.PersistentFlags().StringVar(&opts.agent, "agent", "", "The symbolic name of an agent in the config file")
	command.PersistentFlags().StringVar(&opts.configPath, "config", getDefaultCfgPath(), "The path to the local config file to use")
}

func main() {
	cmd := NewRootCommand()
	if err := cmd.Execute(); err != nil {
		cmdutil.Fatal("Error executing CLI: %v", err)
	}
}
