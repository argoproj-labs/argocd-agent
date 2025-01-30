package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/util/db"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NewAgentCommand() *cobra.Command {
	command := &cobra.Command{
		Short:   "Inspect and manage agent configuration",
		Use:     "agent",
		Aliases: []string{"agents"},
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
		GroupID: "config",
	}
	command.AddCommand(NewAgentCreateCommand())
	command.AddCommand(NewAgentListCommand())
	command.AddCommand(NewAgentInspectCommand())
	command.AddCommand(NewAgentPrintTLSCommand())
	return command
}

func NewAgentCreateCommand() *cobra.Command {
	var (
		kubeProxyServer   string
		kubeProxyUsername string
		kubeProxyPassword string
		addLabels         []string
	)
	command := &cobra.Command{
		Short: "Create a new agent configuration",
		Use:   "create <agent_name>",
		Run: func(c *cobra.Command, args []string) {
			if len(args) != 1 {
				_ = c.Help()
				os.Exit(1)
			}
			agentName := args[0]
			ctx := context.TODO()

			// A set of labels for the cluster secret
			labels := make(map[string]string)
			if len(addLabels) > 0 {
				var err error
				labels, err = labelSliceToMap(addLabels)
				if err != nil {
					cmdutil.Fatal("%v", err)
				}
			}

			// The agent's name will be persisted as a label
			labels[cluster.LabelKeyClusterAgentMapping] = agentName

			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.namespace, "", globalOpts.context)
			if err != nil {
				cmdutil.Fatal("Could not create Kubernetes client: %v", err)
			}

			// Make sure the cluster secret doesn't exist yet
			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Get(ctx, clusterSecretName(agentName), metav1.GetOptions{})
			if err != nil && !errors.IsNotFound(err) {
				cmdutil.Fatal("Reading cluster secret: %s", err)
			} else if err == nil {
				cmdutil.Fatal("Agent %s exists.", agentName)
			}

			// Get desired credentials from the user
			if kubeProxyUsername == "" {
				var err error
				reader := bufio.NewReader(os.Stdin)
				fmt.Print("Username: ")
				kubeProxyUsername, err = reader.ReadString('\n')
				if err != nil {
					cmdutil.Fatal("%v", err)
				}
			}
			if kubeProxyUsername != "" && kubeProxyPassword == "" {
				fmt.Print("Password: ")
				pass1, err := term.ReadPassword(int(syscall.Stdin))
				fmt.Println()
				if err != nil {
					cmdutil.Fatal("%v", err)
				}
				fmt.Print("Repeat password: ")
				pass2, err := term.ReadPassword(int(syscall.Stdin))
				fmt.Println()
				if err != nil {
					cmdutil.Fatal("%v", err)
				}
				if string(pass1) != string(pass2) {
					cmdutil.Fatal("Passwords don't match.")
				}
				kubeProxyPassword = string(pass1)
			}

			// Our CA certificate is stored in a secret
			tlsCert, err := tlsutil.TLSCertFromSecret(ctx, clt.Clientset, globalOpts.namespace, config.SecretNamePrincipalCA)
			if err != nil {
				cmdutil.Fatal("Could not read CA secret: %v", err)
			}
			signerCert, err := x509.ParseCertificate(tlsCert.Certificate[0])
			if err != nil {
				cmdutil.Fatal("Could not parse CA certificate: %v", err)
			}

			// Generate a client cert and sign it using the CA's cert and key
			clientCert, clientKey, err := tlsutil.GenerateClientCertificate(agentName, signerCert, tlsCert.PrivateKey)
			if err != nil {
				cmdutil.Fatal("Could not create client cert: %v", err)
			}

			// We need to re-encode the CA's public certificate back to PEM.
			// It's a little stupid, because it is stored in the secret as
			// PEM already, but the tls.Certificate contains only RAW byte.
			caData, err := tlsutil.CertDataToPEM([]byte(tlsCert.Certificate[0]))
			if err != nil {
				cmdutil.Fatal("Could not encode CA cert to PEM: %v", err)
			}

			// Construct Argo CD cluster configuration
			clus := &v1alpha1.Cluster{
				Server: fmt.Sprintf("https://%s?agentName=%s", kubeProxyServer, agentName),
				Name:   agentName,
				Labels: labels,
				Config: v1alpha1.ClusterConfig{
					TLSClientConfig: v1alpha1.TLSClientConfig{
						CertData: []byte(clientCert),
						KeyData:  []byte(clientKey),
						CAData:   []byte(caData),
					},
					Username: kubeProxyUsername,
					Password: kubeProxyPassword,
				},
			}

			// Then, store this cluster configuration in a secret.
			sec := &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterSecretName(agentName),
					Namespace: globalOpts.namespace,
				},
			}
			err = cluster.ClusterToSecret(clus, sec)
			if err != nil {
				cmdutil.Fatal("Could not convert cluster to secret: %v", err)
			}
			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Create(ctx, sec, metav1.CreateOptions{})
			if err != nil {
				cmdutil.Fatal("Could not create cluster secret: %v", err)
			}
			fmt.Printf("Agent %s created\n", agentName)
		},
	}
	command.Flags().StringVar(&kubeProxyServer, "kube-proxy-server", "192.168.56.2:9090", "Location of principal's kube-proxy")
	command.Flags().StringVar(&kubeProxyUsername, "kube-proxy-username", "", "The username for the kube-proxy")
	command.Flags().StringVar(&kubeProxyPassword, "kube-proxy-password", "", "The password for the kube-proxy")
	command.Flags().StringSliceVarP(&addLabels, "label", "l", []string{}, "Additional labels for the agent")
	return command
}

func NewAgentListCommand() *cobra.Command {
	var (
		labelSelector []string
	)
	command := &cobra.Command{
		Short: "List configured agents",
		Use:   "list",
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.TODO()
			labelSelector = append(labelSelector, cluster.LabelKeyClusterAgentMapping)
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.namespace, "", globalOpts.context)
			if err != nil {
				cmdutil.Fatal("Could not create Kubernetes client: %v", err)
			}
			agentList, err := clt.Clientset.CoreV1().Secrets(globalOpts.namespace).List(ctx, metav1.ListOptions{
				LabelSelector: strings.Join(labelSelector, ","),
			})
			if err != nil {
				cmdutil.Fatal("Could not list secrets: %v", err)
			}
			if len(agentList.Items) == 0 {
				fmt.Printf("No agents found.\n")
				os.Exit(1)
			}
			for _, s := range agentList.Items {
				fmt.Printf("%s\n", strings.TrimPrefix(s.Name, "cluster-"))
			}
		},
	}
	command.Flags().StringSliceVarP(&labelSelector, "label", "l", []string{}, "Only list agents matching label")
	return command
}

func NewAgentInspectCommand() *cobra.Command {
	var (
		outputFormat string
	)
	command := &cobra.Command{
		Short:   "Inspect agent configuration",
		Use:     "inspect",
		Aliases: []string{"show"},
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) != 1 {
				_ = cmd.Help()
				os.Exit(1)
			}
			type clusterOut struct {
				ServerAddr     string    `yaml:"server" json:"server" text:"Server address"`
				Name           string    `yaml:"name" json:"name" text:"Server name"`
				NotValidBefore time.Time `yaml:"notValidBefore" json:"notValidBefore" text:"Not valid before"`
				NotValidAfter  time.Time `yaml:"notValidAfter" json:"notValidAfter" text:"Not valid after"`
			}
			ctx := context.TODO()
			agentName := args[0]
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.namespace, "", globalOpts.context)
			if err != nil {
				cmdutil.Fatal("Could not create Kubernetes client: %v", err)
			}
			agent, err := clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Get(ctx, clusterSecretName(agentName), metav1.GetOptions{})
			if err != nil {
				cmdutil.Fatal("Could not get agent configuration: %v", err)
			}
			argoCluster, err := db.SecretToCluster(agent)
			if err != nil {
				cmdutil.Fatal("Not a valid cluster configuration: %v", err)
			}
			cert, err := tls.X509KeyPair(argoCluster.Config.TLSClientConfig.CertData, argoCluster.Config.TLSClientConfig.KeyData)
			if err != nil {
				cmdutil.Fatal("Not a valid certificate: %v", err)
			}
			cluster := &clusterOut{
				ServerAddr:     argoCluster.Server,
				Name:           argoCluster.Name,
				NotValidAfter:  cert.Leaf.NotAfter,
				NotValidBefore: cert.Leaf.NotBefore,
			}
			var out []byte
			switch strings.ToLower(outputFormat) {
			case "json":
				out, err = json.MarshalIndent(cluster, "", " ")
				out = append(out, '\n')
			case "yaml":
				out, err = yaml.Marshal(cluster)
			case "text":
				bb := &bytes.Buffer{}
				tw := tabwriter.NewWriter(bb, 0, 0, 2, ' ', 0)
				err = cmdutil.StructToTabwriter(cluster, tw)
				tw.Flush()
				out = bb.Bytes()
			default:
				cmdutil.Fatal("Unknown output format: %s", outputFormat)
			}
			if err != nil {
				cmdutil.Fatal("%v", err)
			}
			fmt.Print(string(out))
		},
	}
	command.Flags().StringVarP(&outputFormat, "output", "o", "json", "Output format (json, yaml or text)")
	return command
}

func NewAgentPrintTLSCommand() *cobra.Command {
	var (
		printWhat string
	)
	command := &cobra.Command{
		Short:   "Print the TLS client certificate of an agent to stdout",
		Use:     "print-tls",
		Aliases: []string{"dump-tls"},
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) != 1 {
				_ = cmd.Help()
				os.Exit(1)
			}
			agentName := args[0]
			ctx := context.TODO()
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.namespace, "", globalOpts.context)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}
			sec, err := clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Get(ctx, clusterSecretName(agentName), metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					cmdutil.Fatal("No such agent: %s", agentName)
				} else {
					cmdutil.Fatal("Error fetching agent details: %v", err)
				}
			}
			clus, err := db.SecretToCluster(sec)
			if err != nil {
				cmdutil.Fatal("Invalid cluster secret: %v", err)
			}
			switch printWhat {
			case "cert":
				fmt.Print(string(clus.Config.CertData))
			case "key":
				fmt.Print(string(clus.Config.KeyData))
			case "ca:":
				fmt.Print(string(clus.Config.CAData))
			}
		},
	}

	command.Flags().StringVarP(&printWhat, "type", "t", "cert", "Type of asset to print (cert or key)")
	return command
}

func clusterSecretName(agentName string) string {
	return "cluster-" + agentName
}

func labelSliceToMap(labels []string) (map[string]string, error) {
	m := make(map[string]string)
	for _, ll := range labels {
		l := strings.SplitN(ll, "=", 2)
		if len(l) != 2 {
			return nil, fmt.Errorf("couldn't parse label definition '%s'", ll)
		}
		if v, ok := m[l[0]]; ok {
			return nil, fmt.Errorf("label '%s' already set with value '%s'", l[0], v)
		}
		m[l[0]] = l[1]
	}
	return m, nil
}
