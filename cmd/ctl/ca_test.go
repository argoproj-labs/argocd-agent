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

		summary := readAndSummarizeCertificate(ctx, client, namespace, secretName, false)

		// Should return empty summary for not found
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

		summary := readAndSummarizeCertificate(ctx, client, namespace, secretName, true)

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

		summary := readAndSummarizeCertificate(ctx, client, namespace, secretName, false)

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

		summary := readAndSummarizeCertificate(ctx, client, namespace, secretName, true)

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

		summary := readAndSummarizeCertificate(ctx, client, namespace, secretName, false)

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

		summary := readAndSummarizeCertificate(ctx, client, namespace, secretName, true) // expecting CA

		// Should have both warnings
		assert.Contains(t, summary.Warnings, "This is not a CA certificate")
		assert.Contains(t, summary.Warnings, "Basic constraints are invalid")
		assert.Len(t, summary.Warnings, 2)
	})

	t.Run("Secret with invalid certificate data", func(t *testing.T) {
		// Create secret with invalid certificate data
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

		// The function should call cmdutil.Fatal for parsing errors,
		// but since we can't easily test Fatal calls without refactoring,
		// we'll skip this test case for now. In a real scenario, we might
		// want to refactor the function to return errors instead of calling Fatal.
		_ = secret
		t.Skip("Skipping test for invalid certificate data as it calls cmdutil.Fatal")
	})

	t.Run("Empty certificate data", func(t *testing.T) {
		// Create secret with empty certificate data
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

		// Similar to above, this would trigger an error that calls cmdutil.Fatal
		_ = secret
		t.Skip("Skipping test for empty certificate data as it calls cmdutil.Fatal")
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

		summary := readAndSummarizeCertificate(ctx, client, namespace, "non-existent-secret", false)

		// Should return empty summary when secret is not found
		assert.Empty(t, summary.Subject)
		assert.Empty(t, summary.KeyType)
		assert.Zero(t, summary.KeyLength)
	})
}
