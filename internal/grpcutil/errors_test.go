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
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func Test_NeedReconnectOnError(t *testing.T) {
	t.Run("Need reconnect on EOF", func(t *testing.T) {
		assert.True(t, NeedReconnectOnError(io.EOF))
	})
	t.Run("No reconnect on irrelevant error", func(t *testing.T) {
		assert.False(t, NeedReconnectOnError(fmt.Errorf("some error")))
	})
	t.Run("Need reconnect on gRPC error codes", func(t *testing.T) {
		for k := range reconnectableErrors {
			err := status.Error(k, "")
			assert.True(t, NeedReconnectOnError(err))
		}
	})
	t.Run("No reconnect on irrelevant gRPC error code", func(t *testing.T) {
		err := status.Error(codes.Internal, "")
		assert.False(t, NeedReconnectOnError(err))
	})
}
