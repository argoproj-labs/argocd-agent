// Copyright 2025 The argocd-agent Authors
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
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_GenerateCaCertificate(t *testing.T) {
	// We'll require the CA cert for the tests to come
	certData, keyData, err := GenerateCaCertificate("test", DefaultCACertValidityDays, KeyGenOptions{})
	require.NoError(t, err)
	cert, err := tls.X509KeyPair([]byte(certData), []byte(keyData))
	require.NoError(t, err)
	require.NotNil(t, cert)
	require.NotNil(t, cert.Leaf)
	require.Equal(t, "test", cert.Leaf.Subject.CommonName)
	require.True(t, cert.Leaf.IsCA)

	t.Run("Generate client certificate", func(t *testing.T) {
		certSubj := "abcdefg"
		certData, keyData, err := GenerateClientCertificate(certSubj, cert.Leaf, cert.PrivateKey, DefaultLeafCertValidityDays, KeyGenOptions{})
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
		certData, keyData, err := GenerateServerCertificate(certSubj, cert.Leaf, cert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"}, DefaultLeafCertValidityDays, KeyGenOptions{})
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
		assert.Contains(t, ccert.Leaf.DNSNames, "localhost")
	})

	t.Run("custom validity days", func(t *testing.T) {
		const customDays = 42
		before := time.Now()
		certData, keyData, err := GenerateClientCertificate("custom-days", cert.Leaf, cert.PrivateKey, customDays, KeyGenOptions{})
		require.NoError(t, err)
		ccert, err := tls.X509KeyPair([]byte(certData), []byte(keyData))
		require.NoError(t, err)

		expectedNotAfter := before.AddDate(0, 0, customDays)
		assert.WithinDuration(t, expectedNotAfter, ccert.Leaf.NotAfter, 2*time.Second)
	})

	t.Run("reject invalid validity days", func(t *testing.T) {
		_, _, err := GenerateClientCertificate("bad-days", cert.Leaf, cert.PrivateKey, 0, KeyGenOptions{})
		require.Error(t, err)
		_, _, err = GenerateCaCertificate("bad-days", -1, KeyGenOptions{})
		require.Error(t, err)
	})
}

func Test_ValidateLeafValidityDays(t *testing.T) {
	caCert := &x509.Certificate{
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().AddDate(0, 0, 365),
	}

	t.Run("accept within CA window", func(t *testing.T) {
		err := ValidateLeafValidityDays(caCert, 30)
		require.NoError(t, err)
	})

	t.Run("accept up to CA expiry", func(t *testing.T) {
		maxDays := int(caCert.NotAfter.Sub(time.Now()).Hours() / 24)
		err := ValidateLeafValidityDays(caCert, maxDays)
		require.NoError(t, err)
	})

	t.Run("reject when exceeding CA expiry", func(t *testing.T) {
		err := ValidateLeafValidityDays(caCert, 9999)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds CA expiry")
	})

	t.Run("reject invalid validity days", func(t *testing.T) {
		err := ValidateLeafValidityDays(caCert, 0)
		require.Error(t, err)
	})
}

func Test_GenerateCertificatesWithAlgorithms(t *testing.T) {
	algorithms := []struct {
		name      string
		algorithm string
		rsaBits   int
	}{
		{name: "RSA 4096 default", algorithm: "", rsaBits: 0},
		{name: "RSA 2048", algorithm: "rsa", rsaBits: 2048},
		{name: "ECDSA P-256", algorithm: "ecdsa-p256", rsaBits: 0},
		{name: "ECDSA P-384", algorithm: "ecdsa-p384", rsaBits: 0},
		{name: "ECDSA P-521", algorithm: "ecdsa-p521", rsaBits: 0},
		{name: "Ed25519", algorithm: "ed25519", rsaBits: 0},
	}

	for _, tt := range algorithms {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := ParseKeyAlgorithm(tt.algorithm, tt.rsaBits)
			require.NoError(t, err)

			caCertPEM, caKeyPEM, err := GenerateCaCertificate("test-ca", DefaultCACertValidityDays, opts)
			require.NoError(t, err)
			caPair, err := tls.X509KeyPair([]byte(caCertPEM), []byte(caKeyPEM))
			require.NoError(t, err)
			require.True(t, caPair.Leaf.IsCA)

			if tt.algorithm == "" || tt.algorithm == "rsa" {
				keyType, keyLength := PublicKeySummary(caPair.Leaf.PublicKey)
				assert.Equal(t, "RSA", keyType)
				if tt.rsaBits == 0 {
					assert.Equal(t, 4096, keyLength)
				} else {
					assert.Equal(t, tt.rsaBits, keyLength)
				}
			}

			leafCertPEM, leafKeyPEM, err := GenerateClientCertificate("test-client", caPair.Leaf, caPair.PrivateKey, DefaultLeafCertValidityDays, opts)
			require.NoError(t, err)
			leafPair, err := tls.X509KeyPair([]byte(leafCertPEM), []byte(leafKeyPEM))
			require.NoError(t, err)
			assert.Equal(t, "test-client", leafPair.Leaf.Subject.CommonName)
			assert.Equal(t, "test-ca", leafPair.Leaf.Issuer.CommonName)
			assert.Contains(t, leafKeyPEM, "BEGIN PRIVATE KEY")
		})
	}
}
