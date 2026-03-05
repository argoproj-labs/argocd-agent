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

// helper function that validates a configuration returns an error with
// details on the issues if something is wrong
func validateConfig(cfg *localConfig) error {
	if cfg.DefaultPrincipal == "" {
		return fmt.Errorf("Invalid config: default principal is empty")
	}

	if len(cfg.Contexts.Principals) == 0 {
		return fmt.Errorf("Invalid config: No principals defined")
	}

	// check agent for anything blank
	for agent, agentCfg := range cfg.Contexts.Agents {
		if agent == "" {
			return fmt.Errorf("Invalid config: agent name cannot be empty")
		}

		if agentCfg.KubeContext == "" {
			return fmt.Errorf("Invalid config: agent %s has blank kube-context", agent)
		}

		if agentCfg.Namespace == "" {
			return fmt.Errorf("Invalid config: agent %s has blank kube-context", agent)
		}
	}

	// check agent for anything blank
	for principal, principalCfg := range cfg.Contexts.Principals {
		if principal == "" {
			return fmt.Errorf("Invalid config: principal name cannot be empty")
		}

		if principalCfg.KubeContext == "" {
			return fmt.Errorf("Invalid config: principal %s has blank kube-context", principal)
		}

		if principalCfg.Namespace == "" {
			return fmt.Errorf("Invalid config: principal %s has blank kube-context", principal)
		}
	}

	// check if default principal exists in principal contexts
	if _, exists := cfg.Contexts.Principals[cfg.DefaultPrincipal]; !exists {
		return fmt.Errorf("Invalid config: the default principal %s does not exist in principal contexts", cfg.DefaultPrincipal)
	}

	return nil
}

// helper function that writes the yaml for the local config to specified path
func writeLocalConfig(cfg *localConfig, path string) error {
	bytes, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	err = os.WriteFile(path, bytes, 0o600)
	return err
}

// helper function that loads the config for a local config
func loadLocalConfig(path string) (*localConfig, error) {
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
