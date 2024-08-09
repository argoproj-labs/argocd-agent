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
