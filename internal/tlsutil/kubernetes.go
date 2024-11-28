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

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// TLSCertFromSecret produces
func TLSCertFromSecret(ctx context.Context, kube kubernetes.Interface, namespace, name string) (tls.Certificate, error) {
	secret, err := kube.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("could not read TLS secret: %w", err)
	}
	if secret.Type != "kubernetes.io/tls" {
		return tls.Certificate{}, fmt.Errorf("%s/%s: not a TLS secret", namespace, name)
	}
	if len(secret.Data) == 0 {
		return tls.Certificate{}, fmt.Errorf("%s/%s: empty secret", namespace, name)
	}
	crt := secret.Data["tls.crt"]
	key := secret.Data["tls.key"]
	if crt == nil || key == nil {
		return tls.Certificate{}, fmt.Errorf("need TLS cert and key from kube-proxy-tls, but either was not found")
	}
	cert, err := tls.X509KeyPair(crt, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("could not read proxy certificate: %w", err)
	}

	return cert, nil
}

func TLSCertToSecret(ctx context.Context, kube kubernetes.Interface, namespace, name string, tlsCert tls.Certificate) error {
	rsaKey, ok := tlsCert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return fmt.Errorf("invalid private key format")
	}
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Type: "kubernetes.io/tls",
		Data: map[string][]byte{
			"tls.crt": tlsCert.Certificate[0],
			"tls.key": rsaKey.N.Bytes(),
		},
	}
	_, err := kube.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

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
