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
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
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

// certificateSummary is a summary of a certificate.
type certificateSummary struct {
	Subject   string   `json:"subject" yaml:"subject" text:"Subject"`
	KeyType   string   `json:"keyType" yaml:"keyType" text:"Key type"`
	KeyLength int      `json:"keyLength" yaml:"keyLength" text:"Key length"`
	NotBefore string   `json:"notBefore" yaml:"notBefore" text:"Not valid before"`
	NotAfter  string   `json:"notAfter" yaml:"notAfter" text:"Not valid after"`
	Checksum  string   `json:"sha256" yaml:"sha256" text:"Checksum"`
	IPs       []string `json:"ips,omitempty" yaml:"ips,omitempty" text:"IPs,omitempty"`
	DNS       []string `json:"dns,omitempty" yaml:"dns,omitempty" text:"DNS,omitempty"`
	Warnings  []string `json:"warnings,omitempty" yaml:"warnings,omitempty" text:"Warnings,omitempty"`
}

func NewPKICommand() *cobra.Command {
	command := &cobra.Command{
		Short: "!!NON-PROD!! Inspect and manage the principal's PKI",
		Long: `The pki command provides functions to inspect and manage a public key infrastructure
(PKI) for argocd-agent. Its whole purpose is to get you started without having
to go through hoops with a real CA.

DO NOT USE THE PKI OR THE CERTIFICATES ISSUED BY IT FOR ANY SERIOUS PURPOSE.
DO NOT EVEN THINK ABOUT USING THEM SOMEWHERE IN A PRODUCTION ENVIRONMENT,
OR TO PROTECT ANY KIND OF DATA.
`,
		Use: "pki",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
		GroupID: "config",
	}
	command.AddCommand(NewPKIInitCommand())
	command.AddCommand(NewPKIInspectCommand())
	command.AddCommand(NewPKIDeleteCommand())
	command.AddCommand(NewPKIPrintCommand())
	command.AddCommand(NewPKIIssueCommand())
	command.AddCommand(NewPKIPropagateCommand())
	return command
}

func NewPKIInitCommand() *cobra.Command {
	var (
		force bool
	)
	command := &cobra.Command{
		Short: "NON-PROD!! Initialize the PKI for use with argocd-agent",
		Use:   "init",
		Run: func(c *cobra.Command, args []string) {
			ctx := context.TODO()
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("Error creating Kubernetes client: %v", err)
			}
			exists := false
			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Get(ctx, config.SecretNamePrincipalCA, v1.GetOptions{})
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
					Namespace: globalOpts.principalNamespace,
				},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{
					"tls.crt": []byte(cert),
					"tls.key": []byte(key),
				},
			}

			if !exists {
				_, err = clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Create(ctx, sec, v1.CreateOptions{})
			} else {
				_, err = clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Update(ctx, sec, v1.UpdateOptions{})
			}
			if err != nil {
				cmdutil.Fatal("Could not write to secret: %v", err)
			}
			fmt.Printf("Success. CA data stored in secret %s/%s\n", globalOpts.principalNamespace, config.SecretNamePrincipalCA)
		},
	}

	command.Flags().BoolVarP(&force, "force", "f", false, "Force regeneration of PKI if it exists")
	return command
}

func NewPKIPropagateCommand() *cobra.Command {
	var (
		force bool
	)
	command := &cobra.Command{
		Short: "NON-PROD!! Propagate the PKI to the agent",
		Use:   "propagate",
		Run: func(cmd *cobra.Command, args []string) {
			if globalOpts.principalContext == globalOpts.agentContext {
				cmdutil.Fatal("PKI and agent cannot reside within the same context.")
			} else if globalOpts.principalContext == "" || globalOpts.agentContext == "" {
				cmdutil.Fatal("Must specify both principal and agent contexts.")
			}
			ctx := context.TODO()
			principalClt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("Error creating Kubernetes client: %v", err)
			}
			agentClt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.agentNamespace, "", globalOpts.agentContext)
			if err != nil {
				cmdutil.Fatal("Error creating Kubernetes client: %v", err)
			}
			caSecret, err := principalClt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Get(ctx, config.SecretNamePrincipalCA, v1.GetOptions{})
			if err != nil {
				cmdutil.Fatal("Error getting CA secret from principal: %v", err)
			}
			agentSecret := &corev1.Secret{
				ObjectMeta: v1.ObjectMeta{
					Name:      config.SecretNameAgentCA,
					Namespace: globalOpts.agentNamespace,
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"ca.crt": caSecret.Data["tls.crt"],
				},
			}
			exists := false
			_, err = agentClt.Clientset.CoreV1().Secrets(globalOpts.agentNamespace).Get(ctx, config.SecretNameAgentCA, v1.GetOptions{})
			if err != nil {
				if !errors.IsNotFound(err) {
					cmdutil.Fatal("Error getting agent CA secret: %v", err)
				}
			} else {
				if !force {
					cmdutil.Fatal("Agent CA secret already exists. Please delete it before propagating.")
				}
				exists = true
			}
			if !exists {
				_, err = agentClt.Clientset.CoreV1().Secrets(globalOpts.agentNamespace).Create(ctx, agentSecret, v1.CreateOptions{})
			} else {
				_, err = agentClt.Clientset.CoreV1().Secrets(globalOpts.agentNamespace).Update(ctx, agentSecret, v1.UpdateOptions{})
			}
			if err != nil {
				cmdutil.Fatal("Error updating agent CA secret: %v", err)
			}
			fmt.Printf("Agent CA secret %s/%s created\n", globalOpts.agentNamespace, config.SecretNameAgentCA)
		},
	}
	command.Flags().BoolVarP(&force, "force", "f", false, "Force regeneration of PKI if it exists")
	return command
}

func NewPKIInspectCommand() *cobra.Command {
	var (
		full      bool
		outFormat string
	)

	type inspectSummary struct {
		CA            certificateSummary `json:"ca,omitempty" yaml:"ca,omitempty" text:"CA,omitempty"`
		Principal     certificateSummary `json:"principal,omitempty" yaml:"principal,omitempty" text:"Principal,omitempty"`
		ResourceProxy certificateSummary `json:"resourceProxy,omitempty" yaml:"resourceProxy,omitempty" text:"Resource Proxy,omitempty"`
	}
	command := &cobra.Command{
		Short: "NON-PROD!! Inspect the configured PKI",
		Use:   "inspect",
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.TODO()
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("Error creating Kubernetes client: %v", err)
			}

			caSum, err := readAndSummarizeCertificate(ctx, clt, globalOpts.principalNamespace, config.SecretNamePrincipalCA, true)
			if err != nil {
				cmdutil.Fatal("CA is not initialized: %v", err)
			}
			resourceProxySum, err := readAndSummarizeCertificate(ctx, clt, globalOpts.principalNamespace, "argocd-agent-resource-proxy-tls", false)
			if err != nil && !errors.IsNotFound(err) {
				cmdutil.Fatal("Error reading resource proxy certificate: %v", err)
			}
			principalSum, err := readAndSummarizeCertificate(ctx, clt, globalOpts.principalNamespace, "argocd-agent-principal-tls", false)
			if err != nil && !errors.IsNotFound(err) {
				cmdutil.Fatal("Error reading principal certificate: %v", err)
			}

			sum := inspectSummary{
				CA:            caSum,
				Principal:     principalSum,
				ResourceProxy: resourceProxySum,
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

func NewPKIDeleteCommand() *cobra.Command {
	command := &cobra.Command{
		Short: "Delete the configured PKI",
		Use:   "delete",
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.Background()
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}
			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Get(ctx, config.SecretNamePrincipalCA, v1.GetOptions{})
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
				err := clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Delete(ctx, config.SecretNamePrincipalCA, v1.DeleteOptions{})
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

func NewPKIPrintCommand() *cobra.Command {
	var (
		printCert bool
		printKey  bool
	)
	command := &cobra.Command{
		Short: "Print PKI's CA data in PEM format to stdout",
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
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("%v", err)
			}
			cert, err := tlsutil.TLSCertFromSecret(ctx, clt.Clientset, globalOpts.principalNamespace, config.SecretNamePrincipalCA)
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

func NewPKIIssueCommand() *cobra.Command {
	command := &cobra.Command{
		Short:   "NON-PROD!! Issue TLS certificates signed by the PKI's CA",
		Use:     "issue <component>",
		Aliases: []string{"new-cert"},
	}
	command.AddCommand(NewPKIIssuePrincipalCommand())
	command.AddCommand(NewPKIIssueResourceProxyCommand())
	command.AddCommand(NewPKIIssueAgentClientCert())
	return command
}

func NewPKIIssuePrincipalCommand() *cobra.Command {
	var (
		ips    []string
		dns    []string
		upsert bool
	)
	command := &cobra.Command{
		Short: "Issue a TLS certificate for the principal",
		Use:   "principal",
		Run: func(cmd *cobra.Command, args []string) {
			issueAndSaveSecret(globalOpts.principalContext, "argocd-agent-principal-tls", globalOpts.principalNamespace, upsert, func(c *x509.Certificate, pk crypto.PrivateKey) (string, string, error) {
				return tlsutil.GenerateServerCertificate("argocd-agent-principal", c, pk, ips, dns)
			})
		},
	}
	command.Flags().StringSliceVar(&ips, "ip", []string{"127.0.0.1"}, "The IP addresses this certificate is valid for")
	command.Flags().StringSliceVar(&dns, "dns", []string{"localhost"}, "The DNS names this certificate is valid for")
	command.Flags().BoolVarP(&upsert, "upsert", "u", false, "Whether to update an existing certificate if it exists")
	return command
}

func NewPKIIssueResourceProxyCommand() *cobra.Command {
	var (
		ips    []string
		dns    []string
		upsert bool
		noSAN  bool
	)
	command := &cobra.Command{
		Short: "Issue a TLS certificate for the resource proxy",
		Use:   "resource-proxy",
		Run: func(cmd *cobra.Command, args []string) {
			if len(ips) == 0 && len(dns) == 0 {
				fmt.Println("Please pass at least one of --ips or --dns options or use --no-san to create certificate without SAN")
				os.Exit(1)
			}
			issueAndSaveSecret(globalOpts.principalContext, "argocd-agent-resource-proxy-tls", globalOpts.principalNamespace, upsert, func(c *x509.Certificate, pk crypto.PrivateKey) (string, string, error) {
				return tlsutil.GenerateServerCertificate("argocd-agent-resource-proxy", c, pk, ips, dns)
			})
		},
	}
	command.Flags().StringSliceVar(&ips, "ip", []string{}, "The IP addresses this certificate is valid for")
	command.Flags().StringSliceVar(&dns, "dns", []string{}, "The DNS names this certificate is valid for")
	command.Flags().BoolVarP(&upsert, "upsert", "u", false, "Whether to update an existing certificate if it exists")
	command.Flags().BoolVar(&noSAN, "no-san", false, "Do not add SAN information to the certificate")
	return command
}

func NewPKIIssueAgentClientCert() *cobra.Command {
	var (
		upsert         bool
		sameContext    bool
		agentNamespace string
	)
	command := &cobra.Command{
		Use:   "agent <name>",
		Short: "Issue a client certificate for an agent",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				_ = cmd.Help()
				fmt.Println("Agent name must be given.")
				os.Exit(1)
			}
			agentName := args[0]
			if (globalOpts.principalContext == globalOpts.agentContext) ||
				(globalOpts.principalContext != "" && globalOpts.agentContext == "") &&
					!sameContext {
				fmt.Println("PKI and agent usually do not reside within the same context. Use --same-context if you really mean it.")
				os.Exit(1)
			}
			issueAndSaveSecret(globalOpts.agentContext, config.SecretNameAgentClientCert, agentNamespace, upsert, func(c *x509.Certificate, pk crypto.PrivateKey) (string, string, error) {
				return tlsutil.GenerateClientCertificate(agentName, c, pk)
			})
		},
	}

	command.Flags().BoolVarP(&upsert, "upsert", "u", false, "Whether to update an existing certificate if it exists")
	command.Flags().BoolVar(&sameContext, "same-context", false, "Use when the PKI and agent use the same context")
	command.Flags().StringVar(&agentNamespace, "agent-namespace", "argocd", "The namespace the agent is installed to")
	return command
}

// issueAndSaveSecret uses the issue callback to create a TLS certificate and
// sign it using the principal's CA. The issue function will take the CA's
// certificate and private key as argument, and will return the generated
// secret's certificate and private key as PEM encoded string. The resulting
// certificate will be saved to a secret referred to by outContext, outName and
// outNamespace.
func issueAndSaveSecret(outContext, outName, outNamespace string, upsert bool, issue func(*x509.Certificate, crypto.PrivateKey) (string, string, error)) {
	ctx := context.TODO()

	// Client for principal's kube context - it has the CA
	caClt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
	if err != nil {
		cmdutil.Fatal("%v", err)
	}

	// Client for the kube context to write the resulting secret to - might be different
	var outClt *kube.KubernetesClient
	if outContext == globalOpts.principalContext || outContext == "" {
		outClt = caClt
	} else {
		outClt, err = kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", outContext)
		if err != nil {
			cmdutil.Fatal("%v", err)
		}
	}

	// Load CA keypair from a secret on the principal
	caCert, err := tlsutil.TLSCertFromSecret(ctx, caClt.Clientset, globalOpts.principalNamespace, config.SecretNamePrincipalCA)
	if err != nil {
		if errors.IsNotFound(err) {
			cmdutil.Fatal("CA is not initialized.")
		} else {
			cmdutil.Fatal("Error getting CA certificate: %v", err)
		}
	}

	// Get signer certificate
	signerCert, err := x509.ParseCertificate(caCert.Certificate[0])
	if err != nil {
		cmdutil.Fatal("Could not parse CA certificate: %v", err)
	}

	// Generate a new TLS keypair from the cert and private key
	cert, key, err := issue(signerCert, caCert.PrivateKey)
	if err != nil {
		cmdutil.Fatal("Error generating certificate: %v", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      outName,
			Namespace: outNamespace,
		},
		Type: "kubernetes.io/tls",
		Data: map[string][]byte{
			"tls.crt": []byte(cert),
			"tls.key": []byte(key),
		},
	}

	_, err = outClt.Clientset.CoreV1().Secrets(outNamespace).Create(ctx, secret, v1.CreateOptions{})
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			cmdutil.Fatal("Could not create secret: %v", err)
		} else if !upsert {
			cmdutil.Fatal("Certificate exists, please use --upsert to reissue.")
		} else {
			_, err = outClt.Clientset.CoreV1().Secrets(outNamespace).Update(ctx, secret, v1.UpdateOptions{})
			if err != nil {
				cmdutil.Fatal("Could not update secret: %v", err)
			} else {
				fmt.Printf("Secret %s/%s updated.\n", outNamespace, outName)
			}
		}
	} else {
		fmt.Printf("Secret %s/%s created\n", outNamespace, outName)
	}

}

func readAndSummarizeCertificate(ctx context.Context, clt *kube.KubernetesClient, namespace, name string, isCA bool) (certificateSummary, error) {
	cert, err := tlsutil.TLSCertFromSecret(ctx, clt.Clientset, namespace, name)
	if err != nil {
		return certificateSummary{}, err
	}
	parsedCert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return certificateSummary{}, fmt.Errorf("could not parse certificate: %v", err)
	}

	warnings := []string{}
	if !parsedCert.IsCA && isCA {
		warnings = append(warnings, "This is not a CA certificate")
	}
	if !parsedCert.BasicConstraintsValid {
		warnings = append(warnings, "Basic constraints are invalid")
	}
	ips := []string{}
	for _, ip := range parsedCert.IPAddresses {
		ips = append(ips, ip.String())
	}
	sum := certificateSummary{
		Subject:   parsedCert.Subject.String(),
		KeyType:   "RSA",
		KeyLength: parsedCert.PublicKey.(*rsa.PublicKey).N.BitLen(),
		NotBefore: parsedCert.NotBefore.Format(time.RFC1123Z),
		NotAfter:  parsedCert.NotAfter.Format(time.RFC1123Z),
		Checksum:  fmt.Sprintf("%x", sha256.Sum256(parsedCert.Raw)),
		IPs:       ips,
		DNS:       parsedCert.DNSNames,
		Warnings:  warnings,
	}

	return sum, nil
}
