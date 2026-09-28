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

package e2e

import (
	"context"
	"crypto/tls"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/stretchr/testify/suite"
)

// This suite exercises certificate issuance through an independent strict
// verifier. It needs OpenSSL, but no Kubernetes cluster or existing credentials.
type StrictCertificateTestSuite struct {
	suite.Suite
}

func (s *StrictCertificateTestSuite) Test_VerifyGeneratedCertificates() {
	openssl, err := exec.LookPath("openssl")
	s.Require().NoError(err, "OpenSSL is required for strict certificate verification")
	opts := tlsutil.KeyGenOptions{Algorithm: "ecdsa-p256"}
	caPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate("test-ca", tlsutil.DefaultCACertValidityDays, opts)
	s.Require().NoError(err)
	ca, err := tls.X509KeyPair([]byte(caPEM), []byte(caKeyPEM))
	s.Require().NoError(err)

	serverPEM, _, err := tlsutil.GenerateServerCertificate("test-principal", ca.Leaf, ca.PrivateKey, nil, []string{"principal.example.test"}, tlsutil.DefaultLeafCertValidityDays, opts)
	s.Require().NoError(err)
	clientPEM, _, err := tlsutil.GenerateClientCertificate("test-agent", ca.Leaf, ca.PrivateKey, tlsutil.DefaultLeafCertValidityDays, opts)
	s.Require().NoError(err)

	dir := s.T().TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	s.Require().NoError(os.WriteFile(caPath, []byte(caPEM), 0600))
	for _, tc := range []struct {
		name    string
		purpose string
		certPEM string
	}{
		{name: "server", purpose: "sslserver", certPEM: serverPEM},
		{name: "client", purpose: "sslclient", certPEM: clientPEM},
	} {
		s.Run(tc.name, func() {
			certPath := filepath.Join(dir, tc.name+".pem")
			s.Require().NoError(os.WriteFile(certPath, []byte(tc.certPEM), 0600))
			args := []string{"verify", "-x509_strict", "-purpose", tc.purpose, "-CAfile", caPath}
			if tc.name == "server" {
				args = append(args, "-verify_hostname", "principal.example.test")
			}
			args = append(args, certPath)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, openssl, args...).CombinedOutput()
			s.Require().NoError(err, "strict %s certificate verification: %s", tc.name, output)
		})
	}
}

func Test_VerifyGeneratedCertificatesWithStrictX509(t *testing.T) {
	suite.Run(t, new(StrictCertificateTestSuite))
}
