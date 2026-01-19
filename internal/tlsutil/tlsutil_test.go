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

package tlsutil

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
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
		cert, err := TLSCertFromFile(p+".crt", p+".key", true)
		assert.NoError(t, err)
		assert.NotZero(t, len(cert.Certificate))
	})

	t.Run("Load valid ECDSA certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "ecdsa", p, testcerts.DefaultCertTempl)
		cert, err := TLSCertFromFile(p+".crt", p+".key", true)
		assert.NoError(t, err)
		assert.NotZero(t, len(cert.Certificate))
	})

	t.Run("Load valid ECDH certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "ecdh", p, testcerts.DefaultCertTempl)
		cert, err := TLSCertFromFile(p+".crt", p+".key", true)
		assert.NoError(t, err)
		assert.NotZero(t, len(cert.Certificate))
	})

	t.Run("Load valid ED25519 certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "ed25519", p, testcerts.DefaultCertTempl)
		cert, err := TLSCertFromFile(p+".crt", p+".key", true)
		assert.NoError(t, err)
		assert.NotZero(t, len(cert.Certificate))
	})

	t.Run("Fail on expired certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "rsa", p, testcerts.ExpiredCertTempl)
		cert, err := TLSCertFromFile(p+".crt", p+".key", true)
		assert.ErrorContains(t, err, "expired")
		assert.Zero(t, len(cert.Certificate))
	})

	t.Run("Do not fail on expired certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "rsa", p, testcerts.ExpiredCertTempl)
		cert, err := TLSCertFromFile(p+".crt", p+".key", false)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(cert.Certificate))
	})
	t.Run("Fail on not yet valid certificate key pair", func(t *testing.T) {
		d := t.TempDir()
		p := path.Join(d, "test-cert")
		testcerts.WriteSelfSignedCert(t, "rsa", p, testcerts.NotYetValidCertTempl)
		cert, err := TLSCertFromFile(p+".crt", p+".key", true)
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
		tlsCert, err := TLSCertFromX509(&cert, key)
		require.NoError(t, err)
		require.NotNil(t, tlsCert)
	})
	t.Run("ECDSA certificate", func(t *testing.T) {
		key := testcerts.GeneratePrivateKey(t, "ecdsa")
		raw, err := x509.CreateCertificate(rand.Reader, &testcerts.DefaultCertTempl, &testcerts.DefaultCertTempl, &key.(*ecdsa.PrivateKey).PublicKey, key.(*ecdsa.PrivateKey))
		require.NoError(t, err)
		cert := testcerts.DefaultCertTempl
		cert.Raw = raw
		tlsCert, err := TLSCertFromX509(&cert, key)
		require.NoError(t, err)
		require.NotNil(t, tlsCert)
	})
}

func Test_TLSVersionName(t *testing.T) {
	t.Run("Known TLS versions", func(t *testing.T) {
		assert.Equal(t, "tls1.1", TLSVersionName(tls.VersionTLS11))
		assert.Equal(t, "tls1.2", TLSVersionName(tls.VersionTLS12))
		assert.Equal(t, "tls1.3", TLSVersionName(tls.VersionTLS13))
	})

	t.Run("Unknown TLS version", func(t *testing.T) {
		name := TLSVersionName(0x0999)
		assert.Contains(t, name, "unknown")
		assert.Contains(t, name, "0x0999")
	})
}

func Test_TLSVersionFromName(t *testing.T) {
	t.Run("Valid TLS version names", func(t *testing.T) {
		v, err := TLSVersionFromName("tls1.1")
		assert.NoError(t, err)
		assert.Equal(t, uint16(tls.VersionTLS11), v)

		v, err = TLSVersionFromName("tls1.2")
		assert.NoError(t, err)
		assert.Equal(t, uint16(tls.VersionTLS12), v)

		v, err = TLSVersionFromName("tls1.3")
		assert.NoError(t, err)
		assert.Equal(t, uint16(tls.VersionTLS13), v)
	})

	t.Run("Invalid TLS version names", func(t *testing.T) {
		for _, name := range []string{"tls1.0", "ssl3.0", "invalid", "tls"} {
			v, err := TLSVersionFromName(name)
			assert.Error(t, err)
			assert.Equal(t, uint16(0), v)
		}
	})
}

func Test_ParseCipherSuites(t *testing.T) {
	t.Run("Single valid cipher suite", func(t *testing.T) {
		cs := tls.CipherSuites()[0]
		ids, err := ParseCipherSuites([]string{cs.Name})
		assert.NoError(t, err)
		assert.Equal(t, []uint16{cs.ID}, ids)
	})

	t.Run("Multiple valid cipher suites", func(t *testing.T) {
		ciphers := tls.CipherSuites()
		if len(ciphers) >= 2 {
			ids, err := ParseCipherSuites([]string{ciphers[0].Name, ciphers[1].Name})
			assert.NoError(t, err)
			assert.Equal(t, []uint16{ciphers[0].ID, ciphers[1].ID}, ids)
		}
	})

	t.Run("Empty cipher suites", func(t *testing.T) {
		ids, err := ParseCipherSuites([]string{})
		assert.NoError(t, err)
		assert.Nil(t, ids)
	})

	t.Run("Invalid cipher suite", func(t *testing.T) {
		ids, err := ParseCipherSuites([]string{"cowabunga"})
		assert.Error(t, err)
		assert.Nil(t, ids)
	})

	t.Run("Mix of valid and invalid cipher suites", func(t *testing.T) {
		cs := tls.CipherSuites()[0]
		ids, err := ParseCipherSuites([]string{cs.Name, "invalid"})
		assert.Error(t, err)
		assert.Nil(t, ids)
	})
}

func Test_ValidateTLSConfig(t *testing.T) {
	t.Run("Valid configuration with min < max", func(t *testing.T) {
		err := ValidateTLSConfig(tls.VersionTLS12, tls.VersionTLS13, nil)
		assert.NoError(t, err)
	})

	t.Run("Valid configuration with min == max", func(t *testing.T) {
		err := ValidateTLSConfig(tls.VersionTLS12, tls.VersionTLS12, nil)
		assert.NoError(t, err)
	})

	t.Run("Invalid configuration with min > max", func(t *testing.T) {
		err := ValidateTLSConfig(tls.VersionTLS13, tls.VersionTLS12, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "minimum TLS version")
		assert.Contains(t, err.Error(), "cannot be higher than maximum TLS version")
	})

	t.Run("Valid configuration with only min set", func(t *testing.T) {
		err := ValidateTLSConfig(tls.VersionTLS12, 0, nil)
		assert.NoError(t, err)
	})

	t.Run("Valid configuration with only max set", func(t *testing.T) {
		err := ValidateTLSConfig(0, tls.VersionTLS13, nil)
		assert.NoError(t, err)
	})

	t.Run("Valid cipher suite for TLS 1.2", func(t *testing.T) {
		// Find a cipher that supports TLS 1.2
		var tls12Cipher *tls.CipherSuite
		for _, cs := range tls.CipherSuites() {
			for _, v := range cs.SupportedVersions {
				if v == tls.VersionTLS12 {
					tls12Cipher = cs
					break
				}
			}
			if tls12Cipher != nil {
				break
			}
		}
		if tls12Cipher != nil {
			err := ValidateTLSConfig(tls.VersionTLS12, 0, []uint16{tls12Cipher.ID})
			assert.NoError(t, err)
		}
	})

	t.Run("Incompatible cipher suite for TLS 1.3", func(t *testing.T) {
		// Find a cipher that does NOT support TLS 1.3 (TLS 1.2 only ciphers)
		var tls12OnlyCipher *tls.CipherSuite
		for _, cs := range tls.CipherSuites() {
			supportsTLS13 := false
			for _, v := range cs.SupportedVersions {
				if v == tls.VersionTLS13 {
					supportsTLS13 = true
					break
				}
			}
			if !supportsTLS13 {
				tls12OnlyCipher = cs
				break
			}
		}
		if tls12OnlyCipher != nil {
			err := ValidateTLSConfig(tls.VersionTLS13, 0, []uint16{tls12OnlyCipher.ID})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "is not supported by minimum TLS version")
		}
	})

	t.Run("Empty cipher suites should pass validation", func(t *testing.T) {
		err := ValidateTLSConfig(tls.VersionTLS13, 0, []uint16{})
		assert.NoError(t, err)
	})
}
