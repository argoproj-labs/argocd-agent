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
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"k8s.io/client-go/kubernetes"
)

// WithTLSReloadFromFile loads the client certificate from files and reloads it
// when those files change.
func WithTLSReloadFromFile(certPath, keyPath string) RemoteOption {
	return func(r *Remote) error {
		cert, err := tlsutil.TLSCertFromFile(certPath, keyPath, true)
		if err != nil {
			return fmt.Errorf("unable to read TLS client cert: %w", err)
		}
		provider := tlsutil.NewTLSFileProvider(certPath, keyPath, "", &tlsutil.TLSMaterial{Cert: cert})
		provider.KeepPreviousCertificate()
		r.enableTLSReload(provider)
		return nil
	}
}

// WithTLSReloadFromSecret loads the client certificate from a Kubernetes Secret
// and reloads it when that Secret changes.
func WithTLSReloadFromSecret(kube kubernetes.Interface, namespace, certSecretName string) RemoteOption {
	return func(r *Remote) error {
		cert, err := tlsutil.TLSCertFromSecret(context.Background(), kube, namespace, certSecretName)
		if err != nil {
			return fmt.Errorf("unable to read TLS client from secret: %w", err)
		}
		provider := tlsutil.NewTLSSecretProvider(certSecretName, "", namespace, kube, &tlsutil.TLSMaterial{Cert: cert})
		provider.KeepPreviousCertificate()
		r.enableTLSReload(provider)
		return nil
	}
}

// WithRootCAReloadFromFile loads the root CA pool from a file and reloads it
// when that file changes.
func WithRootCAReloadFromFile(caPath string) RemoteOption {
	return func(r *Remote) error {
		if err := WithRootAuthoritiesFromFile(caPath)(r); err != nil {
			return err
		}
		provider := tlsutil.NewTLSFileProvider("", "", caPath, &tlsutil.TLSMaterial{CAPool: r.tlsConfig.RootCAs})
		provider.KeepPreviousCertificate()
		r.enableRootCAReload(provider)
		return nil
	}
}

// WithRootCAReloadFromSecret loads the root CA pool from a Kubernetes Secret
// and reloads it when that Secret changes.
func WithRootCAReloadFromSecret(kube kubernetes.Interface, namespace, caSecretName string) RemoteOption {
	return func(r *Remote) error {
		if err := WithRootAuthoritiesFromSecret(kube, namespace, caSecretName)(r); err != nil {
			return err
		}
		provider := tlsutil.NewTLSSecretProvider("", caSecretName, namespace, kube, &tlsutil.TLSMaterial{CAPool: r.tlsConfig.RootCAs})
		provider.KeepPreviousCertificate()
		r.enableRootCAReload(provider)
		return nil
	}
}

func (r *Remote) enableTLSReload(source tlsutil.TLSSource) {
	r.tlsSource = source
	setUpdateHandler(source, func(cert tls.Certificate, _ *x509.CertPool) {
		r.noteClientCertificate(cert)
	})
	r.tlsConfig.GetClientCertificate = r.getClientCertificate
	r.noteClientCertificate(currentCertificate(source))
}

func (r *Remote) enableRootCAReload(source tlsutil.TLSSource) {
	r.caSource = source
	setUpdateHandler(source, func(_ tls.Certificate, pool *x509.CertPool) {
		r.storeRootCAs(pool)
	})
	_, pool := source.Load()
	r.storeRootCAs(pool)
}

func setUpdateHandler(source tlsutil.TLSSource, fn func(tls.Certificate, *x509.CertPool)) {
	switch provider := source.(type) {
	case *tlsutil.TLSFileProvider:
		provider.SetUpdateHandler(fn)
	case *tlsutil.TLSSecretProvider:
		provider.SetUpdateHandler(fn)
	}
}

func (r *Remote) storeRootCAs(pool *x509.CertPool) {
	if pool == nil || r.tlsConfig == nil || r.tlsConfig.InsecureSkipVerify {
		return
	}
	r.rootCAs.Store(pool)
}

func (r *Remote) tlsConfigForDial() *tls.Config {
	if r.tlsConfig == nil {
		return nil
	}
	cfg := r.tlsConfig.Clone()
	if pool := r.rootCAs.Load(); pool != nil && !cfg.InsecureSkipVerify {
		cfg.RootCAs = pool
	}
	return cfg
}

func currentCertificate(source tlsutil.TLSSource) tls.Certificate {
	cert, _ := source.Load()
	return cert
}

func (r *Remote) getClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	if r.tlsSource == nil {
		return nil, fmt.Errorf("no client certificate is loaded")
	}
	cert, _ := r.tlsSource.Load()
	if len(cert.Certificate) == 0 {
		return nil, fmt.Errorf("no client certificate is loaded")
	}
	return &cert, nil
}

// SetClientCertificateObserver records the client certificate end time.
// The observer is called for the current certificate and after each reload.
func (r *Remote) SetClientCertificateObserver(fn func(time.Time)) {
	r.tlsMu.Lock()
	r.certObserver = fn
	r.tlsMu.Unlock()
	if r.tlsSource != nil {
		r.noteClientCertificate(currentCertificate(r.tlsSource))
	}
}

func (r *Remote) noteClientCertificate(cert tls.Certificate) {
	if len(cert.Certificate) == 0 {
		return
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		log().WithError(err).Error("Could not read the client certificate")
		return
	}
	identity := certificateIdentity(leaf)
	r.tlsMu.Lock()
	previous := r.certIdentity
	r.certIdentity = identity
	observer := r.certObserver
	r.tlsMu.Unlock()
	if previous != "" && previous != identity {
		log().WithField(logfields.PreviousCertIdentity, previous).
			WithField(logfields.CertIdentity, identity).
			Warn("The reloaded client certificate has a different identity; the principal may treat it as a different agent")
	}
	if observer != nil {
		observer(leaf.NotAfter)
	}
}

// certificateIdentity returns the certificate fields the principal can read
// the agent name from: the subject and the URI SANs.
func certificateIdentity(leaf *x509.Certificate) string {
	identity := leaf.Subject.String()
	for _, uri := range leaf.URIs {
		identity += " " + uri.String()
	}
	return identity
}

// StartTLSReload watches the configured client certificate and root CA sources
// until ctx is canceled. A failed watch is retried. Invalid data does not
// replace the current certificate or CA pool.
func (r *Remote) StartTLSReload(ctx context.Context) {
	for _, source := range []tlsutil.TLSSource{r.tlsSource, r.caSource} {
		if source != nil {
			go watchTLSSource(ctx, source)
		}
	}
}

func watchTLSSource(ctx context.Context, source tlsutil.TLSSource) {
	backoff := time.Second
	for {
		err := source.Watch(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			return
		}
		log().WithError(err).Error("TLS reload watch stopped; retrying")
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
