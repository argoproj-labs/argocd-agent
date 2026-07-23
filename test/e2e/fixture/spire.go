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
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	fakespire "github.com/argoproj-labs/argocd-agent/test/fake/spire"
	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	SpirePrincipalSocket       = "/tmp/fake-spire-principal.sock"
	SpireManagedAgentSocket    = "/tmp/fake-spire-agent-managed.sock"
	SpireAutonomousAgentSocket = "/tmp/fake-spire-agent-autonomous.sock"

	spirePrincipalID       = "spiffe://e2e.test/principal"
	spireManagedAgentID    = "spiffe://e2e.test/agent-managed"
	spireAutonomousAgentID = "spiffe://e2e.test/agent-autonomous"
)

// StaticTLSSecretBackups holds copies of static TLS secrets removed during
// SPIRE e2e tests so they can be restored afterward.
type StaticTLSSecretBackups struct {
	PrincipalTLS          *corev1.Secret
	ManagedAgentClient    *corev1.Secret
	ManagedAgentCA        *corev1.Secret
	AutonomousAgentClient *corev1.Secret
	AutonomousAgentCA     *corev1.Secret
}

type spireSecretSpec struct {
	client    KubeClient
	name      string
	namespace string
	backup    **corev1.Secret
}

// FakeSpireEnvironment runs fake SPIRE Workload API servers for SPIRE e2e tests.
type FakeSpireEnvironment struct {
	Principal       *fakespire.Server
	ManagedAgent    *fakespire.Server
	AutonomousAgent *fakespire.Server
}

// BackupAndDeleteStaticTLSSecrets copies the static TLS secrets that SPIRE
// replaces for SPIRE e2e tests.
func BackupAndDeleteStaticTLSSecrets(ctx context.Context, principalClient, managedAgentClient, autonomousAgentClient KubeClient) (*StaticTLSSecretBackups, error) {
	backups := &StaticTLSSecretBackups{}
	specs := []spireSecretSpec{
		{principalClient, config.SecretNamePrincipalTLS, PrincipalNamespace, &backups.PrincipalTLS},
		{managedAgentClient, config.SecretNameAgentClientCert, ManagedAgentNamespace, &backups.ManagedAgentClient},
		{managedAgentClient, config.SecretNameAgentCA, ManagedAgentNamespace, &backups.ManagedAgentCA},
		{autonomousAgentClient, config.SecretNameAgentClientCert, AutonomousAgentNamespace, &backups.AutonomousAgentClient},
		{autonomousAgentClient, config.SecretNameAgentCA, AutonomousAgentNamespace, &backups.AutonomousAgentCA},
	}

	for _, spec := range specs {
		secret, err := backupSecret(ctx, spec.client, spec.name, spec.namespace)
		if err != nil {
			return nil, fmt.Errorf("backup secret %s/%s: %w", spec.namespace, spec.name, err)
		}
		*spec.backup = secret
	}

	for _, spec := range specs {
		if err := deleteSecret(ctx, spec.client, spec.name, spec.namespace); err != nil {
			return nil, fmt.Errorf("delete secret %s/%s: %w", spec.namespace, spec.name, err)
		}
	}

	return backups, nil
}

// RestoreStaticTLSSecrets restores static TLS secrets after SPIRE e2e tests.
func RestoreStaticTLSSecrets(ctx context.Context, principalClient, managedAgentClient, autonomousAgentClient KubeClient, backups *StaticTLSSecretBackups) error {
	if backups == nil {
		return nil
	}

	restores := []struct {
		client KubeClient
		secret *corev1.Secret
	}{
		{principalClient, backups.PrincipalTLS},
		{managedAgentClient, backups.ManagedAgentClient},
		{managedAgentClient, backups.ManagedAgentCA},
		{autonomousAgentClient, backups.AutonomousAgentClient},
		{autonomousAgentClient, backups.AutonomousAgentCA},
	}

	for _, item := range restores {
		if err := restoreSecret(ctx, item.client, item.secret); err != nil {
			return err
		}
	}

	return nil
}

// NewFakeSpireEnvironment creates fake SPIRE servers for the principal and both
// agents, all sharing the same trust root.
func NewFakeSpireEnvironment() (*FakeSpireEnvironment, error) {
	principal, err := fakespire.New(spirePrincipalID, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create principal fake SPIRE server: %w", err)
	}

	caCert := principal.CACert()
	caKey := principal.CAKey()

	managed, err := fakespire.New(spireManagedAgentID, caCert, caKey)
	if err != nil {
		return nil, fmt.Errorf("create managed agent fake SPIRE server: %w", err)
	}

	autonomous, err := fakespire.New(spireAutonomousAgentID, caCert, caKey)
	if err != nil {
		return nil, fmt.Errorf("create autonomous agent fake SPIRE server: %w", err)
	}

	return &FakeSpireEnvironment{
		Principal:       principal,
		ManagedAgent:    managed,
		AutonomousAgent: autonomous,
	}, nil
}

// Start listens on all fake SPIRE sockets synchronously, then serves in the
// background. It returns when all sockets are ready to accept connections.
func (env *FakeSpireEnvironment) Start() error {
	servers := []struct {
		server *fakespire.Server
		socket string
	}{
		{env.Principal, SpirePrincipalSocket},
		{env.ManagedAgent, SpireManagedAgentSocket},
		{env.AutonomousAgent, SpireAutonomousAgentSocket},
	}

	for _, s := range servers {
		if err := s.server.Listen(s.socket); err != nil {
			return fmt.Errorf("listen on %s: %w", s.socket, err)
		}
	}

	for _, s := range servers {
		go serveFakeSpire(s.server)
	}
	return nil
}

// Stop stops all fake SPIRE servers and removes their socket files.
func (env *FakeSpireEnvironment) Stop() {
	stopFakeSpire(env.Principal)
	stopFakeSpire(env.ManagedAgent)
	stopFakeSpire(env.AutonomousAgent)

	for _, path := range []string{
		SpirePrincipalSocket,
		SpireManagedAgentSocket,
		SpireAutonomousAgentSocket,
	} {
		_ = os.Remove(path)
	}
}

// WriteEnvVars writes SPIRE socket paths for e2e goreman processes.
func (env *FakeSpireEnvironment) WriteEnvVars() error {
	return WriteEnvVarsToFile(map[string]string{
		"ARGOCD_PRINCIPAL_SPIRE_AGENT_SOCKET":        "unix://" + SpirePrincipalSocket,
		"ARGOCD_AGENT_SPIRE_AGENT_SOCKET":            "unix://" + SpireManagedAgentSocket,
		"ARGOCD_AUTONOMOUS_AGENT_SPIRE_AGENT_SOCKET": "unix://" + SpireAutonomousAgentSocket,
	})
}

// VerifyAgentConnected waits until the given agent reports a successful connection to the principal.
func VerifyAgentConnected(t testing.TB, agentName string, clusterDetails *ClusterDetails, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		return HasConnectionStatus(agentName, argoapp.ConnectionState{
			Status:  argoapp.ConnectionStatusSuccessful,
			Message: fmt.Sprintf("Agent: '%s' is %s with principal", agentName, "connected"),
		}, clusterDetails)
	}, 60*time.Second, 1*time.Second, msgAndArgs...)
}

func serveFakeSpire(server *fakespire.Server) {
	if err := server.Serve(); err != nil {
		fmt.Printf("fake SPIRE server stopped: %v\n", err)
	}
}

func stopFakeSpire(server *fakespire.Server) {
	if server != nil {
		server.Stop()
	}
}

func backupSecret(ctx context.Context, client KubeClient, name, namespace string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: name, Namespace: namespace}
	if err := client.Get(ctx, key, secret, metav1.GetOptions{}); err != nil {
		return nil, err
	}
	return secret.DeepCopy(), nil
}

func deleteSecret(ctx context.Context, client KubeClient, name, namespace string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	err := client.Delete(ctx, secret, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

func restoreSecret(ctx context.Context, client KubeClient, backup *corev1.Secret) error {
	if backup == nil {
		return nil
	}

	newSecret := backup.DeepCopy()
	newSecret.ResourceVersion = ""
	newSecret.UID = ""
	newSecret.ManagedFields = nil

	err := client.Create(ctx, newSecret, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		if err := deleteSecret(ctx, client, backup.Name, backup.Namespace); err != nil {
			return fmt.Errorf("delete existing secret %s/%s before restore: %w", backup.Namespace, backup.Name, err)
		}
		return client.Create(ctx, newSecret, metav1.CreateOptions{})
	}
	return err
}
