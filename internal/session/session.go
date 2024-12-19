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

// ClientIdFromContext returns the client ID stored in context ctx. If there
// is no client ID in the context, or the client ID is invalid, returns an
// error.
func ClientIdFromContext(ctx context.Context) (string, error) {
	val := ctx.Value(types.ContextAgentIdentifier)
	clientId, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("no client identifier found in context")
	}
	if !IsValidClientId(clientId) {
		return "", fmt.Errorf("invalid client identifier: %s", clientId)
	}
	return clientId, nil
}

// ClientIdToContext returns a copy of context ctx with the clientId stored
func ClientIdToContext(ctx context.Context, clientId string) context.Context {
	return context.WithValue(ctx, types.ContextAgentIdentifier, clientId)
}

// IsValidClientId returns true if the string s is considered a valid client
// identifier.
func IsValidClientId(s string) bool {
	if errs := validation.NameIsDNSSubdomain(s, false); len(errs) > 0 {
		return false
	}
	return true
}
