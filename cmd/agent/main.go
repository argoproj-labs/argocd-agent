package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jannfis/argocd-agent/agent"
	"github.com/jannfis/argocd-agent/cmd/cmd"
	"github.com/jannfis/argocd-agent/internal/auth"
	"github.com/jannfis/argocd-agent/internal/auth/userpass"
	"github.com/jannfis/argocd-agent/internal/version"
	"github.com/jannfis/argocd-agent/pkg/client"
	"github.com/jannfis/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func NewAgentRunCommand() *cobra.Command {
	var (
		serverAddress string
		serverPort    int
		logLevel      string
		insecure      bool
		rootCAPath    string
		kubeConfig    string
		namespace     string
		agentMode     string
		creds         string
		showVersion   bool
		tlsClientCrt  string
		tlsClientKey  string
	)
	command := &cobra.Command{
		Short: "Run the argocd-agent agent component",
		Run: func(c *cobra.Command, args []string) {
			if showVersion {
				fmt.Printf("%s v%s\n", version.Name(), version.Version())
				os.Exit(0)
			}
			ctx, cancelFn := context.WithCancel(context.Background())
			defer cancelFn()

			agentOpts := []agent.AgentOption{}
			remoteOpts := []client.RemoteOption{}

			if logLevel == "" {
				logLevel = "info"
			}
			if logLevel != "" {
				lvl, err := cmd.StringToLoglevel(logLevel)
				if err != nil {
					cmd.Fatal("invalid log level: %s. Available levels are: %s", logLevel, cmd.AvailableLogLevels())
				}
				logrus.SetLevel(lvl)
			}
			if creds != "" {
				authMethod, authCreds, err := parseCreds(creds)
				if err != nil {
					cmd.Fatal("Error setting up creds: %v", err)
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
			remoteOpts = append(remoteOpts, client.WithClientMode(types.AgentModeFromString(agentMode)))
			if serverAddress != "" && serverPort > 0 && serverPort < 65536 {
				remote, err = client.NewRemote(serverAddress, serverPort, remoteOpts...)
				if err != nil {
					cmd.Fatal("Error creating remote: %v", err)
				}
			}
			if remote == nil {
				cmd.Fatal("No remote specified")
			}

			kubeConfig, err := cmd.GetKubeConfig(ctx, namespace, kubeConfig)
			if err != nil {
				cmd.Fatal("Could not load Kubernetes config: %v", err)
			}
			agentOpts = append(agentOpts, agent.WithRemote(remote))
			agentOpts = append(agentOpts, agent.WithMode(agentMode))
			ag, err := agent.NewAgent(ctx, kubeConfig.Clientset, kubeConfig.ApplicationsClientset, namespace, agentOpts...)
			if err != nil {
				cmd.Fatal("Could not create a new agent instance: %v", err)
			}
			if err := ag.Start(ctx); err != nil {
				cmd.Fatal("Could not start agent: %v", err)
			}
			<-ctx.Done()
		},
	}

	command.Flags().StringVar(&serverAddress, "server-address", "", "Address of the server to connect to")
	command.Flags().IntVar(&serverPort, "server-port", 443, "Port on the server to connect to")
	command.Flags().StringVar(&logLevel, "log-level", "info", "The log level for the agent")
	command.Flags().BoolVar(&insecure, "insecure-tls", false, "INSECURE: Do not verify remote TLS certificate")
	command.Flags().StringVar(&rootCAPath, "root-ca-path", "", "Path to a file containing root CA certificate for verifying remote TLS")
	command.Flags().StringVar(&kubeConfig, "kubeconfig", "", "Path to a kubeconfig file to use")
	command.Flags().StringVarP(&namespace, "namespace", "n", "argocd", "Namespace to manage applications in")
	command.Flags().StringVar(&agentMode, "agent-mode", "autonomous", "Mode of operation")
	command.Flags().StringVar(&creds, "creds", "", "Credentials to use when connecting to server")
	command.Flags().BoolVar(&showVersion, "version", false, "Display version information and exit")
	command.Flags().StringVar(&tlsClientCrt, "tls-client-cert", "", "Path to TLS client certificate")
	command.Flags().StringVar(&tlsClientKey, "tls-client-key", "", "Path to TLS client key")
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
	c := NewAgentRunCommand()
	err := c.Execute()
	if err != nil {
		cmd.Fatal("ERROR: %v", err)
	}
}
