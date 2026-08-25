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

package spire

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

var log = logging.GetDefaultLogger().ComponentLogger("spire")

// Source wraps SPIRE Workload API sources. It connects to the local SPIRE Agent
// and provides access to X.509 SVIDs (for server TLS) and JWT-SVIDs (for client auth).
type Source struct {
	x509Source *workloadapi.X509Source
	jwtSource  *workloadapi.JWTSource
	socketPath string
}

// New connects to the SPIRE Agent at the given socket path and returns a Source.
// The socketPath should be a URI like "unix:///run/spire/sockets/agent.sock".
func New(ctx context.Context, socketPath string) (*Source, error) {
	log.Infof("Connecting to SPIRE Agent at %s", socketPath)

	clientOpts := workloadapi.WithClientOptions(workloadapi.WithAddr(socketPath))

	x509Source, err := workloadapi.NewX509Source(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create SPIRE X509Source: %w", err)
	}

	jwtSource, err := workloadapi.NewJWTSource(ctx, clientOpts)
	if err != nil {
		if closeErr := x509Source.Close(); closeErr != nil {
			log.Warnf("Failed to close X509Source during cleanup: %v", closeErr)
		}
		return nil, fmt.Errorf("failed to create SPIRE JWTSource: %w", err)
	}

	log.Infof("Connected to SPIRE Agent, X.509 and JWT sources ready")
	return &Source{
		x509Source: x509Source,
		jwtSource:  jwtSource,
		socketPath: socketPath,
	}, nil
}

// GetCertificate returns a server-side callback for tls.Config.GetCertificate.
func (s *Source) GetCertificate() func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return tlsconfig.GetCertificate(s.x509Source)
}

// TrustBundle returns the X.509 trust bundle for the local trust domain.
func (s *Source) TrustBundle() (*x509.CertPool, error) {
	svid, err := s.x509Source.GetX509SVID()
	if err != nil {
		return nil, fmt.Errorf("failed to get SVID for trust domain: %w", err)
	}
	bundle, err := s.x509Source.GetX509BundleForTrustDomain(svid.ID.TrustDomain())
	if err != nil {
		return nil, fmt.Errorf("failed to get trust bundle: %w", err)
	}
	pool := x509.NewCertPool()
	for _, cert := range bundle.X509Authorities() {
		pool.AddCert(cert)
	}
	return pool, nil
}

// FetchJWTSVID fetches a JWT-SVID from the local SPIRE Agent for the given audience.
// The returned token string can be sent as a bearer token in gRPC metadata.
func (s *Source) FetchJWTSVID(ctx context.Context, audience string) (string, error) {
	svid, err := s.jwtSource.FetchJWTSVID(ctx, jwtsvid.Params{
		Audience: audience,
	})
	if err != nil {
		return "", fmt.Errorf("failed to fetch JWT-SVID: %w", err)
	}
	log.Debugf("Fetched JWT-SVID for audience %q, SPIFFE ID: %s, expires: %s",
		audience, svid.ID, svid.Expiry)
	return svid.Marshal(), nil
}

// JWTSource returns the underlying JWTSource for use in JWT bundle validation.
func (s *Source) JWTSource() *workloadapi.JWTSource {
	return s.jwtSource
}

// X509Source returns the underlying X509Source.
func (s *Source) X509Source() *workloadapi.X509Source {
	return s.x509Source
}

// Close releases all connections to the SPIRE Agent.
func (s *Source) Close() error {
	var firstErr error
	if s.jwtSource != nil {
		if err := s.jwtSource.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.x509Source != nil {
		if err := s.x509Source.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
