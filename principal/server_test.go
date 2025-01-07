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

	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	fakecerts "github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClient(), testNamespace,
			WithTLSKeyPairFromPath(path.Join(tempDir, "test-cert.crt"), path.Join(tempDir, "test-cert.key")),
			WithGeneratedTokenSigningKey(),
		)
		require.NoError(t, err)
		tlsConfig, err := s.loadTLSConfig()
		assert.NoError(t, err)
		assert.NotNil(t, tlsConfig)
	})
	t.Run("Non-existing TLS key pair", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClient(), testNamespace,
			WithTLSKeyPairFromPath(path.Join(tempDir, "other-cert.crt"), path.Join(tempDir, "other-cert.key")),
			WithGeneratedTokenSigningKey(),
		)
		require.NoError(t, err)
		tlsConfig, err := s.loadTLSConfig()
		assert.ErrorIs(t, err, os.ErrNotExist)
		assert.Nil(t, tlsConfig)
	})

	t.Run("Invalid TLS certificate", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClient(), testNamespace,
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
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClient(), testNamespace, WithListenerAddress("0.0.0.0"), WithGeneratedTokenSigningKey())
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.NotEqual(t, defaultOptions(), s.options)
		assert.Equal(t, "0.0.0.0", s.options.address)
	})

	t.Run("Instantiate new server object with invalid option", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClient(), testNamespace, WithListenerPort(-1), WithGeneratedTokenSigningKey())
		assert.Error(t, err)
		assert.Nil(t, s)
	})
}

func TestRemoveQueueIfUnused(t *testing.T) {
	t.Run("should remove the queue if the namespace is not found", func(t *testing.T) {
		ctx := context.Background()
		fakeClient := kube.NewKubernetesFakeClient()

		s, err := NewServer(context.TODO(), fakeClient, testNamespace, WithGeneratedTokenSigningKey())
		assert.Nil(t, err)

		err = s.queues.Create(testNamespace)
		assert.Nil(t, err)

		s.removeQueueIfUnused(ctx, testNamespace)
		assert.False(t, s.queues.HasQueuePair(testNamespace))
	})

	t.Run("shouldn't remove the queue if the namespace is found", func(t *testing.T) {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		}

		ctx := context.Background()
		fakeClient := kube.NewKubernetesFakeClient()
		_, err := fakeClient.Clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		assert.Nil(t, err)

		s, err := NewServer(context.TODO(), fakeClient, testNamespace, WithGeneratedTokenSigningKey())
		assert.Nil(t, err)

		err = s.queues.Create(testNamespace)
		assert.Nil(t, err)

		s.removeQueueIfUnused(ctx, testNamespace)
		assert.True(t, s.queues.HasQueuePair(testNamespace))
	})
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
