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

package mtls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"net/url"
	"regexp"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

func Test_Authenticate(t *testing.T) {
	regex := `open-cluster-management:cluster:([^:]+):addon:argocd-agent`
	auth := NewMTLSAuthentication(regexp.MustCompile(regex), IdentitySourceSubject)

	t.Run("No peer in context", func(t *testing.T) {
		ctx := context.Background()
		agentID, err := auth.Authenticate(ctx, nil)
		assert.Empty(t, agentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "could not get peer from context")
	})

	t.Run("No TLS credentials in context", func(t *testing.T) {
		ctx := peer.NewContext(context.Background(), &peer.Peer{})
		agentID, err := auth.Authenticate(ctx, nil)
		assert.Empty(t, agentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "connection requires TLS credentials but has none")
	})

	t.Run("No verified certificate chains in context", func(t *testing.T) {
		ctx := generateContext(nil)
		agentID, err := auth.Authenticate(ctx, nil)
		assert.Empty(t, agentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no verified certificates found")
	})

	t.Run("Regex does not match subject", func(t *testing.T) {
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "foobar",
			},
		}
		ctx := generateContext([][]*x509.Certificate{{cert}})
		agentID, err := auth.Authenticate(ctx, nil)
		assert.Empty(t, agentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not match the agent ID regex pattern")
	})

	t.Run("Agent ID is empty", func(t *testing.T) {
		auth := NewMTLSAuthentication(nil, IdentitySourceSubject)
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "open-cluster-management:cluster:cluster1:addon:argocd-agent",
			},
		}
		ctx := generateContext([][]*x509.Certificate{{cert}})

		agentID, err := auth.Authenticate(ctx, nil)
		assert.Empty(t, agentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "agent ID is empty")
	})

	t.Run("Agent ID is invalid", func(t *testing.T) {
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "open-cluster-management:cluster:lob/bo:addon:argocd-agent",
			},
		}
		ctx := generateContext([][]*x509.Certificate{{cert}})

		agentID, err := auth.Authenticate(ctx, nil)
		assert.Empty(t, agentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid agent ID in client certificate")
	})

	t.Run("Authenticated using the regex", func(t *testing.T) {
		subject := "open-cluster-management:cluster:cluster1:addon:argocd-agent"
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: subject,
			},
		}
		ctx := generateContext([][]*x509.Certificate{{cert}})
		agentID, err := auth.Authenticate(ctx, nil)
		assert.NoError(t, err)
		assert.Equal(t, "cluster1", agentID)
	})
}

func Test_AuthenticateWithSPIFFE(t *testing.T) {
	regex := `spiffe://ea1t\.us\.a/ns/argocd-agent/sa/(.+)`
	auth := NewMTLSAuthentication(regexp.MustCompile(regex), IdentitySourceURI)

	t.Run("No URI SANs in certificate", func(t *testing.T) {
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "argocd-agent",
			},
		}
		ctx := generateContext([][]*x509.Certificate{{cert}})
		agentID, err := auth.Authenticate(ctx, nil)
		assert.Empty(t, agentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no URI SANs found")
	})

	t.Run("URI does not match regex", func(t *testing.T) {
		spiffeURI, _ := url.Parse("spiffe://other-domain/ns/argocd-agent/sa/cluster1")
		cert := &x509.Certificate{
			URIs: []*url.URL{spiffeURI},
		}
		ctx := generateContext([][]*x509.Certificate{{cert}})
		agentID, err := auth.Authenticate(ctx, nil)
		assert.Empty(t, agentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not match the agent ID regex pattern")
	})

	t.Run("Authenticated using SPIFFE URI", func(t *testing.T) {
		spiffeURI, _ := url.Parse("spiffe://ea1t.us.a/ns/argocd-agent/sa/cluster-west-1")
		cert := &x509.Certificate{
			URIs: []*url.URL{spiffeURI},
		}
		ctx := generateContext([][]*x509.Certificate{{cert}})
		agentID, err := auth.Authenticate(ctx, nil)
		assert.NoError(t, err)
		assert.Equal(t, "cluster-west-1", agentID)
	})

	t.Run("Invalid agent ID in SPIFFE URI", func(t *testing.T) {
		spiffeURI, _ := url.Parse("spiffe://ea1t.us.a/ns/argocd-agent/sa/invalid/cluster")
		cert := &x509.Certificate{
			URIs: []*url.URL{spiffeURI},
		}
		ctx := generateContext([][]*x509.Certificate{{cert}})
		agentID, err := auth.Authenticate(ctx, nil)
		assert.Empty(t, agentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid agent ID")
	})
}

func generateContext(verifiedChains [][]*x509.Certificate) context.Context {
	p := &peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{
				VerifiedChains: verifiedChains,
			},
		},
	}
	return peer.NewContext(context.Background(), p)
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
