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
	"fmt"
	"regexp"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/spiffe/go-spiffe/v2/bundle/jwtbundle"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
)

var log = logging.GetDefaultLogger().ComponentLogger("spiffejwt")

// SPIFFEJWTAuthentication validates SPIFFE JWT-SVIDs and
// extracts agent identity from the SPIFFE ID.
type SPIFFEJWTAuthentication struct {
	// AgentIDRegex extracts the agent name from the SPIFFE ID.
	// The first capture group becomes the agent ID.
	// Example: spiffe://[^/]+/(.+) extracts the full path after the trust domain.
	AgentIDRegex *regexp.Regexp

	// BundleSource provides JWT bundles for validating JWT-SVID signatures.
	// Must be set; authentication fails if nil.
	BundleSource jwtbundle.Source

	// Audience is the expected audience in the JWT-SVID.
	Audience string
}

// NewSPIFFEJWTAuthentication creates a new SPIFFE JWT authentication method.
func NewSPIFFEJWTAuthentication(agentIDRegex *regexp.Regexp, audience string, bundleSource jwtbundle.Source) *SPIFFEJWTAuthentication {
	return &SPIFFEJWTAuthentication{
		AgentIDRegex: agentIDRegex,
		Audience:     audience,
		BundleSource: bundleSource,
	}
}

// Init initializes the authentication method.
func (a *SPIFFEJWTAuthentication) Init() error {
	return nil
}

// Authenticate validates the SPIFFE JWT-SVID from the agent's credentials and returns
// the agent name extracted from the SPIFFE ID.
func (a *SPIFFEJWTAuthentication) Authenticate(_ context.Context, creds auth.Credentials) (string, error) {
	token, ok := creds["token"]
	if !ok || token == "" {
		return "", fmt.Errorf("no SPIFFE JWT token provided in credentials")
	}

	var svid *jwtsvid.SVID
	var err error

	audiences := []string{a.Audience}

	if a.BundleSource == nil {
		return "", fmt.Errorf("no SPIFFE JWT bundle source configured; cannot validate JWT-SVID signature")
	}
	svid, err = jwtsvid.ParseAndValidate(token, a.BundleSource, audiences)
	if err != nil {
		return "", fmt.Errorf("SPIFFE JWT-SVID validation failed: %w", err)
	}

	spiffeID := svid.ID.String()
	log.Debugf("Validated SPIFFE JWT-SVID, SPIFFE ID: %s", spiffeID)

	if a.AgentIDRegex == nil {
		return "", fmt.Errorf("no agent ID regex configured; cannot extract agent identity from SPIFFE ID %q", spiffeID)
	}

	matches := a.AgentIDRegex.FindStringSubmatch(spiffeID)
	if len(matches) < 2 {
		return "", fmt.Errorf("SPIFFE ID %q did not match agent ID regex %q", spiffeID, a.AgentIDRegex.String())
	}

	agentID := matches[1]
	if agentID == "" {
		return "", fmt.Errorf("agent ID extracted from SPIFFE ID %q is empty", spiffeID)
	}

	log.Debugf("Extracted agent ID %q from SPIFFE ID %q", agentID, spiffeID)
	return agentID, nil
}
