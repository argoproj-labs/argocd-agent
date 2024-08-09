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
