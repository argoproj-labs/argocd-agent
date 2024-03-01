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
