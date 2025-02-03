package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_GenerateCaCertificate(t *testing.T) {
	// We'll require the CA cert for the tests to come
	certData, keyData, err := GenerateCaCertificate("test")
	require.NoError(t, err)
	cert, err := tls.X509KeyPair([]byte(certData), []byte(keyData))
	require.NoError(t, err)
	require.NotNil(t, cert)
	require.NotNil(t, cert.Leaf)
	require.Equal(t, "test", cert.Leaf.Subject.CommonName)
	require.True(t, cert.Leaf.IsCA)

	t.Run("Generate client certificate", func(t *testing.T) {
		certSubj := "abcdefg"
		certData, keyData, err := GenerateClientCertificate(certSubj, cert.Leaf, cert.PrivateKey)
		require.NoError(t, err)
		ccert, err := tls.X509KeyPair([]byte(certData), []byte(keyData))
		require.NoError(t, err)
		require.NotNil(t, ccert)
		assert.Contains(t, ccert.Leaf.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
		assert.NotContains(t, ccert.Leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
		assert.Equal(t, certSubj, ccert.Leaf.Subject.CommonName)
		assert.Equal(t, "test", ccert.Leaf.Issuer.CommonName)
	})
	t.Run("Generate server certificate", func(t *testing.T) {
		certSubj := "abcdefg"
		certData, keyData, err := GenerateServerCertificate(certSubj, cert.Leaf, cert.PrivateKey, []string{"IP:127.0.0.1"})
		require.NoError(t, err)
		ccert, err := tls.X509KeyPair([]byte(certData), []byte(keyData))
		require.NoError(t, err)
		require.NotNil(t, ccert)
		assert.Contains(t, ccert.Leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
		assert.NotContains(t, ccert.Leaf.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
		assert.Equal(t, certSubj, ccert.Leaf.Subject.CommonName)
		assert.Equal(t, "test", ccert.Leaf.Issuer.CommonName)
		ip := net.ParseIP("127.0.0.1")
		assert.Contains(t, ccert.Leaf.IPAddresses, ip[len(ip)-4:])
	})

}
