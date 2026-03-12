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

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"gopkg.in/yaml.v3"
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
			cmdutil.Fatal("agent %s does not exist in config", opts.agent)
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

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
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
