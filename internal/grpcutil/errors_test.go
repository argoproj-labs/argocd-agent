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
