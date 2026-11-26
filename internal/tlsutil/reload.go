// Copyright 2026 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package tlsutil

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
)

// TLSMaterial holds the data to be used for TLS Config
type TLSMaterial struct {
	// Cert is the client cert for the TLS config
	Cert tls.Certificate
	// CAPool contains the CA cert for the TLS config
	CAPool *x509.CertPool
}

// TLSFileProvider watches file paths for updates to the TLS config
type TLSFileProvider struct {
	// CAPath is the path to the file that holds the CA cert
	CAPath string
	// ClientCertPath is the path to the file that holds the client cert
	ClientCertPath string
	// ClientKeyPath is the path to the file that holds the client key
	ClientKeyPath string
	// Material is the current TLS config to be used
	Material atomic.Pointer[TLSMaterial]
	// publicCAOnly validates the agent's CA file as a public PEM bundle.
	publicCAOnly bool
	// writeMu is used for concurrent on writing only
	writeMu sync.Mutex
	// onUpdate is called after valid TLS material is stored.
	onUpdate func(tls.Certificate, *x509.CertPool)
}

// TLSSecretProvider watches Kubernetes secrets for updates to the TLS config
type TLSSecretProvider struct {
	// ClientSecretName is the name of the secret that holds the TLS client data
	ClientSecretName string
	// CASecretName is the name of the secret that holds the CA data
	CASecretName string
	// Namespace is the namespace where both secrets live
	Namespace string
	// Material is the current TLS config to be used
	Material atomic.Pointer[TLSMaterial]
	// kubeClient is the client to use for making the Kubernetes API requests
	kubeClient kubernetes.Interface
	// publicCAOnly validates the agent's CA Secret as a public PEM bundle.
	publicCAOnly bool
	// writeMu is used for concurrent on writing only
	writeMu sync.Mutex
	// onUpdate is called after valid TLS material is stored.
	onUpdate func(tls.Certificate, *x509.CertPool)
}

// TLSSource is an interface for a struct that intends to watch a source for TLS data
type TLSSource interface {
	// Load is meant for returning the current TLS material
	Load() (tls.Certificate, *x509.CertPool)
	// Watch is meant for watching the source for updates and then updating accordingly
	Watch(ctx context.Context) error
	// Reload is meant for reloading all of the sources in the provider to be used on a restart or other scenario
	Reload(ctx context.Context) error
}

// NewTLSFileProvider creates a new TLSFileProvider with the file path provided
func NewTLSFileProvider(clientCertPath, clientKeyPath, caCertPath string, material *TLSMaterial) *TLSFileProvider {
	provider := &TLSFileProvider{
		CAPath:         caCertPath,
		ClientCertPath: clientCertPath,
		ClientKeyPath:  clientKeyPath,
	}
	provider.Material.Store(material)

	return provider
}

// NewPublicCAFileProvider loads the agent's root CA file for hot reload.
func NewPublicCAFileProvider(caPath string) (*TLSFileProvider, error) {
	provider := NewTLSFileProvider("", "", caPath, nil)
	provider.publicCAOnly = true
	pool, err := provider.readCAPool()
	if err != nil {
		return nil, err
	}
	provider.storeCAPool(pool)
	return provider, nil
}

// SetUpdateHandler sets the function called after valid TLS material is stored.
func (t *TLSFileProvider) SetUpdateHandler(fn func(tls.Certificate, *x509.CertPool)) {
	t.onUpdate = fn
}

func (t *TLSFileProvider) store(material *TLSMaterial) {
	t.Material.Store(material)
	if t.onUpdate != nil && material != nil {
		t.onUpdate(material.Cert, material.CAPool)
	}
}

// TLSFileProvider.Load returns the current TLS data
func (t *TLSFileProvider) Load() (tls.Certificate, *x509.CertPool) {
	return t.Material.Load().clone()
}

// clone returns the certificate and a copy of the CA pool. The CA pool is nil
// when the material has no CA source.
func (m *TLSMaterial) clone() (tls.Certificate, *x509.CertPool) {
	if m == nil {
		return tls.Certificate{}, nil
	}
	if m.CAPool == nil {
		return m.Cert, nil
	}
	return m.Cert, m.CAPool.Clone()
}

// TLSFileProvider.Watch watches the file paths provider for the CA, client Cert, and client key for updates
func (t *TLSFileProvider) Watch(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Watch the dirs of the file paths to continue watching on the case where the file is removed or renamed
	if t.CAPath != "" {
		err = watcher.Add(filepath.Dir(t.CAPath))
		if err != nil {
			return err
		}
	}
	if t.ClientCertPath != "" {
		err = watcher.Add(filepath.Dir(t.ClientCertPath))
		if err != nil {
			return err
		}
	}
	if t.ClientKeyPath != "" {
		err = watcher.Add(filepath.Dir(t.ClientKeyPath))
		if err != nil {
			return err
		}
	}

	// Re-read the agent's public CA file after registering its watch to catch
	// replacements made between the initial load and registration.
	if t.publicCAOnly && t.CAPath != "" && ctx.Err() == nil {
		if err := t.caOnChange(); err != nil {
			return err
		}
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("fsnotify watch event channel unexpectedly closed")
			}

			logrus.WithField(logfields.FilePath, event.Name).Info("Event received for a filepath being watched")

			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove) {
				err := t.OnChange(ctx, event)
				if err != nil {
					return err
				}
			}
			continue
		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("fsnotify watch error channel unexpectedly closed")
			}
			return err
		case <-ctx.Done():
			return nil
		}
	}
}

// TLSFileProvider.OnChange handles changing out TLS data based on the event received
func (t *TLSFileProvider) OnChange(ctx context.Context, event fsnotify.Event) error {
	var err error
	logrus.WithFields(logrus.Fields{
		logfields.FileName: event.Name,
		logfields.Event:    event.Op,
	}).Info("Change detected in TLS data, updating internal store")

	// When watching the current directory, fsnotify can report a write to
	// "ca.crt" as "./ca.crt". Clean both paths so the event triggers a reload.
	eventPath := filepath.Clean(event.Name)
	matchesPath := func(configuredPath string) bool {
		return event.Name != "" && configuredPath != "" && eventPath == filepath.Clean(configuredPath)
	}

	switch {
	case filepath.Base(event.Name) == "..data":
		err = t.Reload(ctx)
	case matchesPath(t.CAPath):
		err = t.caOnChange()
	case matchesPath(t.ClientCertPath), matchesPath(t.ClientKeyPath):
		err = t.clientOnChange()
	}
	if err == nil {
		logrus.Info("Successfully updated TLS configuration to have new data")
	}
	return err
}

// TLSFileProvider.clientOnChange reads the client cert from its path and updates the TLS data if it is valid
func (t *TLSFileProvider) clientOnChange() error {
	cert, err := TLSCertFromFile(t.ClientCertPath, t.ClientKeyPath, true)
	if err != nil {
		return err
	}

	if err := validateNewClientCert(cert); err != nil {
		return fmt.Errorf("validation failed on client cert file reload: %v", err)
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	var caPool *x509.CertPool
	currentMaterial := t.Material.Load()
	if currentMaterial != nil {
		caPool = currentMaterial.CAPool
	}

	t.store(&TLSMaterial{
		Cert:   cert,
		CAPool: caPool,
	})

	return nil
}

func (t *TLSFileProvider) readCAPool() (*x509.CertPool, error) {
	caPEM, err := os.ReadFile(t.CAPath)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if t.publicCAOnly {
		certs, err := parsePublicCertificates(caPEM)
		if err != nil {
			return nil, fmt.Errorf("agent root CA file %q %w. Replace it with a PEM bundle containing only public CA certificates", t.CAPath, err)
		}
		if len(certs) == 0 {
			return nil, fmt.Errorf("invalid PEM data in %s", t.CAPath)
		}
		hasCurrent, err := addPublicCAs(pool, certs, time.Now())
		if err != nil {
			return nil, fmt.Errorf("agent root CA file %q %w. Use only CA certificates in this bundle", t.CAPath, err)
		}
		if !hasCurrent {
			return nil, fmt.Errorf("agent root CA file %q contains no currently valid CA certificates", t.CAPath)
		}
		return pool, nil
	}
	if err := validateNewCACert(caPEM); err != nil {
		return nil, err
	}

	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("invalid PEM data in %s", t.CAPath)
	}
	return pool, nil
}

// TLSFileProvider.caOnChange reads the file path the CA is located at and updates the TLS data if it is valid
func (t *TLSFileProvider) caOnChange() error {
	pool, err := t.readCAPool()
	if err != nil {
		return fmt.Errorf("validation failed on CA cert file reload: %w", err)
	}
	t.storeCAPool(pool)
	return nil
}

func (t *TLSFileProvider) storeCAPool(pool *x509.CertPool) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	var cert tls.Certificate
	currentMaterial := t.Material.Load()
	if currentMaterial != nil {
		cert = currentMaterial.Cert
	}

	t.store(&TLSMaterial{
		Cert:   cert,
		CAPool: pool,
	})
}

// TLSFileProvider.Reload reads the sources for the TLS data and sets them if they are new
func (t *TLSFileProvider) Reload(ctx context.Context) error {
	logrus.Info("Reloading TLS data")

	newMaterial := &TLSMaterial{}

	if t.ClientCertPath != "" {
		cert, err := TLSCertFromFile(t.ClientCertPath, t.ClientKeyPath, true)
		if err != nil {
			return err
		}

		if err = validateNewClientCert(cert); err == nil {
			newMaterial.Cert = cert
		} else {
			return fmt.Errorf("failed to validate client cert")
		}
	}

	if t.CAPath != "" {
		pool, err := t.readCAPool()
		if err != nil {
			return fmt.Errorf("validation failed on reloading ca pool: %w", err)
		}
		newMaterial.CAPool = pool
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	t.store(newMaterial)

	logrus.Info("Successfully reloaded TLS data")

	return nil
}

// NewTLSSecretProvider creates a TLSSecretProvider and stores initial TLS data
func NewTLSSecretProvider(clientSecretName, caSecretName, namespace string, kubeClient kubernetes.Interface, material *TLSMaterial) *TLSSecretProvider {
	provider := &TLSSecretProvider{
		ClientSecretName: clientSecretName,
		CASecretName:     caSecretName,
		Namespace:        namespace,
		kubeClient:       kubeClient,
	}
	provider.Material.Store(material)

	return provider
}

// NewPublicCASecretProvider loads the agent's root CA Secret for hot reload.
func NewPublicCASecretProvider(ctx context.Context, kubeClient kubernetes.Interface, namespace, name string) (*TLSSecretProvider, error) {
	secret, err := kubeClient.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not read root CA Secret %s/%s: %w", namespace, name, err)
	}
	provider := NewTLSSecretProvider("", name, namespace, kubeClient, nil)
	provider.publicCAOnly = true
	pool, err := provider.loadCAPool(secret)
	if err != nil {
		return nil, err
	}
	provider.store(&TLSMaterial{CAPool: pool})
	return provider, nil
}

// SetUpdateHandler sets the function called after valid TLS material is stored.
func (t *TLSSecretProvider) SetUpdateHandler(fn func(tls.Certificate, *x509.CertPool)) {
	t.onUpdate = fn
}

func (t *TLSSecretProvider) store(material *TLSMaterial) {
	t.Material.Store(material)
	if t.onUpdate != nil && material != nil {
		t.onUpdate(material.Cert, material.CAPool)
	}
}

// TLSSecretProvider.Load returns the current TLS config that is set
func (t *TLSSecretProvider) Load() (tls.Certificate, *x509.CertPool) {
	return t.Material.Load().clone()
}

// TLSSecretProvider.Watch creates a Kubernetes secret informer to watch kubernetes secrets for new TLS data
func (t *TLSSecretProvider) Watch(ctx context.Context) error {
	informer, err := informer.NewInformer(ctx,
		informer.WithListHandler[*corev1.Secret](func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
			return t.kubeClient.CoreV1().Secrets(t.Namespace).List(ctx, opts)
		}),
		informer.WithWatchHandler[*corev1.Secret](func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			return t.kubeClient.CoreV1().Secrets(t.Namespace).Watch(ctx, opts)
		}),
		informer.WithAddHandler[*corev1.Secret](func(secret *corev1.Secret) {
			t.handleChange(secret)
		}),
		informer.WithUpdateHandler[*corev1.Secret](func(old *corev1.Secret, new *corev1.Secret) {
			t.handleChange(new)
		}),
	)
	if err != nil {
		return err
	}

	go func() {
		if err := informer.Start(ctx); err != nil {
			logrus.WithError(err).Fatal("TLS secret informer exited non-successfully")
		}
	}()
	<-ctx.Done()
	err = informer.Stop()
	if err != nil {
		return err
	}

	return nil
}

func (t *TLSSecretProvider) handleChange(secret *corev1.Secret) {
	if err := t.OnChange(secret); err != nil {
		logrus.WithError(err).WithField(logfields.SecretName, secret.Name).Fatal("Failed to change certificates")
	}
}

// TLSSecretProvider.OnChange handles changing TLS data based on which Kubernetes secret is read
func (t *TLSSecretProvider) OnChange(secret *corev1.Secret) error {
	if secret.Name != t.ClientSecretName && secret.Name != t.CASecretName {
		return nil
	}
	var err error
	logrus.WithFields(logrus.Fields{
		logfields.SecretName: secret.Name,
	}).Info("Change detected in TLS data, updating internal store")
	switch secret.Name {
	case t.ClientSecretName:
		err = t.clientOnChange(secret)
	case t.CASecretName:
		err = t.caOnChange(secret)
	}
	if err != nil {
		return err
	}
	logrus.Info("Successfully updated TLS configuration to have new data")
	return nil
}

// TLSSecretProvider.clientOnChange handles changing the client cert from a Kubernetes secret
func (t *TLSSecretProvider) clientOnChange(secret *corev1.Secret) error {
	if secret.Type != tlsTypeLabelValue {
		return fmt.Errorf("%s/%s is not a tls secret", secret.Namespace, secret.Name)
	}
	if len(secret.Data) == 0 {
		return fmt.Errorf("%s/%s is empty", secret.Namespace, secret.Name)
	}

	crt := secret.Data[tlsCertFieldName]
	key := secret.Data[tlsKeyFieldName]
	if crt == nil || key == nil {
		return fmt.Errorf("missing either cert or key in secret %s/%s", secret.Namespace, secret.Name)
	}
	cert, err := tls.X509KeyPair(crt, key)
	if err != nil {
		return fmt.Errorf("cert or key data in %s/%s is invalid", secret.Namespace, secret.Name)
	}

	if err := validateNewClientCert(cert); err != nil {
		return fmt.Errorf("validation failed on client cert secret reload: %v", err)
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	var caPool *x509.CertPool
	currentMaterial := t.Material.Load()
	if currentMaterial != nil {
		caPool = currentMaterial.CAPool
	}

	t.store(&TLSMaterial{
		Cert:   cert,
		CAPool: caPool,
	})

	return nil
}

// readTLSDataFromKey reads TLS data from a key in a Kubernetes secret and validates it and then adds it to the provided pool
// if the ca is invalid an error will be returned
func readTLSDataFromKey(caPool *x509.CertPool, key string, secret *corev1.Secret) error {
	crtBytes, ok := secret.Data[key]
	if crtBytes != nil && ok {
		if err := validateNewCACert(crtBytes); err != nil {
			return err
		}

		if ok := caPool.AppendCertsFromPEM(crtBytes); !ok {
			return fmt.Errorf("failed to append PEM to cert pool")
		}
	} else {
		return fmt.Errorf("key does not exist in the secret")
	}
	return nil
}

// loadCAPool validates the Secret and loads the same CA fields at startup and on reload.
func (t *TLSSecretProvider) loadCAPool(secret *corev1.Secret) (*x509.CertPool, error) {
	if len(secret.Data) == 0 {
		return nil, fmt.Errorf("%s/%s is empty", secret.Namespace, secret.Name)
	}
	if t.publicCAOnly {
		return loadPublicCASecret(secret)
	}

	pool := x509.NewCertPool()
	loaded := false
	for _, key := range []string{tlsCertFieldName, "ca.crt"} {
		if err := readTLSDataFromKey(pool, key, secret); err == nil {
			loaded = true
		}
	}
	if !loaded {
		return nil, fmt.Errorf("%s/%s: no certs were found. Put valid CA certificates in ca.crt or tls.crt", secret.Namespace, secret.Name)
	}
	return pool, nil
}

func loadPublicCASecret(secret *corev1.Secret) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	now := time.Now()
	loaded, hasCurrent := false, false
	for field, data := range secret.Data {
		if len(data) == 0 {
			continue
		}
		var (
			certs []*x509.Certificate
			err   error
		)
		if field == tlsKeyFieldName || field == "ca.key" {
			err = fmt.Errorf("contains a private key")
		} else {
			certs, err = parsePublicCertificates(data)
		}
		if err == nil && len(certs) == 0 {
			err = fmt.Errorf("contains no PEM certificates")
		}
		if err != nil {
			return nil, fmt.Errorf("agent root CA Secret %s/%s %w in field %q. Replace it with a Secret containing only public CA certificates", secret.Namespace, secret.Name, err, field)
		}
		if field != tlsCertFieldName && field != "ca.crt" {
			continue
		}
		fieldHasCurrent, err := addPublicCAs(pool, certs, now)
		if err != nil {
			return nil, fmt.Errorf("agent root CA Secret %s/%s %w in field %q. Use only CA certificates in this field", secret.Namespace, secret.Name, err, field)
		}
		loaded = true
		hasCurrent = hasCurrent || fieldHasCurrent
	}
	if !loaded {
		return nil, fmt.Errorf("%s/%s: no certs were found. Put valid CA certificates in ca.crt or tls.crt", secret.Namespace, secret.Name)
	}
	if !hasCurrent {
		return nil, fmt.Errorf("agent root CA Secret %s/%s contains no currently valid CA certificates in ca.crt or tls.crt", secret.Namespace, secret.Name)
	}
	return pool, nil
}

func addPublicCAs(pool *x509.CertPool, certs []*x509.Certificate, now time.Time) (bool, error) {
	hasCurrent := false
	for _, cert := range certs {
		if !cert.IsCA {
			return false, fmt.Errorf("contains a leaf certificate")
		}
		// Keep every CA in the bundle. TLS checks dates on the chain it uses.
		pool.AddCert(cert)
		if !now.Before(cert.NotBefore) && !now.After(cert.NotAfter) {
			hasCurrent = true
		}
	}
	return hasCurrent, nil
}

// parsePublicCertificates parses each certificate and checks the surrounding text.
func parsePublicCertificates(data []byte) ([]*x509.Certificate, error) {
	// PEM may include text comments. Check every byte for binary data because
	// pem.Decode can skip bytes before, between, and after PEM blocks.
	if !utf8.Valid(data) || bytes.ContainsFunc(data, func(r rune) bool {
		return unicode.IsControl(r) && !unicode.IsSpace(r)
	}) {
		return nil, fmt.Errorf("contains unexpected binary data")
	}

	var certs []*x509.Certificate
	begin, end := []byte("\n-----BEGIN "), []byte("\n-----END ")
	for len(data) > 0 {
		if !bytes.HasPrefix(data, begin[1:]) {
			line, rest, _ := bytes.Cut(data, []byte("\n"))
			line = bytes.TrimSpace(line)
			if len(line) != 0 && line[0] != '#' {
				return nil, fmt.Errorf("contains unexpected text outside PEM blocks")
			}
			data = rest
			continue
		}

		block, rest := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("contains malformed PEM data")
		}
		// Decode can skip a malformed block and return a later block instead.
		consumed := data[:len(data)-len(rest)]
		if bytes.Contains(consumed, begin) || bytes.Count(consumed, end) != 1 {
			return nil, fmt.Errorf("contains malformed PEM data")
		}
		if block.Type == "CERTIFICATE" && len(block.Headers) == 0 {
			if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
				certs = append(certs, cert)
				data = rest
				continue
			}
		}
		// Recognize key labels (including encrypted keys) and mislabeled key payloads.
		if block.Type == "PRIVATE KEY" || strings.HasSuffix(block.Type, " PRIVATE KEY") || isPrivateKeyDER(block.Bytes) {
			return nil, fmt.Errorf("contains a private key")
		}
		return nil, fmt.Errorf("contains an invalid or unexpected PEM block")
	}
	return certs, nil
}

// isPrivateKeyDER detects supported unencrypted keys whose PEM label may be incorrect.
func isPrivateKeyDER(der []byte) bool {
	if _, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		return true
	}
	if _, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return true
	}
	_, err := x509.ParseECPrivateKey(der)
	return err == nil
}

// TLSSecretProvider.caOnChange handles changing a ca cert from a Kubernetes secret
func (t *TLSSecretProvider) caOnChange(secret *corev1.Secret) error {
	caPool, err := t.loadCAPool(secret)
	if err != nil {
		return err
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	var cert tls.Certificate
	currentMaterial := t.Material.Load()
	if currentMaterial != nil {
		cert = currentMaterial.Cert
	}

	t.store(&TLSMaterial{
		Cert:   cert,
		CAPool: caPool,
	})

	return nil
}

func (t *TLSSecretProvider) Reload(ctx context.Context) error {
	logrus.Info("Reloading TLS data")
	newMaterial := &TLSMaterial{}

	if t.ClientSecretName != "" {
		cert, err := TLSCertFromSecret(ctx, t.kubeClient, t.Namespace, t.ClientSecretName)
		if err != nil {
			return err
		}
		if err := validateNewClientCert(cert); err != nil {
			return fmt.Errorf("failed to validate new client cert: %v", err)
		}
		newMaterial.Cert = cert
	}

	caSecret, err := t.kubeClient.CoreV1().Secrets(t.Namespace).Get(ctx, t.CASecretName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	newMaterial.CAPool, err = t.loadCAPool(caSecret)
	if err != nil {
		return err
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	t.store(newMaterial)

	logrus.Info("Successfully reloaded TLS data")

	return nil
}

// validateNewClientCert is used to validate an incoming client cert. It ensures the cert is not a CA , that it is not expired, and is valid.
func validateNewClientCert(cert tls.Certificate) error {
	if len(cert.Certificate) == 0 || cert.Certificate[0] == nil {
		return fmt.Errorf("no certificate data")
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("could not parse certificate: %v", err)
	}

	if time.Now().Before(leaf.NotBefore) {
		return fmt.Errorf("certificate is not valid until %s", leaf.NotBefore)
	}

	if time.Now().After(leaf.NotAfter) {
		return fmt.Errorf("certificate expired at %s", leaf.NotAfter)
	}

	if leaf.IsCA {
		return fmt.Errorf("certificate provided is a CA")
	}

	return nil
}

// validateNewCACert is used to validate an incoming CA certs. It ensures that the cert is not expired, is valid, and is a CA cert.
func validateNewCACert(caPEM []byte) error {
	found := false
	for {
		var block *pem.Block
		block, caPEM = pem.Decode(caPEM)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return fmt.Errorf("unexpected PEM block type: %s", block.Type)
		}

		caCert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return err
		}

		if time.Now().Before(caCert.NotBefore) {
			return fmt.Errorf("CA certificate is not valid until %s", caCert.NotBefore)
		}

		if time.Now().After(caCert.NotAfter) {
			return fmt.Errorf("CA certificate expired at %s", caCert.NotAfter)
		}

		if !caCert.IsCA {
			return fmt.Errorf("CA certificate provided is not a CA")
		}
		found = true
	}
	if !found {
		return fmt.Errorf("no valid certificate data was found")
	}
	return nil
}
