package grpcutil

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/peer"
)

func Test_AddressFromContext(t *testing.T) {
	const myAddr = "127.0.0.1:23"
	p := peer.Peer{Addr: net.TCPAddrFromAddrPort(netip.MustParseAddrPort(myAddr))}
	ctx := peer.NewContext(context.Background(), &p)
	t.Run("Get valid peer address from context", func(t *testing.T) {
		assert.Equal(t, myAddr, AddressFromContext(ctx))
	})
	t.Run("Get invalid peer address from context", func(t *testing.T) {
		assert.Equal(t, unknownAddress, AddressFromContext(context.Background()))
	})
}
