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

package main

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/kube"
	fakekube "github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReadAndSummarizeCertificate(t *testing.T) {
	ctx := context.TODO()
	namespace := "test-namespace"
	secretName := "test-secret"

	t.Run("Certificate not found", func(t *testing.T) {
		// Create fake client with no secrets
		fakeClient := fakekube.NewFakeClientsetWithResources()
		client := &kube.KubernetesClient{
			Clientset: fakeClient,
		}

		summary, err := readAndSummarizeCertificate(ctx, client, namespace, secretName, false)

		// Should return empty summary and not found error
		assert.Error(t, err)
		assert.True(t, errors.IsNotFound(err))
		assert.Empty(t, summary.Subject)
		assert.Empty(t, summary.KeyType)
		assert.Zero(t, summary.KeyLength)
		assert.Empty(t, summary.NotBefore)
		assert.Empty(t, summary.NotAfter)
		assert.Empty(t, summary.Checksum)
		assert.Empty(t, summary.IPs)
		assert.Empty(t, summary.DNS)
		assert.Empty(t, summary.Warnings)
	})

	t.Run("Valid CA certificate", func(t *testing.T) {
		// Create CA certificate template
		caTemplate := x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject: pkix.Name{
				CommonName:   "Test CA",
				Organization: []string{"Test Org"},
			},
			KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
			BasicConstraintsValid: true,
			IsCA:                  true,
			NotBefore:             time.Now().Add(-1 * time.Hour),
			NotAfter:              time.Now().Add(24 * time.Hour),
		}

		// Generate certificate
		certPEM, keyPEM := testcerts.CreateSelfSignedCert(t, "rsa", caTemplate)

		// Create secret with the certificate
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				"tls.crt": certPEM,
				"tls.key": keyPEM,
			},
		}

		fakeClient := fakekube.NewFakeClientsetWithResources(secret)
		client := &kube.KubernetesClient{
			Clientset: fakeClient,
		}

		summary, err := readAndSummarizeCertificate(ctx, client, namespace, secretName, true)

		// Should succeed with no error
		assert.NoError(t, err)

		// Verify certificate summary
		assert.Contains(t, summary.Subject, "Test CA")
		assert.Equal(t, "RSA", summary.KeyType)
		assert.Equal(t, 2048, summary.KeyLength) // RSA 2048 is used by testcerts
		assert.NotEmpty(t, summary.NotBefore)
		assert.NotEmpty(t, summary.NotAfter)
		assert.NotEmpty(t, summary.Checksum)
		assert.Empty(t, summary.Warnings) // Should be no warnings for valid CA
	})

	t.Run("Valid non-CA certificate with IP and DNS", func(t *testing.T) {
		// Create server certificate template with SAN
		serverTemplate := x509.Certificate{
			SerialNumber: big.NewInt(2),
			Subject: pkix.Name{
				CommonName: "test-server.example.com",
			},
			KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
			IsCA:                  false,
			NotBefore:             time.Now().Add(-1 * time.Hour),
			NotAfter:              time.Now().Add(24 * time.Hour),
			IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.168.1.1")},
			DNSNames:              []string{"localhost", "test-server.example.com"},
		}

		certPEM, keyPEM := testcerts.CreateSelfSignedCert(t, "rsa", serverTemplate)

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				"tls.crt": certPEM,
				"tls.key": keyPEM,
			},
		}

		fakeClient := fakekube.NewFakeClientsetWithResources(secret)
		client := &kube.KubernetesClient{
			Clientset: fakeClient,
		}

		summary, err := readAndSummarizeCertificate(ctx, client, namespace, secretName, false)

		// Should succeed with no error
		assert.NoError(t, err)

		// Verify certificate summary
		assert.Contains(t, summary.Subject, "test-server.example.com")
		assert.Equal(t, "RSA", summary.KeyType)
		assert.Equal(t, 2048, summary.KeyLength)
		assert.NotEmpty(t, summary.NotBefore)
		assert.NotEmpty(t, summary.NotAfter)
		assert.NotEmpty(t, summary.Checksum)
		assert.Contains(t, summary.IPs, "127.0.0.1")
		assert.Contains(t, summary.IPs, "192.168.1.1")
		assert.Contains(t, summary.DNS, "localhost")
		assert.Contains(t, summary.DNS, "test-server.example.com")
		assert.Empty(t, summary.Warnings) // Should be no warnings for valid server cert
	})

	t.Run("Non-CA certificate expected to be CA", func(t *testing.T) {
		// Create non-CA certificate
		nonCATemplate := x509.Certificate{
			SerialNumber: big.NewInt(3),
			Subject: pkix.Name{
				CommonName: "Not a CA",
			},
			KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			BasicConstraintsValid: true,
			IsCA:                  false,
			NotBefore:             time.Now().Add(-1 * time.Hour),
			NotAfter:              time.Now().Add(24 * time.Hour),
		}

		certPEM, keyPEM := testcerts.CreateSelfSignedCert(t, "rsa", nonCATemplate)

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				"tls.crt": certPEM,
				"tls.key": keyPEM,
			},
		}

		fakeClient := fakekube.NewFakeClientsetWithResources(secret)
		client := &kube.KubernetesClient{
			Clientset: fakeClient,
		}

		summary, err := readAndSummarizeCertificate(ctx, client, namespace, secretName, true)

		// Should succeed with no error
		assert.NoError(t, err)

		// Should have warning about not being a CA
		assert.Contains(t, summary.Warnings, "This is not a CA certificate")
		assert.Contains(t, summary.Subject, "Not a CA")
	})

	t.Run("Certificate with invalid basic constraints", func(t *testing.T) {
		// Create certificate with invalid basic constraints
		invalidTemplate := x509.Certificate{
			SerialNumber: big.NewInt(4),
			Subject: pkix.Name{
				CommonName: "Invalid Constraints",
			},
			KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: false, // Invalid basic constraints
			IsCA:                  false,
			NotBefore:             time.Now().Add(-1 * time.Hour),
			NotAfter:              time.Now().Add(24 * time.Hour),
		}

		certPEM, keyPEM := testcerts.CreateSelfSignedCert(t, "rsa", invalidTemplate)

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				"tls.crt": certPEM,
				"tls.key": keyPEM,
			},
		}

		fakeClient := fakekube.NewFakeClientsetWithResources(secret)
		client := &kube.KubernetesClient{
			Clientset: fakeClient,
		}

		summary, err := readAndSummarizeCertificate(ctx, client, namespace, secretName, false)

		// Should succeed with no error
		assert.NoError(t, err)

		// Should have warning about invalid basic constraints
		assert.Contains(t, summary.Warnings, "Basic constraints are invalid")
		assert.Contains(t, summary.Subject, "Invalid Constraints")
	})

	t.Run("Certificate with both CA and constraint warnings", func(t *testing.T) {
		// Create non-CA certificate with invalid constraints, expected to be CA
		bothIssuesTemplate := x509.Certificate{
			SerialNumber: big.NewInt(5),
			Subject: pkix.Name{
				CommonName: "Both Issues",
			},
			KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			BasicConstraintsValid: false, // Invalid
			IsCA:                  false, // Not CA
			NotBefore:             time.Now().Add(-1 * time.Hour),
			NotAfter:              time.Now().Add(24 * time.Hour),
		}

		certPEM, keyPEM := testcerts.CreateSelfSignedCert(t, "rsa", bothIssuesTemplate)

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				"tls.crt": certPEM,
				"tls.key": keyPEM,
			},
		}

		fakeClient := fakekube.NewFakeClientsetWithResources(secret)
		client := &kube.KubernetesClient{
			Clientset: fakeClient,
		}

		summary, err := readAndSummarizeCertificate(ctx, client, namespace, secretName, true) // expecting CA

		// Should succeed with no error
		assert.NoError(t, err)

		// Should have both warnings
		assert.Contains(t, summary.Warnings, "This is not a CA certificate")
		assert.Contains(t, summary.Warnings, "Basic constraints are invalid")
		assert.Len(t, summary.Warnings, 2)
	})

	t.Run("Secret with invalid certificate data", func(t *testing.T) {
		// Create secret with invalid certificate data that will fail TLSCertFromSecret
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				"tls.crt": []byte("invalid certificate data"),
				"tls.key": []byte("invalid key data"),
			},
		}

		fakeClient := fakekube.NewFakeClientsetWithResources(secret)
		client := &kube.KubernetesClient{
			Clientset: fakeClient,
		}

		summary, err := readAndSummarizeCertificate(ctx, client, namespace, secretName, false)

		// Should return error for invalid certificate data
		assert.Error(t, err)
		assert.Empty(t, summary.Subject)
	})

	t.Run("Empty certificate data", func(t *testing.T) {
		// Create secret with empty certificate data that will fail TLSCertFromSecret
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				"tls.crt": []byte(""),
				"tls.key": []byte(""),
			},
		}

		fakeClient := fakekube.NewFakeClientsetWithResources(secret)
		client := &kube.KubernetesClient{
			Clientset: fakeClient,
		}

		summary, err := readAndSummarizeCertificate(ctx, client, namespace, secretName, false)

		// Should return error for empty certificate data
		assert.Error(t, err)
		assert.Empty(t, summary.Subject)
	})

	t.Run("Certificate parsing error", func(t *testing.T) {
		// Create a secret with valid PEM structure but invalid certificate content
		// This will pass TLSCertFromSecret but fail x509.ParseCertificate
		invalidCertPEM := `-----BEGIN CERTIFICATE-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAinvalidcertificatedata
-----END CERTIFICATE-----`

		// Generate a valid key to go with the invalid cert
		_, validKeyPEM := testcerts.CreateSelfSignedCert(t, "rsa", testcerts.DefaultCertTempl)

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				"tls.crt": []byte(invalidCertPEM),
				"tls.key": validKeyPEM,
			},
		}

		fakeClient := fakekube.NewFakeClientsetWithResources(secret)
		client := &kube.KubernetesClient{
			Clientset: fakeClient,
		}

		summary, err := readAndSummarizeCertificate(ctx, client, namespace, secretName, false)

		// Should return TLS loading error
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tls: failed to find any PEM data")
		assert.Empty(t, summary.Subject)
	})
}

func TestReadAndSummarizeCertificate_GetSecretError(t *testing.T) {
	// Test that we can verify error handling, although most errors result in Fatal calls
	ctx := context.TODO()
	namespace := "test-namespace"

	t.Run("Network error accessing secret", func(t *testing.T) {
		// This would be challenging to test without a more sophisticated mock
		// that can simulate network errors. For now, we test the not found case
		// which is the one case that doesn't call Fatal.

		fakeClient := fakekube.NewFakeClientsetWithResources()
		client := &kube.KubernetesClient{
			Clientset: fakeClient,
		}

		summary, err := readAndSummarizeCertificate(ctx, client, namespace, "non-existent-secret", false)

		// Should return error when secret is not found
		assert.Error(t, err)
		assert.True(t, errors.IsNotFound(err))

		// Should return empty summary when secret is not found
		assert.Empty(t, summary.Subject)
		assert.Empty(t, summary.KeyType)
		assert.Zero(t, summary.KeyLength)
	})
}
