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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	workloadPB "github.com/spiffe/go-spiffe/v2/proto/spiffe/workload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const testSpiffeID = "spiffe://example.org/test-workload"

func Test_New_GeneratesCA(t *testing.T) {
	srv, err := New(testSpiffeID, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, srv)

	assert.NotNil(t, srv.CACert())
	assert.NotNil(t, srv.CAKey())
	assert.True(t, srv.CACert().IsCA)
	assert.Equal(t, testSpiffeID, srv.spiffeID)
	assert.Equal(t, "spiffe://example.org", srv.trustDomain)
}

func Test_New_WithProvidedCA(t *testing.T) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(100),
		Subject:      pkix.Name{CommonName: "Test CA"},
		URIs:         []*url.URL{mustParseURL("spiffe://example.org")},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	srv, err := New(testSpiffeID, caCert, caKey)
	require.NoError(t, err)
	require.NotNil(t, srv)

	assert.Equal(t, caCert, srv.CACert())
	assert.Equal(t, caKey, srv.CAKey())
}

func Test_New_InvalidSpiffeID(t *testing.T) {
	_, err := New("://invalid", nil, nil)
	require.Error(t, err)
}

func Test_ListenServeStop(t *testing.T) {
	srv, err := New(testSpiffeID, nil, nil)
	require.NoError(t, err)

	socketPath := filepath.Join(t.TempDir(), "test.sock")

	err = srv.Listen(socketPath)
	require.NoError(t, err)

	// Verify socket file was created
	_, err = os.Stat(socketPath)
	require.NoError(t, err)

	// Start serving in background
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve()
	}()

	// Connect a client to verify the server is serving
	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer conn.Close()

	client := workloadPB.NewSpiffeWorkloadAPIClient(conn)

	// Test FetchX509SVID
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	svidStream, err := client.FetchX509SVID(ctx, &workloadPB.X509SVIDRequest{})
	require.NoError(t, err)

	svidResp, err := svidStream.Recv()
	require.NoError(t, err)
	require.Len(t, svidResp.Svids, 1)
	assert.Equal(t, testSpiffeID, svidResp.Svids[0].SpiffeId)
	assert.NotEmpty(t, svidResp.Svids[0].X509Svid)
	assert.NotEmpty(t, svidResp.Svids[0].X509SvidKey)
	assert.NotEmpty(t, svidResp.Svids[0].Bundle)

	// Verify the SVID certificate is valid
	svidCert, err := x509.ParseCertificate(svidResp.Svids[0].X509Svid)
	require.NoError(t, err)
	require.Len(t, svidCert.URIs, 1)
	assert.Equal(t, testSpiffeID, svidCert.URIs[0].String())

	// Test FetchX509Bundles
	bundleCtx, bundleCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer bundleCancel()

	bundleStream, err := client.FetchX509Bundles(bundleCtx, &workloadPB.X509BundlesRequest{})
	require.NoError(t, err)

	bundleResp, err := bundleStream.Recv()
	require.NoError(t, err)
	require.Contains(t, bundleResp.Bundles, "spiffe://example.org")
	assert.NotEmpty(t, bundleResp.Bundles["spiffe://example.org"])

	// Stop the server
	srv.Stop()

	// Wait for Serve to return
	select {
	case serveErr := <-serveDone:
		assert.NoError(t, serveErr)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop in time")
	}
}

func Test_Stop_NilServer(t *testing.T) {
	srv, err := New(testSpiffeID, nil, nil)
	require.NoError(t, err)

	// Stop without Listen/Serve should not panic
	assert.NotPanics(t, func() {
		srv.Stop()
	})
}

func Test_trustDomainFromID(t *testing.T) {
	tests := []struct {
		name       string
		spiffeID   string
		wantDomain string
		wantErr    bool
	}{
		{
			name:       "valid SPIFFE ID",
			spiffeID:   "spiffe://example.org/workload",
			wantDomain: "spiffe://example.org",
		},
		{
			name:       "SPIFFE ID with nested path",
			spiffeID:   "spiffe://cluster.local/ns/default/sa/myapp",
			wantDomain: "spiffe://cluster.local",
		},
		{
			name:       "trust domain only",
			spiffeID:   "spiffe://example.org",
			wantDomain: "spiffe://example.org",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trustDomainFromID(tt.spiffeID)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantDomain, got)
			}
		})
	}
}

func Test_SharedCA_TwoServers(t *testing.T) {
	srv1, err := New("spiffe://example.org/principal", nil, nil)
	require.NoError(t, err)

	srv2, err := New("spiffe://example.org/agent", srv1.CACert(), srv1.CAKey())
	require.NoError(t, err)

	// Both servers should share the same CA
	assert.Equal(t, srv1.CACert().Raw, srv2.CACert().Raw)

	// Parse their SVIDs and verify they were signed by the same CA
	svid1, err := x509.ParseCertificate(srv1.svidCertDER)
	require.NoError(t, err)
	svid2, err := x509.ParseCertificate(srv2.svidCertDER)
	require.NoError(t, err)

	// Both SVIDs should be verifiable by the shared CA
	pool := x509.NewCertPool()
	pool.AddCert(srv1.CACert())

	_, err = svid1.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	assert.NoError(t, err)

	_, err = svid2.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	assert.NoError(t, err)

	// Different SPIFFE IDs
	assert.NotEqual(t, svid1.URIs[0].String(), svid2.URIs[0].String())
	assert.Equal(t, "spiffe://example.org/principal", svid1.URIs[0].String())
	assert.Equal(t, "spiffe://example.org/agent", svid2.URIs[0].String())
}
