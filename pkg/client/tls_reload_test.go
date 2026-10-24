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

package client

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	fakecerts "github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func Test_WithTLSReloadFromFile(t *testing.T) {
	caPEM, _, certPEM, keyPEM := testClientCertificate(t, "agent-v1")
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	caPath := filepath.Join(dir, "ca.crt")
	writeKeyPair(t, certPath, keyPath, certPEM, keyPEM)
	require.NoError(t, os.WriteFile(caPath, []byte(caPEM), 0o600))

	remote, err := NewRemote("principal.example.com", 443, WithTLSReloadFromFile(certPath, keyPath, caPath))
	require.NoError(t, err)
	first, err := remote.clientCertificate()
	require.NoError(t, err)
	require.Equal(t, "agent-v1", certificateName(t, first))

	updates := recordUpdates(t, remote)
	remote.StartTLSReload(t.Context())

	// The writes repeat because the watch can start after the first write.
	_, _, nextCert, nextKey := testClientCertificate(t, "agent-v2")
	require.Eventually(t, func() bool {
		writeKeyPair(t, certPath, keyPath, nextCert, nextKey)
		return receivedUpdate(updates, "agent-v2", 250*time.Millisecond)
	}, 10*time.Second, 10*time.Millisecond)
	current, err := remote.clientCertificate()
	require.NoError(t, err)
	require.Equal(t, "agent-v2", certificateName(t, current))

	// The provider handles the expired files without an error and keeps the current certificate.
	drainUpdates(updates)
	expiredCert, expiredKey := expiredClientCertificate(t, "agent-expired")
	writeKeyPair(t, certPath, keyPath, expiredCert, expiredKey)
	provider, ok := remote.tlsSource.(*tlsutil.TLSFileProvider)
	require.True(t, ok)
	require.NoError(t, provider.OnChange(t.Context(), fsnotify.Event{Name: certPath, Op: fsnotify.Write}))
	current, err = remote.clientCertificate()
	require.NoError(t, err)
	require.Equal(t, "agent-v2", certificateName(t, current))

	_, _, lastCert, lastKey := testClientCertificate(t, "agent-v3")
	writeKeyPair(t, certPath, keyPath, lastCert, lastKey)
	stored := updatesUntil(updates, "agent-v3", 10*time.Second)
	require.Contains(t, stored, "agent-v3", "the watch should continue after invalid data")
	require.NotContains(t, stored, "agent-expired")
}

func Test_WithTLSReloadFromSecret(t *testing.T) {
	caPEM, caKeyPEM, certPEM, keyPEM := testClientCertificate(t, "agent-v1")
	ca, err := tls.X509KeyPair([]byte(caPEM), []byte(caKeyPEM))
	require.NoError(t, err)

	kube := fake.NewSimpleClientset(
		clientTLSSecret(certPEM, keyPEM),
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-agent-ca", Namespace: "argocd"},
			Data:       map[string][]byte{"ca.crt": []byte(caPEM)},
		},
	)
	remote, err := NewRemote("principal.example.com", 443, WithTLSReloadFromSecret(kube, "argocd", "argocd-agent-client-tls", "argocd-agent-ca"))
	require.NoError(t, err)
	first, err := remote.clientCertificate()
	require.NoError(t, err)
	require.Equal(t, "agent-v1", certificateName(t, first))

	updates := recordUpdates(t, remote)
	remote.StartTLSReload(t.Context())

	// The informer lists the client Secret and the CA Secret before it watches.
	require.True(t, receivedUpdate(updates, "agent-v1", 10*time.Second))
	require.True(t, receivedUpdate(updates, "agent-v1", 10*time.Second))

	nextCert, nextKey := testClientCertificateFromCA(t, "agent-v2", ca)
	require.Eventually(t, func() bool {
		updateClientTLSSecret(t, kube, nextCert, nextKey)
		return receivedUpdate(updates, "agent-v2", 250*time.Millisecond)
	}, 10*time.Second, 10*time.Millisecond)
	current, err := remote.clientCertificate()
	require.NoError(t, err)
	require.Equal(t, "agent-v2", certificateName(t, current))

	// The informer delivers each Secret version in order, so every update
	// before agent-v3 was handled first. None of them stored the invalid data.
	drainUpdates(updates)
	updateClientTLSSecret(t, kube, "bad", "bad")
	expiredCert, expiredKey := expiredClientCertificate(t, "agent-expired")
	updateClientTLSSecret(t, kube, expiredCert, expiredKey)
	lastCert, lastKey := testClientCertificateFromCA(t, "agent-v3", ca)
	updateClientTLSSecret(t, kube, lastCert, lastKey)
	stored := updatesUntil(updates, "agent-v3", 10*time.Second)
	require.NotEmpty(t, stored)
	require.Equal(t, "agent-v3", stored[len(stored)-1])
	for _, name := range stored[:len(stored)-1] {
		require.Equal(t, "agent-v2", name, "invalid Secret data must not replace the certificate")
	}
}

func Test_certificateIdentity(t *testing.T) {
	agentURI, err := url.Parse("spiffe://example.com/ns/argocd/sa/agent-a")
	require.NoError(t, err)
	otherURI, err := url.Parse("spiffe://example.com/ns/argocd/sa/agent-b")
	require.NoError(t, err)
	identity := func(subject pkix.Name, uris ...*url.URL) string {
		return certificateIdentity(&x509.Certificate{Subject: subject, URIs: uris})
	}
	agentA := pkix.Name{CommonName: "agent-a", Organization: []string{"Example"}}

	require.Equal(t, identity(agentA, agentURI), identity(agentA, agentURI))
	require.NotEqual(t, identity(agentA), identity(pkix.Name{CommonName: "agent-b", Organization: []string{"Example"}}))
	require.NotEqual(t, identity(agentA), identity(pkix.Name{CommonName: "agent-a", Organization: []string{"Other"}}))
	require.NotEqual(t, identity(agentA, agentURI), identity(agentA, otherURI))
}

// Test_tlsConfigForDial checks that a new handshake presents the reloaded
// client certificate and verifies the server with the reloaded CA pool.
func Test_tlsConfigForDial(t *testing.T) {
	caPEM, caKeyPEM, certPEM, keyPEM := testClientCertificate(t, "agent-v1")
	ca, err := tls.X509KeyPair([]byte(caPEM), []byte(caKeyPEM))
	require.NoError(t, err)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM([]byte(caPEM)))

	var serverCert atomic.Pointer[tls.Certificate]
	serverCert.Store(testServerCertificate(t, ca))
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCAs,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return serverCert.Load(), nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	presented := make(chan string, 10)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			tlsConn := conn.(*tls.Conn)
			if err := tlsConn.Handshake(); err == nil {
				presented <- tlsConn.ConnectionState().PeerCertificates[0].Subject.CommonName
			}
			_ = conn.Close()
		}
	}()

	kube := fake.NewSimpleClientset(
		clientTLSSecret(certPEM, keyPEM),
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-agent-ca", Namespace: "argocd"},
			Data:       map[string][]byte{"ca.crt": []byte(caPEM)},
		},
	)
	port := listener.Addr().(*net.TCPAddr).Port
	remote, err := NewRemote("localhost", port, WithTLSReloadFromSecret(kube, "argocd", "argocd-agent-client-tls", "argocd-agent-ca"))
	require.NoError(t, err)
	updates := recordUpdates(t, remote)
	remote.StartTLSReload(t.Context())

	dial := func() error {
		conn, err := tls.Dial("tcp", listener.Addr().String(), remote.tlsConfigForDial())
		if err != nil {
			return err
		}
		return conn.Close()
	}
	nextHandshakeName := func() string {
		require.NoError(t, dial())
		select {
		case name := <-presented:
			return name
		case <-time.After(5 * time.Second):
			t.Fatal("the server did not complete a handshake")
			return ""
		}
	}

	require.Equal(t, "agent-v1", nextHandshakeName())

	nextCert, nextKey := testClientCertificateFromCA(t, "agent-v2", ca)
	require.Eventually(t, func() bool {
		updateClientTLSSecret(t, kube, nextCert, nextKey)
		return receivedUpdate(updates, "agent-v2", 250*time.Millisecond)
	}, 10*time.Second, 10*time.Millisecond)
	require.Equal(t, "agent-v2", nextHandshakeName())

	// A server certificate from a new CA fails until the agent reloads that CA.
	newCAPEM, newCAKeyPEM, err := tlsutil.GenerateCaCertificate("test-ca-2", 30, tlsutil.KeyGenOptions{})
	require.NoError(t, err)
	newCA, err := tls.X509KeyPair([]byte(newCAPEM), []byte(newCAKeyPEM))
	require.NoError(t, err)
	serverCert.Store(testServerCertificate(t, newCA))
	require.Error(t, dial())

	require.Eventually(t, func() bool {
		_, err := kube.CoreV1().Secrets("argocd").Update(t.Context(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-agent-ca", Namespace: "argocd"},
			Data:       map[string][]byte{"ca.crt": []byte(newCAPEM)},
		}, metav1.UpdateOptions{})
		require.NoError(t, err)
		return dial() == nil
	}, 10*time.Second, 100*time.Millisecond)
}

// Test_enableTLSReload covers the CA sources the agent command combines with a reloaded client certificate.
func Test_enableTLSReload(t *testing.T) {
	caPEM, _, certPEM, keyPEM := testClientCertificate(t, "agent-v1")
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	caPath := filepath.Join(dir, "ca.crt")
	writeKeyPair(t, certPath, keyPath, certPEM, keyPEM)
	require.NoError(t, os.WriteFile(caPath, []byte(caPEM), 0o600))
	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM([]byte(caPEM)))
	newKube := func() kubernetes.Interface {
		return fake.NewSimpleClientset(
			clientTLSSecret(certPEM, keyPEM),
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "argocd-agent-ca", Namespace: "argocd"},
				Data:       map[string][]byte{"ca.crt": []byte(caPEM)},
			},
		)
	}

	tests := []struct {
		name     string
		opts     func(kube kubernetes.Interface) []RemoteOption
		insecure bool
	}{
		{
			name: "secret certificate and CA secret",
			opts: func(kube kubernetes.Interface) []RemoteOption {
				return []RemoteOption{
					WithRootAuthoritiesFromSecret(kube, "argocd", "argocd-agent-ca", ""),
					WithTLSReloadFromSecret(kube, "argocd", "argocd-agent-client-tls", "argocd-agent-ca"),
				}
			},
		},
		{
			name: "secret certificate and CA file",
			opts: func(kube kubernetes.Interface) []RemoteOption {
				return []RemoteOption{
					WithRootAuthoritiesFromFile(caPath),
					WithTLSReloadFromSecret(kube, "argocd", "argocd-agent-client-tls", ""),
				}
			},
		},
		{
			name: "certificate files and CA secret",
			opts: func(kube kubernetes.Interface) []RemoteOption {
				return []RemoteOption{
					WithRootAuthoritiesFromSecret(kube, "argocd", "argocd-agent-ca", ""),
					WithTLSReloadFromFile(certPath, keyPath, ""),
				}
			},
		},
		{
			name: "secret certificate without server verification",
			opts: func(kube kubernetes.Interface) []RemoteOption {
				return []RemoteOption{
					WithInsecureSkipTLSVerify(),
					WithTLSReloadFromSecret(kube, "argocd", "argocd-agent-client-tls", ""),
				}
			},
			insecure: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote, err := NewRemote("principal.example.com", 443, tt.opts(newKube())...)
			require.NoError(t, err)
			cert, err := remote.clientCertificate()
			require.NoError(t, err)
			require.Equal(t, "agent-v1", certificateName(t, cert))

			cfg := remote.tlsConfigForDial()
			require.Equal(t, tt.insecure, cfg.InsecureSkipVerify)
			if !tt.insecure {
				require.NotNil(t, cfg.RootCAs)
				require.True(t, caPool.Equal(cfg.RootCAs))
			}
		})
	}
}

func Test_SetClientCertificateObserver(t *testing.T) {
	caPEM, caKeyPEM, certPEM, keyPEM := testClientCertificate(t, "agent-v1")
	ca, err := tls.X509KeyPair([]byte(caPEM), []byte(caKeyPEM))
	require.NoError(t, err)
	kube := fake.NewSimpleClientset(clientTLSSecret(certPEM, keyPEM))
	remote, err := NewRemote("principal.example.com", 443, WithTLSReloadFromSecret(kube, "argocd", "argocd-agent-client-tls", ""))
	require.NoError(t, err)

	expiry := make(chan time.Time, 100)
	remote.SetClientCertificateObserver(func(notAfter time.Time) { expiry <- notAfter })
	require.True(t, certificateNotAfter(t, certPEM).Equal(<-expiry))

	remote.StartTLSReload(t.Context())
	// A longer validity gives the renewed certificate a different end time.
	nextCert, nextKey, err := tlsutil.GenerateClientCertificate("agent-v1", ca.Leaf, ca.PrivateKey, 60, tlsutil.KeyGenOptions{})
	require.NoError(t, err)
	want := certificateNotAfter(t, nextCert)
	require.Eventually(t, func() bool {
		updateClientTLSSecret(t, kube, nextCert, nextKey)
		for {
			select {
			case got := <-expiry:
				if got.Equal(want) {
					return true
				}
			case <-time.After(250 * time.Millisecond):
				return false
			}
		}
	}, 10*time.Second, 10*time.Millisecond)
}

// clientCertificate returns the client certificate the next handshake will present.
func (r *Remote) clientCertificate() (tls.Certificate, error) {
	cert, err := r.getClientCertificate(nil)
	if err != nil {
		return tls.Certificate{}, err
	}
	return *cert, nil
}

// recordUpdates sends the name of each stored client certificate to the returned channel.
func recordUpdates(t *testing.T, r *Remote) <-chan string {
	t.Helper()
	updates := make(chan string, 1000)
	handler := func(cert tls.Certificate, pool *x509.CertPool) {
		r.onTLSMaterial(cert, pool)
		name := ""
		if len(cert.Certificate) > 0 {
			if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
				name = leaf.Subject.CommonName
			}
		}
		select {
		case updates <- name:
		default:
		}
	}
	source, ok := r.tlsSource.(interface {
		SetUpdateHandler(func(tls.Certificate, *x509.CertPool))
	})
	require.True(t, ok)
	source.SetUpdateHandler(handler)
	return updates
}

// receivedUpdate reports whether an update with the given name arrives within timeout.
func receivedUpdate(updates <-chan string, name string, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case got := <-updates:
			if got == name {
				return true
			}
		case <-timer.C:
			return false
		}
	}
}

func drainUpdates(updates <-chan string) []string {
	var names []string
	for {
		select {
		case name := <-updates:
			names = append(names, name)
		default:
			return names
		}
	}
}

// updatesUntil returns the names of the stored certificates up to and including
// the first one with the given name, or all names seen before timeout.
func updatesUntil(updates <-chan string, name string, timeout time.Duration) []string {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var names []string
	for {
		select {
		case got := <-updates:
			names = append(names, got)
			if got == name {
				return names
			}
		case <-timer.C:
			return names
		}
	}
}

func writeKeyPair(t *testing.T, certPath, keyPath, certPEM, keyPEM string) {
	t.Helper()
	require.NoError(t, os.WriteFile(certPath, []byte(certPEM), 0o600))
	require.NoError(t, os.WriteFile(keyPath, []byte(keyPEM), 0o600))
}

func clientTLSSecret(certPEM, keyPEM string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "argocd-agent-client-tls", Namespace: "argocd"},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": []byte(certPEM),
			"tls.key": []byte(keyPEM),
		},
	}
}

func updateClientTLSSecret(t *testing.T, kube kubernetes.Interface, certPEM, keyPEM string) {
	t.Helper()
	_, err := kube.CoreV1().Secrets("argocd").Update(t.Context(), clientTLSSecret(certPEM, keyPEM), metav1.UpdateOptions{})
	require.NoError(t, err)
}

func testClientCertificate(t *testing.T, name string) (string, string, string, string) {
	t.Helper()
	caPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate("test-ca", 30, tlsutil.KeyGenOptions{})
	require.NoError(t, err)
	ca, err := tls.X509KeyPair([]byte(caPEM), []byte(caKeyPEM))
	require.NoError(t, err)
	certPEM, keyPEM := testClientCertificateFromCA(t, name, ca)
	return caPEM, caKeyPEM, certPEM, keyPEM
}

func testClientCertificateFromCA(t *testing.T, name string, ca tls.Certificate) (string, string) {
	t.Helper()
	certPEM, keyPEM, err := tlsutil.GenerateClientCertificate(name, ca.Leaf, ca.PrivateKey, 30, tlsutil.KeyGenOptions{})
	require.NoError(t, err)
	return certPEM, keyPEM
}

func expiredClientCertificate(t *testing.T, name string) (string, string) {
	t.Helper()
	certPEM, keyPEM := fakecerts.CreateSelfSignedCert(t, "rsa", x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		NotBefore:             time.Now().Add(-2 * time.Hour),
		NotAfter:              time.Now().Add(-1 * time.Hour),
	})
	return string(certPEM), string(keyPEM)
}

func testServerCertificate(t *testing.T, ca tls.Certificate) *tls.Certificate {
	t.Helper()
	certPEM, keyPEM, err := tlsutil.GenerateServerCertificate("principal", ca.Leaf, ca.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"}, 30, tlsutil.KeyGenOptions{})
	require.NoError(t, err)
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	require.NoError(t, err)
	return &cert
}

func certificateName(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	require.NotEmpty(t, cert.Certificate)
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)
	return leaf.Subject.CommonName
}

func certificateNotAfter(t *testing.T, certPEM string) time.Time {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	require.NotNil(t, block)
	leaf, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return leaf.NotAfter
}
