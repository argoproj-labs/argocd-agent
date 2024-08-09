package tlsutil

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"path"
	"testing"

	"github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_LoadTlsCertFromFile(t *testing.T) {
	t.Run("Load valid RSA certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "rsa", p, testcerts.DefaultCertTempl)
		cert, err := TlsCertFromFile(p+".crt", p+".key", true)
		assert.NoError(t, err)
		assert.NotZero(t, len(cert.Certificate))
	})

	t.Run("Load valid ECDSA certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "ecdsa", p, testcerts.DefaultCertTempl)
		cert, err := TlsCertFromFile(p+".crt", p+".key", true)
		assert.NoError(t, err)
		assert.NotZero(t, len(cert.Certificate))
	})

	t.Run("Load valid ECDH certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "ecdh", p, testcerts.DefaultCertTempl)
		cert, err := TlsCertFromFile(p+".crt", p+".key", true)
		assert.NoError(t, err)
		assert.NotZero(t, len(cert.Certificate))
	})

	t.Run("Load valid ED25519 certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "ed25519", p, testcerts.DefaultCertTempl)
		cert, err := TlsCertFromFile(p+".crt", p+".key", true)
		assert.NoError(t, err)
		assert.NotZero(t, len(cert.Certificate))
	})

	t.Run("Fail on expired certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "rsa", p, testcerts.ExpiredCertTempl)
		cert, err := TlsCertFromFile(p+".crt", p+".key", true)
		assert.ErrorContains(t, err, "expired")
		assert.Zero(t, len(cert.Certificate))
	})

	t.Run("Do not fail on expired certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "rsa", p, testcerts.ExpiredCertTempl)
		cert, err := TlsCertFromFile(p+".crt", p+".key", false)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(cert.Certificate))
	})
	t.Run("Fail on not yet valid certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "rsa", p, testcerts.NotYetValidCertTempl)
		cert, err := TlsCertFromFile(p+".crt", p+".key", true)
		assert.ErrorContains(t, err, "not yet valid")
		assert.Zero(t, len(cert.Certificate))
	})
}

func Test_TlsCertFromX509(t *testing.T) {
	t.Run("RSA certificate", func(t *testing.T) {
		key := testcerts.GeneratePrivateKey(t, "rsa")
		raw, err := x509.CreateCertificate(rand.Reader, &testcerts.DefaultCertTempl, &testcerts.DefaultCertTempl, &key.(*rsa.PrivateKey).PublicKey, key.(*rsa.PrivateKey))
		require.NoError(t, err)
		cert := testcerts.DefaultCertTempl
		cert.Raw = raw
		tlsCert, err := TlsCertFromX509(&cert, key)
		require.NoError(t, err)
		require.NotNil(t, tlsCert)
	})
	t.Run("ECDSA certificate", func(t *testing.T) {
		key := testcerts.GeneratePrivateKey(t, "ecdsa")
		raw, err := x509.CreateCertificate(rand.Reader, &testcerts.DefaultCertTempl, &testcerts.DefaultCertTempl, &key.(*ecdsa.PrivateKey).PublicKey, key.(*ecdsa.PrivateKey))
		require.NoError(t, err)
		cert := testcerts.DefaultCertTempl
		cert.Raw = raw
		tlsCert, err := TlsCertFromX509(&cert, key)
		require.NoError(t, err)
		require.NotNil(t, tlsCert)
	})
}
