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
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
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

func NewJWTCommand() *cobra.Command {
	command := &cobra.Command{
		Short: "Inspect and manage JWT signing keys",
		Long: `The jwt command provides functions to create, inspect and manage JWT signing keys
for argocd-agent principal. JWT signing keys are used by the principal to sign
authentication tokens for agents.`,
		Use: "jwt",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
		GroupID: "config",
	}
	command.AddCommand(NewJWTCreateKeyCommand())
	command.AddCommand(NewJWTInspectKeyCommand())
	command.AddCommand(NewJWTDeleteKeyCommand())
	return command
}

func NewJWTCreateKeyCommand() *cobra.Command {
	var (
		upsert bool
	)
	command := &cobra.Command{
		Short: "Create a JWT signing key and store it in a Kubernetes secret",
		Use:   "create-key",
		Long: `Creates a new RSA private key for JWT signing and stores it in a Kubernetes secret.
The key will be generated with 4096 bits and stored in PKCS#8 PEM format in the
secret specified by the JWT_SECRET_NAME constant.

The secret will be created in the principal's namespace on the principal's context.`,
		Run: func(c *cobra.Command, args []string) {
			ctx := context.TODO()
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("Error creating Kubernetes client: %v", err)
			}

			exists := false
			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Get(ctx, config.SecretNameJWT, v1.GetOptions{})
			if !errors.IsNotFound(err) {
				if err != nil {
					cmdutil.Fatal("Error getting JWT secret: %v", err)
				} else if !upsert {
					cmdutil.Fatal("JWT secret already exists. Use --force to recreate it.")
				}
				exists = true
			}

			fmt.Println("Generating JWT signing key...")

			// Generate 4096-bit RSA private key
			privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
			if err != nil {
				cmdutil.Fatal("Could not generate RSA private key: %v", err)
			}

			// Convert to PKCS#8 PEM format
			keyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
			if err != nil {
				cmdutil.Fatal("Could not marshal private key: %v", err)
			}

			keyPEM := pem.EncodeToMemory(&pem.Block{
				Type:  "PRIVATE KEY",
				Bytes: keyBytes,
			})

			// Create the secret
			secret := &corev1.Secret{
				ObjectMeta: v1.ObjectMeta{
					Name:      config.SecretNameJWT,
					Namespace: globalOpts.principalNamespace,
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"jwt.key": keyPEM,
				},
			}

			if !exists {
				_, err = clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Create(ctx, secret, v1.CreateOptions{})
				if err != nil {
					cmdutil.Fatal("Could not create JWT secret: %v", err)
				}
				fmt.Printf("Success. JWT signing key created and stored in secret %s/%s\n", globalOpts.principalNamespace, config.SecretNameJWT)
			} else {
				_, err = clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Update(ctx, secret, v1.UpdateOptions{})
				if err != nil {
					cmdutil.Fatal("Could not update JWT secret: %v", err)
				}
				fmt.Printf("Success. JWT signing key updated in secret %s/%s\n", globalOpts.principalNamespace, config.SecretNameJWT)
			}
		},
	}

	command.Flags().BoolVarP(&upsert, "upsert", "u", false, "Upsert the JWT key if it exists")
	return command
}

func NewJWTInspectKeyCommand() *cobra.Command {
	var (
		outFormat string
	)

	type summary struct {
		KeyType   string `json:"keyType" yaml:"keyType" text:"Key type"`
		KeyLength int    `json:"keyLength" yaml:"keyLength" text:"Key length"`
		Created   string `json:"created" yaml:"created" text:"Created"`
		Checksum  string `json:"sha256" yaml:"sha256" text:"Key fingerprint"`
	}

	command := &cobra.Command{
		Short: "Inspect the JWT signing key",
		Use:   "inspect-key",
		Long:  `Displays information about the JWT signing key stored in the Kubernetes secret.`,
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.TODO()
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("Error creating Kubernetes client: %v", err)
			}

			key, err := tlsutil.JWTSigningKeyFromSecret(ctx, clt.Clientset, globalOpts.principalNamespace, config.SecretNameJWT)
			if err != nil {
				cmdutil.Fatal("Could not read JWT signing key from secret: %v", err)
			}

			rsaKey, ok := key.(*rsa.PrivateKey)
			if !ok {
				cmdutil.Fatal("JWT signing key is not an RSA private key")
			}

			// Get secret metadata for creation time
			secret, err := clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Get(ctx, config.SecretNameJWT, v1.GetOptions{})
			if err != nil {
				cmdutil.Fatal("Could not read JWT secret metadata: %v", err)
			}

			// Create a checksum of the public key for identification
			pubKeyBytes, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
			if err != nil {
				cmdutil.Fatal("Could not marshal public key: %v", err)
			}
			checksum := sha256.Sum256(pubKeyBytes)

			sum := summary{
				KeyType:   "RSA",
				KeyLength: rsaKey.Size() * 8,
				Created:   secret.CreationTimestamp.Format(time.RFC1123Z),
				Checksum:  fmt.Sprintf("%x", checksum),
			}

			out, err := cmdutil.MarshalStruct(sum, outFormat)
			if err != nil {
				cmdutil.Fatal("Could not marshal summary: %v", err)
			}
			fmt.Print(string(out))
		},
	}

	command.Flags().StringVarP(&outFormat, "output", "o", "json", "Output format (json, yaml or text)")
	return command
}

func NewJWTDeleteKeyCommand() *cobra.Command {
	var (
		force bool
	)
	command := &cobra.Command{
		Short: "Delete the JWT signing key secret",
		Use:   "delete-key",
		Long:  `Deletes the Kubernetes secret containing the JWT signing key.`,
		Run: func(c *cobra.Command, args []string) {
			ctx := context.TODO()
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("Error creating Kubernetes client: %v", err)
			}

			if !force {
				fmt.Print("Are you sure you want to delete the JWT signing key? [y/N]: ")
				var response string
				fmt.Scanln(&response)
				if response != "y" && response != "Y" {
					fmt.Println("Aborted.")
					return
				}
			}

			err = clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Delete(ctx, config.SecretNameJWT, v1.DeleteOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					fmt.Printf("JWT secret %s/%s does not exist.\n", globalOpts.principalNamespace, config.SecretNameJWT)
				} else {
					cmdutil.Fatal("Could not delete JWT secret: %v", err)
				}
			} else {
				fmt.Printf("JWT signing key secret %s/%s deleted successfully.\n", globalOpts.principalNamespace, config.SecretNameJWT)
			}
		},
	}

	command.Flags().BoolVarP(&force, "force", "f", false, "Force deletion without confirmation")
	return command
}
