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
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ParsePrivateKeyFromPEM(t *testing.T) {
	t.Run("PKCS#8 round trip", func(t *testing.T) {
		key, err := GeneratePrivateKey(KeyGenOptions{})
		require.NoError(t, err)

		pemData, err := PrivateKeyToPEM(key)
		require.NoError(t, err)

		parsed, err := ParsePrivateKeyFromPEM([]byte(pemData))
		require.NoError(t, err)
		assert.IsType(t, key, parsed)
	})

	t.Run("PKCS#1 RSA key", func(t *testing.T) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)

		block := &pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key),
		}
		parsed, err := ParsePrivateKeyFromPEM(pem.EncodeToMemory(block))
		require.NoError(t, err)
		assert.IsType(t, &rsa.PrivateKey{}, parsed)
	})
}

func Test_ParseKeyAlgorithm(t *testing.T) {
	t.Run("defaults for empty values", func(t *testing.T) {
		opts, err := ParseKeyAlgorithm("", 0)
		require.NoError(t, err)
		assert.Equal(t, "rsa", opts.Algorithm)
		assert.Equal(t, 4096, opts.RSABits)
	})

	t.Run("valid RSA sizes", func(t *testing.T) {
		for _, size := range []int{2048, 3072, 4096} {
			opts, err := ParseKeyAlgorithm("rsa", size)
			require.NoError(t, err)
			assert.Equal(t, "rsa", opts.Algorithm)
			assert.Equal(t, size, opts.RSABits)
		}
	})

	t.Run("valid ECDSA and Ed25519", func(t *testing.T) {
		for _, alg := range []string{"ecdsa-p256", "ecdsa-p384", "ecdsa-p521", "ed25519"} {
			opts, err := ParseKeyAlgorithm(alg, 4096)
			require.NoError(t, err)
			assert.Equal(t, alg, opts.Algorithm)
		}
	})

	t.Run("reject invalid algorithm", func(t *testing.T) {
		_, err := ParseKeyAlgorithm("ecdh-p256", 0)
		require.Error(t, err)
	})

	t.Run("reject invalid RSA size", func(t *testing.T) {
		_, err := ParseKeyAlgorithm("rsa", 1024)
		require.Error(t, err)
	})
}

func Test_GeneratePrivateKey(t *testing.T) {
	tests := []struct {
		name      string
		algorithm string
		rsaBits   int
	}{
		{name: "RSA 4096", algorithm: "rsa", rsaBits: 4096},
		{name: "RSA 2048", algorithm: "rsa", rsaBits: 2048},
		{name: "ECDSA P-256", algorithm: "ecdsa-p256"},
		{name: "ECDSA P-384", algorithm: "ecdsa-p384"},
		{name: "ECDSA P-521", algorithm: "ecdsa-p521"},
		{name: "Ed25519", algorithm: "ed25519"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := ParseKeyAlgorithm(tt.algorithm, tt.rsaBits)
			require.NoError(t, err)

			key, err := GeneratePrivateKey(opts)
			require.NoError(t, err)

			pem, err := PrivateKeyToPEM(key)
			require.NoError(t, err)
			assert.Contains(t, pem, "BEGIN PRIVATE KEY")

			pub, err := publicKey(key)
			require.NoError(t, err)

			keyType, keyLength := PublicKeySummary(pub)
			switch tt.algorithm {
			case "rsa":
				assert.Equal(t, "RSA", keyType)
				assert.Equal(t, tt.rsaBits, keyLength)
				_, ok := key.(*rsa.PrivateKey)
				assert.True(t, ok)
			case "ecdsa-p256":
				assert.Equal(t, "ECDSA", keyType)
				assert.Equal(t, 256, keyLength)
				_, ok := key.(*ecdsa.PrivateKey)
				assert.True(t, ok)
			case "ecdsa-p384":
				assert.Equal(t, "ECDSA", keyType)
				assert.Equal(t, 384, keyLength)
				_, ok := key.(*ecdsa.PrivateKey)
				assert.True(t, ok)
			case "ecdsa-p521":
				assert.Equal(t, "ECDSA", keyType)
				assert.Equal(t, 521, keyLength)
				_, ok := key.(*ecdsa.PrivateKey)
				assert.True(t, ok)
			case "ed25519":
				assert.Equal(t, "Ed25519", keyType)
				assert.Equal(t, 256, keyLength)
				_, ok := key.(ed25519.PrivateKey)
				assert.True(t, ok)
			}
		})
	}
}
