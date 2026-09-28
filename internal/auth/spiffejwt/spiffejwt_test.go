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

package spiffejwt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"regexp"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/spiffe/go-spiffe/v2/bundle/jwtbundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateTestJWT(t *testing.T, key *ecdsa.PrivateKey, spiffeID string, audience []string) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"),
	)
	require.NoError(t, err)

	now := time.Now()
	claims := josejwt.Claims{
		Subject:  spiffeID,
		Audience: audience,
		IssuedAt: josejwt.NewNumericDate(now),
		Expiry:   josejwt.NewNumericDate(now.Add(1 * time.Hour)),
	}
	token, err := josejwt.Signed(signer).Claims(claims).Serialize()
	require.NoError(t, err)
	return token
}

func setupBundleSource(t *testing.T, key *ecdsa.PrivateKey, trustDomainStr string) jwtbundle.Source {
	t.Helper()
	td, err := spiffeid.TrustDomainFromString(trustDomainStr)
	require.NoError(t, err)

	authorities := map[string]crypto.PublicKey{
		"test-key": &key.PublicKey,
	}
	bundle := jwtbundle.FromJWTAuthorities(td, authorities)

	set := jwtbundle.NewSet(bundle)
	return set
}

func TestAuthenticate_Success(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	spiffeID := "spiffe://example.org/argocd/agent/spoke-1"
	audience := "argocd-agent-principal"
	token := generateTestJWT(t, key, spiffeID, []string{audience})

	bundleSource := setupBundleSource(t, key, "example.org")

	regex := regexp.MustCompile(`spiffe://[^/]+/(.+)`)
	a := NewSPIFFEJWTAuthentication(regex, audience, bundleSource)

	agentID, err := a.Authenticate(context.Background(), auth.Credentials{"token": token})
	require.NoError(t, err)
	assert.Equal(t, "argocd/agent/spoke-1", agentID)
}

func TestAuthenticate_NoToken(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	bundleSource := setupBundleSource(t, key, "example.org")

	a := NewSPIFFEJWTAuthentication(nil, "aud", bundleSource)

	_, err = a.Authenticate(context.Background(), auth.Credentials{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no SPIFFE JWT token provided")
}

func TestAuthenticate_NilBundleSource(t *testing.T) {
	a := NewSPIFFEJWTAuthentication(nil, "aud", nil)

	_, err := a.Authenticate(context.Background(), auth.Credentials{"token": "some.jwt.token"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no SPIFFE JWT bundle source configured")
}

func TestAuthenticate_InvalidToken(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	bundleSource := setupBundleSource(t, key, "example.org")
	a := NewSPIFFEJWTAuthentication(nil, "aud", bundleSource)

	_, err = a.Authenticate(context.Background(), auth.Credentials{"token": "invalid.jwt.token"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT-SVID validation failed")
}

func TestAuthenticate_RegexNoMatch(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	spiffeID := "spiffe://example.org/other-service"
	audience := "aud"
	token := generateTestJWT(t, key, spiffeID, []string{audience})

	bundleSource := setupBundleSource(t, key, "example.org")

	regex := regexp.MustCompile(`spiffe://[^/]+/argocd/agent/(.+)`)
	a := NewSPIFFEJWTAuthentication(regex, audience, bundleSource)

	_, err = a.Authenticate(context.Background(), auth.Credentials{"token": token})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not match agent ID regex")
}

func TestAuthenticate_NoRegex_ReturnsError(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	spiffeID := "spiffe://example.org/argocd/agent/spoke-1"
	audience := "aud"
	token := generateTestJWT(t, key, spiffeID, []string{audience})

	bundleSource := setupBundleSource(t, key, "example.org")

	a := NewSPIFFEJWTAuthentication(nil, audience, bundleSource)

	_, err = a.Authenticate(context.Background(), auth.Credentials{"token": token})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no agent ID regex configured")
}

func TestAuthenticate_WrongAudience(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	token := generateTestJWT(t, key, "spiffe://example.org/agent", []string{"other-audience"})
	bundleSource := setupBundleSource(t, key, "example.org")

	a := NewSPIFFEJWTAuthentication(nil, "expected-audience", bundleSource)

	_, err = a.Authenticate(context.Background(), auth.Credentials{"token": token})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT-SVID validation failed")
}
