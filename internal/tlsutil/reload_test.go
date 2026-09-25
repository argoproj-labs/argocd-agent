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
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"path"
	"testing"
	"time"

	fakecerts "github.com/argoproj-labs/argocd-agent/test/fake/testcerts"

	"github.com/fsnotify/fsnotify"
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

	err = provider.OnChange(event)
	require.NoError(t, err)

	event.Name = path.Join(tempDir, "test-ca.crt")
	err = provider.OnChange(event)
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

func Test_ValidateNewClientCert(t *testing.T) {
	t.Run("cert not active yet", func(t *testing.T) {
		invalidCertTempl := clientTempl
		invalidCertTempl.NotBefore = time.Now().Add(1 * time.Hour)

		certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", invalidCertTempl)
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		require.NoError(t, err)
		assert.Error(t, ValidateNewClientCert(cert))
	})
	t.Run("cert is expired", func(t *testing.T) {
		invalidCertTempl := clientTempl
		invalidCertTempl.NotAfter = time.Now().Add(-1 * time.Hour)

		certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", invalidCertTempl)
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		require.NoError(t, err)
		assert.Error(t, ValidateNewClientCert(cert))
	})
	t.Run("cert is a CA", func(t *testing.T) {
		invalidCertTempl := clientTempl
		invalidCertTempl.IsCA = true
		invalidCertTempl.BasicConstraintsValid = true

		certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", invalidCertTempl)
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		require.NoError(t, err)
		assert.Error(t, ValidateNewClientCert(cert))
	})
	t.Run("cert is valid", func(t *testing.T) {
		certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", clientTempl)
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		require.NoError(t, err)
		assert.NoError(t, ValidateNewClientCert(cert))
	})
}

func Test_ValidateNewCACert(t *testing.T) {
	t.Run("ca not active yet", func(t *testing.T) {
		invalidCATempl := caTempl
		invalidCATempl.NotBefore = time.Now().Add(1 * time.Hour)

		caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", invalidCATempl)
		assert.Error(t, ValidateNewCACert(caPEM))
	})
	t.Run("ca is expired", func(t *testing.T) {
		invalidCATempl := caTempl
		invalidCATempl.NotAfter = time.Now().Add(-1 * time.Hour)

		caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", invalidCATempl)
		assert.Error(t, ValidateNewCACert(caPEM))
	})
	t.Run("ca is not a ca", func(t *testing.T) {
		invalidCATempl := caTempl
		invalidCATempl.IsCA = false
		invalidCATempl.BasicConstraintsValid = false

		caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", invalidCATempl)
		assert.Error(t, ValidateNewCACert(caPEM))
	})
	t.Run("ca is valid", func(t *testing.T) {
		caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", caTempl)
		assert.NoError(t, ValidateNewCACert(caPEM))
	})
	t.Run("multiple CAs provided", func(t *testing.T) {
		caCerts := []byte{}
		for range 5 {
			caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", caTempl)
			caCerts = append(caCerts, caPEM...)
		}
		assert.NoError(t, ValidateNewCACert(caCerts))
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
		assert.Error(t, ValidateNewCACert(caCerts))
	})
}
