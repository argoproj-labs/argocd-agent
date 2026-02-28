// Copyright 2024 The argocd-agent Authors
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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/haadminapi"
	"github.com/argoproj-labs/argocd-agent/pkg/ha"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

const (
	haAdminPort       = 8405
	principalPodLabel = "app.kubernetes.io/name=argocd-agent-principal"
)

func NewHACommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ha",
		Short: "Manage HA state of the principal server",
	}

	cmd.AddCommand(NewHAStatusCommand())
	cmd.AddCommand(NewHAPromoteCommand())
	cmd.AddCommand(NewHADemoteCommand())

	return cmd
}

func NewHAStatusCommand() *cobra.Command {
	var (
		address      string
		outputFormat string
		timeout      time.Duration
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show HA status of the principal server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			client, cleanup, err := getHAAdminClient(ctx, address)
			if err != nil {
				return err
			}
			defer cleanup()

			resp, err := client.Status(ctx, &haadminapi.StatusRequest{})
			if err != nil {
				return fmt.Errorf("failed to get HA status: %w", err)
			}

			return printHAStatus(resp, outputFormat)
		},
	}

	cmd.Flags().StringVarP(&address, "address", "a", "", "Direct gRPC address (bypasses kube port-forward)")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "text", "Output format: text, yaml, json")
	cmd.Flags().DurationVarP(&timeout, "timeout", "t", 10*time.Second, "Timeout for the operation")

	return cmd
}

func NewHAPromoteCommand() *cobra.Command {
	var (
		address string
		force   bool
		timeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Promote the principal to active",
		Long: `Promote this principal to the ACTIVE role.

Refuses if the peer is also ACTIVE (split-brain safety).
Use --force to override the safety check.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			client, cleanup, err := getHAAdminClient(ctx, address)
			if err != nil {
				return err
			}
			defer cleanup()

			if !force {
				fmt.Println("WARNING: This will promote the principal to ACTIVE.")
				fmt.Println("Connected agents may need to reconnect.")
				fmt.Print("\nProceed? [y/N]: ")

				var response string
				fmt.Scanln(&response) //nolint:errcheck
				if response != "y" && response != "Y" {
					fmt.Println("Operation cancelled.")
					return nil
				}
			}

			resp, err := client.Promote(ctx, &haadminapi.PromoteRequest{Force: force})
			if err != nil {
				return fmt.Errorf("promote failed: %w", err)
			}

			fmt.Printf("Promoted. State is now: %s\n", resp.State)
			return nil
		},
	}

	cmd.Flags().StringVarP(&address, "address", "a", "", "Direct gRPC address (bypasses kube port-forward)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip safety check and confirmation")
	cmd.Flags().DurationVarP(&timeout, "timeout", "t", 30*time.Second, "Timeout for the operation")

	return cmd
}

func NewHADemoteCommand() *cobra.Command {
	var (
		address string
		force   bool
		timeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "demote",
		Short: "Demote the principal to replica",
		Long: `Demote this principal from ACTIVE to REPLICATING.

The principal will stop accepting agents and begin replicating
from the peer.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			client, cleanup, err := getHAAdminClient(ctx, address)
			if err != nil {
				return err
			}
			defer cleanup()

			if !force {
				fmt.Println("WARNING: This will demote the principal to REPLICA.")
				fmt.Println("All connected agents will be disconnected.")
				fmt.Print("\nProceed? [y/N]: ")

				var response string
				fmt.Scanln(&response) //nolint:errcheck
				if response != "y" && response != "Y" {
					fmt.Println("Operation cancelled.")
					return nil
				}
			}

			resp, err := client.Demote(ctx, &haadminapi.DemoteRequest{})
			if err != nil {
				return fmt.Errorf("demote failed: %w", err)
			}

			fmt.Printf("Demoted. State is now: %s\n", resp.State)
			return nil
		},
	}

	cmd.Flags().StringVarP(&address, "address", "a", "", "Direct gRPC address (bypasses kube port-forward)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation prompt")
	cmd.Flags().DurationVarP(&timeout, "timeout", "t", 30*time.Second, "Timeout for the operation")

	return cmd
}

// getHAAdminClient returns an HA admin gRPC client. If address is set, dials
// directly. Otherwise uses --principal-context to port-forward to the pod.
func getHAAdminClient(ctx context.Context, address string) (haadminapi.HAAdminClient, func(), error) {
	if address != "" {
		client, conn, err := dialHAAdmin(address)
		if err != nil {
			return nil, nil, err
		}
		return client, func() { conn.Close() }, nil
	}

	localPort, stopCh, err := portForwardToPrincipal(ctx)
	if err != nil {
		return nil, nil, err
	}

	addr := fmt.Sprintf("localhost:%d", localPort)
	client, conn, err := dialHAAdmin(addr)
	if err != nil {
		close(stopCh)
		return nil, nil, err
	}

	cleanup := func() {
		conn.Close()
		close(stopCh)
	}
	return client, cleanup, nil
}

func dialHAAdmin(address string) (haadminapi.HAAdminClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to %s: %w", address, err)
	}
	return haadminapi.NewHAAdminClient(conn), conn, nil
}

// portForwardToPrincipal finds the principal pod via --principal-context and
// sets up a port-forward to port 8405. Returns the local port and a stop channel.
func portForwardToPrincipal(ctx context.Context) (uint16, chan struct{}, error) {
	kubeClient, err := kube.NewKubernetesClientFromConfig(
		ctx,
		globalOpts.principalNamespace,
		"",
		globalOpts.principalContext,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	pods, err := kubeClient.Clientset.CoreV1().Pods(globalOpts.principalNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: principalPodLabel,
		Limit:         1,
	})
	if err != nil {
		return 0, nil, fmt.Errorf("failed to list principal pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return 0, nil, fmt.Errorf("no principal pod found with label %s in namespace %s", principalPodLabel, globalOpts.principalNamespace)
	}

	podName := pods.Items[0].Name
	restConfig := kubeClient.RestConfig

	reqURL := kubeClient.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(globalOpts.principalNamespace).
		Name(podName).
		SubResource("portforward").
		URL()

	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create SPDY transport: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, reqURL)
	stopCh := make(chan struct{})
	readyCh := make(chan struct{})

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", haAdminPort)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create port-forwarder: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fw.ForwardPorts()
	}()

	select {
	case <-readyCh:
	case err := <-errCh:
		return 0, nil, fmt.Errorf("port-forward failed: %w", err)
	case <-ctx.Done():
		close(stopCh)
		return 0, nil, ctx.Err()
	}

	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		close(stopCh)
		return 0, nil, fmt.Errorf("failed to get forwarded port: %w", err)
	}

	return ports[0].Local, stopCh, nil
}

func printHAStatus(resp *haadminapi.HAStatusResponse, format string) error {
	switch format {
	case "json":
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))

	case "yaml":
		data, err := yaml.Marshal(resp)
		if err != nil {
			return err
		}
		fmt.Print(string(data))

	case "text":
		fmt.Printf("HA Status\n")
		fmt.Printf("---------\n")
		fmt.Printf("State:             %s\n", resp.State)
		fmt.Printf("Preferred Role:    %s\n", resp.PreferredRole)
		if resp.PeerAddress != "" {
			fmt.Printf("Peer Address:      %s\n", resp.PeerAddress)
		}
		if resp.State == string(ha.StateActive) {
			fmt.Printf("Connected Replicas: %d\n", resp.ConnectedReplicas)
		} else {
			fmt.Printf("Replicating:       %v\n", resp.PeerReachable)
		}
		fmt.Printf("Connected Agents:  %d\n", resp.ConnectedAgents)
		if resp.LastEventTimestamp > 0 {
			ago := time.Now().Unix() - resp.LastEventTimestamp
			fmt.Printf("Last Event:        %ds ago\n", ago)
		}
		if resp.LastSequenceNum > 0 {
			fmt.Printf("Last Sequence:     %d\n", resp.LastSequenceNum)
		}

	default:
		return fmt.Errorf("unknown output format: %s", format)
	}

	return nil
}
