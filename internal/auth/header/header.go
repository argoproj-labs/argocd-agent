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

// Package header implements generic header-based authentication for service mesh environments.
// It extracts client identity from any HTTP header using a configurable regex pattern.
package header

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"google.golang.org/grpc/metadata"
	"k8s.io/apimachinery/pkg/api/validation"
)

var log = logging.ComponentLogger("header-auth")

// HeaderAuthentication implements authentication using identity extracted from
// HTTP headers injected by a sidecar proxy or service mesh.
//
// This is designed for service mesh environments where sidecars terminate mTLS
// and forward client identity via headers (e.g., Istio's x-forwarded-client-cert,
// custom identity headers, etc.).
type HeaderAuthentication struct {
	// HeaderName is the name of the HTTP header to extract identity from.
	// Header names are case-insensitive in HTTP/gRPC.
	HeaderName string

	// ExtractionRegex is used to extract the agent ID from the header value.
	// The first capture group becomes the agent ID.
	// Example: ^spiffe://[^/]+/ns/([^/]+)/sa/ extracts namespace from SPIFFE URI
	ExtractionRegex *regexp.Regexp
}

// NewHeaderAuthentication creates a new header-based authentication method.
//
// Example SPIFFE URI extraction:
//
//	headerName: "x-forwarded-client-cert"
//	extractionRegex: regexp.MustCompile(`^.*URI=spiffe://[^/]+/ns/[^/]+/sa/([^,;]+)`)
//
// Example simple header:
//
//	headerName: "x-client-id"
//	extractionRegex: regexp.MustCompile(`^(.+)$`)
func NewHeaderAuthentication(headerName string, extractionRegex *regexp.Regexp) *HeaderAuthentication {
	return &HeaderAuthentication{
		HeaderName:      headerName,
		ExtractionRegex: extractionRegex,
	}
}

// Authenticate extracts the agent ID from the configured header using the regex pattern.
func (h *HeaderAuthentication) Authenticate(ctx context.Context, creds auth.Credentials) (string, error) {
	log.Debugf("Starting header-based authentication (header: %s)", h.HeaderName)
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		log.Debug("No metadata found in incoming context")
		return "", fmt.Errorf("no metadata in incoming context")
	}

	headerValues := md.Get(strings.ToLower(h.HeaderName))
	if len(headerValues) == 0 {
		log.Debugf("Header '%s' not found in request metadata", h.HeaderName)
		return "", fmt.Errorf("header '%s' not found in request", h.HeaderName)
	}

	headerValue := headerValues[0]
	log.Debugf("Received header '%s': %s", h.HeaderName, headerValue)

	// Extract agent ID from header value using regex
	agentID, err := h.extractAgentID(headerValue)
	if err != nil {
		log.Debugf("Failed to extract agent ID from header: %v", err)
		return "", err
	}
	log.Debugf("Extracted agent ID: %s", agentID)

	errs := validation.NameIsDNSLabel(agentID, false)
	if len(errs) > 0 {
		log.Debugf("Invalid agent ID format '%s': %v", agentID, errs)
		return "", fmt.Errorf("invalid agent ID '%s' extracted from header: %v", agentID, errs)
	}

	log.Infof("Successfully authenticated agent: %s", agentID)
	return agentID, nil
}

// Init initializes the authentication method.
func (h *HeaderAuthentication) Init() error {
	if h.HeaderName == "" {
		return fmt.Errorf("header name cannot be empty")
	}
	if h.ExtractionRegex == nil {
		return fmt.Errorf("extraction regex cannot be nil")
	}
	return nil
}

// extractAgentID extracts the agent ID from a header value using the configured regex.
func (h *HeaderAuthentication) extractAgentID(headerValue string) (string, error) {
	log.Debugf("Applying regex pattern: %s", h.ExtractionRegex.String())

	matches := h.ExtractionRegex.FindStringSubmatch(headerValue)
	if len(matches) < 2 {
		log.Debugf("Header value '%s' does not match regex pattern '%s'", headerValue, h.ExtractionRegex.String())
		return "", fmt.Errorf("header value does not match the extraction regex pattern")
	}

	log.Debugf("Regex matched with %d capture groups: %v", len(matches), matches)

	agentID := matches[1]
	if agentID == "" {
		log.Debugf("Extracted agent ID is empty from header value '%s'", headerValue)
		return "", fmt.Errorf("extracted agent ID is empty from header value")
	}

	return agentID, nil
}
