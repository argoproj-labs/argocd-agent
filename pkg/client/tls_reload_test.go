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
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	fakecerts "github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func Test_WithTLSReloadFromFile(t *testing.T) {
	_, _, certPEM, keyPEM := testClientCertificate(t, "agent-v1")
	files := newMountedFiles(t, map[string]string{"tls.crt": certPEM, "tls.key": keyPEM})

	remote, err := NewRemote("principal.example.com", 443, WithTLSReloadFromFile(files.path("tls.crt"), files.path("tls.key")))
	require.NoError(t, err)
	first, err := remote.clientCertificate()
	require.NoError(t, err)
	require.Equal(t, "agent-v1", certificateName(t, first))

	provider := fileProvider(t, remote.tlsSource)
	updates := recordUpdates(t, remote)

	_, _, nextCert, nextKey := testClientCertificate(t, "agent-v2")
	files.update(t, map[string]string{"tls.crt": nextCert, "tls.key": nextKey})
	require.NoError(t, provider.OnChange(t.Context(), files.swapEvent()))
	require.Equal(t, []string{"agent-v2"}, drainUpdates(updates))
	current, err := remote.clientCertificate()
	require.NoError(t, err)
	require.Equal(t, "agent-v2", certificateName(t, current))

	// Invalid files return an error, which ends the watch and exits the agent
	// (see Test_watchTLSSource), and are never used.
	expiredCert, expiredKey := expiredClientCertificate(t, "agent-expired")
	caCert, caKey := caTestCertificate(t)
	_, otherKey := testClientCertificateFromCA(t, "agent-other", caTLSCertificate(t, caCert, caKey))
	for name, data := range map[string][2]string{
		"unparsable":     {"bad", "bad"},
		"expired":        {expiredCert, expiredKey},
		"CA certificate": {caCert, caKey},
		"mismatched key": {nextCert, otherKey},
	} {
		t.Run(name, func(t *testing.T) {
			files.update(t, map[string]string{"tls.crt": data[0], "tls.key": data[1]})
			require.Error(t, provider.OnChange(t.Context(), files.swapEvent()))
			require.Empty(t, drainUpdates(updates))
			current, err := remote.clientCertificate()
			require.NoError(t, err)
			require.Equal(t, "agent-v2", certificateName(t, current))
		})
	}
}

// Test_watchTLSSource checks that a failed watch exits the agent, as on the
// principal, and that a watch ended by shutdown does not.
func Test_watchTLSSource(t *testing.T) {
	t.Run("failed watch exits", func(t *testing.T) {
		exits := captureExit(t)
		watchTLSSource(t.Context(), watchResult{err: errors.New("invalid CA file")})
		requireExit(t, exits)
	})
	t.Run("canceled watch does not exit", func(t *testing.T) {
		exits := captureExit(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		watchTLSSource(ctx, watchResult{err: context.Canceled})
		require.Empty(t, exits)
	})
}

// watchResult is a TLS source whose Watch returns err.
type watchResult struct {
	tlsutil.TLSSource
	err error
}

func (w watchResult) Watch(context.Context) error {
	return w.err
}

func Test_WithTLSReloadFromSecret(t *testing.T) {
	caPEM, caKeyPEM, certPEM, keyPEM := testClientCertificate(t, "agent-v1")
	ca, err := tls.X509KeyPair([]byte(caPEM), []byte(caKeyPEM))
	require.NoError(t, err)

	kube := fake.NewSimpleClientset(clientTLSSecret(certPEM, keyPEM))
	remote, err := NewRemote("principal.example.com", 443, WithTLSReloadFromSecret(kube, "argocd", "argocd-agent-client-tls"))
	require.NoError(t, err)
	first, err := remote.clientCertificate()
	require.NoError(t, err)
	require.Equal(t, "agent-v1", certificateName(t, first))

	exits := captureExit(t)
	updates := recordUpdates(t, remote)
	remote.StartTLSReload(t.Context())

	// The informer lists the client Secret before it watches.
	require.True(t, receivedUpdate(updates, "agent-v1", 10*time.Second))

	nextCert, nextKey := testClientCertificateFromCA(t, "agent-v2", ca)
	require.Eventually(t, func() bool {
		updateClientTLSSecret(t, kube, nextCert, nextKey)
		return receivedUpdate(updates, "agent-v2", 250*time.Millisecond)
	}, 10*time.Second, 10*time.Millisecond)
	current, err := remote.clientCertificate()
	require.NoError(t, err)
	require.Equal(t, "agent-v2", certificateName(t, current))
	require.Empty(t, exits)

	// Each kind of invalid Secret data exits the agent and is never used.
	expiredCert, expiredKey := expiredClientCertificate(t, "agent-expired")
	caCert, caKey := caTestCertificate(t)
	for name, data := range map[string][2]string{
		"unparsable":     {"bad", "bad"},
		"expired":        {expiredCert, expiredKey},
		"CA certificate": {caCert, caKey},
	} {
		t.Run(name, func(t *testing.T) {
			drainUpdates(updates)
			updateClientTLSSecret(t, kube, data[0], data[1])
			requireExit(t, exits)
			require.Empty(t, drainUpdates(updates))
			current, err := remote.clientCertificate()
			require.NoError(t, err)
			require.Equal(t, "agent-v2", certificateName(t, current))
		})
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

	kube := fake.NewSimpleClientset(clientTLSSecret(certPEM, keyPEM), caSecret(caPEM))
	port := listener.Addr().(*net.TCPAddr).Port
	remote, err := NewRemote("localhost", port,
		WithRootCAReloadFromSecret(kube, "argocd", "argocd-agent-ca"),
		WithTLSReloadFromSecret(kube, "argocd", "argocd-agent-client-tls"),
	)
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
		_, err := kube.CoreV1().Secrets("argocd").Update(t.Context(), caSecret(newCAPEM), metav1.UpdateOptions{})
		require.NoError(t, err)
		return dial() == nil
	}, 10*time.Second, 100*time.Millisecond)
}

// Test_WithRootCAReload checks that the CA source is watched whichever source
// the client certificate comes from.
func Test_WithRootCAReload(t *testing.T) {
	caPEM, _, certPEM, keyPEM := testClientCertificate(t, "agent-v1")
	newCAPEM, _, err := tlsutil.GenerateCaCertificate("test-ca-2", 30, tlsutil.KeyGenOptions{})
	require.NoError(t, err)

	type sources struct {
		kube      kubernetes.Interface
		certFiles mountedFiles
		caFile    mountedFiles
	}
	newSources := func(t *testing.T) sources {
		return sources{
			kube: fake.NewSimpleClientset(
				clientTLSSecret(certPEM, keyPEM),
				caSecret(caPEM),
			),
			certFiles: newMountedFiles(t, map[string]string{"tls.crt": certPEM, "tls.key": keyPEM}),
			caFile:    newMountedFiles(t, map[string]string{"ca.crt": caPEM}),
		}
	}
	// writeCASecret updates the CA Secret; the running informer picks it up.
	writeCASecret := func(t *testing.T, _ *Remote, s sources, pemData string) error {
		_, err := s.kube.CoreV1().Secrets("argocd").Update(t.Context(), caSecret(pemData), metav1.UpdateOptions{})
		require.NoError(t, err)
		return nil
	}
	// writeCAFile swaps the CA file and delivers the swap event to the provider.
	writeCAFile := func(t *testing.T, r *Remote, s sources, pemData string) error {
		s.caFile.update(t, map[string]string{"ca.crt": pemData})
		return fileProvider(t, r.caSource).OnChange(t.Context(), s.caFile.swapEvent())
	}
	certFromSecret := func(s sources) RemoteOption {
		return WithTLSReloadFromSecret(s.kube, "argocd", "argocd-agent-client-tls")
	}
	certFromFiles := func(s sources) RemoteOption {
		return WithTLSReloadFromFile(s.certFiles.path("tls.crt"), s.certFiles.path("tls.key"))
	}
	caFromSecret := func(s sources) RemoteOption { return WithRootCAReloadFromSecret(s.kube, "argocd", "argocd-agent-ca") }
	caFromFile := func(s sources) RemoteOption { return WithRootCAReloadFromFile(s.caFile.path("ca.crt")) }

	tests := []struct {
		name    string
		cert    func(sources) RemoteOption
		ca      func(sources) RemoteOption
		writeCA func(*testing.T, *Remote, sources, string) error
		caFile  bool
	}{
		{"secret certificate and CA secret", certFromSecret, caFromSecret, writeCASecret, false},
		{"secret certificate and CA file", certFromSecret, caFromFile, writeCAFile, true},
		{"certificate files and CA secret", certFromFiles, caFromSecret, writeCASecret, false},
		{"certificate files and CA file", certFromFiles, caFromFile, writeCAFile, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSources(t)
			remote, err := NewRemote("principal.example.com", 443, tt.ca(s), tt.cert(s))
			require.NoError(t, err)
			require.True(t, certPool(t, caPEM).Equal(remote.tlsConfigForDial().RootCAs))

			exits := captureExit(t)
			remote.StartTLSReload(t.Context())
			want := certPool(t, newCAPEM)
			require.NoError(t, tt.writeCA(t, remote, s, newCAPEM))
			require.Eventually(t, func() bool {
				pool := remote.tlsConfigForDial().RootCAs
				require.NotNil(t, pool)
				return want.Equal(pool)
			}, 10*time.Second, 100*time.Millisecond)
			require.Empty(t, exits)

			cert, err := remote.clientCertificate()
			require.NoError(t, err)
			require.Equal(t, "agent-v1", certificateName(t, cert), "a CA reload must not change the client certificate")

			// Invalid CA data is never used. A CA Secret exits the agent from
			// the informer; a CA file returns the error that ends the watch.
			err = tt.writeCA(t, remote, s, "not a certificate")
			if tt.caFile {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				requireExit(t, exits)
			}
			require.True(t, want.Equal(remote.tlsConfigForDial().RootCAs))
		})
	}

	t.Run("secret certificate without server verification", func(t *testing.T) {
		s := newSources(t)
		remote, err := NewRemote("principal.example.com", 443, WithInsecureSkipTLSVerify(), certFromSecret(s))
		require.NoError(t, err)
		require.Nil(t, remote.caSource)
		cfg := remote.tlsConfigForDial()
		require.True(t, cfg.InsecureSkipVerify)
		require.Nil(t, cfg.RootCAs)
		cert, err := remote.clientCertificate()
		require.NoError(t, err)
		require.Equal(t, "agent-v1", certificateName(t, cert))
	})
}

// Test_noteClientCertificate checks that each new client certificate is logged
// once with its fingerprint and expiry, and that an identity change is a warning.
func Test_noteClientCertificate(t *testing.T) {
	caPEM, caKeyPEM, certPEM, keyPEM := testClientCertificate(t, "agent-a")
	ca, err := tls.X509KeyPair([]byte(caPEM), []byte(caKeyPEM))
	require.NoError(t, err)
	logger := logrus.StandardLogger()
	previousLevel := logger.GetLevel()
	previousHooks := logger.ReplaceHooks(make(logrus.LevelHooks))
	logger.SetLevel(logrus.InfoLevel)
	hook := logtest.NewLocal(logger)
	t.Cleanup(func() {
		logger.ReplaceHooks(previousHooks)
		logger.SetLevel(previousLevel)
	})

	kube := fake.NewSimpleClientset(clientTLSSecret(certPEM, keyPEM))
	remote, err := NewRemote("principal.example.com", 443, WithTLSReloadFromSecret(kube, "argocd", "argocd-agent-client-tls"))
	require.NoError(t, err)

	loaded := func() []*logrus.Entry {
		var entries []*logrus.Entry
		for _, entry := range hook.AllEntries() {
			if entry.Message == "Loaded client certificate" {
				entries = append(entries, entry)
			}
		}
		return entries
	}
	warnings := func() int {
		count := 0
		for _, entry := range hook.AllEntries() {
			if entry.Level == logrus.WarnLevel && entry.Data[logfields.PreviousCertIdentity] != nil {
				count++
			}
		}
		return count
	}
	keyPair := func(certPEM, keyPEM string) tls.Certificate {
		cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		require.NoError(t, err)
		return cert
	}

	first := loaded()
	require.Len(t, first, 1)
	leaf := parseLeaf(t, certPEM)
	require.Equal(t, tlsutil.CertificateFingerprint(leaf), first[0].Data[logfields.CertFingerprint])
	require.Equal(t, leaf.NotAfter.UTC().Format(time.RFC3339), first[0].Data[logfields.CertExpiry])

	// The same certificate again, as on the informer's first list, is not logged.
	remote.noteClientCertificate(keyPair(certPEM, keyPEM))
	require.Len(t, loaded(), 1)

	// A renewed certificate with the same identity is logged without a warning.
	renewedCert, renewedKey := testClientCertificateFromCA(t, "agent-a", ca)
	remote.noteClientCertificate(keyPair(renewedCert, renewedKey))
	require.Len(t, loaded(), 2)
	require.Equal(t, tlsutil.CertificateFingerprint(parseLeaf(t, renewedCert)), loaded()[1].Data[logfields.CertFingerprint])
	require.Zero(t, warnings())

	// A certificate for another agent name is logged with a warning.
	otherCert, otherKey := testClientCertificateFromCA(t, "agent-b", ca)
	remote.noteClientCertificate(keyPair(otherCert, otherKey))
	require.Len(t, loaded(), 3)
	require.Equal(t, 1, warnings())
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
	handler := func(cert tls.Certificate, _ *x509.CertPool) {
		r.noteClientCertificate(cert)
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

// mountedFiles lays out files like a mounted Secret volume: each file is a
// link into ..data, and an update swaps ..data to a new directory in one rename.
type mountedFiles struct {
	dir string
}

func newMountedFiles(t *testing.T, files map[string]string) mountedFiles {
	t.Helper()
	m := mountedFiles{dir: t.TempDir()}
	m.update(t, files)
	for name := range files {
		require.NoError(t, os.Symlink(filepath.Join("..data", name), m.path(name)))
	}
	return m
}

func (m mountedFiles) path(name string) string {
	return filepath.Join(m.dir, name)
}

func (m mountedFiles) update(t *testing.T, files map[string]string) {
	t.Helper()
	version, err := os.MkdirTemp(m.dir, "..version_")
	require.NoError(t, err)
	for name, data := range files {
		require.NoError(t, os.WriteFile(filepath.Join(version, name), []byte(data), 0o600))
	}
	link := filepath.Join(m.dir, "..data_tmp")
	require.NoError(t, os.Symlink(filepath.Base(version), link))
	require.NoError(t, os.Rename(link, filepath.Join(m.dir, "..data")))
}

// swapEvent is the event inotify reports for the ..data swap. kqueue on macOS
// does not report a rename over an existing link, so tests deliver it directly.
func (m mountedFiles) swapEvent() fsnotify.Event {
	return fsnotify.Event{Name: filepath.Join(m.dir, "..data"), Op: fsnotify.Create}
}

func fileProvider(t *testing.T, source tlsutil.TLSSource) *tlsutil.TLSFileProvider {
	t.Helper()
	provider, ok := source.(*tlsutil.TLSFileProvider)
	require.True(t, ok)
	return provider
}

func caTLSCertificate(t *testing.T, caPEM, caKeyPEM string) tls.Certificate {
	t.Helper()
	ca, err := tls.X509KeyPair([]byte(caPEM), []byte(caKeyPEM))
	require.NoError(t, err)
	return ca
}

// captureExit makes logrus Fatal calls send the exit code to the returned
// channel instead of ending the test process.
func captureExit(t *testing.T) <-chan int {
	t.Helper()
	exits := make(chan int, 100)
	logger := logrus.StandardLogger()
	previous := logger.ExitFunc
	logger.ExitFunc = func(code int) { exits <- code }
	t.Cleanup(func() { logger.ExitFunc = previous })
	return exits
}

func requireExit(t *testing.T, exits <-chan int) {
	t.Helper()
	select {
	case code := <-exits:
		require.Equal(t, 1, code)
	case <-time.After(10 * time.Second):
		t.Fatal("invalid TLS material did not exit the agent")
	}
}

func caTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	caPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate("test-ca-3", 30, tlsutil.KeyGenOptions{})
	require.NoError(t, err)
	return caPEM, caKeyPEM
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

func caSecret(caPEM string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "argocd-agent-ca", Namespace: "argocd"},
		Data:       map[string][]byte{"ca.crt": []byte(caPEM)},
	}
}

func certPool(t *testing.T, caPEM string) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM([]byte(caPEM)))
	return pool
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

func parseLeaf(t *testing.T, certPEM string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	require.NotNil(t, block)
	leaf, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return leaf
}
