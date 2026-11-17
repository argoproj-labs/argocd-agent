// Copyright 2025 The argocd-agent Authors
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

package redisproxy

import (
	"bufio"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/stretchr/testify/require"
)

func newRESPReader(s string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(s))
}

func Test_readString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantStr string
		wantRaw []string
		wantErr bool
		errIs   error
	}{
		{name: "simple string", input: "+OK\r\n", wantStr: "OK", wantRaw: []string{"+OK\r\n"}},
		{name: "error reply", input: "-ERR bad\r\n", wantStr: "ERR bad", wantRaw: []string{"-ERR bad\r\n"}},
		{name: "integer reply", input: ":42\r\n", wantStr: "42", wantRaw: []string{":42\r\n"}},
		{name: "bulk string", input: "$5\r\nhello\r\n", wantStr: "hello", wantRaw: []string{"$5", "hello"}},
		{name: "empty bulk string", input: "$0\r\n\r\n", wantStr: "", wantRaw: []string{"$0", ""}},
		{name: "nil bulk string", input: "$-1\r\n", wantStr: "", wantRaw: []string{"$-1"}},
		{name: "unknown type byte", input: "?x\r\n", wantErr: true, errIs: ErrInvalidRequest},
		{name: "line too short", input: "+\n", wantErr: true, errIs: ErrInvalidRequest},
		{name: "non-numeric bulk length", input: "$x\r\n", wantErr: true},
		{name: "bulk shorter than declared length", input: "$5\r\nhi\r\n", wantErr: true},
		{name: "empty input", input: "", wantErr: true, errIs: io.EOF},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStr, gotRaw, err := readString(newRESPReader(tt.input))

			if tt.wantErr {
				require.Error(t, err)
				if tt.errIs != nil {
					require.ErrorIs(t, err, tt.errIs)
				}
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantStr, gotStr)
			require.Equal(t, tt.wantRaw, gotRaw)
		})
	}
}

func Test_readRedisArray(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantFields []string
		wantRaw    []string
		wantErr    bool
	}{
		{
			name:       "two element array",
			input:      "*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n",
			wantFields: []string{"foo", "bar"},
			wantRaw:    []string{"*2", "$3", "foo", "$3", "bar"},
		},
		{name: "empty array", input: "*0\r\n", wantFields: nil, wantRaw: []string{"*0"}},
		{name: "nil array", input: "*-1\r\n", wantFields: nil, wantRaw: []string{"*-1"}},
		{name: "not an array", input: "+OK\r\n", wantErr: true},
		{name: "line too short", input: "*\n", wantErr: true},
		{name: "non-numeric element count", input: "*x\r\n", wantErr: true},
		{name: "element shorter than declared length", input: "*1\r\n$3\r\nfo\r\n", wantErr: true},
		{name: "empty input", input: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFields, gotRaw, err := readRedisArray(newRESPReader(tt.input))

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantFields, gotFields)
			require.Equal(t, tt.wantRaw, gotRaw)
		})
	}
}

// recvRedisCommand receives one value from the reader channel, failing the test
// rather than blocking forever if nothing arrives.
func recvRedisCommand(t *testing.T, ch <-chan parsedRedisCommand) (parsedRedisCommand, bool) {
	t.Helper()
	select {
	case v, ok := <-ch:
		return v, ok
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting on redis reader channel")
		return parsedRedisCommand{}, false
	}
}

func Test_startArgoCDRedisEndpointReader(t *testing.T) {
	logEntry := logging.GetDefaultLogger().ModuleLogger("RedisProxy")

	t.Run("emits the parsed command then closes the channel on EOF", func(t *testing.T) {
		ch := startArgoCDRedisEndpointReader(logEntry, newRESPReader("*1\r\n$4\r\nPING\r\n"))

		got, ok := recvRedisCommand(t, ch)
		require.True(t, ok, "expected one command before the channel closes")
		require.Equal(t, []string{"PING"}, got.parsedReceived)
		require.Equal(t, []string{"*1", "$4", "PING"}, got.rawReceived)

		// The input is exhausted, so the goroutine returns and closes the channel.
		_, ok = recvRedisCommand(t, ch)
		require.False(t, ok, "channel should be closed once the input is exhausted")
	})

	t.Run("closes the channel without emitting on malformed input", func(t *testing.T) {
		ch := startArgoCDRedisEndpointReader(logEntry, newRESPReader("garbage"))

		_, ok := recvRedisCommand(t, ch)
		require.False(t, ok, "channel should close without emitting a command")
	})
}
