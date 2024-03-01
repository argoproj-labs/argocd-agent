package grpcutil

import (
	"errors"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var reconnectableErrors map[codes.Code]bool = map[codes.Code]bool{
	codes.Unavailable:     true,
	codes.Unauthenticated: true,
	codes.Canceled:        true,
}

func NeedReconnectOnError(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	status, ok := status.FromError(err)
	if !ok {
		return false
	}
	v, ok := reconnectableErrors[status.Code()]
	if ok && v {
		return true
	}
	return false
}
