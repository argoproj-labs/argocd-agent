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
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

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
	// writeMu is used for concurrent on writing only
	writeMu sync.Mutex
	// keepPreviousOnError keeps the last valid certificate when new material is invalid.
	keepPreviousOnError bool
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
	// writeMu is used for concurrent on writing only
	writeMu sync.Mutex
	// keepPreviousOnError keeps the last valid certificate when new material is invalid.
	keepPreviousOnError bool
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

// KeepPreviousCertificate makes an invalid certificate update keep the last valid certificate.
func (t *TLSFileProvider) KeepPreviousCertificate() {
	t.keepPreviousOnError = true
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
	err = watcher.Add(filepath.Dir(t.ClientCertPath))
	if err != nil {
		return err
	}
	err = watcher.Add(filepath.Dir(t.ClientKeyPath))
	if err != nil {
		return err
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

	if filepath.Base(event.Name) == "..data" {
		err = t.Reload(ctx)
	} else {
		switch event.Name {
		case t.CAPath:
			err = t.caOnChange()
		case t.ClientCertPath, t.ClientKeyPath:
			err = t.clientOnChange()
		}
	}
	if err != nil && t.keepPreviousOnError {
		logrus.WithError(err).WithField(logfields.FileName, event.Name).Error("Failed to reload TLS data; keeping the previous certificate")
		return nil
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

// TLSFileProvider.caOnChange reads the file path the CA is located at and updates the TLS data if it is valid
func (t *TLSFileProvider) caOnChange() error {
	bytes, err := os.ReadFile(t.CAPath)
	if err != nil {
		return err
	}

	if err := validateNewCACert(bytes); err != nil {
		return fmt.Errorf("validation failed on CA cert file reload: %v", err)
	}

	certPool := x509.NewCertPool()
	ok := certPool.AppendCertsFromPEM(bytes)
	if !ok {
		return fmt.Errorf("invalid certificate data in %s when updating", t.CAPath)
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
		CAPool: certPool,
	})

	return nil
}

// TLSFileProvider.Reload reads the sources for the TLS data and sets them if they are new
func (t *TLSFileProvider) Reload(ctx context.Context) error {
	logrus.Info("Reloading TLS data")

	newMaterial := &TLSMaterial{}

	cert, err := TLSCertFromFile(t.ClientCertPath, t.ClientKeyPath, true)
	if err != nil {
		return err
	}

	if err = validateNewClientCert(cert); err == nil {
		newMaterial.Cert = cert
	} else {
		return fmt.Errorf("failed to validate client cert")
	}

	if t.CAPath != "" {
		bytes, err := os.ReadFile(t.CAPath)
		if err != nil {
			return err
		}

		if err = validateNewCACert(bytes); err == nil {
			caPool := x509.NewCertPool()
			ok := caPool.AppendCertsFromPEM(bytes)
			if ok {
				newMaterial.CAPool = caPool
			} else {
				return fmt.Errorf("failed to append ca pem to pool")
			}
		} else {
			return fmt.Errorf("validation failed on reloading ca pool: %v", err)
		}
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

// KeepPreviousCertificate makes an invalid certificate update keep the last valid certificate.
func (t *TLSSecretProvider) KeepPreviousCertificate() {
	t.keepPreviousOnError = true
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
	err := t.OnChange(secret)
	if err == nil {
		return
	}
	if t.keepPreviousOnError {
		logrus.WithError(err).WithField(logfields.SecretName, secret.Name).Error("Failed to change certificates; keeping the previous certificate")
		return
	}
	logrus.WithError(err).Fatal("Failed to change certificates")
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

// TLSSecretProvider.caOnChange handles changing a ca cert from a Kubernetes secret
func (t *TLSSecretProvider) caOnChange(secret *corev1.Secret) error {
	if len(secret.Data) == 0 {
		return fmt.Errorf("%s/%s is empty", secret.Namespace, secret.Name)
	}

	caPool := x509.NewCertPool()
	certsInPool := 0

	err := readTLSDataFromKey(caPool, "tls.crt", secret)
	if err == nil {
		certsInPool++
	}

	err = readTLSDataFromKey(caPool, "ca.crt", secret)
	if err == nil {
		certsInPool++
	}

	if certsInPool == 0 {
		return fmt.Errorf("no certs were found under correct keys, please  use ca.crt or tls.crt")
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

	cert, err := TLSCertFromSecret(ctx, t.kubeClient, t.Namespace, t.ClientSecretName)
	if err != nil {
		return err
	}
	if err = validateNewClientCert(cert); err == nil {
		newMaterial.Cert = cert
	} else {
		return fmt.Errorf("failed to validate new client cert: %v", err)
	}

	caPool := x509.NewCertPool()
	caSecret, err := t.kubeClient.CoreV1().Secrets(t.Namespace).Get(ctx, t.CASecretName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	certsInPool := 0
	if caSecret != nil {
		err := readTLSDataFromKey(caPool, "tls.crt", caSecret)
		if err == nil {
			certsInPool++
		}

		err = readTLSDataFromKey(caPool, "ca.crt", caSecret)
		if err == nil {
			certsInPool++
		}
	}

	if certsInPool > 0 {
		newMaterial.CAPool = caPool
	} else {
		return fmt.Errorf("no certs loaded on reload")
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
