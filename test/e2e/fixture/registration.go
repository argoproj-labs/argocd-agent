// Copyright 2026 The argocd-agent Authors
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

package fixture

import (
	"context"
	"fmt"
	"os"

	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const (
	// SharedClientCertSecretName is the name of the shared client certificate secret for self-registration
	SharedClientCertSecretName = "argocd-agent-shared-client-cert"
)

func EnableSelfAgentRegistration(ctx context.Context, principalClient, agentClient KubeClient) error {
	// Create the shared client cert secret for self-registration
	if err := CreateSharedClientCertSecret(ctx, principalClient, agentClient); err != nil {
		return fmt.Errorf("failed to create shared client cert secret: %v", err)
	}

	// Set both the enable flag and the client cert secret name environment variables
	envVars := fmt.Sprintf("ARGOCD_PRINCIPAL_ENABLE_SELF_CLUSTER_REGISTRATION=true\nARGOCD_PRINCIPAL_SELF_REGISTRATION_CLIENT_CERT_SECRET=%s\n", SharedClientCertSecretName)

	return os.WriteFile(EnvVariablesFromE2EFile, []byte(envVars), 0644)
}

func DisableSelfAgentRegistration(ctx context.Context, client KubeClient) error {
	// Clean up the shared client cert secret
	_ = DeleteSharedClientCertSecret(ctx, client)

	return os.Remove(EnvVariablesFromE2EFile)
}

// ClusterSecretExists checks if a cluster secret exists for the given agent.
func ClusterSecretExists(ctx context.Context, client KubeClient, agentName string) bool {
	kubeclient, err := kubernetes.NewForConfig(client.Config)
	if err != nil {
		return false
	}
	secret, err := cluster.GetClusterSecret(ctx, kubeclient, "argocd", agentName)
	if err != nil {
		return false
	}
	return secret != nil
}

// GetClusterSecret retrieves the cluster secret for the given agent.
func GetClusterSecret(ctx context.Context, client KubeClient, agentName string) (*corev1.Secret, error) {
	secretName := "cluster-" + agentName
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Name: secretName, Namespace: "argocd"}
	err := client.Get(ctx, secretKey, secret, metav1.GetOptions{})
	return secret, err
}

// DeleteClusterSecret deletes the cluster secret for the given agent.
func DeleteClusterSecret(ctx context.Context, client KubeClient, agentName string) error {
	secretName := "cluster-" + agentName
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: "argocd",
		},
	}
	return client.Delete(ctx, secret, metav1.DeleteOptions{})
}

// CreateSharedClientCertSecret creates a shared client certificate secret for self-registration.
// For the test it copies the existing agent client certificate from the managed agent cluster and adds the CA cert.
func CreateSharedClientCertSecret(ctx context.Context, principalClient, agentClient KubeClient) error {
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Name: SharedClientCertSecretName, Namespace: "argocd"}
	if err := principalClient.Get(ctx, secretKey, secret, metav1.GetOptions{}); err == nil {
		// Secret already exists, no need to create
		return nil
	}

	// Get the existing agent client certificate from the managed agent cluster that was created during E2E setup
	agentClientCert := &corev1.Secret{}
	agentClientCertKey := types.NamespacedName{Name: config.SecretNameAgentClientCert, Namespace: "argocd"}
	if err := agentClient.Get(ctx, agentClientCertKey, agentClientCert, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("failed to get agent client certificate: %v", err)
	}

	tlsCert, ok := agentClientCert.Data["tls.crt"]
	if !ok {
		return fmt.Errorf("agent client cert secret missing tls.crt")
	}

	tlsKey, ok := agentClientCert.Data["tls.key"]
	if !ok {
		return fmt.Errorf("agent client cert secret missing tls.key")
	}

	// Get the CA certificate from the principal's CA secret
	caSecret := &corev1.Secret{}
	caSecretKey := types.NamespacedName{Name: config.SecretNamePrincipalCA, Namespace: "argocd"}
	if err := principalClient.Get(ctx, caSecretKey, caSecret, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("failed to get CA certificate secret: %v", err)
	}

	caCert, ok := caSecret.Data["tls.crt"]
	if !ok {
		return fmt.Errorf("CA secret missing tls.crt")
	}

	// Create the shared client cert secret with tls.crt, tls.key, and ca.crt
	sharedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SharedClientCertSecretName,
			Namespace: "argocd",
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": tlsCert,
			"tls.key": tlsKey,
			"ca.crt":  caCert,
		},
	}

	if err := principalClient.Create(ctx, sharedSecret, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create shared client cert secret: %v", err)
	}

	return nil
}

// DeleteSharedClientCertSecret deletes the shared client certificate secret.
func DeleteSharedClientCertSecret(ctx context.Context, client KubeClient) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SharedClientCertSecretName,
			Namespace: "argocd",
		},
	}
	err := client.Delete(ctx, secret, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}
