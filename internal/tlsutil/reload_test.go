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

package tlsutil

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"os"
	"path"
	"testing"
	"time"

	fakecerts "github.com/argoproj-labs/argocd-agent/test/fake/testcerts"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var clientTempl = x509.Certificate{
	SerialNumber:          big.NewInt(1),
	KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	BasicConstraintsValid: true,
	NotBefore:             time.Now().Add(-1 * time.Hour),
	NotAfter:              time.Now().Add(1 * time.Hour),
}

var caTempl = x509.Certificate{
	SerialNumber:          big.NewInt(1),
	KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	BasicConstraintsValid: true,
	NotBefore:             time.Now().Add(-1 * time.Hour),
	NotAfter:              time.Now().Add(1 * time.Hour),
	IsCA:                  true,
}

func Test_TLSFileProviderOnChange(t *testing.T) {
	tempDir := t.TempDir()
	fakecerts.WriteSelfSignedCert(t, "rsa", path.Join(tempDir, "test-cert"), clientTempl)
	fakecerts.WriteSelfSignedCert(t, "rsa", path.Join(tempDir, "test-ca"), caTempl)

	certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", clientTempl)
	caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", caTempl)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(caPEM))

	startingMaterial := &TLSMaterial{
		Cert:   cert,
		CAPool: caPool,
	}

	provider := NewTLSFileProvider(path.Join(tempDir, "test-cert.crt"), path.Join(tempDir, "test-cert.key"), path.Join(tempDir, "test-ca.crt"), startingMaterial)
	require.NotNil(t, provider)

	event := fsnotify.Event{
		Name: path.Join(tempDir, "test-cert.crt"),
		Op:   fsnotify.Write,
	}

	err = provider.OnChange(context.Background(), event)
	require.NoError(t, err)

	event.Name = path.Join(tempDir, "test-ca.crt")
	err = provider.OnChange(context.Background(), event)
	require.NoError(t, err)

	cert, caPool = provider.Load()
	assert.NotEqual(t, startingMaterial.Cert, cert)
	assert.False(t, startingMaterial.CAPool.Equal(caPool))
}

func Test_TLSSecretProviderOnChange(t *testing.T) {
	clientSecretName := "test-client-secret"
	caSecretName := "test-ca-secret"
	testNamespace := "testNamespace"

	certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", clientTempl)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)

	caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", caTempl)
	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(caPEM))

	startingMaterial := &TLSMaterial{
		Cert:   cert,
		CAPool: caPool,
	}

	provider := NewTLSSecretProvider(clientSecretName, caSecretName, testNamespace, &kubernetes.Clientset{}, startingMaterial)
	require.NotNil(t, provider)

	secretCertPEM, secretKeyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", clientTempl)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clientSecretName,
			Namespace: testNamespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": secretCertPEM,
			"tls.key": secretKeyPEM,
		},
	}

	err = provider.OnChange(secret)
	require.NoError(t, err)

	secretCAPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", caTempl)
	secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      caSecretName,
			Namespace: testNamespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"ca.crt":  secretCAPEM,
			"tls.crt": caPEM,
		},
	}

	err = provider.OnChange(secret)
	require.NoError(t, err)

	cert, caPool = provider.Load()
	assert.NotEqual(t, startingMaterial.Cert, cert)
	assert.False(t, startingMaterial.CAPool.Equal(caPool))
}

// Test_TLSSourceLoad checks that both providers load material without a CA pool.
func Test_TLSSourceLoad(t *testing.T) {
	certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", clientTempl)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)

	sources := map[string]TLSSource{
		"file":   NewTLSFileProvider("tls.crt", "tls.key", "", &TLSMaterial{Cert: cert}),
		"secret": NewTLSSecretProvider("client-tls", "", "argocd", &kubernetes.Clientset{}, &TLSMaterial{Cert: cert}),
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			loaded, pool := source.Load()
			assert.Equal(t, cert.Certificate, loaded.Certificate)
			assert.Nil(t, pool)
		})
	}
}

func Test_TLSSecretProviderHandleChange(t *testing.T) {
	certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", clientTempl)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	newCertPEM, newKeyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", clientTempl)

	clientSecret := func(name string, crt, key []byte) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "argocd"},
			Type:       corev1.SecretTypeTLS,
			Data:       map[string][]byte{"tls.crt": crt, "tls.key": key},
		}
	}
	newProvider := func(t *testing.T) (*TLSSecretProvider, *int) {
		t.Helper()
		provider := NewTLSSecretProvider("client-tls", "agent-ca", "argocd", &kubernetes.Clientset{}, &TLSMaterial{Cert: cert})
		updates := 0
		provider.SetUpdateHandler(func(tls.Certificate, *x509.CertPool) { updates++ })
		return provider, &updates
	}

	t.Run("invalid data keeps the previous certificate", func(t *testing.T) {
		exited := captureFatal(t)
		provider, updates := newProvider(t)
		provider.KeepPreviousCertificate()

		provider.handleChange(clientSecret("client-tls", []byte("bad"), []byte("bad")))

		assert.False(t, *exited)
		assert.Equal(t, 0, *updates)
		loaded, _ := provider.Load()
		assert.Equal(t, cert.Certificate, loaded.Certificate)
	})
	t.Run("invalid data is fatal without KeepPreviousCertificate", func(t *testing.T) {
		exited := captureFatal(t)
		provider, updates := newProvider(t)

		provider.handleChange(clientSecret("client-tls", []byte("bad"), []byte("bad")))

		assert.True(t, *exited)
		assert.Equal(t, 0, *updates)
		loaded, _ := provider.Load()
		assert.Equal(t, cert.Certificate, loaded.Certificate)
	})
	t.Run("other Secrets are ignored", func(t *testing.T) {
		exited := captureFatal(t)
		provider, updates := newProvider(t)

		require.NoError(t, provider.OnChange(clientSecret("argocd-secret", []byte("bad"), []byte("bad"))))
		provider.handleChange(clientSecret("argocd-secret", newCertPEM, newKeyPEM))

		assert.False(t, *exited)
		assert.Equal(t, 0, *updates)
		loaded, _ := provider.Load()
		assert.Equal(t, cert.Certificate, loaded.Certificate)
	})
	t.Run("valid data calls the update handler", func(t *testing.T) {
		provider, updates := newProvider(t)
		provider.KeepPreviousCertificate()

		provider.handleChange(clientSecret("client-tls", newCertPEM, newKeyPEM))

		assert.Equal(t, 1, *updates)
		loaded, _ := provider.Load()
		assert.NotEqual(t, cert.Certificate, loaded.Certificate)
	})
}

func Test_TLSFileProviderKeepPreviousCertificate(t *testing.T) {
	tempDir := t.TempDir()
	certPath := path.Join(tempDir, "tls.crt")
	keyPath := path.Join(tempDir, "tls.key")
	certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", clientTempl)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(certPath, []byte("bad"), 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))
	event := fsnotify.Event{Name: certPath, Op: fsnotify.Write}

	t.Run("invalid data keeps the previous certificate", func(t *testing.T) {
		provider := NewTLSFileProvider(certPath, keyPath, "", &TLSMaterial{Cert: cert})
		provider.KeepPreviousCertificate()

		require.NoError(t, provider.OnChange(context.Background(), event))

		loaded, _ := provider.Load()
		assert.Equal(t, cert.Certificate, loaded.Certificate)
	})
	t.Run("invalid data returns an error without KeepPreviousCertificate", func(t *testing.T) {
		provider := NewTLSFileProvider(certPath, keyPath, "", &TLSMaterial{Cert: cert})

		require.Error(t, provider.OnChange(context.Background(), event))

		loaded, _ := provider.Load()
		assert.Equal(t, cert.Certificate, loaded.Certificate)
	})
}

// captureFatal makes logrus Fatal calls record the exit instead of ending the test process.
func captureFatal(t *testing.T) *bool {
	t.Helper()
	exited := false
	logger := logrus.StandardLogger()
	previous := logger.ExitFunc
	logger.ExitFunc = func(int) { exited = true }
	t.Cleanup(func() { logger.ExitFunc = previous })
	return &exited
}

func Test_ValidateNewClientCert(t *testing.T) {
	t.Run("cert not active yet", func(t *testing.T) {
		invalidCertTempl := clientTempl
		invalidCertTempl.NotBefore = time.Now().Add(1 * time.Hour)

		certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", invalidCertTempl)
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		require.NoError(t, err)
		assert.Error(t, validateNewClientCert(cert))
	})
	t.Run("cert is expired", func(t *testing.T) {
		invalidCertTempl := clientTempl
		invalidCertTempl.NotAfter = time.Now().Add(-1 * time.Hour)

		certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", invalidCertTempl)
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		require.NoError(t, err)
		assert.Error(t, validateNewClientCert(cert))
	})
	t.Run("cert is a CA", func(t *testing.T) {
		invalidCertTempl := clientTempl
		invalidCertTempl.IsCA = true
		invalidCertTempl.BasicConstraintsValid = true

		certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", invalidCertTempl)
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		require.NoError(t, err)
		assert.Error(t, validateNewClientCert(cert))
	})
	t.Run("cert is valid", func(t *testing.T) {
		certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", clientTempl)
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		require.NoError(t, err)
		assert.NoError(t, validateNewClientCert(cert))
	})
}

func Test_ValidateNewCACert(t *testing.T) {
	t.Run("ca not active yet", func(t *testing.T) {
		invalidCATempl := caTempl
		invalidCATempl.NotBefore = time.Now().Add(1 * time.Hour)

		caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", invalidCATempl)
		assert.Error(t, validateNewCACert(caPEM))
	})
	t.Run("ca is expired", func(t *testing.T) {
		invalidCATempl := caTempl
		invalidCATempl.NotAfter = time.Now().Add(-1 * time.Hour)

		caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", invalidCATempl)
		assert.Error(t, validateNewCACert(caPEM))
	})
	t.Run("ca is not a ca", func(t *testing.T) {
		invalidCATempl := caTempl
		invalidCATempl.IsCA = false
		invalidCATempl.BasicConstraintsValid = false

		caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", invalidCATempl)
		assert.Error(t, validateNewCACert(caPEM))
	})
	t.Run("ca is valid", func(t *testing.T) {
		caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", caTempl)
		assert.NoError(t, validateNewCACert(caPEM))
	})
	t.Run("multiple CAs provided", func(t *testing.T) {
		caCerts := []byte{}
		for range 5 {
			caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", caTempl)
			caCerts = append(caCerts, caPEM...)
		}
		assert.NoError(t, validateNewCACert(caCerts))
	})
	t.Run("multiple CAs with a mix or valid and invalid", func(t *testing.T) {
		caCerts := []byte{}
		for i := range 5 {
			templ := caTempl
			switch i {
			case 0:
				templ.IsCA = false
				templ.BasicConstraintsValid = false
			case 2:
				templ.NotBefore = time.Now().Add(1 * time.Hour)
			case 3:
				templ.NotAfter = time.Now().Add(-1 * time.Hour)
			}

			caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", templ)
			caCerts = append(caCerts, caPEM...)
		}
		assert.Error(t, validateNewCACert(caCerts))
	})
}
