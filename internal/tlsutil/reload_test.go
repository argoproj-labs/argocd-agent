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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io/fs"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fakecerts "github.com/argoproj-labs/argocd-agent/test/fake/testcerts"

	"github.com/argoproj/argo-cd/v3/util/settings"
	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func init() {
	// A fake clientset does not send the bookmark event that the watch list
	// feature of client-go waits for, so informers on it would take ten seconds
	// to deliver anything.
	settings.ConfigureGoClientFeatures()
}

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

	t.Run("invalid data is fatal", func(t *testing.T) {
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
		exited := captureFatal(t)
		provider, updates := newProvider(t)

		provider.handleChange(clientSecret("client-tls", newCertPEM, newKeyPEM))

		assert.False(t, *exited)
		assert.Equal(t, 1, *updates)
		loaded, _ := provider.Load()
		assert.NotEqual(t, cert.Certificate, loaded.Certificate)
	})
}

// Test_TLSFileProviderOnChangeInvalid checks that invalid files return an
// error, which ends Watch, and do not replace the current certificate.
func Test_TLSFileProviderOnChangeInvalid(t *testing.T) {
	tempDir := t.TempDir()
	certPath := path.Join(tempDir, "tls.crt")
	keyPath := path.Join(tempDir, "tls.key")
	certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", clientTempl)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(certPath, []byte("bad"), 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	for name, event := range map[string]fsnotify.Event{
		"file write":  {Name: certPath, Op: fsnotify.Write},
		"..data swap": {Name: path.Join(tempDir, "..data"), Op: fsnotify.Create},
	} {
		t.Run(name, func(t *testing.T) {
			provider := NewTLSFileProvider(certPath, keyPath, "", &TLSMaterial{Cert: cert})
			updates := 0
			provider.SetUpdateHandler(func(tls.Certificate, *x509.CertPool) { updates++ })

			require.Error(t, provider.OnChange(context.Background(), event))

			assert.Equal(t, 0, updates)
			loaded, _ := provider.Load()
			assert.Equal(t, cert.Certificate, loaded.Certificate)
		})
	}
}

// Test_TLSFileProviderReload checks that a provider with only a CA path reloads
// the CA, for example when a mounted Secret volume swaps its ..data link.
func Test_TLSFileProviderReload(t *testing.T) {
	tempDir := t.TempDir()
	caPath := path.Join(tempDir, "ca.crt")
	caPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", caTempl)
	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(caPEM))
	newCAPEM, _ := fakecerts.CreateSelfSignedCert(t, "rsa", caTempl)
	require.NoError(t, os.WriteFile(caPath, newCAPEM, 0o600))

	provider := NewTLSFileProvider("", "", caPath, &TLSMaterial{CAPool: caPool})
	require.NoError(t, provider.OnChange(context.Background(), fsnotify.Event{Name: path.Join(tempDir, "..data"), Op: fsnotify.Create}))

	cert, pool := provider.Load()
	assert.Empty(t, cert.Certificate)
	want := x509.NewCertPool()
	require.True(t, want.AppendCertsFromPEM(newCAPEM))
	assert.True(t, want.Equal(pool))
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

// newCertPEM returns a self-signed certificate made from the template and its
// key, both PEM encoded. The key is an ECDSA one, which costs next to nothing,
// unlike the RSA keys of the shared test helper.
func newCertPEM(t *testing.T, template x509.Certificate) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

// writeCertFiles writes a certificate and its key, as newCertPEM makes them, to
// basePath with the suffixes ".crt" and ".key".
func writeCertFiles(t *testing.T, basePath string, template x509.Certificate) {
	t.Helper()
	certPEM, keyPEM := newCertPEM(t, template)
	require.NoError(t, os.WriteFile(basePath+".crt", certPEM, 0o644))
	require.NoError(t, os.WriteFile(basePath+".key", keyPEM, 0o600))
}

// tlsFiles holds the paths of a client certificate, its key and a CA certificate.
type tlsFiles struct {
	cert, key, ca string
}

// writeTLSFiles writes a valid client certificate with its key and a valid CA
// certificate to dir, replacing the ones that are there.
func writeTLSFiles(t *testing.T, dir string) tlsFiles {
	t.Helper()
	writeCertFiles(t, filepath.Join(dir, "client"), clientTempl)
	writeCertFiles(t, filepath.Join(dir, "ca"), caTempl)
	return tlsFiles{
		cert: filepath.Join(dir, "client.crt"),
		key:  filepath.Join(dir, "client.key"),
		ca:   filepath.Join(dir, "ca.crt"),
	}
}

// materialFromFiles reads the material that the files hold.
func materialFromFiles(t *testing.T, f tlsFiles) *TLSMaterial {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(f.cert, f.key)
	require.NoError(t, err)
	caPEM, err := os.ReadFile(f.ca)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(caPEM))
	return &TLSMaterial{Cert: cert, CAPool: pool}
}

// newFileProvider returns a file provider that holds what the files hold.
func newFileProvider(t *testing.T) (*TLSFileProvider, tlsFiles) {
	t.Helper()
	files := writeTLSFiles(t, t.TempDir())
	return NewTLSFileProvider(files.cert, files.key, files.ca, materialFromFiles(t, files)), files
}

func Test_TLSFileProvider_Load(t *testing.T) {
	t.Run("a provider without material returns nothing", func(t *testing.T) {
		provider := NewTLSFileProvider("cert", "key", "ca", nil)

		cert, pool := provider.Load()

		assert.Empty(t, cert.Certificate)
		assert.Nil(t, pool)
	})

	t.Run("the pool that is returned cannot change the one of the provider", func(t *testing.T) {
		provider, _ := newFileProvider(t)
		material := provider.Material.Load()
		snapshot := material.CAPool.Clone()

		cert, pool := provider.Load()
		extraCA, _ := newCertPEM(t, caTempl)
		require.True(t, pool.AppendCertsFromPEM(extraCA))
		_, again := provider.Load()

		assert.Equal(t, material.Cert.Certificate, cert.Certificate)
		assert.True(t, snapshot.Equal(again), "the provider's pool must not have grown")
		assert.False(t, pool.Equal(again))
	})
}

func Test_TLSFileProvider_OnChange_Details(t *testing.T) {
	t.Run("a file that is not watched changes nothing", func(t *testing.T) {
		provider, files := newFileProvider(t)
		before := provider.Material.Load()

		err := provider.OnChange(context.Background(), fsnotify.Event{Name: filepath.Join(filepath.Dir(files.cert), "other.txt"), Op: fsnotify.Write})

		require.NoError(t, err)
		assert.Same(t, before, provider.Material.Load())
	})

	t.Run("a change of the data link reloads the certificate and the CA", func(t *testing.T) {
		provider, files := newFileProvider(t)
		before := provider.Material.Load()
		writeTLSFiles(t, filepath.Dir(files.cert))

		err := provider.OnChange(context.Background(), fsnotify.Event{Name: filepath.Join(filepath.Dir(files.cert), "..data"), Op: fsnotify.Create})

		require.NoError(t, err)
		after := provider.Material.Load()
		assert.NotEqual(t, before.Cert.Certificate, after.Cert.Certificate)
		assert.False(t, before.CAPool.Equal(after.CAPool))
		assert.Equal(t, materialFromFiles(t, files).Cert.Certificate, after.Cert.Certificate)
	})

	t.Run("a change of the key reloads the certificate and keeps the CA", func(t *testing.T) {
		provider, files := newFileProvider(t)
		before := provider.Material.Load()
		writeCertFiles(t, strings.TrimSuffix(files.cert, ".crt"), clientTempl)

		err := provider.OnChange(context.Background(), fsnotify.Event{Name: files.key, Op: fsnotify.Write})

		require.NoError(t, err)
		after := provider.Material.Load()
		assert.NotEqual(t, before.Cert.Certificate, after.Cert.Certificate)
		assert.Same(t, before.CAPool, after.CAPool)
	})

	t.Run("a change of the CA reloads the CA and keeps the certificate", func(t *testing.T) {
		provider, files := newFileProvider(t)
		before := provider.Material.Load()
		writeCertFiles(t, strings.TrimSuffix(files.ca, ".crt"), caTempl)

		err := provider.OnChange(context.Background(), fsnotify.Event{Name: files.ca, Op: fsnotify.Write})

		require.NoError(t, err)
		after := provider.Material.Load()
		assert.False(t, before.CAPool.Equal(after.CAPool))
		assert.Equal(t, before.Cert.Certificate, after.Cert.Certificate)
	})

	t.Run("material that is not acceptable is refused and the current one is kept", func(t *testing.T) {
		tests := []struct {
			name    string
			change  func(t *testing.T, files tlsFiles)
			event   func(files tlsFiles) string
			wantErr string
			wantIs  error
		}{
			{
				name: "a client certificate that is a CA",
				change: func(t *testing.T, f tlsFiles) {
					writeCertFiles(t, strings.TrimSuffix(f.cert, ".crt"), caTempl)
				},
				event:   func(f tlsFiles) string { return f.cert },
				wantErr: "validation failed on client cert file reload",
			},
			{
				name: "an expired client certificate",
				change: func(t *testing.T, f tlsFiles) {
					writeCertFiles(t, strings.TrimSuffix(f.cert, ".crt"), fakecerts.ExpiredCertTempl)
				},
				event:   func(f tlsFiles) string { return f.cert },
				wantErr: "expired",
			},
			{
				name:    "a client certificate that does not match the key",
				change:  func(t *testing.T, f tlsFiles) { writeCertOnly(t, f.cert) },
				event:   func(f tlsFiles) string { return f.cert },
				wantErr: "private key does not match",
			},
			{
				name:   "a client certificate that is gone",
				change: func(t *testing.T, f tlsFiles) { require.NoError(t, os.Remove(f.cert)) },
				event:  func(f tlsFiles) string { return f.cert },
				wantIs: fs.ErrNotExist,
			},
			{
				name: "a CA that is not a CA",
				change: func(t *testing.T, f tlsFiles) {
					writeCertFiles(t, strings.TrimSuffix(f.ca, ".crt"), clientTempl)
				},
				event:   func(f tlsFiles) string { return f.ca },
				wantErr: "validation failed on CA cert file reload",
			},
			{
				name: "a CA that is not a certificate",
				change: func(t *testing.T, f tlsFiles) {
					require.NoError(t, os.WriteFile(f.ca, []byte("not a certificate"), 0o600))
				},
				event:   func(f tlsFiles) string { return f.ca },
				wantErr: "no valid certificate data was found",
			},
			{
				name:   "a CA that is gone",
				change: func(t *testing.T, f tlsFiles) { require.NoError(t, os.Remove(f.ca)) },
				event:  func(f tlsFiles) string { return f.ca },
				wantIs: fs.ErrNotExist,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				provider, files := newFileProvider(t)
				before := provider.Material.Load()
				tt.change(t, files)

				err := provider.OnChange(context.Background(), fsnotify.Event{Name: tt.event(files), Op: fsnotify.Write})

				requireRefusal(t, err, tt.wantErr, tt.wantIs)
				assert.Same(t, before, provider.Material.Load())
			})
		}
	})
}

// requireRefusal checks that err is the refusal of a change: the error that is
// wanted, if there is one, otherwise one that contains wantErr.
func requireRefusal(t *testing.T, err error, wantErr string, wantIs error) {
	t.Helper()
	if wantIs != nil {
		require.ErrorIs(t, err, wantIs)
		return
	}
	require.ErrorContains(t, err, wantErr)
}

// writeCertOnly replaces the certificate at certPath with one whose key is not
// the one next to it.
func writeCertOnly(t *testing.T, certPath string) {
	t.Helper()
	certPEM, _ := newCertPEM(t, clientTempl)
	require.NoError(t, os.WriteFile(certPath, certPEM, 0o600))
}

func Test_TLSFileProvider_Reload(t *testing.T) {
	t.Run("the certificate and the CA are replaced by the ones in the files", func(t *testing.T) {
		provider, files := newFileProvider(t)
		before := provider.Material.Load()
		writeTLSFiles(t, filepath.Dir(files.cert))

		err := provider.Reload(context.Background())

		require.NoError(t, err)
		after := provider.Material.Load()
		want := materialFromFiles(t, files)
		assert.NotEqual(t, before.Cert.Certificate, after.Cert.Certificate)
		assert.Equal(t, want.Cert.Certificate, after.Cert.Certificate)
		assert.True(t, want.CAPool.Equal(after.CAPool))
	})

	t.Run("without a CA path only the certificate is loaded", func(t *testing.T) {
		files := writeTLSFiles(t, t.TempDir())
		provider := NewTLSFileProvider(files.cert, files.key, "", materialFromFiles(t, files))

		err := provider.Reload(context.Background())

		require.NoError(t, err)
		after := provider.Material.Load()
		assert.Equal(t, materialFromFiles(t, files).Cert.Certificate, after.Cert.Certificate)
		assert.Nil(t, after.CAPool)
	})

	t.Run("material that is not acceptable is refused and the current one is kept", func(t *testing.T) {
		tests := []struct {
			name    string
			change  func(t *testing.T, files tlsFiles)
			wantErr string
			wantIs  error
		}{
			{
				name:   "a client certificate that is gone",
				change: func(t *testing.T, f tlsFiles) { require.NoError(t, os.Remove(f.cert)) },
				wantIs: fs.ErrNotExist,
			},
			{
				name: "a client certificate that is a CA",
				change: func(t *testing.T, f tlsFiles) {
					writeCertFiles(t, strings.TrimSuffix(f.cert, ".crt"), caTempl)
				},
				wantErr: "failed to validate client cert",
			},
			{
				name:   "a CA that is gone",
				change: func(t *testing.T, f tlsFiles) { require.NoError(t, os.Remove(f.ca)) },
				wantIs: fs.ErrNotExist,
			},
			{
				name: "a CA that is not a CA",
				change: func(t *testing.T, f tlsFiles) {
					writeCertFiles(t, strings.TrimSuffix(f.ca, ".crt"), clientTempl)
				},
				wantErr: "validation failed on reloading ca pool",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				provider, files := newFileProvider(t)
				before := provider.Material.Load()
				tt.change(t, files)

				err := provider.Reload(context.Background())

				requireRefusal(t, err, tt.wantErr, tt.wantIs)
				assert.Same(t, before, provider.Material.Load())
			})
		}
	})
}

// startWatch runs source.Watch in the background and returns a function that
// stops it, and the channel on which what it returned arrives. When the test
// ends, Watch is stopped and has to have returned.
func startWatch(t *testing.T, source TLSSource) (stop func(), done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		res <- source.Watch(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(watchTimeout):
			t.Error("Watch did not return after the context was cancelled")
		}
	})
	return cancel, res
}

const (
	watchTimeout = 10 * time.Second
	watchTick    = 20 * time.Millisecond
)

func Test_TLSFileProvider_Watch(t *testing.T) {
	t.Run("a directory that does not exist is an error", func(t *testing.T) {
		existing := t.TempDir()
		missing := filepath.Join(t.TempDir(), "missing")
		tests := []struct {
			name          string
			cert, key, ca string
		}{
			{name: "of the certificate", cert: filepath.Join(missing, "client.crt"), key: filepath.Join(existing, "client.key"), ca: filepath.Join(existing, "ca.crt")},
			{name: "of the key", cert: filepath.Join(existing, "client.crt"), key: filepath.Join(missing, "client.key"), ca: filepath.Join(existing, "ca.crt")},
			{name: "of the CA", cert: filepath.Join(existing, "client.crt"), key: filepath.Join(existing, "client.key"), ca: filepath.Join(missing, "ca.crt")},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				provider := NewTLSFileProvider(tt.cert, tt.key, tt.ca, nil)

				err := provider.Watch(context.Background())

				require.ErrorIs(t, err, fs.ErrNotExist)
			})
		}
	})

	t.Run("it returns without an error when the context is cancelled", func(t *testing.T) {
		provider, _ := newFileProvider(t)
		stop, done := startWatch(t, provider)

		stop()

		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(watchTimeout):
			t.Fatal("Watch did not return after the context was cancelled")
		}
	})

	t.Run("a change of the data link is picked up", func(t *testing.T) {
		provider, files := newFileProvider(t)
		before := provider.Material.Load()
		dir := filepath.Dir(files.cert)
		writeTLSFiles(t, dir)
		want := materialFromFiles(t, files)
		startWatch(t, provider)

		// The watcher may not be listening yet when the first event is sent, so
		// the link is touched until the change shows.
		require.Eventually(t, func() bool {
			if !assert.NoError(t, os.WriteFile(filepath.Join(dir, "..data"), []byte(time.Now().String()), 0o600)) {
				return false
			}
			// The watcher may also report the files that were already there when it
			// started, and those events replace the certificate and the CA one at
			// a time, so both must match before the change has been picked up.
			material := provider.Material.Load()
			return material != before &&
				assert.ObjectsAreEqual(want.Cert.Certificate, material.Cert.Certificate) &&
				want.CAPool.Equal(material.CAPool)
		}, watchTimeout, watchTick)
	})

	t.Run("it returns the error of a change that is refused", func(t *testing.T) {
		provider, files := newFileProvider(t)
		_, done := startWatch(t, provider)

		ticker := time.NewTicker(watchTick)
		defer ticker.Stop()
		timeout := time.After(watchTimeout)
		for {
			require.NoError(t, os.WriteFile(files.ca, []byte("not a certificate"), 0o600))
			select {
			case err := <-done:
				require.ErrorContains(t, err, "validation failed on CA cert file reload")
				return
			case <-timeout:
				t.Fatal("Watch did not return the error of the refused change")
			case <-ticker.C:
			}
		}
	})
}

const (
	testClientSecret = "client-secret"
	testCASecret     = "ca-secret"
	testSecretNS     = "argocd"
)

// newTLSSecret returns a TLS secret with the given certificate and key.
func newTLSSecret(name string, crt, key []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testSecretNS},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{"tls.crt": crt, "tls.key": key},
	}
}

// newCASecret returns a secret that holds the given data as its CA certificates.
func newCASecret(name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testSecretNS},
		Data:       data,
	}
}

// secretFixture holds the secrets of a valid client certificate and a valid
// CA, and a provider that holds the material of those secrets.
type secretFixture struct {
	client, ca *corev1.Secret
	provider   *TLSSecretProvider
	kube       *fake.Clientset
}

func newSecretFixture(t *testing.T) *secretFixture {
	t.Helper()
	crt, key := newCertPEM(t, clientTempl)
	caPEM, _ := newCertPEM(t, caTempl)
	f := &secretFixture{
		client: newTLSSecret(testClientSecret, crt, key),
		ca:     newCASecret(testCASecret, map[string][]byte{"ca.crt": caPEM}),
	}
	f.kube = fake.NewClientset(f.client, f.ca)
	cert, err := tls.X509KeyPair(crt, key)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(caPEM))
	f.provider = NewTLSSecretProvider(testClientSecret, testCASecret, testSecretNS, f.kube, &TLSMaterial{Cert: cert, CAPool: pool})
	return f
}

func Test_TLSSecretProvider_Load(t *testing.T) {
	t.Run("a provider without material returns nothing", func(t *testing.T) {
		provider := NewTLSSecretProvider("client", "ca", testSecretNS, fake.NewClientset(), nil)

		cert, pool := provider.Load()

		assert.Empty(t, cert.Certificate)
		assert.Nil(t, pool)
	})

	t.Run("the pool that is returned cannot change the one of the provider", func(t *testing.T) {
		f := newSecretFixture(t)
		material := f.provider.Material.Load()
		snapshot := material.CAPool.Clone()

		cert, pool := f.provider.Load()
		extraCA, _ := newCertPEM(t, caTempl)
		require.True(t, pool.AppendCertsFromPEM(extraCA))
		_, again := f.provider.Load()

		assert.Equal(t, material.Cert.Certificate, cert.Certificate)
		assert.True(t, snapshot.Equal(again), "the provider's pool must not have grown")
		assert.False(t, pool.Equal(again))
	})
}

func Test_TLSSecretProvider_OnChange_Details(t *testing.T) {
	t.Run("a secret that is not watched changes nothing", func(t *testing.T) {
		f := newSecretFixture(t)
		before := f.provider.Material.Load()

		err := f.provider.OnChange(newCASecret("other", map[string][]byte{"ca.crt": []byte("junk")}))

		require.NoError(t, err)
		assert.Same(t, before, f.provider.Material.Load())
	})

	t.Run("a new client certificate replaces the certificate and keeps the CA", func(t *testing.T) {
		f := newSecretFixture(t)
		before := f.provider.Material.Load()
		crt, key := newCertPEM(t, clientTempl)

		err := f.provider.OnChange(newTLSSecret(testClientSecret, crt, key))

		require.NoError(t, err)
		after := f.provider.Material.Load()
		assert.NotEqual(t, before.Cert.Certificate, after.Cert.Certificate)
		assert.Same(t, before.CAPool, after.CAPool)
	})

	t.Run("a client secret that is not acceptable is refused and the current material is kept", func(t *testing.T) {
		goodCrt, goodKey := newCertPEM(t, clientTempl)
		caCrt, caKey := newCertPEM(t, caTempl)
		expiredCrt, expiredKey := newCertPEM(t, fakecerts.ExpiredCertTempl)
		notTLS := newTLSSecret(testClientSecret, goodCrt, goodKey)
		notTLS.Type = corev1.SecretTypeOpaque
		empty := newTLSSecret(testClientSecret, nil, nil)
		empty.Data = nil
		tests := []struct {
			name    string
			secret  *corev1.Secret
			wantErr string
		}{
			{name: "not a TLS secret", secret: notTLS, wantErr: "is not a tls secret"},
			{name: "an empty secret", secret: empty, wantErr: "is empty"},
			{name: "no key", secret: newTLSSecret(testClientSecret, goodCrt, nil), wantErr: "missing either cert or key"},
			{name: "no certificate", secret: newTLSSecret(testClientSecret, nil, goodKey), wantErr: "missing either cert or key"},
			{name: "a certificate that does not match the key", secret: newTLSSecret(testClientSecret, goodCrt, caKey), wantErr: "is invalid"},
			{name: "a client certificate that is a CA", secret: newTLSSecret(testClientSecret, caCrt, caKey), wantErr: "validation failed on client cert secret reload"},
			{name: "an expired client certificate", secret: newTLSSecret(testClientSecret, expiredCrt, expiredKey), wantErr: "validation failed on client cert secret reload"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f := newSecretFixture(t)
				before := f.provider.Material.Load()

				err := f.provider.OnChange(tt.secret)

				require.ErrorContains(t, err, tt.wantErr)
				assert.Same(t, before, f.provider.Material.Load())
			})
		}
	})

	t.Run("a CA secret holds its certificates under ca.crt and tls.crt", func(t *testing.T) {
		caA, _ := newCertPEM(t, caTempl)
		caB, _ := newCertPEM(t, caTempl)
		pool := func(pems ...[]byte) *x509.CertPool {
			p := x509.NewCertPool()
			for _, data := range pems {
				require.True(t, p.AppendCertsFromPEM(data))
			}
			return p
		}
		tests := []struct {
			name string
			data map[string][]byte
			want *x509.CertPool
		}{
			{name: "only ca.crt", data: map[string][]byte{"ca.crt": caA}, want: pool(caA)},
			{name: "only tls.crt", data: map[string][]byte{"tls.crt": caA}, want: pool(caA)},
			{name: "both", data: map[string][]byte{"ca.crt": caA, "tls.crt": caB}, want: pool(caA, caB)},
			{name: "a valid ca.crt next to an invalid tls.crt", data: map[string][]byte{"ca.crt": caA, "tls.crt": []byte("junk")}, want: pool(caA)},
			{name: "other keys are ignored", data: map[string][]byte{"ca.crt": caA, "other": caB}, want: pool(caA)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f := newSecretFixture(t)
				before := f.provider.Material.Load()

				err := f.provider.OnChange(newCASecret(testCASecret, tt.data))

				require.NoError(t, err)
				after := f.provider.Material.Load()
				assert.True(t, tt.want.Equal(after.CAPool))
				assert.Equal(t, before.Cert.Certificate, after.Cert.Certificate, "the certificate must be kept")
			})
		}
	})

	t.Run("a CA secret that is not acceptable is refused and the current material is kept", func(t *testing.T) {
		clientCrt, _ := newCertPEM(t, clientTempl)
		tests := []struct {
			name    string
			data    map[string][]byte
			wantErr string
		}{
			{name: "an empty secret", data: nil, wantErr: "is empty"},
			{name: "no known key", data: map[string][]byte{"other": []byte("x")}, wantErr: "no certs were found"},
			{name: "a certificate that is not a CA", data: map[string][]byte{"ca.crt": clientCrt}, wantErr: "no certs were found"},
			{name: "data that is not a certificate", data: map[string][]byte{"ca.crt": []byte("junk")}, wantErr: "no certs were found"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f := newSecretFixture(t)
				before := f.provider.Material.Load()

				err := f.provider.OnChange(newCASecret(testCASecret, tt.data))

				require.ErrorContains(t, err, tt.wantErr)
				assert.Same(t, before, f.provider.Material.Load())
			})
		}
	})
}

func Test_readTLSDataFromKey(t *testing.T) {
	caPEM, _ := newCertPEM(t, caTempl)
	clientPEM, _ := newCertPEM(t, clientTempl)
	secret := newCASecret(testCASecret, map[string][]byte{
		"ca.crt":     caPEM,
		"client.crt": clientPEM,
		"junk":       []byte("junk"),
		"nil":        nil,
	})

	tests := []struct {
		name    string
		key     string
		wantErr string
	}{
		{name: "a CA is added to the pool", key: "ca.crt"},
		{name: "a key that does not exist", key: "missing", wantErr: "key does not exist in the secret"},
		{name: "a key without data", key: "nil", wantErr: "key does not exist in the secret"},
		{name: "a certificate that is not a CA", key: "client.crt", wantErr: "not a CA"},
		{name: "data that is not a certificate", key: "junk", wantErr: "no valid certificate data was found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := x509.NewCertPool()

			err := readTLSDataFromKey(pool, tt.key, secret)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.True(t, x509.NewCertPool().Equal(pool), "nothing must be added to the pool")
				return
			}
			require.NoError(t, err)
			want := x509.NewCertPool()
			require.True(t, want.AppendCertsFromPEM(caPEM))
			assert.True(t, want.Equal(pool))
		})
	}
}

func Test_TLSSecretProvider_Reload(t *testing.T) {
	t.Run("the certificate and the CA are replaced by the ones in the secrets", func(t *testing.T) {
		f := newSecretFixture(t)
		before := f.provider.Material.Load()
		crt, key := newCertPEM(t, clientTempl)
		caPEM, _ := newCertPEM(t, caTempl)
		otherCAPEM, _ := newCertPEM(t, caTempl)
		_, err := f.kube.CoreV1().Secrets(testSecretNS).Update(context.Background(), newTLSSecret(testClientSecret, crt, key), metav1.UpdateOptions{})
		require.NoError(t, err)
		_, err = f.kube.CoreV1().Secrets(testSecretNS).Update(context.Background(), newCASecret(testCASecret, map[string][]byte{"ca.crt": caPEM, "tls.crt": otherCAPEM}), metav1.UpdateOptions{})
		require.NoError(t, err)

		err = f.provider.Reload(context.Background())

		require.NoError(t, err)
		after := f.provider.Material.Load()
		wantCert, err := tls.X509KeyPair(crt, key)
		require.NoError(t, err)
		wantPool := x509.NewCertPool()
		require.True(t, wantPool.AppendCertsFromPEM(caPEM))
		require.True(t, wantPool.AppendCertsFromPEM(otherCAPEM), "the CA is read from both ca.crt and tls.crt")
		assert.NotEqual(t, before.Cert.Certificate, after.Cert.Certificate)
		assert.Equal(t, wantCert.Certificate, after.Cert.Certificate)
		assert.True(t, wantPool.Equal(after.CAPool))
	})

	for _, key := range []string{"ca.crt", "tls.crt"} {
		t.Run("a CA that is only in "+key+" is enough", func(t *testing.T) {
			f := newSecretFixture(t)
			caPEM, _ := newCertPEM(t, caTempl)
			_, err := f.kube.CoreV1().Secrets(testSecretNS).Update(context.Background(), newCASecret(testCASecret, map[string][]byte{key: caPEM}), metav1.UpdateOptions{})
			require.NoError(t, err)

			err = f.provider.Reload(context.Background())

			require.NoError(t, err)
			want := x509.NewCertPool()
			require.True(t, want.AppendCertsFromPEM(caPEM))
			assert.True(t, want.Equal(f.provider.Material.Load().CAPool))
		})
	}

	t.Run("material that is not acceptable is refused and the current one is kept", func(t *testing.T) {
		caCrt, caKey := newCertPEM(t, caTempl)
		tests := []struct {
			name    string
			change  func(t *testing.T, f *secretFixture)
			wantErr string
		}{
			{
				name: "a client secret that is gone",
				change: func(t *testing.T, f *secretFixture) {
					require.NoError(t, f.kube.CoreV1().Secrets(testSecretNS).Delete(context.Background(), testClientSecret, metav1.DeleteOptions{}))
				},
				wantErr: "not found",
			},
			{
				name: "a client certificate that is a CA",
				change: func(t *testing.T, f *secretFixture) {
					_, err := f.kube.CoreV1().Secrets(testSecretNS).Update(context.Background(), newTLSSecret(testClientSecret, caCrt, caKey), metav1.UpdateOptions{})
					require.NoError(t, err)
				},
				wantErr: "failed to validate new client cert",
			},
			{
				name: "a CA secret that is gone",
				change: func(t *testing.T, f *secretFixture) {
					require.NoError(t, f.kube.CoreV1().Secrets(testSecretNS).Delete(context.Background(), testCASecret, metav1.DeleteOptions{}))
				},
				wantErr: "not found",
			},
			{
				name: "a CA secret without certificates",
				change: func(t *testing.T, f *secretFixture) {
					_, err := f.kube.CoreV1().Secrets(testSecretNS).Update(context.Background(), newCASecret(testCASecret, map[string][]byte{"other": []byte("x")}), metav1.UpdateOptions{})
					require.NoError(t, err)
				},
				wantErr: "no certs loaded on reload",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f := newSecretFixture(t)
				before := f.provider.Material.Load()
				tt.change(t, f)

				err := f.provider.Reload(context.Background())

				require.ErrorContains(t, err, tt.wantErr)
				assert.Same(t, before, f.provider.Material.Load())
			})
		}
	})
}

func Test_TLSSecretProvider_Watch(t *testing.T) {
	t.Run("the material is loaded from the secrets, follows their changes, and Watch stops with the context", func(t *testing.T) {
		f := newSecretFixture(t)
		// Start without material: it has to come from the secrets.
		provider := NewTLSSecretProvider(testClientSecret, testCASecret, testSecretNS, f.kube, nil)
		stop, done := startWatch(t, provider)

		wantCert, err := tls.X509KeyPair(f.client.Data["tls.crt"], f.client.Data["tls.key"])
		require.NoError(t, err)
		wantPool := x509.NewCertPool()
		require.True(t, wantPool.AppendCertsFromPEM(f.ca.Data["ca.crt"]))
		require.Eventually(t, func() bool {
			m := provider.Material.Load()
			return m != nil && assert.ObjectsAreEqual(wantCert.Certificate, m.Cert.Certificate) && wantPool.Equal(m.CAPool)
		}, watchTimeout, watchTick, "the material of the secrets must be loaded")

		crt, key := newCertPEM(t, clientTempl)
		newCert, err := tls.X509KeyPair(crt, key)
		require.NoError(t, err)
		_, err = f.kube.CoreV1().Secrets(testSecretNS).Update(context.Background(), newTLSSecret(testClientSecret, crt, key), metav1.UpdateOptions{})
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			return assert.ObjectsAreEqual(newCert.Certificate, provider.Material.Load().Cert.Certificate)
		}, watchTimeout, watchTick, "an update of the secret must be picked up")

		stop()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(watchTimeout):
			t.Fatal("Watch did not return after the context was cancelled")
		}
	})
}

func Test_validateNewClientCert_Malformed(t *testing.T) {
	tests := []struct {
		name    string
		cert    tls.Certificate
		wantErr string
	}{
		{name: "no certificate", cert: tls.Certificate{}, wantErr: "no certificate data"},
		{name: "an empty certificate", cert: tls.Certificate{Certificate: [][]byte{nil}}, wantErr: "no certificate data"},
		{name: "data that is not a certificate", cert: tls.Certificate{Certificate: [][]byte{[]byte("junk")}}, wantErr: "could not parse certificate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorContains(t, validateNewClientCert(tt.cert), tt.wantErr)
		})
	}
}

func Test_validateNewCACert_Malformed(t *testing.T) {
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("key")})
	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{name: "no PEM data", data: []byte("junk"), wantErr: "no valid certificate data was found"},
		{name: "no data", data: nil, wantErr: "no valid certificate data was found"},
		{name: "a PEM block that is not a certificate", data: keyPEM, wantErr: "unexpected PEM block type: PRIVATE KEY"},
		{name: "a certificate block that cannot be parsed", data: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("junk")}), wantErr: "malformed certificate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorContains(t, validateNewCACert(tt.data), tt.wantErr)
		})
	}
}
