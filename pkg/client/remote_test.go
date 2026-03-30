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

package client

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"path"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/auth/userpass"
	"github.com/argoproj-labs/argocd-agent/internal/issuer"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/principal"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Connect(t *testing.T) {
	tempDir := t.TempDir()
	basePath := path.Join(tempDir, "certs")
	testcerts.WriteSelfSignedCert(t, "rsa", basePath, x509.Certificate{SerialNumber: big.NewInt(1)})

	s, err := principal.NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps("default"), "default",
		principal.WithGRPC(true),
		principal.WithListenerPort(0),
		principal.WithTLSKeyPairFromPath(basePath+".crt", basePath+".key"),
		principal.WithGeneratedTokenSigningKey(),
	)

	am := userpass.NewUserPassAuthentication("")
	am.UpsertUser("default", "password")
	s.AuthMethodsForE2EOnly().RegisterMethod("userpass", am)
	require.NoError(t, err)
	errch := make(chan error)
	err = s.Start(context.Background(), errch)
	require.NoError(t, err)

	t.Cleanup(func() {
		if s != nil {
			require.NoError(t, s.Shutdown())
		}
	})

	t.Run("Connect to a server", func(t *testing.T) {
		r, err := NewRemote("127.0.0.1", s.ListenerForE2EOnly().Port(),
			WithInsecureSkipTLSVerify(),
			WithAuth("userpass", auth.Credentials{userpass.ClientIDField: "default", userpass.ClientSecretField: "password"}),
			WithClientMode(types.AgentModeManaged),
		)
		require.NoError(t, err)
		require.NotNil(t, r)
		ctx, cancelFn := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelFn()
		err = r.Connect(ctx, false)
		assert.NoError(t, err)
		assert.NotNil(t, r.conn)
		require.NotNil(t, r.accessToken)
		require.NotNil(t, r.accessToken.Claims)
		sub, err := r.accessToken.Claims.GetSubject()
		assert.NoError(t, err)
		assert.NotEmpty(t, sub)
		authSub, err := auth.ParseAuthSubject(sub)
		require.NoError(t, err)
		assert.Equal(t, "default", authSub.ClientID)
	})

	t.Run("Invalid auth and context deadline reached", func(t *testing.T) {
		r, err := NewRemote("127.0.0.1", s.ListenerForE2EOnly().Port(),
			WithInsecureSkipTLSVerify(),
			WithAuth("userpass", auth.Credentials{userpass.ClientIDField: "default", userpass.ClientSecretField: "passwor"}),
		)
		require.NoError(t, err)
		require.NotNil(t, r)
		ctx, cancelFn := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelFn()
		err = r.Connect(ctx, false)
		assert.Error(t, err)
		assert.Nil(t, r.conn)
	})

}

func Test_WithMinimumTLSVersion(t *testing.T) {
	t.Run("All valid minimum TLS versions", func(t *testing.T) {
		versions := map[string]uint16{
			"tls1.1": tls.VersionTLS11,
			"tls1.2": tls.VersionTLS12,
			"tls1.3": tls.VersionTLS13,
		}
		for k, v := range versions {
			r, err := NewRemote("localhost", 443, WithMinimumTLSVersion(k))
			assert.NoError(t, err)
			assert.Equal(t, v, r.tlsConfig.MinVersion)
		}
	})

	t.Run("Invalid minimum TLS versions", func(t *testing.T) {
		for _, v := range []string{"tls1.0", "ssl3.0", "invalid", "tls"} {
			r, err := NewRemote("localhost", 443, WithMinimumTLSVersion(v))
			assert.Error(t, err)
			assert.Nil(t, r)
		}
	})
}

func Test_WithMaximumTLSVersion(t *testing.T) {
	t.Run("All valid maximum TLS versions", func(t *testing.T) {
		versions := map[string]uint16{
			"tls1.1": tls.VersionTLS11,
			"tls1.2": tls.VersionTLS12,
			"tls1.3": tls.VersionTLS13,
		}
		for k, v := range versions {
			r, err := NewRemote("localhost", 443, WithMaximumTLSVersion(k))
			assert.NoError(t, err)
			assert.Equal(t, v, r.tlsConfig.MaxVersion)
		}
	})

	t.Run("Invalid maximum TLS versions", func(t *testing.T) {
		for _, v := range []string{"tls1.0", "ssl3.0", "invalid", "tls"} {
			r, err := NewRemote("localhost", 443, WithMaximumTLSVersion(v))
			assert.Error(t, err)
			assert.Nil(t, r)
		}
	})
}

func Test_WithTLSCipherSuites(t *testing.T) {
	t.Run("Single valid cipher suite", func(t *testing.T) {
		cs := tls.CipherSuites()[0]
		r, err := NewRemote("localhost", 443, WithTLSCipherSuites([]string{cs.Name}))
		assert.NoError(t, err)
		assert.Equal(t, []uint16{cs.ID}, r.tlsConfig.CipherSuites)
	})

	t.Run("Multiple valid cipher suites", func(t *testing.T) {
		ciphers := tls.CipherSuites()
		if len(ciphers) >= 2 {
			r, err := NewRemote("localhost", 443, WithTLSCipherSuites([]string{ciphers[0].Name, ciphers[1].Name}))
			assert.NoError(t, err)
			assert.Equal(t, []uint16{ciphers[0].ID, ciphers[1].ID}, r.tlsConfig.CipherSuites)
		}
	})

	t.Run("Empty cipher suites", func(t *testing.T) {
		r, err := NewRemote("localhost", 443, WithTLSCipherSuites([]string{}))
		assert.NoError(t, err)
		assert.Nil(t, r.tlsConfig.CipherSuites)
	})

	t.Run("Invalid cipher suite", func(t *testing.T) {
		r, err := NewRemote("localhost", 443, WithTLSCipherSuites([]string{"cowabunga"}))
		assert.Error(t, err)
		assert.Nil(t, r)
	})
}

func Test_validateTLSConfig(t *testing.T) {
	t.Run("Valid configuration with min < max", func(t *testing.T) {
		r, err := NewRemote("localhost", 443,
			WithMinimumTLSVersion("tls1.2"),
			WithMaximumTLSVersion("tls1.3"),
		)
		assert.NoError(t, err)
		assert.NotNil(t, r)
	})

	t.Run("Valid configuration with min == max", func(t *testing.T) {
		r, err := NewRemote("localhost", 443,
			WithMinimumTLSVersion("tls1.2"),
			WithMaximumTLSVersion("tls1.2"),
		)
		assert.NoError(t, err)
		assert.NotNil(t, r)
	})

	t.Run("Invalid configuration with min > max", func(t *testing.T) {
		r, err := NewRemote("localhost", 443,
			WithMinimumTLSVersion("tls1.3"),
			WithMaximumTLSVersion("tls1.2"),
		)
		assert.Error(t, err)
		assert.Nil(t, r)
		assert.Contains(t, err.Error(), "minimum TLS version")
		assert.Contains(t, err.Error(), "cannot be higher than maximum TLS version")
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
			r, err := NewRemote("localhost", 443,
				WithMinimumTLSVersion("tls1.3"),
				WithTLSCipherSuites([]string{tls12OnlyCipher.Name}),
			)
			assert.Error(t, err)
			assert.Nil(t, r)
			assert.Contains(t, err.Error(), "is not supported by minimum TLS version")
		}
	})
}

// issueTestToken creates a signed JWT with the given subject and expiry using the provided issuer.
func issueTestToken(t *testing.T, iss issuer.Issuer, subject string, expiry time.Duration) *token {
	t.Helper()
	raw, err := iss.IssueAccessToken(subject, expiry)
	require.NoError(t, err)
	tok, err := NewToken(raw)
	require.NoError(t, err)
	return tok
}

func issueTestRefreshToken(t *testing.T, iss issuer.Issuer, subject string, expiry time.Duration) *token {
	t.Helper()
	raw, err := iss.IssueRefreshToken(subject, expiry)
	require.NoError(t, err)
	tok, err := NewToken(raw)
	require.NoError(t, err)
	return tok
}

func Test_getValidAccessToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	iss, err := issuer.NewIssuer("test", issuer.WithRSAPrivateKey(key))
	require.NoError(t, err)

	subject := `{"clientID":"test-agent","mode":"managed"}`

	t.Run("returns empty string when no tokens are set", func(t *testing.T) {
		r := &Remote{}
		tok := r.getValidAccessToken(context.Background())
		assert.Equal(t, "", tok)
	})

	t.Run("returns access token when refresh token is nil", func(t *testing.T) {
		r := &Remote{
			accessToken: issueTestToken(t, iss, subject, 5*time.Minute),
		}
		tok := r.getValidAccessToken(context.Background())
		assert.Equal(t, r.accessToken.RawToken, tok)
	})

	t.Run("returns current token when not near expiry", func(t *testing.T) {
		r := &Remote{
			accessToken:  issueTestToken(t, iss, subject, 5*time.Minute),
			refreshToken: issueTestRefreshToken(t, iss, subject, 24*time.Hour),
		}
		originalToken := r.accessToken.RawToken
		tok := r.getValidAccessToken(context.Background())
		assert.Equal(t, originalToken, tok)
	})

	t.Run("attempts refresh when token is near expiry", func(t *testing.T) {
		r := &Remote{
			accessToken:  issueTestToken(t, iss, subject, 10*time.Second),
			refreshToken: issueTestRefreshToken(t, iss, subject, 24*time.Hour),
		}
		originalToken := r.accessToken.RawToken
		tok := r.getValidAccessToken(context.Background())
		assert.Equal(t, originalToken, tok)
	})

	t.Run("attempts refresh when token is already expired", func(t *testing.T) {
		r := &Remote{
			accessToken:  issueTestToken(t, iss, subject, 1*time.Millisecond),
			refreshToken: issueTestRefreshToken(t, iss, subject, 24*time.Hour),
		}
		time.Sleep(5 * time.Millisecond)
		originalToken := r.accessToken.RawToken
		tok := r.getValidAccessToken(context.Background())
		assert.Equal(t, originalToken, tok)
	})
}

func Test_TokenRefresh(t *testing.T) {
	tempDir := t.TempDir()
	basePath := path.Join(tempDir, "certs")
	testcerts.WriteSelfSignedCert(t, "rsa", basePath, x509.Certificate{SerialNumber: big.NewInt(1)})

	s, err := principal.NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps("default"), "default",
		principal.WithGRPC(true),
		principal.WithListenerPort(0),
		principal.WithTLSKeyPairFromPath(basePath+".crt", basePath+".key"),
		principal.WithGeneratedTokenSigningKey(),
	)
	require.NoError(t, err)

	am := userpass.NewUserPassAuthentication("")
	am.UpsertUser("default", "password")
	s.AuthMethodsForE2EOnly().RegisterMethod("userpass", am)

	errch := make(chan error)
	err = s.Start(context.Background(), errch)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, s.Shutdown())
	})

	t.Run("token refresh succeeds with valid connection", func(t *testing.T) {
		r, err := NewRemote("127.0.0.1", s.ListenerForE2EOnly().Port(),
			WithInsecureSkipTLSVerify(),
			WithAuth("userpass", auth.Credentials{userpass.ClientIDField: "default", userpass.ClientSecretField: "password"}),
			WithClientMode(types.AgentModeManaged),
		)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = r.Connect(ctx, false)
		require.NoError(t, err)

		originalAccessToken := r.accessToken.RawToken
		originalRefreshToken := r.refreshToken.RawToken

		// Create a new access token that will expire in 1 second
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		iss, err := issuer.NewIssuer("test", issuer.WithRSAPrivateKey(key))
		require.NoError(t, err)
		r.accessToken = issueTestToken(t, iss, `{"clientID":"default","mode":"managed"}`, 1*time.Second)
		nearExpiry := r.accessToken.RawToken

		tok := r.getValidAccessToken(ctx)

		assert.NotEmpty(t, tok)
		assert.NotEqual(t, tok, nearExpiry, "token should have been refreshed from the near-expiry token")
		assert.NotEqual(t, tok, originalAccessToken, "refreshed token should differ from the original")

		// Refresh token should remain unchanged
		assert.Equal(t, originalRefreshToken, r.refreshToken.RawToken)
	})
}
