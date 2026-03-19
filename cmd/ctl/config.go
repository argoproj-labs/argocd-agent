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
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/tools/clientcmd"
)

// struct to hold mappings of names to principal/agent configurations
type contexts struct {
	Principals map[string]componentConfig `yaml:"principals"`
	Agents     map[string]componentConfig `yaml:"agents"`
}

// struct to hold principal/agent configurations
type componentConfig struct {
	KubeContext string `yaml:"kube-context"`
	Namespace   string `yaml:"namespace"`
}

// struct that holds the local configuration for argocd-agentctl
type localConfig struct {
	Contexts         contexts `yaml:"contexts"`
	DefaultPrincipal string   `yaml:"default-principal"`
}

var (
	principalCfg componentConfig
	agentCfg     componentConfig
)

func NewConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Operations related to config file for argocd-agentctl",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
		GroupID: "config",
	}
	cmd.AddCommand(NewConfigCreateCommand())
	cmd.AddCommand(NewConfigAddCommand())
	cmd.AddCommand(NewConfigEditCommand())
	cmd.AddCommand(NewConfigDeleteCommand())
	cmd.AddCommand(NewConfigListCommand())
	return cmd
}

func NewConfigCreateCommand() *cobra.Command {
	var (
		kubeConfigPath string
		outputPath     string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Creates and populates a local user config file for Argo CD Agent by parsing the kubernetes config file",
		Run: func(cmd *cobra.Command, args []string) {
			CreateLocalConfig(kubeConfigPath, outputPath)
		},
	}

	cmd.Flags().StringVar(&kubeConfigPath, "kubeconfig-path", getDefaultKubeConfig(), "Path to where kubeconfig file lives")
	cmd.Flags().StringVar(&outputPath, "output-path", getDefaultCfgPath(), "Path to write output to")
	return cmd
}

func NewConfigAddCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add new entries to the local config file",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
	}
	cmd.AddCommand(NewConfigAddPrincipalCommand())
	cmd.AddCommand(NewConfigAddAgentCommand())
	return cmd
}

func NewConfigAddPrincipalCommand() *cobra.Command {
	var (
		name       string
		context    string
		namespace  string
		setDefault bool
	)
	cmd := &cobra.Command{
		Use:   "principal",
		Short: "Add new principal to the local config file",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := readLocalConfig(globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to read config: %v", err)
			}

			if _, exists := cfg.Contexts.Principals[name]; exists {
				cmdutil.Fatal("A principal with the name %s already exists", name)
			}

			cfg.Contexts.Principals[name] = componentConfig{
				KubeContext: context,
				Namespace:   namespace,
			}

			if setDefault {
				cfg.DefaultPrincipal = name
			}

			err = validateConfig(cfg)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}

			err = writeLocalConfig(cfg, globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to write config: %v", err)
			}
			fmt.Printf("New principal %s added successfully\n", name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "The symbolic name to use for the new principal")
	cmd.Flags().StringVar(&context, "context", "", "The context of the new principal")
	cmd.Flags().StringVar(&namespace, "namespace", "", "The namespace of the new principal")
	cmd.Flags().BoolVar(&setDefault, "set-default", false, "Set the new principal to default")

	err := cmd.MarkFlagRequired("name")
	if err != nil {
		cmdutil.Fatal("Failed to mark flag required: %v", err)
	}
	err = cmd.MarkFlagRequired("context")
	if err != nil {
		cmdutil.Fatal("Failed to mark flag required: %v", err)
	}
	err = cmd.MarkFlagRequired("namespace")
	if err != nil {
		cmdutil.Fatal("Failed to mark flag required: %v", err)
	}
	return cmd
}

func NewConfigAddAgentCommand() *cobra.Command {
	var (
		name      string
		context   string
		namespace string
	)
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Add new agent to the local config file",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := readLocalConfig(globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to read config: %v", err)
			}

			if _, exists := cfg.Contexts.Agents[name]; exists {
				cmdutil.Fatal("An agent with the name %s already exists", name)
			}

			cfg.Contexts.Agents[name] = componentConfig{
				KubeContext: context,
				Namespace:   namespace,
			}

			err = validateConfig(cfg)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}

			err = writeLocalConfig(cfg, globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to write config: %v", err)
			}
			fmt.Printf("New agent %s added successfully\n", name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "The symbolic name to use for the new agent")
	cmd.Flags().StringVar(&context, "context", "", "The context of the new agent")
	cmd.Flags().StringVar(&namespace, "namespace", "", "The namespace of the new agent")

	err := cmd.MarkFlagRequired("name")
	if err != nil {
		cmdutil.Fatal("Failed to mark flag required: %v", err)
	}
	err = cmd.MarkFlagRequired("context")
	if err != nil {
		cmdutil.Fatal("Failed to mark flag required: %v", err)
	}
	err = cmd.MarkFlagRequired("namespace")
	if err != nil {
		cmdutil.Fatal("Failed to mark flag required: %v", err)
	}
	return cmd
}

func NewConfigEditCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Edit entries in the local config file",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
	}
	cmd.AddCommand(NewConfigEditPrincipalCommand())
	cmd.AddCommand(NewConfigEditAgentCommand())
	return cmd
}

func NewConfigEditPrincipalCommand() *cobra.Command {
	var (
		name       string
		context    string
		namespace  string
		setDefault bool
	)
	cmd := &cobra.Command{
		Use:   "principal",
		Short: "Edit a principal in the local config file",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := readLocalConfig(globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to read config: %v", err)
			}

			principalCfg, exists := cfg.Contexts.Principals[name]
			if !exists {
				cmdutil.Fatal("The principal %s does not exist in the config", name)
			}

			if context != "" {
				principalCfg.KubeContext = context
			}
			if namespace != "" {
				principalCfg.Namespace = namespace
			}
			cfg.Contexts.Principals[name] = principalCfg

			if setDefault {
				cfg.DefaultPrincipal = name
			}

			err = validateConfig(cfg)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}

			err = writeLocalConfig(cfg, globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to write config: %v", err)
			}
			fmt.Printf("Principal %s updated successfully\n", name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "The symbolic name of the principal to edit")
	cmd.Flags().StringVar(&context, "context", "", "The new context of the principal")
	cmd.Flags().StringVar(&namespace, "namespace", "", "The new namespace of the principal")
	cmd.Flags().BoolVar(&setDefault, "set-default", false, "Set the principal to the default")

	err := cmd.MarkFlagRequired("name")
	if err != nil {
		cmdutil.Fatal("Failed to mark flag required: %v", err)
	}
	return cmd
}

func NewConfigEditAgentCommand() *cobra.Command {
	var (
		name      string
		context   string
		namespace string
	)
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Edit an agent in the local config file",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := readLocalConfig(globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to read config: %v", err)
			}

			agentCfg, exists := cfg.Contexts.Agents[name]
			if !exists {
				cmdutil.Fatal("The agent %s does not exist in the config", name)
			}

			if context != "" {
				agentCfg.KubeContext = context
			}
			if namespace != "" {
				agentCfg.Namespace = namespace
			}
			cfg.Contexts.Agents[name] = agentCfg

			err = validateConfig(cfg)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}

			err = writeLocalConfig(cfg, globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to write config: %v", err)
			}
			fmt.Printf("Agent %s updated successfully\n", name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "The symbolic name of the agent to edit")
	cmd.Flags().StringVar(&context, "context", "", "The new context of the agent")
	cmd.Flags().StringVar(&namespace, "namespace", "", "The new namespace of the agent")

	err := cmd.MarkFlagRequired("name")
	if err != nil {
		cmdutil.Fatal("Failed to mark flag required: %v", err)
	}

	return cmd
}

func NewConfigDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete entries from the local config file",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
	}
	cmd.AddCommand(NewConfigDeletePrincipalCommand())
	cmd.AddCommand(NewConfigDeleteAgentCommand())
	return cmd
}

func NewConfigDeletePrincipalCommand() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "principal",
		Short: "Remove a principal from the local config file",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := readLocalConfig(globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to read config: %v", err)
			}

			_, exists := cfg.Contexts.Principals[name]
			if !exists {
				cmdutil.Fatal("The principal %s does not exist in the config", name)
			}

			if cfg.DefaultPrincipal == name {
				cmdutil.Fatal("Cannot delete the default principal")
			}

			delete(cfg.Contexts.Principals, name)
			err = validateConfig(cfg)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}

			err = writeLocalConfig(cfg, globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to write config: %v", err)
			}
			fmt.Printf("Principal %s deleted successfully\n", name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "The symbolic name of the principal to delete")

	err := cmd.MarkFlagRequired("name")
	if err != nil {
		cmdutil.Fatal("Failed to mark flag required: %v", err)
	}

	return cmd
}

func NewConfigDeleteAgentCommand() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Remove an agent from the local config file",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := readLocalConfig(globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to read config: %v", err)
			}

			_, exists := cfg.Contexts.Agents[name]
			if !exists {
				cmdutil.Fatal("The agent %s does not exist in the config", name)
			}

			delete(cfg.Contexts.Agents, name)
			err = validateConfig(cfg)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}

			err = writeLocalConfig(cfg, globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to write config: %v", err)
			}
			fmt.Printf("Agent %s deleted successfully\n", name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "The symbolic name of the agent to delete")

	err := cmd.MarkFlagRequired("name")
	if err != nil {
		cmdutil.Fatal("Failed to mark flag required: %v", err)
	}

	return cmd
}

func NewConfigListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List components in the config",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := readLocalConfig(globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to read config: %v", err)
			}

			w := new(tabwriter.Writer)
			w.Init(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "COMPONENT\tNAME\tCONTEXT\tNAMESPACE")

			for name, principalCfg := range cfg.Contexts.Principals {
				fmt.Fprintf(w, "principal\t%s\t%s\t%s\n", name, principalCfg.KubeContext, principalCfg.Namespace)
			}

			for name, agentCfg := range cfg.Contexts.Agents {
				fmt.Fprintf(w, "agent\t%s\t%s\t%s\n", name, agentCfg.KubeContext, agentCfg.Namespace)
			}

			w.Flush()
		},
	}
	cmd.AddCommand(NewConfigListPrincipalCommand())
	cmd.AddCommand(NewConfigListAgentCommand())
	return cmd
}

func NewConfigListPrincipalCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "principal",
		Short: "List principals in the config",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := readLocalConfig(globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to read config: %v", err)
			}

			w := new(tabwriter.Writer)
			w.Init(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tCONTEXT\tNAMESPACE\tDEFAULT")

			for name, principalCfg := range cfg.Contexts.Principals {
				isDefault := ""
				if name == cfg.DefaultPrincipal {
					isDefault = "X"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, principalCfg.KubeContext, principalCfg.Namespace, isDefault)
			}

			w.Flush()
		},
	}
	return cmd
}

func NewConfigListAgentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "List agents in the config",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := readLocalConfig(globalOpts.configPath)
			if err != nil {
				cmdutil.Fatal("Failed to read config: %v", err)
			}

			w := new(tabwriter.Writer)
			w.Init(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tCONTEXT\tNAMESPACE")

			for name, agentCfg := range cfg.Contexts.Agents {
				fmt.Fprintf(w, "%s\t%s\t%s\n", name, agentCfg.KubeContext, agentCfg.Namespace)
			}

			w.Flush()
		},
	}
	return cmd
}

// CreateLocalConfig parses the user's kubeconfig and sets up the config file based on that
func CreateLocalConfig(kubeConfigPath, outputPath string) {
	kubeConfig, err := clientcmd.LoadFromFile(kubeConfigPath)
	if err != nil {
		cmdutil.Fatal("Failed to load kubeconfig: %v", err)
	}

	var cfg localConfig
	cfg.Contexts.Principals = make(map[string]componentConfig)
	cfg.Contexts.Agents = make(map[string]componentConfig)
	for context := range kubeConfig.Contexts {

		contextPrompt := fmt.Sprintf("Found context %s, is this a principal or agent? If is neither type leave blank to skip.", context)
		contextType, err := cmdutil.ReadFromTerm(contextPrompt, -1, func(s string) bool {
			return s == "principal" || s == "agent" || s == ""
		})
		if err != nil {
			cmdutil.Fatal("%v", err)
		}

		if contextType == "" {
			fmt.Println()
			continue
		}

		nsPrompt := fmt.Sprintf("Which namespace is the %s installed in?", contextType)
		namespace, err := cmdutil.ReadFromTerm(nsPrompt, -1, func(s string) bool {
			s = strings.TrimSpace(s)
			return s != "" && !strings.Contains(s, " ")
		})
		if err != nil {
			cmdutil.Fatal("%v", err)
		}
		namespace = strings.TrimSpace(namespace)

		namePrompt := fmt.Sprintf("What name would you like to use for this %s?", contextType)
		name, err := cmdutil.ReadFromTerm(namePrompt, -1, func(s string) bool {
			s = strings.TrimSpace(s)
			var exists bool
			switch contextType {
			case "principal":
				_, exists = cfg.Contexts.Principals[s]
			case "agent":
				_, exists = cfg.Contexts.Agents[s]
			}
			return s != "" && !strings.Contains(s, " ") && !exists
		})
		if err != nil {
			cmdutil.Fatal("%v", err)
		}
		name = strings.TrimSpace(name)

		componentCfg := componentConfig{KubeContext: context, Namespace: namespace}
		switch contextType {
		case "principal":
			cfg.Contexts.Principals[name] = componentCfg
		case "agent":
			cfg.Contexts.Agents[name] = componentCfg
		}

		fmt.Println()
	}

	if len(cfg.Contexts.Principals) > 0 {
		for name := range cfg.Contexts.Principals {
			fmt.Printf("> %s\n", name)
		}

		defaultPrincipal, err := cmdutil.ReadFromTerm("Which of the above principals do you want to use as the default?", -1, func(s string) bool {
			s = strings.TrimSpace(s)
			_, exists := cfg.Contexts.Principals[s]
			return exists && s != "" && !strings.Contains(s, " ")
		})
		if err != nil {
			cmdutil.Fatal("%v", err)
		}
		defaultPrincipal = strings.TrimSpace(defaultPrincipal)

		cfg.DefaultPrincipal = defaultPrincipal
		fmt.Println()
	}

	err = writeLocalConfig(&cfg, outputPath)
	if err != nil {
		cmdutil.Fatal("Error writing config: %v", err)
	}
	fmt.Printf("Successfully wrote config to %s\n", outputPath)
}

// helper function that gets the default kube config path
func getDefaultKubeConfig() string {
	homedir, err := os.UserHomeDir()
	if err != nil {
		cmdutil.Fatal("error getting home directory: %v", err)
	}
	return filepath.Join(homedir, ".kube", "config")
}

// helper function that returns the default path for the config
func getDefaultCfgPath() string {
	homedir, err := os.UserHomeDir()
	if err != nil {
		cmdutil.Fatal("Error getting home directory: %v", err)
	}
	return filepath.Join(homedir, ".config", "argocd-agent", "ctl.conf")
}

// helper function that determines which context and namespace to use based on the globals opts
// Priority follows the below order:
// Principal: (Highest Priority) Context and Namespace flags -> Principal flag (config lookup) -> Default principal in config -> Current context selected in kubeconfig(Lowest Priority)
// Agent: (Highest Priority) Context and namespace flags -> Agent flag (config lookup) -> Current context selected in kubeconfig (Lowest Priority)
func determineConfigs(opts *GlobalFlags, cfg *localConfig) (principal, agent componentConfig) {
	if opts.principalContext != "" {
		principal.KubeContext = opts.principalContext
		principal.Namespace = opts.principalNamespace
	} else if opts.principal != "" {
		config, exists := cfg.Contexts.Principals[opts.principal]
		if !exists {
			cmdutil.Fatal("Principal %s does not exist in config", opts.principal)
		}
		principal = config
	} else if config, exists := cfg.Contexts.Principals[cfg.DefaultPrincipal]; exists {
		principal = config
	}

	if opts.agentContext != "" {
		agent.KubeContext = opts.agentContext
		agent.Namespace = opts.agentNamespace
	} else if opts.agent != "" {
		config, exists := cfg.Contexts.Agents[opts.agent]
		if !exists {
			cmdutil.Fatal("Agent %s does not exist in config", opts.agent)
		}
		agent = config
	}

	return principal, agent
}

// helper function that validates a configuration returns an error with
// details on the issues if something is wrong
func validateConfig(cfg *localConfig) error {
	// check agents for anything blank
	for agent, agentCfg := range cfg.Contexts.Agents {
		if agent == "" {
			return fmt.Errorf("invalid config: agent name cannot be empty")
		}

		if agentCfg.KubeContext == "" {
			return fmt.Errorf("invalid config: agent %s has blank kube-context", agent)
		}

		if agentCfg.Namespace == "" {
			return fmt.Errorf("invalid config: agent %s has blank namespace", agent)
		}
	}

	// check principals for anything blank
	for principal, principalCfg := range cfg.Contexts.Principals {
		if principal == "" {
			return fmt.Errorf("invalid config: principal name cannot be empty")
		}

		if principalCfg.KubeContext == "" {
			return fmt.Errorf("invalid config: principal %s has blank kube-context", principal)
		}

		if principalCfg.Namespace == "" {
			return fmt.Errorf("invalid config: principal %s has blank namespace", principal)
		}
	}

	// check if default principal exists in principal contexts
	if _, exists := cfg.Contexts.Principals[cfg.DefaultPrincipal]; !exists && cfg.DefaultPrincipal != "" {
		return fmt.Errorf("invalid config: the default principal %s does not exist in principal contexts", cfg.DefaultPrincipal)
	}

	return nil
}

// helper function that writes the yaml for the local config to specified path and creates all directories
// needed for it
func writeLocalConfig(cfg *localConfig, path string) error {
	// create subdirectories that need to exist for file to exist
	dirPath := filepath.Dir(path)
	err := os.MkdirAll(dirPath, 0o744)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)

	err = encoder.Encode(cfg)
	return err
}

// helper function that reads the config for a local config from a file
func readLocalConfig(path string) (*localConfig, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg localConfig
	err = yaml.Unmarshal(bytes, &cfg)
	if err != nil {
		return nil, err
	}

	if cfg.Contexts.Principals == nil {
		cfg.Contexts.Principals = make(map[string]componentConfig)
	}
	if cfg.Contexts.Agents == nil {
		cfg.Contexts.Agents = make(map[string]componentConfig)
	}

	return &cfg, nil
}

// helper function that loads, validates, and determines which configs to use
func loadLocalConfig() (principal, agent componentConfig) {
	localCfg, err := readLocalConfig(globalOpts.configPath)
	if err != nil {
		// Fail if there is a problem with the config
		if !os.IsNotExist(err) {
			cmdutil.Fatal("An error occurred while reading config: %v", err)
		}
		localCfg = &localConfig{}
	}

	err = validateConfig(localCfg)
	if err != nil {
		cmdutil.Warn("%v, defaulting to using no config", err)
		localCfg = &localConfig{}
	}

	principal, agent = determineConfigs(globalOpts, localCfg)
	return principal, agent
}
