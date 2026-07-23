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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/url"
	"os"
	"time"

	workloadPB "github.com/spiffe/go-spiffe/v2/proto/spiffe/workload"
	"google.golang.org/grpc"
)

// Server is a fake SPIFFE Workload API gRPC server that issues SVIDs from an in-memory CA.
type Server struct {
	workloadPB.UnimplementedSpiffeWorkloadAPIServer
	grpcServer  *grpc.Server
	listener    net.Listener
	caCert      *x509.Certificate
	caKey       *ecdsa.PrivateKey
	svidCertDER []byte
	svidKeyDER  []byte
	spiffeID    string
	trustDomain string
}

// New creates a fake Workload API server that issues SVIDs for the given spiffeID.
func New(spiffeID string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (*Server, error) {
	trustDomain, err := trustDomainFromID(spiffeID)
	if err != nil {
		return nil, err
	}

	s := &Server{
		spiffeID:    spiffeID,
		trustDomain: trustDomain,
		caCert:      caCert,
		caKey:       caKey,
	}

	if s.caCert == nil || s.caKey == nil {
		if err := s.generateCA(); err != nil {
			return nil, err
		}
	}
	if err := s.generateSVID(); err != nil {
		return nil, err
	}

	return s, nil
}

// CACert returns the server's CA certificate.
func (s *Server) CACert() *x509.Certificate {
	return s.caCert
}

// CAKey returns the server's CA private key.
func (s *Server) CAKey() *ecdsa.PrivateKey {
	return s.caKey
}

// Listen creates a Unix domain socket at the given path and starts accepting connections.
func (s *Server) Listen(socketPath string) error {
	_ = os.Remove(socketPath)

	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	s.listener = lis

	s.grpcServer = grpc.NewServer()
	workloadPB.RegisterSpiffeWorkloadAPIServer(s.grpcServer, s)
	return nil
}

// Serve starts accepting connections on the listener created by Listen.
// It blocks until the server is stopped.
func (s *Server) Serve() error {
	return s.grpcServer.Serve(s.listener)
}

// Stop stops the gRPC server.
func (s *Server) Stop() {
	if s.grpcServer != nil {
		s.grpcServer.Stop()
	}
}

// FetchX509SVID implements the streaming Workload API method.
// It sends one response containing the SVID and then blocks until the client
// disconnects, matching real SPIRE Agent behavior.
func (s *Server) FetchX509SVID(_ *workloadPB.X509SVIDRequest, stream grpc.ServerStreamingServer[workloadPB.X509SVIDResponse]) error {
	resp := &workloadPB.X509SVIDResponse{
		Svids: []*workloadPB.X509SVID{
			{
				SpiffeId:    s.spiffeID,
				X509Svid:    s.svidCertDER,
				X509SvidKey: s.svidKeyDER,
				Bundle:      s.caCert.Raw,
			},
		},
	}

	if err := stream.Send(resp); err != nil {
		return err
	}

	// Block until the client disconnects
	<-stream.Context().Done()
	return nil
}

// FetchX509Bundles implements the streaming Workload API method.
func (s *Server) FetchX509Bundles(_ *workloadPB.X509BundlesRequest, stream grpc.ServerStreamingServer[workloadPB.X509BundlesResponse]) error {
	resp := &workloadPB.X509BundlesResponse{
		Bundles: map[string][]byte{
			s.trustDomain: s.caCert.Raw,
		},
	}

	if err := stream.Send(resp); err != nil {
		return err
	}

	<-stream.Context().Done()
	return nil
}

// generateCA generates a new CA certificate and key.
func (s *Server) generateCA() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "Fake SPIRE CA",
		},
		URIs:                  []*url.URL{mustParseURL(s.trustDomain)},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}

	s.caCert = cert
	s.caKey = key
	return nil
}

// generateSVID generates a new SVID certificate and key.
func (s *Server) generateSVID() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "SVID",
		},
		URIs:                  []*url.URL{mustParseURL(s.spiffeID)},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, s.caCert, &key.PublicKey, s.caKey)
	if err != nil {
		return err
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}

	s.svidCertDER = certDER
	s.svidKeyDER = keyDER
	return nil
}

// trustDomainFromID extracts the trust domain from a spiffeID.
func trustDomainFromID(spiffeID string) (string, error) {
	u, err := url.Parse(spiffeID)
	if err != nil {
		return "", err
	}
	return "spiffe://" + u.Host, nil
}

// mustParseURL parses a URL and panics if it fails.
func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
