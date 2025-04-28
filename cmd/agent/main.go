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
	"bufio"
	"context"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/argoproj-labs/argocd-agent/agent"
	"github.com/argoproj-labs/argocd-agent/cmd/cmd"
	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/auth/userpass"
	"github.com/argoproj-labs/argocd-agent/internal/env"
	"github.com/argoproj-labs/argocd-agent/internal/version"
	"github.com/argoproj-labs/argocd-agent/pkg/client"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func NewAgentRunCommand() *cobra.Command {
	var (
		serverAddress     string
		serverPort        int
		logLevel          string
		logFormat         string
		insecure          bool
		rootCAPath        string
		kubeConfig        string
		kubeContext       string
		namespace         string
		agentMode         string
		creds             string
		showVersion       bool
		versionFormat     string
		tlsClientCrt      string
		tlsClientKey      string
		enableWebSocket   bool
		metricsPort       int
		healthzPort       int
		enableCompression bool
		pprofPort         int

		// Time interval for agent to principal ping
		// Ex: "30m", "1h" or "1h20m10s". Valid time units are "s", "m", "h".
		keepAlivePingInterval time.Duration
	)
	command := &cobra.Command{
		Short: "Run the argocd-agent agent component",
		Run: func(c *cobra.Command, args []string) {
			if showVersion {
				cmdutil.PrintVersion(version.New("argocd-agent", "agent"), versionFormat)
				os.Exit(0)
			}
			ctx, cancelFn := context.WithCancel(context.Background())
			defer cancelFn()

			if pprofPort > 0 {
				logrus.Infof("Starting pprof server on 127.0.0.1:%d", pprofPort)

				go func() {
					err := http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", pprofPort), nil)
					if err != nil {
						cmdutil.Fatal("Error starting pprof server: %v", err)
					}
				}()
			}

			agentOpts := []agent.AgentOption{}
			remoteOpts := []client.RemoteOption{}

			if logLevel == "" {
				logLevel = "info"
			}
			if logLevel != "" {
				lvl, err := cmdutil.StringToLoglevel(logLevel)
				if err != nil {
					cmdutil.Fatal("invalid log level: %s. Available levels are: %s", logLevel, cmdutil.AvailableLogLevels())
				}
				logrus.SetLevel(lvl)
			}
			if formatter, err := cmdutil.LogFormatter(logFormat); err != nil {
				cmdutil.Fatal("%s", err.Error())
			} else {
				logrus.SetFormatter(formatter)
			}
			if creds != "" {
				authMethod, authCreds, err := parseCreds(creds)
				if err != nil {
					cmdutil.Fatal("Error setting up creds: %v", err)
				}
				remoteOpts = append(remoteOpts, client.WithAuth(authMethod, authCreds))
			}
			var remote *client.Remote
			var err error
			if insecure {
				remoteOpts = append(remoteOpts, client.WithInsecureSkipTLSVerify())
			} else if rootCAPath != "" {
				remoteOpts = append(remoteOpts, client.WithRootAuthoritiesFromFile(rootCAPath))
			}
			if tlsClientCrt != "" && tlsClientKey != "" {
				remoteOpts = append(remoteOpts, client.WithTLSClientCertFromFile(tlsClientCrt, tlsClientKey))
			}
			remoteOpts = append(remoteOpts, client.WithWebSocket(enableWebSocket))
			remoteOpts = append(remoteOpts, client.WithClientMode(types.AgentModeFromString(agentMode)))
			remoteOpts = append(remoteOpts, client.WithKeepAlivePingInterval(keepAlivePingInterval))
			remoteOpts = append(remoteOpts, client.WithCompression(enableCompression))

			if serverAddress != "" && serverPort > 0 && serverPort < 65536 {
				remote, err = client.NewRemote(serverAddress, serverPort, remoteOpts...)
				if err != nil {
					cmdutil.Fatal("Error creating remote: %v", err)
				}
			}
			if remote == nil {
				cmdutil.Fatal("No remote specified")
			}

			if namespace == "" {
				cmdutil.Fatal("namespace value is empty and must be specified")
			}

			kubeConfig, err := cmdutil.GetKubeConfig(ctx, namespace, kubeConfig, kubeContext)
			if err != nil {
				cmdutil.Fatal("Could not load Kubernetes config: %v", err)
			}
			agentOpts = append(agentOpts, agent.WithRemote(remote))
			agentOpts = append(agentOpts, agent.WithMode(agentMode))
			agentOpts = append(agentOpts, agent.WithHealthzPort(healthzPort))

			if metricsPort > 0 {
				agentOpts = append(agentOpts, agent.WithMetricsPort(metricsPort))
			}

			ag, err := agent.NewAgent(ctx, kubeConfig, namespace, agentOpts...)
			if err != nil {
				cmdutil.Fatal("Could not create a new agent instance: %v", err)
			}
			if err := ag.Start(ctx); err != nil {
				cmdutil.Fatal("Could not start agent: %v", err)
			}
			<-ctx.Done()
		},
	}

	command.Flags().StringVar(&serverAddress, "server-address",
		env.StringWithDefault("ARGOCD_AGENT_REMOTE_SERVER", nil, ""),
		"Address of the server to connect to")
	command.Flags().IntVar(&serverPort, "server-port",
		env.NumWithDefault("ARGOCD_AGENT_REMOTE_PORT", nil, 443),
		"Port on the server to connect to")
	command.Flags().StringVar(&logLevel, "log-level",
		env.StringWithDefault("ARGOCD_AGENT_LOG_LEVEL", nil, "info"),
		"The log level for the agent")
	command.Flags().StringVar(&logFormat, "log-format",
		env.StringWithDefault("ARGOCD_PRINCIPAL_LOG_FORMAT", nil, "text"),
		"The log format to use (one of: text, json)")
	command.Flags().BoolVar(&insecure, "insecure-tls",
		env.BoolWithDefault("ARGOCD_AGENT_TLS_INSECURE", false),
		"INSECURE: Do not verify remote TLS certificate")
	command.Flags().StringVarP(&namespace, "namespace", "n",
		env.StringWithDefault("ARGOCD_AGENT_NAMESPACE", nil, "argocd"),
		"Namespace to manage applications in")
	command.Flags().StringVar(&agentMode, "agent-mode",
		env.StringWithDefault("ARGOCD_AGENT_MODE", nil, "autonomous"),
		"Mode of operation")
	command.Flags().StringVar(&creds, "creds",
		env.StringWithDefault("ARGOCD_AGENT_CREDS", nil, ""),
		"Credentials to use when connecting to server")
	command.Flags().StringVar(&rootCAPath, "root-ca-path",
		env.StringWithDefault("ARGOCD_AGENT_TLS_ROOT_CA_PATH", nil, ""),
		"Path to a file containing root CA certificate for verifying remote TLS")
	command.Flags().StringVar(&tlsClientCrt, "tls-client-cert",
		env.StringWithDefault("ARGOCD_AGENT_TLS_CLIENT_CERT_PATH", nil, ""),
		"Path to TLS client certificate")
	command.Flags().StringVar(&tlsClientKey, "tls-client-key",
		env.StringWithDefault("ARGOCD_AGENT_TLS_CLIENT_KEY_PATH", nil, ""),
		"Path to TLS client key")
	command.Flags().BoolVar(&enableWebSocket, "enable-websocket",
		env.BoolWithDefault("ARGOCD_AGENT_ENABLE_WEBSOCKET", false),
		"Agent will rely on gRPC over WebSocket to stream events to the Principal")
	command.Flags().IntVar(&metricsPort, "metrics-port",
		env.NumWithDefault("ARGOCD_AGENT_METRICS_PORT", cmd.ValidPort, 8181),
		"Port the metrics server will listen on")
	command.Flags().IntVar(&healthzPort, "healthz-port",
		env.NumWithDefault("ARGOCD_AGENT_HEALTH_CHECK_PORT", cmd.ValidPort, 8001),
		"Port the health check server will listen on")
	command.Flags().DurationVar(&keepAlivePingInterval, "keep-alive-ping-interval",
		env.DurationWithDefault("ARGOCD_AGENT_KEEP_ALIVE_PING_INTERVAL", nil, 0),
		"Ping interval to keep connection alive with Principal")
	command.Flags().BoolVar(&enableCompression, "enable-compression",
		env.BoolWithDefault("ARGOCD_AGENT_ENABLE_COMPRESSION", false),
		"Use compression while sending data between Principal and Agent using gRPC")
	command.Flags().IntVar(&pprofPort, "pprof-port",
		env.NumWithDefault("ARGOCD_AGENT_PPROF_PORT", cmdutil.ValidPort, 0),
		"Port the pprof server will listen on")

	command.Flags().StringVar(&kubeConfig, "kubeconfig", "", "Path to a kubeconfig file to use")
	command.Flags().StringVar(&kubeContext, "kubecontext", "", "Override the default kube context")
	command.Flags().BoolVar(&showVersion, "version", false, "Display version information and exit")
	command.Flags().StringVar(&versionFormat, "version-format", "text", "Output version information in format: text, json, json-indent")
	return command
}

func parseCreds(credStr string) (string, auth.Credentials, error) {
	p := strings.SplitN(credStr, ":", 2)
	if len(p) != 2 {
		return "", nil, fmt.Errorf("invalid cred string")
	}
	var creds auth.Credentials
	var err error
	switch p[0] {
	case "userpass":
		creds, err = loadCreds(p[1])
		if err != nil {
			return "", nil, err
		}
		return "userpass", creds, nil
	case "mtls":
		return "mtls", auth.Credentials{}, nil
	default:
		return "", nil, fmt.Errorf("unknown auth method: %s", p[0])
	}
}

func loadCreds(path string) (auth.Credentials, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not load creds: %w", err)
	}
	s := bufio.NewScanner(f)
	if !s.Scan() {
		return nil, fmt.Errorf("could not load file: %s: empty file", path)
	}
	credsln := s.Text()
	c := strings.SplitN(credsln, ":", 2)
	if len(c) != 2 {
		return nil, fmt.Errorf("invalid credentials in %s", path)
	}
	creds := auth.Credentials{
		userpass.ClientIDField:     c[0],
		userpass.ClientSecretField: c[1],
	}
	return creds, nil
}

func main() {
	cmdutil.InitLogging()
	c := NewAgentRunCommand()
	err := c.Execute()
	if err != nil {
		cmdutil.Fatal("ERROR: %v", err)
	}
}
