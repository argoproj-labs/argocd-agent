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

package session

import (
	"context"
	"fmt"

	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"k8s.io/apimachinery/pkg/api/validation"
)

/*
Package session contains various functions to access and manipulate session
data.
*/

// ClientIDFromContext returns the client ID stored in context ctx. If there
// is no client ID in the context, or the client ID is invalid, returns an
// error.
func ClientIDFromContext(ctx context.Context) (string, error) {
	val := ctx.Value(types.ContextAgentIdentifier)
	clientID, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("no client identifier found in context")
	}
	if !IsValidClientID(clientID) {
		return "", fmt.Errorf("invalid client identifier: %s", clientID)
	}
	return clientID, nil
}

// ClientModeFromContext returns the client mode stored in context ctx. Returns an
// error if there is no client mode in the ctx.
func ClientModeFromContext(ctx context.Context) (string, error) {
	val := ctx.Value(types.ContextAgentMode)
	clientMode, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("no client mode found in context")
	}
	return clientMode, nil
}

// ClientInfoToContext returns a copy of context ctx with the clientID and clientMode stored
func ClientInfoToContext(ctx context.Context, clientID, clientMode string) context.Context {
	clientCtx := context.WithValue(ctx, types.ContextAgentIdentifier, clientID)
	return context.WithValue(clientCtx, types.ContextAgentMode, clientMode)
}

// IsValidClientID returns true if the string s is considered a valid client
// identifier.
func IsValidClientID(s string) bool {
	if errs := validation.NameIsDNSSubdomain(s, false); len(errs) > 0 {
		return false
	}
	return true
}
