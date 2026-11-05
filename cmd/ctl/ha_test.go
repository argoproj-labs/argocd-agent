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

package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func Test_isTLSRelatedError(t *testing.T) {
	tlsHandshakeErr := status.Error(codes.Unavailable,
		"transport: authentication handshake failed: tls: bad certificate")

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "generic unavailable",
			err:  status.Error(codes.Unavailable, "connection refused"),
			want: false,
		},
		{
			name: "deadline exceeded",
			err:  status.Error(codes.DeadlineExceeded, "context deadline exceeded"),
			want: false,
		},
		{
			name: "io eof",
			err:  io.EOF,
			want: false,
		},
		{
			name: "wrapped eof",
			err:  fmt.Errorf("failed to get HA status: %w", io.EOF),
			want: false,
		},
		{
			name: "grpc tls handshake failure",
			err:  tlsHandshakeErr,
			want: true,
		},
		{
			name: "wrapped grpc tls handshake failure",
			err:  fmt.Errorf("failed to get HA status: %w", tlsHandshakeErr),
			want: true,
		},
		{
			name: "unauthenticated from admin interceptor",
			err:  status.Error(codes.Unauthenticated, "no verified client certificate"),
			want: true,
		},
		{
			name: "permission denied for ha-admin-auth mismatch",
			err:  status.Error(codes.PermissionDenied, `client identity "CN=wrong" does not match ha-admin-auth pattern`),
			want: true,
		},
		{
			name: "permission denied unrelated",
			err:  status.Error(codes.PermissionDenied, "access denied"),
			want: false,
		},
		{
			name: "tls record header error",
			err:  &tls.RecordHeaderError{Msg: "not a TLS handshake"},
			want: true,
		},
		{
			name: "x509 unknown authority",
			err:  x509.UnknownAuthorityError{},
			want: true,
		},
		{
			name: "tls error prefix in chain",
			err:  errors.New("tls: bad certificate"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isTLSRelatedError(tt.err))
		})
	}
}

func Test_wrapWithTLSHint(t *testing.T) {
	t.Run("does not add hint for generic connectivity errors", func(t *testing.T) {
		err := status.Error(codes.Unavailable, "connection refused")
		got := wrapWithTLSHint(fmt.Errorf("failed to get HA status: %w", err), haAdminTLSOptions{})
		assert.Equal(t, "failed to get HA status: rpc error: code = Unavailable desc = connection refused", got.Error())
	})

	t.Run("adds mTLS flags hint for handshake failure without TLS options", func(t *testing.T) {
		err := status.Error(codes.Unavailable, "transport: authentication handshake failed: tls: bad certificate")
		got := wrapWithTLSHint(fmt.Errorf("failed to get HA status: %w", err), haAdminTLSOptions{})
		assert.Contains(t, got.Error(), "Hint: the admin endpoint may require mTLS")
	})

	t.Run("adds ha-admin-auth hint for permission denied", func(t *testing.T) {
		err := status.Error(codes.PermissionDenied, `client identity "CN=wrong" does not match ha-admin-auth pattern`)
		got := wrapWithTLSHint(fmt.Errorf("promote failed: %w", err), haAdminTLSOptions{
			certPath: "/tmp/cert.pem",
			keyPath:  "/tmp/key.pem",
			caPath:   "/tmp/ca.pem",
		})
		assert.Contains(t, got.Error(), "Hint: client certificate identity does not match")
	})
}
