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

package principal

import (
	"context"
	"crypto/x509"
	"math/big"
	"os"
	"path"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	fakecerts "github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
)

var certTempl = x509.Certificate{
	SerialNumber:          big.NewInt(1),
	KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	BasicConstraintsValid: true,
	NotBefore:             time.Now().Add(-1 * time.Hour),
	NotAfter:              time.Now().Add(1 * time.Hour),
}

var testNamespace = "default"

func Test_ServerWithTLSConfig(t *testing.T) {
	tempDir := t.TempDir()
	t.Run("Valid TLS key pair", func(t *testing.T) {
		templ := certTempl
		fakecerts.WriteSelfSignedCert(t, "rsa", path.Join(tempDir, "test-cert"), templ)
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
			WithTLSKeyPairFromPath(path.Join(tempDir, "test-cert.crt"), path.Join(tempDir, "test-cert.key")),
			WithGeneratedTokenSigningKey(),
		)
		require.NoError(t, err)
		tlsConfig, err := s.loadTLSConfig()
		assert.NoError(t, err)
		assert.NotNil(t, tlsConfig)
	})
	t.Run("Non-existing TLS key pair", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
			WithTLSKeyPairFromPath(path.Join(tempDir, "other-cert.crt"), path.Join(tempDir, "other-cert.key")),
			WithGeneratedTokenSigningKey(),
		)
		require.NoError(t, err)
		tlsConfig, err := s.loadTLSConfig()
		assert.ErrorIs(t, err, os.ErrNotExist)
		assert.Nil(t, tlsConfig)
	})

	t.Run("Invalid TLS certificate", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
			WithTLSKeyPairFromPath("server_test.go", "server_test.go"),
			WithGeneratedTokenSigningKey(),
		)
		require.NoError(t, err)
		require.NotNil(t, s)
		tlsConfig, err := s.loadTLSConfig()
		assert.ErrorContains(t, err, "failed to find any PEM data")
		assert.Nil(t, tlsConfig)
	})
}

func Test_NewServer(t *testing.T) {
	t.Run("Instantiate new server object with non-default options", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace, WithListenerAddress("0.0.0.0"), WithGeneratedTokenSigningKey())
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.NotEqual(t, defaultOptions(), s.options)
		assert.Equal(t, "0.0.0.0", s.options.address)
	})

	t.Run("Instantiate new server object with invalid option", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace, WithListenerPort(-1), WithGeneratedTokenSigningKey())
		assert.Error(t, err)
		assert.Nil(t, s)
	})

	t.Run("Informer sync timeout should be configurable", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace, WithGeneratedTokenSigningKey(), WithInformerSyncTimeout(10*time.Second))
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.Equal(t, 10*time.Second, s.options.informerSyncTimeout)
	})

	t.Run("Informer sync timeout should default to 60s when not set", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace, WithGeneratedTokenSigningKey())
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.Equal(t, 60*time.Second, s.options.informerSyncTimeout)
	})
}

func Test_handleResyncOnConnect(t *testing.T) {
	s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
		WithGeneratedTokenSigningKey(),
	)
	s.kubeClient.RestConfig = &rest.Config{}
	require.NoError(t, err)
	s.events = event.NewEventSource("test")

	agent := types.NewAgent("test", types.AgentModeAutonomous.String())
	err = s.queues.Create(agent.Name())
	assert.Nil(t, err)

	t.Run("return if the agent is already synced", func(t *testing.T) {
		s.resyncStatus.resynced(agent.Name())

		err = s.handleResyncOnConnect(agent)
		assert.Nil(t, err)

		sendQ := s.queues.SendQ(agent.Name())
		assert.Zero(t, sendQ.Len())
	})

	t.Run("request SyncedResourceList in autonomous mode", func(t *testing.T) {
		s.resyncStatus = newResyncStatus()

		err = s.handleResyncOnConnect(agent)
		assert.Nil(t, err)

		sendQ := s.queues.SendQ(agent.Name())
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		assert.False(t, shutdown)
		assert.Equal(t, event.SyncedResourceList.String(), ev.Type())
	})

	t.Run("request resource resync in managed mode", func(t *testing.T) {
		agent = types.NewAgent("test", types.AgentModeManaged.String())
		s.resyncStatus = newResyncStatus()

		err = s.handleResyncOnConnect(agent)
		assert.Nil(t, err)

		sendQ := s.queues.SendQ(agent.Name())
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		assert.False(t, shutdown)
		assert.Equal(t, event.EventRequestResourceResync.String(), ev.Type())
	})
}

func Test_RunHandlersOnConnect(t *testing.T) {
	s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
		WithGeneratedTokenSigningKey(),
	)
	require.NoError(t, err)
	s.events = event.NewEventSource("test")

	expected := types.NewAgent("test", types.AgentModeAutonomous.String())

	output := make(chan types.Agent)

	s.handlersOnConnect = []handlersOnConnect{func(agent types.Agent) error {
		output <- types.NewAgent(agent.Name(), agent.Mode())
		return nil
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go s.RunHandlersOnConnect(ctx)

	s.notifyOnConnect <- expected

	got := <-output
	assert.Equal(t, expected, got)
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
