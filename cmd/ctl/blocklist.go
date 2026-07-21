// Copyright 2026 The argocd-agent Authors
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
	"context"
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/blocklist"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/spf13/cobra"
)

const (
	errFmtKubeClient    = "failed to create Kubernetes client: %w"
	errFmtLoadBlocklist = "failed to load blocklist: %w"
	errFmtSaveBlocklist = "failed to save blocklist: %w"
)

func NewBlocklistCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blocklist",
		Short: "Manage the TLS certificate blocklist",
	}

	cmd.AddCommand(NewBlocklistAddCommand())
	cmd.AddCommand(NewBlocklistRemoveCommand())
	cmd.AddCommand(NewBlocklistListCommand())

	return cmd
}

func NewBlocklistAddCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <fingerprint>",
		Short: "Add a certificate fingerprint to the blocklist",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fingerprint := args[0]
			ctx := context.TODO()

			clt, err := kube.NewKubernetesClientFromConfig(ctx, principalCfg.Namespace, "", principalCfg.KubeContext)
			if err != nil {
				return fmt.Errorf(errFmtKubeClient, err)
			}

			fingerprints, err := blocklist.LoadFromConfigMap(ctx, clt.Clientset, principalCfg.Namespace)
			if err != nil {
				return fmt.Errorf(errFmtLoadBlocklist, err)
			}

			for _, fp := range fingerprints {
				if fp == fingerprint {
					fmt.Println("Fingerprint already in blocklist.")
					return nil
				}
			}

			fingerprints = append(fingerprints, fingerprint)

			if err := blocklist.SaveToConfigMap(ctx, clt.Clientset, principalCfg.Namespace, fingerprints); err != nil {
				return fmt.Errorf(errFmtSaveBlocklist, err)
			}

			fmt.Printf("Added to blocklist: %s\n", fingerprint)
			return nil
		},
	}

	return cmd
}

func NewBlocklistRemoveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <fingerprint>",
		Short: "Remove a certificate fingerprint from the blocklist",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fingerprint := args[0]
			ctx := context.TODO()

			clt, err := kube.NewKubernetesClientFromConfig(ctx, principalCfg.Namespace, "", principalCfg.KubeContext)
			if err != nil {
				return fmt.Errorf(errFmtKubeClient, err)
			}

			fingerprints, err := blocklist.LoadFromConfigMap(ctx, clt.Clientset, principalCfg.Namespace)
			if err != nil {
				return fmt.Errorf(errFmtLoadBlocklist, err)
			}

			found := false
			filtered := make([]string, 0, len(fingerprints))
			for _, fp := range fingerprints {
				if fp == fingerprint {
					found = true
					continue
				}
				filtered = append(filtered, fp)
			}

			if !found {
				fmt.Println("Fingerprint not found in blocklist.")
				return nil
			}

			if err := blocklist.SaveToConfigMap(ctx, clt.Clientset, principalCfg.Namespace, filtered); err != nil {
				return fmt.Errorf(errFmtSaveBlocklist, err)
			}

			fmt.Printf("Removed from blocklist: %s\n", fingerprint)
			return nil
		},
	}

	return cmd
}

func NewBlocklistListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all fingerprints in the blocklist",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.TODO()

			clt, err := kube.NewKubernetesClientFromConfig(ctx, principalCfg.Namespace, "", principalCfg.KubeContext)
			if err != nil {
				return fmt.Errorf(errFmtKubeClient, err)
			}

			fingerprints, err := blocklist.LoadFromConfigMap(ctx, clt.Clientset, principalCfg.Namespace)
			if err != nil {
				return fmt.Errorf(errFmtLoadBlocklist, err)
			}

			if len(fingerprints) == 0 {
				fmt.Println("No entries in the blocklist.")
				return nil
			}

			fmt.Println("FINGERPRINT")
			for _, fp := range fingerprints {
				fmt.Println(fp)
			}
			return nil
		},
	}

	return cmd
}
