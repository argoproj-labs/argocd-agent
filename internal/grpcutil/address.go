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

package grpcutil

import (
	"context"

	"google.golang.org/grpc/peer"
)

const unknownAddress = "unknown"

// AddressFromContext returns the peer's address as string from the context.
// If there is no peer information in the context, returns "unknown".
func AddressFromContext(ctx context.Context) string {
	c, ok := peer.FromContext(ctx)
	if !ok {
		return unknownAddress
	}
	return c.Addr.String()
}
