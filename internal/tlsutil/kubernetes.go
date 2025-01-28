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

package tlsutil

/*
This file holds all TLS utility functions that interact with Kubernetes
*/

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const tlsCertFieldName = "tls.crt"
const tlsKeyFieldName = "tls.key"
const tlsTypeLabelValue = "kubernetes.io/tls"

// TLSCertFromSecret reads a Kubernetes TLS secrets, and parses its data into
// a tls.Certificate.
func TLSCertFromSecret(ctx context.Context, kube kubernetes.Interface, namespace, name string) (tls.Certificate, error) {
	secret, err := kube.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("could not read TLS secret %s/%s: %w", namespace, name, err)
	}
	if secret.Type != tlsTypeLabelValue {
		return tls.Certificate{}, fmt.Errorf("%s/%s: not a TLS secret", namespace, name)
	}
	if len(secret.Data) == 0 {
		return tls.Certificate{}, fmt.Errorf("%s/%s: empty secret", namespace, name)
	}
	crt := secret.Data[tlsCertFieldName]
	key := secret.Data[tlsKeyFieldName]
	if crt == nil || key == nil {
		return tls.Certificate{}, fmt.Errorf("either cert or key is missing in the secret")
	}
	cert, err := tls.X509KeyPair(crt, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("invalid cert or key data: %w", err)
	}

	return cert, nil
}

// TLSCertToSecret writes a TLS certificate to a Kubernetes TLS secret. The data
// in the TLS certificate will be converted to PEM prior to being written out.
func TLSCertToSecret(ctx context.Context, kube kubernetes.Interface, namespace, name string, tlsCert tls.Certificate) error {
	rsaKey, ok := tlsCert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return fmt.Errorf("invalid private key format")
	}
	certPem, err := CertDataToPEM(tlsCert.Certificate[0])
	if err != nil {
		return err
	}
	keyPem, err := KeyDataToPEM(rsaKey)
	if err != nil {
		return err
	}
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Type: tlsTypeLabelValue,
		Data: map[string][]byte{
			tlsCertFieldName: []byte(certPem),
			tlsKeyFieldName:  []byte(keyPem),
		},
	}
	_, err = kube.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

// X509CertPoolFromSecret reads certificate data from a Kubernetes secret and
// appends the data to a X509 cert pool to be returned. If field is given,
// only data from this field will be parsed into the cert pool. Otherwise, if
// field is the empty string, all fields in the secret are expected to have
// valid certificate data and will be parsed.
func X509CertPoolFromSecret(ctx context.Context, kube kubernetes.Interface, namespace, name, field string) (*x509.CertPool, error) {
	secret, err := kube.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not read secret: %w", err)
	}
	if len(secret.Data) == 0 {
		return nil, fmt.Errorf("%s/%s: empty secret", namespace, name)
	}

	pool := x509.NewCertPool()
	certsInPool := 0
	for f, crtBytes := range secret.Data {
		if field == "" || f == field {
			ok := pool.AppendCertsFromPEM(crtBytes)
			if !ok {
				return nil, fmt.Errorf("%s/%s: field %s does not hold valid certificate data", namespace, name, f)
			}
			certsInPool += 1
		}
	}

	return pool, nil
}

// TransportFromConfig creates an HTTP transport that is configured to use the
// TLS credentials and configuration from the given REST config.
func TransportFromConfig(config *rest.Config) (*http.Transport, error) {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(config.CAData)
	ccert, err := GetKubeConfigClientCert(config)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates: append([]tls.Certificate{}, *ccert),
			RootCAs:      pool,
		},
	}
	return tr, nil
}

// GetKubeConfigClientCert extracts the client certificate and private key from
// a given Kubernetes configuration. It supports both, loading the data from a
// file, as well as using base64-encoded embedded data.
func GetKubeConfigClientCert(conf *rest.Config) (*tls.Certificate, error) {
	var cert tls.Certificate
	var err error
	if conf.CertFile != "" && conf.KeyFile != "" {
		cert, err = TlsCertFromFile(conf.CertFile, conf.KeyFile, true)
	} else if len(conf.CertData) > 0 && len(conf.KeyData) > 0 {
		cert, err = tls.X509KeyPair(conf.CertData, conf.KeyData)
	} else {
		return nil, fmt.Errorf("invalid TLS config in configuration")
	}

	return &cert, err
}
