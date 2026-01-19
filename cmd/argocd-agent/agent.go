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
	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/auth/userpass"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/env"
	"github.com/argoproj-labs/argocd-agent/internal/tracing"
	"github.com/argoproj-labs/argocd-agent/pkg/client"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// NewAgentRunCommand returns a new agent run command.
func NewAgentRunCommand() *cobra.Command {
	var (
		serverAddress       string
		serverPort          int
		logLevel            string
		logFormat           string
		insecure            bool
		insecurePlaintext   bool
		rootCASecretName    string
		rootCAPath          string
		kubeConfig          string
		kubeContext         string
		namespace           string
		agentMode           string
		creds               string
		tlsSecretName       string
		tlsClientCrt        string
		tlsClientKey        string
		enableWebSocket     bool
		metricsPort         int
		healthzPort         int
		enableCompression   bool
		pprofPort           int
		redisAddr           string
		redisUsername       string
		redisPassword       string
		enableResourceProxy bool

		// Time interval for agent to principal ping
		// Ex: "30m", "1h" or "1h20m10s". Valid time units are "s", "m", "h".
		keepAlivePingInterval time.Duration

		// Time interval for agent to refresh cluster cache info in principal
		cacheRefreshInterval time.Duration

		// Time interval for application-level heartbeats over the Subscribe stream.
		// This is used to keep the connection alive through service meshes like Istio.
		heartbeatInterval time.Duration

		// OpenTelemetry configuration
		otlpAddress  string
		otlpInsecure bool
	)
	command := &cobra.Command{
		Use:   "agent",
		Short: "Run the argocd-agent agent component",
		Run: func(c *cobra.Command, args []string) {
			ctx, cancelFn := context.WithCancel(context.Background())
			defer cancelFn()

			// Initialize OpenTelemetry tracing if enabled
			if otlpAddress != "" {
				shutdownTracer, err := tracing.InitTracer(ctx, "agent-"+agentMode, otlpAddress, otlpInsecure)
				if err != nil {
					cmdutil.Fatal("Failed to initialize OpenTelemetry tracing: %v", err)
				}
				defer func() {
					if err := shutdownTracer(ctx); err != nil {
						logrus.Errorf("Error shutting down tracer: %v", err)
					}
				}()
				logrus.Infof("OpenTelemetry tracing initialized (address=%s)", otlpAddress)
			}

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
			if namespace == "" {
				cmdutil.Fatal("namespace value is empty and must be specified")
			}

			kubeConfig, err := cmdutil.GetKubeConfig(ctx, namespace, kubeConfig, kubeContext)
			if err != nil {
				cmdutil.Fatal("Could not load Kubernetes config: %v", err)
			}
			if creds != "" {
				authMethod, authCreds, err := parseCreds(creds)
				if err != nil {
					cmdutil.Fatal("Error setting up creds: %v", err)
				}
				remoteOpts = append(remoteOpts, client.WithAuth(authMethod, authCreds))
			}
			var remote *client.Remote

			// Configure TLS or plaintext mode
			if insecurePlaintext {
				// Plaintext mode - skip all TLS configuration (e.g., when behind Istio)
				logrus.Warn("INSECURE: Connecting without TLS - ensure Istio or similar service mesh provides mTLS")
				remoteOpts = append(remoteOpts, client.WithInsecurePlaintext())
			} else {
				// The certificate pool for verifying TLS certificates can be
				// loaded from a file if requested on the command line.
				// Otherwise the pool will be loaded from a secret, unless the
				// insecure option was given - in which case, certificates will
				// not be verified.
				if insecure {
					logrus.Warn("INSECURE: Not verifying remote TLS certificate")
					remoteOpts = append(remoteOpts, client.WithInsecureSkipTLSVerify())
				} else if rootCAPath != "" {
					logrus.Infof("Loading root CA certificate from file %s", rootCAPath)
					remoteOpts = append(remoteOpts, client.WithRootAuthoritiesFromFile(rootCAPath))
				} else {
					logrus.Infof("Loading root CA certificate from secret %s/%s", namespace, rootCASecretName)
					remoteOpts = append(remoteOpts, client.WithRootAuthoritiesFromSecret(kubeConfig.Clientset, namespace, rootCASecretName, ""))
				}

				// If both a certificate and a key are specified on the command
				// line, the agent will load the client cert from these files.
				// Otherwise, it will try and load the TLS keypair from a secret.
				if tlsClientCrt != "" && tlsClientKey != "" {
					logrus.Infof("Loading client TLS configuration from files cert=%s and key=%s", tlsClientCrt, tlsClientKey)
					remoteOpts = append(remoteOpts, client.WithTLSClientCertFromFile(tlsClientCrt, tlsClientKey))
				} else if (tlsClientCrt != "" && tlsClientKey == "") || (tlsClientCrt == "" && tlsClientKey != "") {
					cmdutil.Fatal("Both --tls-client-cert and --tls-client-key have to be given")
				} else {
					logrus.Infof("Loading client TLS certificate from secret %s/%s", namespace, tlsSecretName)
					remoteOpts = append(remoteOpts, client.WithTLSClientCertFromSecret(kubeConfig.Clientset, namespace, tlsSecretName))
				}
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

			agentOpts = append(agentOpts, agent.WithRemote(remote))
			agentOpts = append(agentOpts, agent.WithMode(agentMode))
			agentOpts = append(agentOpts, agent.WithHealthzPort(healthzPort))

			agentOpts = append(agentOpts, agent.WithRedisHost(redisAddr))
			agentOpts = append(agentOpts, agent.WithRedisUsername(redisUsername))
			agentOpts = append(agentOpts, agent.WithRedisPassword(redisPassword))

			agentOpts = append(agentOpts, agent.WithEnableResourceProxy(enableResourceProxy))
			agentOpts = append(agentOpts, agent.WithCacheRefreshInterval(cacheRefreshInterval))
			agentOpts = append(agentOpts, agent.WithHeartbeatInterval(heartbeatInterval))

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

	command.Flags().StringVar(&redisAddr, "redis-addr",
		env.StringWithDefault("REDIS_ADDR", nil, "argocd-redis:6379"),
		"The redis host to connect to")

	command.Flags().StringVar(&redisUsername, "redis-username",
		env.StringWithDefault("REDIS_USERNAME", nil, ""),
		"The username to connect to redis with")

	command.Flags().StringVar(&redisPassword, "redis-password",
		env.StringWithDefault("REDIS_PASSWORD", nil, ""),
		"The password to connect to redis with")

	command.Flags().StringVar(&logFormat, "log-format",
		env.StringWithDefault("ARGOCD_PRINCIPAL_LOG_FORMAT", nil, "text"),
		"The log format to use (one of: text, json)")
	command.Flags().BoolVar(&insecure, "insecure-tls",
		env.BoolWithDefault("ARGOCD_AGENT_TLS_INSECURE", false),
		"INSECURE: Do not verify remote TLS certificate")
	command.Flags().BoolVar(&insecurePlaintext, "insecure-plaintext",
		env.BoolWithDefault("ARGOCD_AGENT_INSECURE_PLAINTEXT", false),
		"INSECURE: Connect without TLS (use with Istio or similar service mesh)")
	command.Flags().StringVarP(&namespace, "namespace", "n",
		env.StringWithDefault("ARGOCD_AGENT_NAMESPACE", nil, "argocd"),
		"Namespace to manage applications in")
	command.Flags().StringVar(&agentMode, "agent-mode",
		env.StringWithDefault("ARGOCD_AGENT_MODE", nil, "autonomous"),
		"Mode of operation")
	command.Flags().StringVar(&creds, "creds",
		env.StringWithDefault("ARGOCD_AGENT_CREDS", nil, ""),
		"Credentials to use when connecting to server")
	command.Flags().StringVar(&rootCASecretName, "root-ca-secret-name",
		env.StringWithDefault("ARGOCD_AGENT_TLS_ROOT_CA_SECRET_NAME", nil, config.SecretNamePrincipalCA),
		"Name of the secret containing the root CA certificate")
	command.Flags().StringVar(&rootCAPath, "root-ca-path",
		env.StringWithDefault("ARGOCD_AGENT_TLS_ROOT_CA_PATH", nil, ""),
		"Path to a file containing root CA certificate for verifying remote TLS")
	command.Flags().StringVar(&tlsSecretName, "tls-secret-name",
		env.StringWithDefault("ARGOCD_AGENT_TLS_SECRET_NAME", nil, config.SecretNameAgentClientCert),
		"Name of the secret containing the TLS certificate")
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
		env.NumWithDefault("ARGOCD_AGENT_METRICS_PORT", cmdutil.ValidPort, 8181),
		"Port the metrics server will listen on")
	command.Flags().IntVar(&healthzPort, "healthz-port",
		env.NumWithDefault("ARGOCD_AGENT_HEALTH_CHECK_PORT", cmdutil.ValidPort, 8001),
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
	command.Flags().BoolVar(&enableResourceProxy, "enable-resource-proxy",
		env.BoolWithDefault("ARGOCD_AGENT_ENABLE_RESOURCE_PROXY", true),
		"Enable resource proxy")
	command.Flags().DurationVar(&cacheRefreshInterval, "cache-refresh-interval",
		env.DurationWithDefault("ARGOCD_AGENT_CACHE_REFRESH_INTERVAL", nil, 10*time.Second),
		"Interval to refresh cluster cache info in principal")
	command.Flags().DurationVar(&heartbeatInterval, "heartbeat-interval",
		env.DurationWithDefault("ARGOCD_AGENT_HEARTBEAT_INTERVAL", nil, 0),
		"Interval for application-level heartbeats over the Subscribe stream (e.g., 30s). "+
			"Set to 0 to disable. Useful to keep connections alive through service meshes like Istio.")

	command.Flags().StringVar(&otlpAddress, "otlp-address",
		env.StringWithDefault("ARGOCD_AGENT_OTLP_ADDRESS", nil, ""),
		"Experimental: OpenTelemetry collector address for sending traces (e.g., localhost:4317)")
	command.Flags().BoolVar(&otlpInsecure, "otlp-insecure",
		env.BoolWithDefault("ARGOCD_AGENT_OTLP_INSECURE", false),
		"Experimental: Use insecure connection to OpenTelemetry collector endpoint")

	command.Flags().StringVar(&kubeConfig, "kubeconfig", "", "Path to a kubeconfig file to use")
	command.Flags().StringVar(&kubeContext, "kubecontext", "", "Override the default kube context")
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
	case "header":
		// Header-based auth doesn't require credentials - the sidecar/proxy injects the header
		return "header", auth.Credentials{}, nil
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
