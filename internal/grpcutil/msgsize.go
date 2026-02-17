// Copyright 2026 The argocd-agent Authors
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
	"fmt"

	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// messageSizeLoggerStream wraps a grpc.ServerStream or grpc.ClientStream to
// log warnings when sent or received messages exceed a threshold percentage
// of the configured maximum gRPC message size.
type messageSizeLoggerStream struct {
	grpc.ServerStream
	maxSize int
	method  string
}

// messageSizeLoggerClientStream wraps a grpc.ClientStream with the same
// message size logging behavior.
type messageSizeLoggerClientStream struct {
	grpc.ClientStream
	maxSize int
	method  string
}

// protoSize returns the serialized size of a message if it implements
// proto.Message, or -1 if the size cannot be determined.
func protoSize(msg interface{}) int {
	if pm, ok := msg.(proto.Message); ok {
		return proto.Size(pm)
	}
	return -1
}

// warnIfExceedsThreshold logs a warning when the serialized size of msg meets
// or exceeds x% of maxSize.
func warnIfExceedsThreshold(method string, msg interface{}, maxSize int, direction string) {
	if maxSize <= 0 {
		return
	}
	size := protoSize(msg)
	if size < maxSize*4/5 { // Log warning at 80%
		return
	}
	fields := logrus.Fields{
		"method":       method,
		"message_size": size,
		"max_size":     maxSize,
		"direction":    direction,
	}
	if ev, ok := msg.(*eventstreamapi.Event); ok {
		if ce := ev.GetEvent(); ce != nil {
			fields["event_source"] = ce.GetSource()
			fields["event_type"] = ce.GetType()
		}
	} else {
		fields["message_type"] = fmt.Sprintf("%T", msg)
	}
	pct := 100 * size / maxSize
	logrus.WithFields(fields).Warnf("gRPC message size (%d bytes) is %d%% of max (%d bytes)", size, pct, maxSize)
}

func (s *messageSizeLoggerStream) SendMsg(m interface{}) error {
	warnIfExceedsThreshold(s.method, m, s.maxSize, "send")
	return s.ServerStream.SendMsg(m)
}

func (s *messageSizeLoggerStream) RecvMsg(m interface{}) error {
	err := s.ServerStream.RecvMsg(m)
	if err != nil {
		return err
	}
	warnIfExceedsThreshold(s.method, m, s.maxSize, "recv")
	return nil
}

func (s *messageSizeLoggerClientStream) SendMsg(m interface{}) error {
	warnIfExceedsThreshold(s.method, m, s.maxSize, "send")
	return s.ClientStream.SendMsg(m)
}

func (s *messageSizeLoggerClientStream) RecvMsg(m interface{}) error {
	err := s.ClientStream.RecvMsg(m)
	if err != nil {
		return err
	}
	warnIfExceedsThreshold(s.method, m, s.maxSize, "recv")
	return nil
}

// StreamServerMsgSizeInterceptor returns a gRPC stream server interceptor that
// logs a warning when any message sent or received on a stream exceeds x% of
// maxGRPCMessageSize.
func StreamServerMsgSizeInterceptor(maxGRPCMessageSize int) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		wrapped := &messageSizeLoggerStream{
			ServerStream: ss,
			maxSize:      maxGRPCMessageSize,
			method:       info.FullMethod,
		}
		return handler(srv, wrapped)
	}
}

// StreamClientMsgSizeInterceptor returns a gRPC stream client interceptor that
// logs a warning when any message sent or received on a stream exceeds x% of
// maxGRPCMessageSize.
func StreamClientMsgSizeInterceptor(maxGRPCMessageSize int) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		cs, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			return cs, err
		}
		return &messageSizeLoggerClientStream{
			ClientStream: cs,
			maxSize:      maxGRPCMessageSize,
			method:       method,
		}, nil
	}
}

// UnaryServerMsgSizeInterceptor returns a gRPC unary server interceptor that
// logs a warning when the request or response message exceeds x% of
// maxGRPCMessageSize.
func UnaryServerMsgSizeInterceptor(maxGRPCMessageSize int) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		warnIfExceedsThreshold(info.FullMethod, req, maxGRPCMessageSize, "recv")
		resp, err := handler(ctx, req)
		if err != nil {
			return resp, err
		}
		warnIfExceedsThreshold(info.FullMethod, resp, maxGRPCMessageSize, "send")
		return resp, err
	}
}

// UnaryClientMsgSizeInterceptor returns a gRPC unary client interceptor that
// logs a warning when the request or response message exceeds x% of
// maxGRPCMessageSize.
func UnaryClientMsgSizeInterceptor(maxGRPCMessageSize int) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		warnIfExceedsThreshold(method, req, maxGRPCMessageSize, "send")
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err != nil {
			return err
		}
		warnIfExceedsThreshold(method, reply, maxGRPCMessageSize, "recv")
		return nil
	}
}
