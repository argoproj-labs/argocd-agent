package tlsutil

import (
	"context"
	"testing"

	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/argoproj-labs/argocd-agent/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

func Test_TLSCertFromSecret(t *testing.T) {
	t.Run("Secret not found", func(t *testing.T) {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tls-one",
				Namespace: "argocd",
			},
			Type: tlsTypeLabelValue,
			Data: map[string][]byte{
				tlsCertFieldName: testutil.MustReadFile("testdata/001_test_cert.pem"),
				tlsKeyFieldName:  testutil.MustReadFile("testdata/001_test_key.pem"),
			},
		}
		kcl := kube.NewFakeClientsetWithResources(secret)
		cert, err := TLSCertFromSecret(context.TODO(), kcl, "argocd", "tls-two")
		assert.ErrorContains(t, err, "not found")
		assert.NotNil(t, cert)
	})
	t.Run("Valid secret", func(t *testing.T) {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tls-one",
				Namespace: "argocd",
			},
			Type: tlsTypeLabelValue,
			Data: map[string][]byte{
				tlsCertFieldName: testutil.MustReadFile("testdata/001_test_cert.pem"),
				tlsKeyFieldName:  testutil.MustReadFile("testdata/001_test_key.pem"),
			},
		}
		kcl := kube.NewFakeClientsetWithResources(secret)
		cert, err := TLSCertFromSecret(context.TODO(), kcl, "argocd", "tls-one")
		assert.NoError(t, err)
		assert.NotNil(t, cert)
	})
	t.Run("Missing data in secret", func(t *testing.T) {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tls-one",
				Namespace: "argocd",
			},
			Type: tlsTypeLabelValue,
			Data: nil,
		}
		kcl := kube.NewFakeClientsetWithResources(secret)
		cert, err := TLSCertFromSecret(context.TODO(), kcl, "argocd", "tls-one")
		assert.ErrorContains(t, err, "empty secret")
		assert.NotNil(t, cert)
	})
	t.Run("Missing cert in secret", func(t *testing.T) {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tls-one",
				Namespace: "argocd",
			},
			Type: tlsTypeLabelValue,
			Data: map[string][]byte{
				tlsKeyFieldName: testutil.MustReadFile("testdata/001_test_key.pem"),
			},
		}
		kcl := kube.NewFakeClientsetWithResources(secret)
		cert, err := TLSCertFromSecret(context.TODO(), kcl, "argocd", "tls-one")
		assert.ErrorContains(t, err, "either cert or key is missing")
		assert.NotNil(t, cert)

	})
	t.Run("Missing key in secret", func(t *testing.T) {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tls-one",
				Namespace: "argocd",
			},
			Type: tlsTypeLabelValue,
			Data: map[string][]byte{
				tlsCertFieldName: testutil.MustReadFile("testdata/001_test_cert.pem"),
			},
		}
		kcl := kube.NewFakeClientsetWithResources(secret)
		cert, err := TLSCertFromSecret(context.TODO(), kcl, "argocd", "tls-one")
		assert.ErrorContains(t, err, "either cert or key is missing")
		assert.NotNil(t, cert)

	})

	t.Run("Not a TLS secret", func(t *testing.T) {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tls-one",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				tlsKeyFieldName:  testutil.MustReadFile("testdata/001_test_key.pem"),
				tlsCertFieldName: testutil.MustReadFile("testdata/001_test_cert.pem"),
			},
		}
		kcl := kube.NewFakeClientsetWithResources(secret)
		cert, err := TLSCertFromSecret(context.TODO(), kcl, "argocd", "tls-one")
		assert.ErrorContains(t, err, "not a TLS secret")
		assert.NotNil(t, cert)

	})

	t.Run("Invalid data in secret", func(t *testing.T) {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tls-one",
				Namespace: "argocd",
			},
			Type: tlsTypeLabelValue,
			Data: map[string][]byte{
				tlsKeyFieldName:  []byte("something"),
				tlsCertFieldName: []byte("someother"),
			},
		}
		kcl := kube.NewFakeClientsetWithResources(secret)
		cert, err := TLSCertFromSecret(context.TODO(), kcl, "argocd", "tls-one")
		assert.ErrorContains(t, err, "invalid cert or key data")
		assert.NotNil(t, cert)

	})

}

func Test_TlsCertToSecret(t *testing.T) {
	keyPem := testutil.MustReadFile("testdata/001_test_key.pem")
	certPem := testutil.MustReadFile("testdata/001_test_cert.pem")

	t.Run("Successfully create a secret", func(t *testing.T) {
		kcl := kube.NewFakeClientsetWithResources()
		cert, err := TLSCertFromFile("testdata/001_test_cert.pem", "testdata/001_test_key.pem", false)
		require.NoError(t, err)
		err = TLSCertToSecret(context.TODO(), kcl, "argocd", "tls-one", cert)
		assert.NoError(t, err)
		s, err := kcl.CoreV1().Secrets("argocd").Get(context.TODO(), "tls-one", metav1.GetOptions{})
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.Equal(t, keyPem, s.Data[tlsKeyFieldName])
		assert.Equal(t, certPem, s.Data[tlsCertFieldName])
	})

	t.Run("Incomplete certificate", func(t *testing.T) {
		kcl := kube.NewFakeClientsetWithResources()
		cert, err := TLSCertFromFile("testdata/001_test_cert.pem", "testdata/001_test_key.pem", false)
		require.NoError(t, err)
		cert.PrivateKey = nil
		err = TLSCertToSecret(context.TODO(), kcl, "argocd", "tls-one", cert)
		assert.ErrorContains(t, err, "invalid private key")
		_, err = kcl.CoreV1().Secrets("argocd").Get(context.TODO(), "tls-one", metav1.GetOptions{})
		assert.True(t, errors.IsNotFound(err))
	})

}

func Test_X509CertPoolFromSecret(t *testing.T) {
	certPem := testutil.MustReadFile("testdata/001_test_cert.pem")
	s := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ca-certs",
			Namespace: "argocd",
		},
		Data: map[string][]byte{
			"ca.crt": []byte(certPem),
		},
	}
	kcl := kube.NewFakeClientsetWithResources(s)
	t.Run("Read pool from single field", func(t *testing.T) {
		pool, err := X509CertPoolFromSecret(context.TODO(), kcl, "argocd", "ca-certs", "ca.crt")
		assert.NoError(t, err)
		assert.NotNil(t, pool)
	})
	t.Run("Read pool from all fields", func(t *testing.T) {
		pool, err := X509CertPoolFromSecret(context.TODO(), kcl, "argocd", "ca-certs", "")
		assert.NoError(t, err)
		assert.NotNil(t, pool)
	})
	t.Run("Read pool from non-existing field", func(t *testing.T) {
		pool, err := X509CertPoolFromSecret(context.TODO(), kcl, "argocd", "ca-certs", "")
		assert.NoError(t, err)
		assert.NotNil(t, pool)
	})

	t.Run("Read pool from non-existing secret", func(t *testing.T) {
		pool, err := X509CertPoolFromSecret(context.TODO(), kcl, "argocd", "ca-certs-2", "")
		assert.True(t, errors.IsNotFound(err))
		assert.Nil(t, pool)
	})
}

func Test_TransportFromConfig(t *testing.T) {
	keyPem := testutil.MustReadFile("testdata/001_test_key.pem")
	certPem := testutil.MustReadFile("testdata/001_test_cert.pem")
	t.Run("Valid embedded client certificate data", func(t *testing.T) {
		restConf := &rest.Config{
			TLSClientConfig: rest.TLSClientConfig{
				CertData: []byte(certPem),
				KeyData:  []byte(keyPem),
			},
		}
		ht, err := TransportFromConfig(restConf)
		assert.NoError(t, err)
		assert.Len(t, ht.TLSClientConfig.Certificates, 1)
	})
	t.Run("Invalid embedded client certificate data", func(t *testing.T) {
		restConf := &rest.Config{
			TLSClientConfig: rest.TLSClientConfig{
				CertData: []byte("foo"),
				KeyData:  []byte("bar"),
			},
		}
		ht, err := TransportFromConfig(restConf)
		assert.Error(t, err)
		assert.Nil(t, ht)
	})
	t.Run("Valid client certificate data in file", func(t *testing.T) {
		restConf := &rest.Config{
			TLSClientConfig: rest.TLSClientConfig{
				CertFile: "testdata/001_test_cert.pem",
				KeyFile:  "testdata/001_test_key.pem",
			},
		}
		ht, err := TransportFromConfig(restConf)
		assert.NoError(t, err)
		assert.Len(t, ht.TLSClientConfig.Certificates, 1)
	})
	t.Run("Client certificate data from non-existing file", func(t *testing.T) {
		restConf := &rest.Config{
			TLSClientConfig: rest.TLSClientConfig{
				CertFile: "testdata/00x_test_cert.pem",
				KeyFile:  "testdata/00x_test_key.pem",
			},
		}
		ht, err := TransportFromConfig(restConf)
		assert.Error(t, err)
		assert.Nil(t, ht)
	})
	t.Run("No client certificate data specified", func(t *testing.T) {
		ht, err := TransportFromConfig(&rest.Config{})
		assert.Error(t, err)
		assert.Nil(t, ht)
	})
}

func Test_JWTSigningKeyFromSecret(t *testing.T) {
	t.Run("Secret not found", func(t *testing.T) {
		kcl := kube.NewFakeClientsetWithResources()
		_, err := JWTSigningKeyFromSecret(context.TODO(), kcl, "argocd", "jwt-secret")
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("Valid JWT key secret", func(t *testing.T) {
		jwtKeyPem := testutil.MustReadFile("testdata/001_test_key.pem")
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "jwt-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				jwtKeyFieldName: jwtKeyPem,
			},
		}
		kcl := kube.NewFakeClientsetWithResources(secret)
		key, err := JWTSigningKeyFromSecret(context.TODO(), kcl, "argocd", "jwt-secret")
		assert.NoError(t, err)
		assert.NotNil(t, key)
	})

	t.Run("Empty secret", func(t *testing.T) {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "jwt-secret",
				Namespace: "argocd",
			},
			Data: nil,
		}
		kcl := kube.NewFakeClientsetWithResources(secret)
		_, err := JWTSigningKeyFromSecret(context.TODO(), kcl, "argocd", "jwt-secret")
		assert.ErrorContains(t, err, "empty secret")
	})

	t.Run("Missing JWT key in secret", func(t *testing.T) {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "jwt-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				"other-field": []byte("some data"),
			},
		}
		kcl := kube.NewFakeClientsetWithResources(secret)
		_, err := JWTSigningKeyFromSecret(context.TODO(), kcl, "argocd", "jwt-secret")
		assert.ErrorContains(t, err, "JWT signing key is missing")
	})

	t.Run("Invalid JWT key data", func(t *testing.T) {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "jwt-secret",
				Namespace: "argocd",
			},
			Data: map[string][]byte{
				jwtKeyFieldName: []byte("invalid key data"),
			},
		}
		kcl := kube.NewFakeClientsetWithResources(secret)
		_, err := JWTSigningKeyFromSecret(context.TODO(), kcl, "argocd", "jwt-secret")
		assert.ErrorContains(t, err, "malformed PEM data")
	})
}
