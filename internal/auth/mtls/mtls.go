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
	"fmt"
	"regexp"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"k8s.io/apimachinery/pkg/api/validation"
)

// IdentitySource specifies where to extract the agent identity from
type IdentitySource string

const (
	// IdentitySourceSubject extracts identity from cert.Subject (DN)
	IdentitySourceSubject IdentitySource = "subject"
	// IdentitySourceURI extracts identity from cert.URIs (e.g., SPIFFE)
	IdentitySourceURI IdentitySource = "uri"
)

// MTLSAuthentication implements a mTLS authentication method
//
// It extracts the agent ID from the TLS certificate subject or URI SANs.
type MTLSAuthentication struct {
	AgentIDRegex   *regexp.Regexp
	IdentitySource IdentitySource
}

func NewMTLSAuthentication(regex *regexp.Regexp, source IdentitySource) *MTLSAuthentication {
	if source == "" {
		source = IdentitySourceSubject
	}
	return &MTLSAuthentication{
		AgentIDRegex:   regex,
		IdentitySource: source,
	}
}

// Authenticate extracts the agent ID from the TLS context
func (m *MTLSAuthentication) Authenticate(ctx context.Context, creds auth.Credentials) (string, error) {
	c, ok := peer.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("could not get peer from context")
	}
	tlsInfo, ok := c.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", fmt.Errorf("connection requires TLS credentials but has none")
	}
	if len(tlsInfo.State.VerifiedChains) < 1 {
		return "", fmt.Errorf("no verified certificates found in TLS cred")
	}
	cert := tlsInfo.State.VerifiedChains[0][0]

	var identityString string
	switch m.IdentitySource {
	case IdentitySourceURI:
		if len(cert.URIs) == 0 {
			return "", fmt.Errorf("no URI SANs found in client certificate")
		}
		identityString = cert.URIs[0].String()
	default:
		identityString = cert.Subject.String()
	}

	var agentID string
	if m.AgentIDRegex != nil {
		matches := m.AgentIDRegex.FindStringSubmatch(identityString)
		if len(matches) < 2 {
			return "", fmt.Errorf("certificate %s '%s' does not match the agent ID regex pattern", m.IdentitySource, identityString)
		}
		agentID = matches[1]
	}
	if agentID == "" {
		return "", fmt.Errorf("agent ID is empty")
	}
	errs := validation.NameIsDNSLabel(agentID, false)
	if len(errs) > 0 {
		return "", fmt.Errorf("invalid agent ID in client certificate: %v", errs)
	}
	return agentID, nil
}

func (m *MTLSAuthentication) Init() error {
	return nil
}
