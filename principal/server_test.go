package principal

import (
	"context"
	"crypto/x509"
	"math/big"
	"os"
	"path"
	"testing"
	"time"

	fakeappclient "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	fakecerts "github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		s, err := NewServer(context.TODO(), fakeappclient.NewSimpleClientset(), testNamespace,
			WithTLSKeyPairFromPath(path.Join(tempDir, "test-cert.crt"), path.Join(tempDir, "test-cert.key")),
			WithGeneratedTokenSigningKey(),
		)
		require.NoError(t, err)
		tlsConfig, err := s.loadTLSConfig()
		assert.NoError(t, err)
		assert.NotNil(t, tlsConfig)
	})
	t.Run("Non-existing TLS key pair", func(t *testing.T) {
		s, err := NewServer(context.TODO(), fakeappclient.NewSimpleClientset(), testNamespace,
			WithTLSKeyPairFromPath(path.Join(tempDir, "other-cert.crt"), path.Join(tempDir, "other-cert.key")),
			WithGeneratedTokenSigningKey(),
		)
		require.NoError(t, err)
		tlsConfig, err := s.loadTLSConfig()
		assert.ErrorIs(t, err, os.ErrNotExist)
		assert.Nil(t, tlsConfig)
	})

	t.Run("Invalid TLS certificate", func(t *testing.T) {
		s, err := NewServer(context.TODO(), fakeappclient.NewSimpleClientset(), testNamespace,
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
		s, err := NewServer(context.TODO(), fakeappclient.NewSimpleClientset(), testNamespace, WithListenerAddress("0.0.0.0"), WithGeneratedTokenSigningKey())
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.NotEqual(t, defaultOptions(), s.options)
		assert.Equal(t, "0.0.0.0", s.options.address)
	})

	t.Run("Instantiate new server object with invalid option", func(t *testing.T) {
		s, err := NewServer(context.TODO(), fakeappclient.NewSimpleClientset(), testNamespace, WithListenerPort(-1), WithGeneratedTokenSigningKey())
		assert.Error(t, err)
		assert.Nil(t, s)
	})
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
