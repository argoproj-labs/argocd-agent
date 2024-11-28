package main

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj/argo-cd/v2/util/glob"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

/*
The commands defined in this file manage an oversimplified CA for testing
purposes.

DO NOT USE THE CA OR THE CERTIFICATES ISSUED BY IT FOR ANY SERIOUS PURPOSE.
DO NOT EVEN THINK ABOUT USING THEM SOMEWHERE IN A PRODUCTION ENVIRONMENT,
OR TO PROTECT ANY KIND OF DATA.

You have been warned.
*/

func NewCACommand() *cobra.Command {
	command := &cobra.Command{
		Short: "!!NON-PROD!! Inspect and manage the principal's CA",
		Long: `The ca command provides functions to inspect and manage a Certificate Authority
for argocd-agent. Its whole purpose is to get you started without having to go
through hoops with a real CA.

DO NOT USE THE CA OR THE CERTIFICATES ISSUED BY IT FOR ANY SERIOUS PURPOSE.
DO NOT EVEN THINK ABOUT USING THEM SOMEWHERE IN A PRODUCTION ENVIRONMENT,
OR TO PROTECT ANY KIND OF DATA.
`,
		Use: "ca",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
		GroupID: "config",
	}
	command.AddCommand(NewCAGenerateCommand())
	command.AddCommand(NewCAInspectCommand())
	command.AddCommand(NewCADeleteCommand())
	command.AddCommand(NewCAPrintCommand())
	command.AddCommand(NewCAIssueCommand())
	return command
}

func NewCAGenerateCommand() *cobra.Command {
	var (
		force bool
	)
	command := &cobra.Command{
		Short: "NON-PROD!! Generate a new Certificate Authority for argocd-agent",
		Use:   "generate",
		Run: func(c *cobra.Command, args []string) {
			ctx := context.TODO()
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.namespace, "", globalOpts.context)
			if err != nil {
				cmdutil.Fatal("Error creating Kubernetes client: %v", err)
			}
			exists := false
			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Get(ctx, config.SecretNamePrincipalCA, metav1.GetOptions{})
			if !errors.IsNotFound(err) {
				if err != nil {
					cmdutil.Fatal("Error getting secret: %v", err)
				} else if !force {
					cmdutil.Fatal("CA secret already exist. Please delete it before generating.")
				}
				exists = true
			}
			fmt.Println("Generating CA and storing it in secret")
			cert, key, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
			if err != nil {
				cmdutil.Fatal("Could not generate certificate: %v", err)
			}

			sec := &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SecretNamePrincipalCA,
					Namespace: globalOpts.namespace,
				},
				Type: "kubernetes.io/tls",
				Data: map[string][]byte{
					"tls.crt": []byte(cert),
					"tls.key": []byte(key),
				},
			}

			if !exists {
				_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Create(ctx, sec, metav1.CreateOptions{})
			} else {
				_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Update(ctx, sec, metav1.UpdateOptions{})
			}
			if err != nil {
				cmdutil.Fatal("Could not write to secret: %v", err)
			}
			fmt.Printf("Success. CA data stored in secret %s/%s\n", globalOpts.namespace, config.SecretNamePrincipalCA)
		},
	}

	command.Flags().BoolVarP(&force, "force", "f", false, "Force regeneration of CA if it exists")
	return command
}

func NewCAInspectCommand() *cobra.Command {
	var (
		full      bool
		outFormat string
	)
	type summary struct {
		Subject   string   `json:"subject" yaml:"subject"`
		KeyType   string   `json:"keyType" yaml:"keyType"`
		Keylength int      `json:"keyLength" yaml:"keyLength"`
		NotBefore string   `json:"notBefore" yaml:"notBefore"`
		NotAfter  string   `json:"notAfter" yaml:"notAfter"`
		Checksum  string   `json:"sha256" yaml:"sha256"`
		Warnings  []string `json:"warnings,omitempty" yaml:"warnings,omitempty"`
	}
	command := &cobra.Command{
		Short: "NON-PROD!! Inspect the configured CA",
		Use:   "inspect",
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.TODO()
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.namespace, "", globalOpts.context)
			if err != nil {
				cmdutil.Fatal("Error creating Kubernetes client: %v", err)
			}
			tlsCert, err := tlsutil.TLSCertFromSecret(ctx, clt.Clientset, globalOpts.namespace, config.SecretNamePrincipalCA)
			if err != nil {
				cmdutil.Fatal("Could not read CA from secret: %v", err)
			}
			if len(tlsCert.Certificate[0]) == 0 {
				cmdutil.Fatal("No certificate in data")
			}
			cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
			if err != nil {
				cmdutil.Fatal("Could not parse x509 data: %v", err)
			}

			key, ok := tlsCert.PrivateKey.(*rsa.PrivateKey)
			if !ok {
				cmdutil.Fatal("Not an RSA private key")
			}

			warnings := []string{}
			if !cert.IsCA {
				warnings = append(warnings, "- Is not a CA certificate")
			}
			if !cert.BasicConstraintsValid {
				warnings = append(warnings, "- Basic constraints are invalid")
			}

			sum := summary{
				Subject:   cert.Subject.String(),
				KeyType:   "RSA",
				Keylength: key.Size() * 8,
				NotBefore: cert.NotBefore.Format(time.RFC1123Z),
				NotAfter:  cert.NotAfter.Format(time.RFC1123Z),
				Checksum:  fmt.Sprintf("%x", sha256.Sum256(cert.Raw)),
				Warnings:  warnings,
			}

			if outFormat == "json" {
				jsonOut, err := json.MarshalIndent(sum, "", "  ")
				if err != nil {
					cmdutil.Fatal("Could not marshal summary to JSON: %v", err)
				}
				fmt.Println(string(jsonOut))
			} else if outFormat == "yaml" {
				yamlOut, err := yaml.Marshal(sum)
				if err != nil {
					cmdutil.Fatal("Could not marshal summary: %v", err)
				}
				fmt.Print(string(yamlOut))
			} else if outFormat == "text" {
				tw := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
				fmt.Fprintf(tw, "Subject:\t%s\n", sum.Subject)
				fmt.Fprintf(tw, "Key type:\t%s\n", sum.KeyType)
				fmt.Fprintf(tw, "Key length:\t%d\n", sum.Keylength)
				fmt.Fprintf(tw, "Not valid before:\t%s\n", sum.NotBefore)
				fmt.Fprintf(tw, "Not valid after:\t%s\n", sum.NotAfter)
				fmt.Fprintf(tw, "Checksum:\t%s\n", sum.Checksum)
				tw.Flush()
			} else {
				cmd.Help()
				cmdutil.Fatal("Unknown output format: %s", outFormat)
			}
		},
	}

	command.Flags().BoolVarP(&full, "full", "f", false, "Display the full certificate, not the summary")
	command.Flags().StringVarP(&outFormat, "output", "o", "json", "Output format (json, yaml or text)")
	return command
}

func NewCADeleteCommand() *cobra.Command {
	command := &cobra.Command{
		Short: "Delete the configured CA",
		Use:   "delete",
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.Background()
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.namespace, "", globalOpts.context)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}
			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Get(ctx, config.SecretNamePrincipalCA, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					cmdutil.Fatal("CA not configured.")
				} else {
					cmdutil.Fatal("%v", err)
				}
			}
			answer, err := cmdutil.ReadFromTerm("Really delete CA (use uppercase YES to delete)?", 0, nil)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}
			if answer == "YES" {
				err := clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Delete(ctx, config.SecretNamePrincipalCA, metav1.DeleteOptions{})
				if err != nil {
					cmdutil.Fatal("Could not delete secret %s: %v", config.SecretNamePrincipalCA, err)
				}
				fmt.Println("CA successfully deleted.")
			} else {
				fmt.Printf("%s\n", answer)
				fmt.Println("Aborted.")
				os.Exit(1)
			}
		},
	}

	return command
}

func NewCAPrintCommand() *cobra.Command {
	var (
		printCert bool
		printKey  bool
	)
	command := &cobra.Command{
		Short: "Print CA data in PEM format to stdout",
		Long: `
Prints the RSA private key, the public cert or both from the CA to stdout.
Output format is PEM.

Only private keys of type RSA are currently supported.
		`,
		Use: "dump",
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.Background()
			if cmd.Flag("all").Changed {
				printCert = true
				printKey = true
			}
			if !printCert && !printKey {
				cmd.Help()
				cmdutil.Fatal("One of --all, --key or --cert must be specified.")
			}
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.namespace, "", globalOpts.context)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}
			cert, err := tlsutil.TLSCertFromSecret(ctx, clt.Clientset, globalOpts.namespace, config.SecretNamePrincipalCA)
			if errors.IsNotFound(err) {
				cmdutil.Fatal("CA not configured.")
			}
			certPem := new(bytes.Buffer)
			pem.Encode(certPem, &pem.Block{
				Type:  "CERTIFICATE",
				Bytes: cert.Certificate[0],
			})

			// We expect and only support RSA as the key type for now
			key, ok := cert.PrivateKey.(*rsa.PrivateKey)
			if !ok {
				cmdutil.Fatal("Private key is not in RSA format.")
			}
			keyPem := new(bytes.Buffer)
			pem.Encode(keyPem, &pem.Block{
				Type:  "RSA PRIVATE KEY",
				Bytes: x509.MarshalPKCS1PrivateKey(key),
			})

			if printCert {
				fmt.Print(certPem.String())
			}
			if printKey {
				fmt.Print(keyPem.String())
			}
		},
	}
	command.Flags().Bool("all", false, "Print both, key and cert")
	command.Flags().BoolVar(&printKey, "key", false, "Print the private key")
	command.Flags().BoolVar(&printCert, "cert", false, "Print the public certificate")
	return command
}

var validComponents = []string{"resource-proxy"}

func NewCAIssueCommand() *cobra.Command {
	var (
		component string
		san       []string
	)
	command := &cobra.Command{
		Short:   "NON-PROD!! Issue TLS certificates signed by the CA",
		Use:     "issue <component>",
		Aliases: []string{"new-cert"},
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) != 1 {
				cmd.Help()
				cmdutil.Fatal("Secret name not given.")
			}
			component = args[0]
			if !glob.MatchStringInList(validComponents, component, glob.EXACT) {
				cmdutil.Fatal("Component must be one of: %s", strings.Join(validComponents, ", "))
			}
			ctx := context.TODO()
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.namespace, "", globalOpts.context)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}

			// Check if secret already exists
			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Get(ctx, component, metav1.GetOptions{})
			if err != nil {
				if !errors.IsNotFound(err) {
					cmdutil.Fatal("Error getting secret %s/%s: %v", globalOpts.namespace, component, err)
				}
			} else {
				cmdutil.Fatal("Secret %s/%s already exist", globalOpts.namespace, component)
			}

			// Get CA certificate
			caCert, err := tlsutil.TLSCertFromSecret(ctx, clt.Clientset, globalOpts.namespace, config.SecretNamePrincipalCA)
			if err != nil {
				if errors.IsNotFound(err) {
					cmdutil.Fatal("CA is not initialized.")
				} else {
					cmdutil.Fatal("Error getting CA certificate: %v", err)
				}
			}
			signerCert, err := x509.ParseCertificate(caCert.Certificate[0])
			if err != nil {
				cmdutil.Fatal("Could not parse CA certificate: %v", err)
			}
			var secret *v1.Secret = nil
			cert, key, err := tlsutil.GenerateServerCertificate("server", signerCert, caCert.PrivateKey, san)
			if err != nil {
				cmdutil.Fatal("Could not generate server cert: %v", err)
			}
			secret = &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-tls", component),
					Namespace: globalOpts.namespace,
				},
				Type: "kubernetes.io/tls",
				Data: map[string][]byte{
					"tls.crt": []byte(cert),
					"tls.key": []byte(key),
				},
			}

			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Create(ctx, secret, metav1.CreateOptions{})
			if err != nil {
				cmdutil.Fatal("Could not create secret: %v", err)
			}
			fmt.Printf("Secret %s/%s created\n", globalOpts.namespace, fmt.Sprintf("%s-tls", component))
		},
	}

	command.Flags().StringSliceVarP(&san, "san", "N", []string{"IP:127.0.0.1"}, "Subject Alternative Names (SAN) for the cert")
	return command
}
