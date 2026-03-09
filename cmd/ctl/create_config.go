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

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
)

func getDefaultKubeConfig() string {
	homedir, err := os.UserHomeDir()
	if err != nil {
		cmdutil.Fatal("error getting home directory: %v", err)
	}
	return filepath.Join(homedir, ".kube", "config")
}

func NewCreateConfigCommand() *cobra.Command {
	var (
		kubeConfigPath string
		outputPath     string
	)

	cmd := &cobra.Command{
		Use:   "create-config",
		Short: "Creates and populates a local user config file for Argo CD Agent by parsing the kubernetes config file",
		Run: func(cmd *cobra.Command, args []string) {
			CreateLocalConfig(kubeConfigPath, outputPath)
		},
		GroupID: "config",
	}

	cmd.Flags().StringVar(&kubeConfigPath, "kubeconfig-path", getDefaultKubeConfig(), "Path to where kubeconfig file lives")
	cmd.Flags().StringVar(&outputPath, "output-path", getDefaultCfgPath(), "Path to write output to")
	return cmd
}

func CreateLocalConfig(kubeConfigPath, outputPath string) {
	kubeConfig, err := clientcmd.LoadFromFile(kubeConfigPath)
	if err != nil {
		cmdutil.Fatal("Failed to load kubeconfig: %v", err)
	}

	var cfg localConfig
	cfg.Contexts.Principals = make(map[string]componentConfig)
	cfg.Contexts.Agents = make(map[string]componentConfig)
	for context := range kubeConfig.Contexts {
		var contextType string
		var namespace string
		var name string

		fmt.Printf("Found context %s, is this a principal or agent, if it is neither type skip? ", context)
		_, err := fmt.Scanln(&contextType)
		if err != nil {
			cmdutil.Fatal("Error reading input: %v", err)
		}

		for contextType != "principal" && contextType != "agent" && contextType != "skip" {
			fmt.Print("Invalid input, please enter principal, agent, or skip: ")
			_, err = fmt.Scanln(&contextType)
			if err != nil {
				cmdutil.Fatal("Error reading input: %v", err)
			}
		}

		if contextType == "skip" {
			fmt.Println()
			continue
		}

		fmt.Printf("Which namespace is the %s installed in? ", contextType)
		_, err = fmt.Scanln(&namespace)
		if err != nil {
			cmdutil.Fatal("Error reading input: %v", err)
		}
		namespace = strings.TrimSpace(namespace)

		for namespace == "" {
			fmt.Print("Invalid input, namespace cannot be blank: ")
			_, err = fmt.Scanln(&namespace)
			if err != nil {
				cmdutil.Fatal("Error reading input: %v", err)
			}
			namespace = strings.TrimSpace(namespace)
		}

		fmt.Printf("What name would you like to use for this %s? ", contextType)
		_, err = fmt.Scanln(&name)
		if err != nil {
			cmdutil.Fatal("Error reading input: %v", err)
		}
		name = strings.TrimSpace(namespace)

		for name == "" {
			fmt.Print("Invalid input, name cannot be blank: ")
			_, err = fmt.Scanln(&name)
			if err != nil {
				cmdutil.Fatal("Error reading input: %v", err)
			}
			name = strings.TrimSpace(namespace)
		}

		componentCfg := componentConfig{KubeContext: context, Namespace: namespace}
		switch contextType {
		case "principal":
			_, exists := cfg.Contexts.Principals[name]
			for exists {
				fmt.Printf("Name for principal, %s, already exists. Enter a new name: ", name)
				_, err = fmt.Scanln(&name)
				if err != nil {
					cmdutil.Fatal("Error reading input: %v", err)
				}
				name = strings.TrimSpace(name)
				_, exists = cfg.Contexts.Principals[name]
			}
			cfg.Contexts.Principals[name] = componentCfg
		case "agent":
			_, exists := cfg.Contexts.Agents[name]
			for exists {
				fmt.Printf("Name for agent, %s, already exists. Enter a new name: ", name)
				_, err = fmt.Scanln(&name)
				if err != nil {
					cmdutil.Fatal("Error reading input: %v", err)
				}
				name = strings.TrimSpace(name)
				_, exists = cfg.Contexts.Agents[name]
			}
			cfg.Contexts.Agents[name] = componentCfg
		}

		fmt.Println()
	}

	if len(cfg.Contexts.Principals) > 0 {
		for name := range cfg.Contexts.Principals {
			fmt.Printf("> %s\n", name)
		}

		var defaultPrincipal string
		fmt.Printf("Which of the above principals do you want to use as the default? ")
		_, err = fmt.Scanln(&defaultPrincipal)
		if err != nil {
			cmdutil.Fatal("Error reading input: %v", err)
		}

		_, exists := cfg.Contexts.Principals[defaultPrincipal]
		for !exists {
			fmt.Printf("Invalid input, selected principal %s is not a valid principal: ", defaultPrincipal)
			_, err = fmt.Scanln(&defaultPrincipal)
			if err != nil {
				cmdutil.Fatal("Error reading input: %v", err)
			}
			_, exists = cfg.Contexts.Principals[defaultPrincipal]
		}
		cfg.DefaultPrincipal = defaultPrincipal
		fmt.Println()
	}

	err = writeLocalConfig(&cfg, outputPath)
	if err != nil {
		cmdutil.Fatal("Error writing config: %v", err)
	}
	fmt.Printf("Successfully wrote config to %s\n", outputPath)
}
