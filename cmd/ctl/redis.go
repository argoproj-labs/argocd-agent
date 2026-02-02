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
	"crypto/x509"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

const (
	redisTLSCertPath     = "/app/tls"
	argoCDRedisTLSCAPath = "/app/config/redis-tls"
)

func NewRedisCommand() *cobra.Command {
	command := &cobra.Command{
		Short: "Manage Redis TLS configuration",
		Long: `Configure TLS encryption for Redis connections.

Commands help you generate certificates and configure Redis deployments for TLS-only mode.
For production, consider using external certificate management with --cert-from-secret flags.`,
		Use: "redis",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
		GroupID: "config",
	}
	command.AddCommand(NewRedisGenerateCertsCommand())
	command.AddCommand(NewRedisConfigureTLSCommand())
	command.AddCommand(NewRedisConfigureArgoCDCommand())
	return command
}

func NewRedisGenerateCertsCommand() *cobra.Command {
	var (
		ips            []string
		dns            []string
		certFromSecret string
		caFromSecret   string
		upsert         bool
		secretName     string
	)
	command := &cobra.Command{
		Short: "Generate TLS certificates for Redis",
		Long: `Generate TLS certificates for Redis, signed by the agent CA.

Creates a TLS certificate and stores it in a Kubernetes secret.
For production, use --cert-from-secret and --ca-from-secret for external certificates.`,
		Use: "generate-certs",
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.Background()

			// Validate that both --cert-from-secret and --ca-from-secret are provided together
			if (certFromSecret != "" && caFromSecret == "") || (certFromSecret == "" && caFromSecret != "") {
				cmdutil.Fatal("Both --cert-from-secret and --ca-from-secret must be provided together")
			}

			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("Could not create Kubernetes client: %v", err)
			}

			// If user didn't provide custom DNS names, use defaults with actual namespace
			if !cmd.Flags().Changed("dns") {
				dns = []string{
					"localhost",
					"argocd-redis",
					fmt.Sprintf("argocd-redis.%s.svc.cluster.local", globalOpts.principalNamespace),
				}
			}

			var certData, keyData, caData string

			if certFromSecret != "" && caFromSecret != "" {
				// Use external certificates from existing secrets
				certData, keyData, caData, err = readTLSFromExistingSecrets(ctx, clt, certFromSecret, caFromSecret)
				if err != nil {
					cmdutil.Fatal("%v", err)
				}
			} else {
				// Generate certificate using agent CA
				tlsCert, err := tlsutil.TLSCertFromSecret(ctx, clt.Clientset, globalOpts.principalNamespace, config.SecretNamePrincipalCA)
				if err != nil {
					cmdutil.Fatal("Could not read agent CA secret: %v\nHint: Run 'argocd-agentctl pki init' first", err)
				}

				signerCert, err := x509.ParseCertificate(tlsCert.Certificate[0])
				if err != nil {
					cmdutil.Fatal("Could not parse CA certificate: %v", err)
				}

				certData, keyData, err = tlsutil.GenerateServerCertificate("argocd-redis", signerCert, tlsCert.PrivateKey, ips, dns)
				if err != nil {
					cmdutil.Fatal("Could not generate Redis certificate: %v", err)
				}

				caData, err = tlsutil.CertDataToPEM(tlsCert.Certificate[0])
				if err != nil {
					cmdutil.Fatal("Could not encode CA cert to PEM: %v", err)
				}
			}

			// Create secret with TLS data
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: globalOpts.principalNamespace,
				},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{
					"tls.crt": []byte(certData),
					"tls.key": []byte(keyData),
					"ca.crt":  []byte(caData),
				},
			}

			// Create or update secret
			_, err = clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Create(ctx, secret, metav1.CreateOptions{})
			if errors.IsAlreadyExists(err) {
				if !upsert {
					cmdutil.Fatal("Secret %s already exists. Use --upsert to update it.", secretName)
				}
				_, err = clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Update(ctx, secret, metav1.UpdateOptions{})
			}

			if err != nil {
				cmdutil.Fatal("Could not create/update secret: %v", err)
			}

			fmt.Printf("Secret %s created in namespace %s\n", secretName, globalOpts.principalNamespace)
			fmt.Printf("\nNext: argocd-agentctl redis configure-tls --principal-context %s --principal-namespace %s\n", globalOpts.principalContext, globalOpts.principalNamespace)
		},
	}
	command.Flags().StringSliceVar(&ips, "ip", []string{"127.0.0.1"}, "IP addresses for the certificate's SAN")
	command.Flags().StringSliceVar(&dns, "dns", []string{}, "DNS names for the certificate's SAN (default: localhost, argocd-redis, argocd-redis.<principal-namespace>.svc.cluster.local)")
	command.Flags().StringVar(&certFromSecret, "cert-from-secret", "", "Use TLS certificate from existing secret (format: [namespace/]name)")
	command.Flags().StringVar(&caFromSecret, "ca-from-secret", "", "Use CA certificate from existing secret (format: [namespace/]name)")
	command.Flags().BoolVarP(&upsert, "upsert", "u", false, "Update secret if it already exists")
	command.Flags().StringVar(&secretName, "secret-name", "argocd-redis-tls", "Name of the Kubernetes secret to create/update")
	return command
}

func NewRedisConfigureTLSCommand() *cobra.Command {
	var (
		redisDeployment string
		skipScaleDown   bool
		secretName      string
	)
	command := &cobra.Command{
		Short: "Configure Redis deployment for TLS",
		Long: `Configure a Redis deployment to use TLS-only mode.

Scales down Argo CD components, adds TLS configuration to Redis, and waits for rollout.
Prerequisite: Redis TLS secret must exist (run 'redis generate-certs' first).`,
		Use: "configure-tls",
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.Background()

			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("Could not create Kubernetes client: %v", err)
			}

			// Verify TLS secret exists
			if _, err = clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Get(ctx, secretName, metav1.GetOptions{}); err != nil {
				cmdutil.Fatal("Redis TLS secret '%s' not found. Run 'argocd-agentctl redis generate-certs' first: %v", secretName, err)
			}

			// Scale down Argo CD components (prevents connection errors during transition)
			if !skipScaleDown {
				scaleDownComponent(ctx, clt, "deployment", "argocd-repo-server")
				scaleDownComponent(ctx, clt, "statefulset", "argocd-application-controller")
				scaleDownComponent(ctx, clt, "deployment", "argocd-server")
			}

			// Configure Redis for TLS
			err = configureRedisDeploymentForTLS(ctx, clt, redisDeployment, secretName)
			if err != nil {
				cmdutil.Fatal("Failed to configure Redis: %v", err)
			}

			// Wait for rollout
			err = waitForDeploymentRollout(ctx, clt, redisDeployment, globalOpts.principalNamespace)
			if err != nil {
				cmdutil.Fatal("Rollout failed: %v", err)
			}

			fmt.Println("\nRedis TLS configured!")
			fmt.Println("\nNext: Configure Argo CD, then scale up components")
			fmt.Printf("  argocd-agentctl redis configure-argocd --principal-context %s --principal-namespace %s\n", globalOpts.principalContext, globalOpts.principalNamespace)
			fmt.Printf("  kubectl scale deployment argocd-repo-server,argocd-server -n %s --replicas=1 --context %s\n", globalOpts.principalNamespace, globalOpts.principalContext)
			fmt.Printf("  kubectl scale statefulset argocd-application-controller -n %s --replicas=1 --context %s\n", globalOpts.principalNamespace, globalOpts.principalContext)
		},
	}
	command.Flags().StringVar(&redisDeployment, "redis-deployment", "argocd-redis", "Name of the Redis deployment")
	command.Flags().BoolVar(&skipScaleDown, "skip-scale-down", false, "Skip scaling down Argo CD components (not recommended)")
	command.Flags().StringVar(&secretName, "secret-name", "argocd-redis-tls", "Name of the Kubernetes secret containing the Redis TLS certificate")
	return command
}

func NewRedisConfigureArgoCDCommand() *cobra.Command {
	var skipRepoServer bool
	var skipAppController bool
	var skipServer bool
	var secretName string

	command := &cobra.Command{
		Short: "Configure Argo CD components to connect to Redis with TLS",
		Long: `Configure Argo CD components to connect to Redis using TLS.

Adds TLS volume, volumeMount, and required args to server, repo-server, and application-controller.
Prerequisite: Redis TLS secret must exist.`,
		Use: "configure-argocd",
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.Background()

			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("Could not create Kubernetes client: %v", err)
			}

			// Verify TLS secret exists
			if _, err = clt.Clientset.CoreV1().Secrets(globalOpts.principalNamespace).Get(ctx, secretName, metav1.GetOptions{}); err != nil {
				cmdutil.Fatal("Redis TLS secret '%s' not found. Run 'argocd-agentctl redis generate-certs' first: %v", secretName, err)
			}

			// Configure argocd-server
			if !skipServer {
				if err := configureArgoCDDeploymentForRedisTLS(ctx, clt, "argocd-server", secretName); err != nil {
					if !errors.IsNotFound(err) {
						cmdutil.Fatal("Failed to configure argocd-server: %v", err)
					}
				}
			}

			// Configure argocd-repo-server
			if !skipRepoServer {
				if err := configureArgoCDDeploymentForRedisTLS(ctx, clt, "argocd-repo-server", secretName); err != nil {
					if !errors.IsNotFound(err) {
						cmdutil.Fatal("Failed to configure argocd-repo-server: %v", err)
					}
				}
			}

			// Configure argocd-application-controller (StatefulSet)
			if !skipAppController {
				if err := configureArgoCDStatefulSetForRedisTLS(ctx, clt, "argocd-application-controller", secretName); err != nil {
					if !errors.IsNotFound(err) {
						cmdutil.Fatal("Failed to configure argocd-application-controller: %v", err)
					}
				}
			}

			fmt.Println("\n Argo CD components configured for Redis TLS")
		},
	}
	command.Flags().BoolVar(&skipRepoServer, "skip-repo-server", false, "Skip configuring argocd-repo-server")
	command.Flags().BoolVar(&skipAppController, "skip-application-controller", false, "Skip configuring argocd-application-controller")
	command.Flags().BoolVar(&skipServer, "skip-server", false, "Skip configuring argocd-server")
	command.Flags().StringVar(&secretName, "secret-name", "argocd-redis-tls", "Name of the Kubernetes secret containing the Redis TLS CA certificate")
	return command
}

func scaleDownComponent(ctx context.Context, clt *kube.KubernetesClient, kind, name string) {
	patch := []byte(`{"spec":{"replicas":0}}`)
	var err error

	switch kind {
	case "deployment":
		_, err = clt.Clientset.AppsV1().Deployments(globalOpts.principalNamespace).Patch(
			ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	case "statefulset":
		_, err = clt.Clientset.AppsV1().StatefulSets(globalOpts.principalNamespace).Patch(
			ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	}

	if err != nil && !errors.IsNotFound(err) {
		fmt.Printf("Warning: Could not scale down %s/%s: %v\n", kind, name, err)
	}
}

func configureRedisDeploymentForTLS(ctx context.Context, clt *kube.KubernetesClient, deploymentName, secretName string) error {
	// Use retry logic to handle resource conflicts
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch the latest version of the deployment
		deployment, err := clt.Clientset.AppsV1().Deployments(globalOpts.principalNamespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if len(deployment.Spec.Template.Spec.Containers) == 0 {
			return fmt.Errorf("deployment has no containers")
		}

		// Add TLS volume
		volumeExists := false
		for _, vol := range deployment.Spec.Template.Spec.Volumes {
			if vol.Name == "redis-tls" {
				volumeExists = true
				break
			}
		}
		if !volumeExists {
			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
				Name: "redis-tls",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: secretName,
					},
				},
			})
		}

		// Add TLS volume mount
		container := &deployment.Spec.Template.Spec.Containers[0]
		mountExists := false
		for _, mount := range container.VolumeMounts {
			if mount.Name == "redis-tls" {
				mountExists = true
				break
			}
		}
		if !mountExists {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      "redis-tls",
				MountPath: redisTLSCertPath,
				ReadOnly:  true,
			})
		}

		// Add REDIS_PASSWORD env var if not already present
		envExists := false
		for _, env := range container.Env {
			if env.Name == "REDIS_PASSWORD" {
				envExists = true
				break
			}
		}
		if !envExists {
			container.Env = append(container.Env, corev1.EnvVar{
				Name: "REDIS_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "argocd-redis"},
						Key:                  "auth",
					},
				},
			})
		}

		container.Command = []string{"/bin/sh", "-c"}
		container.Args = []string{
			"exec redis-server " +
				"--appendonly no " +
				"--requirepass \"$REDIS_PASSWORD\" " +
				"--tls-port 6379 " +
				"--port 0 " +
				"--tls-cert-file " + redisTLSCertPath + "/tls.crt " +
				"--tls-key-file " + redisTLSCertPath + "/tls.key " +
				"--tls-ca-cert-file " + redisTLSCertPath + "/ca.crt " +
				"--tls-auth-clients no",
		}

		// Attempt to update the deployment
		_, err = clt.Clientset.AppsV1().Deployments(globalOpts.principalNamespace).Update(ctx, deployment, metav1.UpdateOptions{})
		return err
	})

	if err != nil {
		return fmt.Errorf("could not update deployment: %w", err)
	}

	return nil
}

func waitForDeploymentRollout(ctx context.Context, clt *kube.KubernetesClient, deploymentName, namespace string) error {
	timeout := 120 * time.Second
	pollInterval := 2 * time.Second
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		deployment, err := clt.Clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get deployment: %w", err)
		}

		// Check if deployment is ready
		if deployment.Status.Replicas > 0 &&
			deployment.Status.Replicas == deployment.Status.AvailableReplicas &&
			deployment.Status.UpdatedReplicas == deployment.Status.Replicas {
			return nil
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for rollout (check: kubectl rollout status deployment/%s -n %s)", deploymentName, namespace)
}

func addVolumeIfNotExists(volumes *[]corev1.Volume, volume corev1.Volume) bool {
	for _, v := range *volumes {
		if v.Name == volume.Name {
			return false
		}
	}
	*volumes = append(*volumes, volume)
	return true
}

func addVolumeMountIfNotExists(mounts *[]corev1.VolumeMount, mount corev1.VolumeMount) bool {
	for _, m := range *mounts {
		if m.Name == mount.Name {
			return false
		}
	}
	*mounts = append(*mounts, mount)
	return true
}

func addArgsIfNotExists(args *[]string, searchArg string, newArgs ...string) bool {
	for _, arg := range *args {
		if arg == searchArg {
			return false
		}
	}
	*args = append(*args, newArgs...)
	return true
}

func configureArgoCDDeploymentForRedisTLS(ctx context.Context, clt *kube.KubernetesClient, deploymentName, secretName string) error {
	// Use retry logic to handle resource conflicts
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch the latest version of the deployment
		deployment, err := clt.Clientset.AppsV1().Deployments(globalOpts.principalNamespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if len(deployment.Spec.Template.Spec.Containers) == 0 {
			return fmt.Errorf("deployment has no containers")
		}

		modified := false
		container := &deployment.Spec.Template.Spec.Containers[0]

		// Add redis-tls-ca volume
		if addVolumeIfNotExists(&deployment.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: "redis-tls-ca",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
					Items:      []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}},
				},
			},
		}) {
			modified = true
		}

		// Add redis-tls-ca volumeMount
		if addVolumeMountIfNotExists(&container.VolumeMounts, corev1.VolumeMount{
			Name:      "redis-tls-ca",
			MountPath: argoCDRedisTLSCAPath,
			ReadOnly:  true,
		}) {
			modified = true
		}

		// Add Redis TLS args
		if addArgsIfNotExists(&container.Args, "--redis-use-tls",
			"--redis-use-tls",
			fmt.Sprintf("--redis-ca-certificate=%s/ca.crt", argoCDRedisTLSCAPath),
		) {
			modified = true
		}

		if !modified {
			// Already configured, no update needed
			return nil
		}

		// Attempt to update the deployment
		_, err = clt.Clientset.AppsV1().Deployments(globalOpts.principalNamespace).Update(ctx, deployment, metav1.UpdateOptions{})
		return err
	})

	if err != nil {
		return fmt.Errorf("could not update deployment: %w", err)
	}

	fmt.Printf("Configured %s for Redis TLS\n", deploymentName)
	return nil
}

func configureArgoCDStatefulSetForRedisTLS(ctx context.Context, clt *kube.KubernetesClient, statefulSetName, secretName string) error {
	// Use retry logic to handle resource conflicts
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch the latest version of the statefulset
		statefulSet, err := clt.Clientset.AppsV1().StatefulSets(globalOpts.principalNamespace).Get(ctx, statefulSetName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if len(statefulSet.Spec.Template.Spec.Containers) == 0 {
			return fmt.Errorf("statefulset has no containers")
		}

		modified := false
		container := &statefulSet.Spec.Template.Spec.Containers[0]

		// Add redis-tls-ca volume
		if addVolumeIfNotExists(&statefulSet.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: "redis-tls-ca",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
					Items:      []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}},
				},
			},
		}) {
			modified = true
		}

		// Add redis-tls-ca volumeMount
		if addVolumeMountIfNotExists(&container.VolumeMounts, corev1.VolumeMount{
			Name:      "redis-tls-ca",
			MountPath: argoCDRedisTLSCAPath,
			ReadOnly:  true,
		}) {
			modified = true
		}

		// Add Redis TLS args
		if addArgsIfNotExists(&container.Args, "--redis-use-tls",
			"--redis-use-tls",
			fmt.Sprintf("--redis-ca-certificate=%s/ca.crt", argoCDRedisTLSCAPath),
		) {
			modified = true
		}

		if !modified {
			// Already configured, no update needed
			return nil
		}

		// Attempt to update the statefulset
		_, err = clt.Clientset.AppsV1().StatefulSets(globalOpts.principalNamespace).Update(ctx, statefulSet, metav1.UpdateOptions{})
		return err
	})

	if err != nil {
		return fmt.Errorf("could not update statefulset: %w", err)
	}

	fmt.Printf("Configured %s for Redis TLS\n", statefulSetName)
	return nil
}
