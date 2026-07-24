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
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

var log = logging.GetDefaultLogger().ComponentLogger("spire")

// Source wraps a SPIRE Workload API X509Source. It connects to the local
// SPIRE Agent and provides access to X.509 SVIDs and trust bundles.
type Source struct {
	x509Source *workloadapi.X509Source
}

// New connects to the SPIRE Agent at the given socket path and returns a Source.
// The socketPath should be a URI like "unix:///run/spire/sockets/agent.sock".
// The caller must call Close when the Source is no longer needed.
func New(ctx context.Context, socketPath string) (*Source, error) {
	log.Infof("Connecting to SPIRE Agent at %s", socketPath)
	x509Source, err := workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(socketPath)))
	if err != nil {
		return nil, fmt.Errorf("failed to create SPIRE X509Source: %w", err)
	}
	log.Infof("Connected to SPIRE Agent, SVID obtained")
	return &Source{x509Source: x509Source}, nil
}

// GetCertificate returns a server-side callback for tls.Config.GetCertificate
func (s *Source) GetCertificate() func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return tlsconfig.GetCertificate(s.x509Source)
}

// GetClientCertificate returns a client-side callback for tls.Config.GetClientCertificate
func (s *Source) GetClientCertificate() func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return tlsconfig.GetClientCertificate(s.x509Source)
}

// TrustBundle returns the trust bundle for the SPIRE Agent.
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

// X509Source returns the underlying workloadapi.X509Source, which implements
// both x509svid.Source and x509bundle.Source interfaces needed by the
// go-spiffe tlsconfig helpers.
func (s *Source) X509Source() *workloadapi.X509Source {
	return s.x509Source
}

// Close releases the connection to the SPIRE Agent.
// This must be called when the Source is no longer needed.
func (s *Source) Close() error {
	if s.x509Source == nil {
		return nil
	}
	return s.x509Source.Close()
}
