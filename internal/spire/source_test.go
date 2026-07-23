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

package spire

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	fakespire "github.com/argoproj-labs/argocd-agent/test/fake/spire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSpiffeID = "spiffe://example.org/test-workload"

func startFakeSpire(t *testing.T) string {
	t.Helper()
	srv, err := fakespire.New(testSpiffeID, nil, nil)
	require.NoError(t, err)

	socketPath := filepath.Join(t.TempDir(), "agent.sock")
	require.NoError(t, srv.Listen(socketPath))

	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { srv.Stop() })

	return socketPath
}

func TestNew_ConnectsToFakeSPIRE(t *testing.T) {
	socketPath := startFakeSpire(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	source, err := New(ctx, "unix://"+socketPath)
	require.NoError(t, err)
	require.NotNil(t, source)
	defer source.Close()

	assert.NotNil(t, source.X509Source())
}

func TestNew_FailsWithInvalidSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := New(ctx, "unix:///nonexistent/path/agent.sock")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create SPIRE X509Source")
}

func TestSource_GetCertificate(t *testing.T) {
	socketPath := startFakeSpire(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	source, err := New(ctx, "unix://"+socketPath)
	require.NoError(t, err)
	defer source.Close()

	cb := source.GetCertificate()
	require.NotNil(t, cb)
}

func TestSource_GetClientCertificate(t *testing.T) {
	socketPath := startFakeSpire(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	source, err := New(ctx, "unix://"+socketPath)
	require.NoError(t, err)
	defer source.Close()

	cb := source.GetClientCertificate()
	require.NotNil(t, cb)
}

func TestSource_TrustBundle(t *testing.T) {
	socketPath := startFakeSpire(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	source, err := New(ctx, "unix://"+socketPath)
	require.NoError(t, err)
	defer source.Close()

	pool, err := source.TrustBundle()
	require.NoError(t, err)
	require.NotNil(t, pool)
	//nolint:staticcheck
	assert.NotEmpty(t, pool.Subjects(), "trust bundle should contain at least one CA")
}

func TestSource_Close_NilSource(t *testing.T) {
	s := &Source{x509Source: nil}
	err := s.Close()
	assert.NoError(t, err)
}

func TestSource_Close(t *testing.T) {
	socketPath := startFakeSpire(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	source, err := New(ctx, "unix://"+socketPath)
	require.NoError(t, err)

	err = source.Close()
	assert.NoError(t, err)
}
