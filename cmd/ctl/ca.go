package main

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj/argo-cd/v2/util/glob"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
			_ = cmd.Help()
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
			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Get(ctx, config.SecretNamePrincipalCA, v1.GetOptions{})
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

			sec := &corev1.Secret{
				ObjectMeta: v1.ObjectMeta{
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
				_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Create(ctx, sec, v1.CreateOptions{})
			} else {
				_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Update(ctx, sec, v1.UpdateOptions{})
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
		Subject   string   `json:"subject" yaml:"subject" text:"Subject"`
		KeyType   string   `json:"keyType" yaml:"keyType" text:"Key type"`
		KeyLength int      `json:"keyLength" yaml:"keyLength" text:"Key length"`
		NotBefore string   `json:"notBefore" yaml:"notBefore" text:"Not valid before"`
		NotAfter  string   `json:"notAfter" yaml:"notAfter" text:"Not valid after"`
		Checksum  string   `json:"sha256" yaml:"sha256" text:"Checksum"`
		Warnings  []string `json:"warnings,omitempty" yaml:"warnings,omitempty" text:"Warnings,omitempty"`
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
				KeyLength: key.Size() * 8,
				NotBefore: cert.NotBefore.Format(time.RFC1123Z),
				NotAfter:  cert.NotAfter.Format(time.RFC1123Z),
				Checksum:  fmt.Sprintf("%x", sha256.Sum256(cert.Raw)),
				Warnings:  warnings,
			}

			out, err := cmdutil.MarshalStruct(sum, outFormat)
			if err != nil {
				cmdutil.Fatal("Could not marshal summary: %v", err)
			}
			fmt.Print(string(out))
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
			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Get(ctx, config.SecretNamePrincipalCA, v1.GetOptions{})
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
				err := clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Delete(ctx, config.SecretNamePrincipalCA, v1.DeleteOptions{})
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
				_ = cmd.Help()
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
			err = pem.Encode(certPem, &pem.Block{
				Type:  "CERTIFICATE",
				Bytes: cert.Certificate[0],
			})
			if err != nil {
				cmdutil.Fatal("Could not encode cert: %v", err)
			}

			// We expect and only support RSA as the key type for now
			key, ok := cert.PrivateKey.(*rsa.PrivateKey)
			if !ok {
				cmdutil.Fatal("Private key is not in RSA format.")
			}
			keyPem := new(bytes.Buffer)
			err = pem.Encode(keyPem, &pem.Block{
				Type:  "RSA PRIVATE KEY",
				Bytes: x509.MarshalPKCS1PrivateKey(key),
			})
			if err != nil {
				cmdutil.Fatal("Could not encode key: %v", err)
			}

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
		upsert    bool
	)
	command := &cobra.Command{
		Short:   "NON-PROD!! Issue TLS certificates signed by the CA",
		Use:     "issue <component>",
		Aliases: []string{"new-cert"},
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) != 1 {
				_ = cmd.Help()
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
			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Get(ctx, component, v1.GetOptions{})
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
			var secret *corev1.Secret
			cert, key, err := tlsutil.GenerateServerCertificate("server", signerCert, caCert.PrivateKey, san)
			if err != nil {
				cmdutil.Fatal("Could not generate server cert: %v", err)
			}
			secret = &corev1.Secret{
				ObjectMeta: v1.ObjectMeta{
					Name:      fmt.Sprintf("%s-tls", component),
					Namespace: globalOpts.namespace,
				},
				Type: "kubernetes.io/tls",
				Data: map[string][]byte{
					"tls.crt": []byte(cert),
					"tls.key": []byte(key),
				},
			}

			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Create(ctx, secret, v1.CreateOptions{})
			if err != nil {
				if !errors.IsAlreadyExists(err) {
					cmdutil.Fatal("Could not create secret: %v", err)
				} else if !upsert {
					cmdutil.Fatal("Certificate exists, please use --upsert to reissue.")
				} else {
					_, err = clt.Clientset.CoreV1().Secrets(globalOpts.namespace).Update(ctx, secret, v1.UpdateOptions{})
					if err != nil {
						cmdutil.Fatal("Could not update secret: %v", err)
					} else {
						fmt.Printf("Secret updated.\n")
					}
				}
			} else {
				fmt.Printf("Secret %s/%s created\n", globalOpts.namespace, fmt.Sprintf("%s-tls", component))
			}
		},
	}

	command.Flags().StringSliceVarP(&san, "san", "N", []string{"IP:127.0.0.1"}, "Subject Alternative Names (SAN) for the cert")
	command.Flags().BoolVarP(&upsert, "upsert", "u", false, "Update existing certificate if it exists")
	return command
}
