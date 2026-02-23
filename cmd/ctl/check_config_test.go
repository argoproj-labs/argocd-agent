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
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj/argo-cd/v3/common"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func mustCreateTLSSecret(t *testing.T, client kubernetes.Interface, ns, name, certPEM, keyPEM string) {
	t.Helper()
	_, err := client.CoreV1().Secrets(ns).Create(context.TODO(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": []byte(certPEM),
			"tls.key": []byte(keyPEM),
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err, "create secret %s/%s", ns, name)
}

// Helper function to create an expired certificate
func createExpiredCertificate(t *testing.T, name string, signerCert *x509.Certificate, signerKey crypto.PrivateKey, ips []string, dns []string) (string, string) {
	t.Helper()
	ipAddresses := []net.IP{}
	for _, ip := range ips {
		addr := net.ParseIP(ip)
		if addr != nil {
			ipAddresses = append(ipAddresses, addr)
		}
	}

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   name,
			Organization: []string{"DO NOT USE IN PRODUCTION"},
		},
		NotBefore:             time.Now().Add(-2 * time.Hour),
		NotAfter:              time.Now().Add(-1 * time.Hour), // Expired 1 hour ago
		IsCA:                  false,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ipAddresses,
	}

	certPEM, keyPEM, err := tlsutil.GenerateCertificate(cert, signerCert, signerKey)
	require.NoError(t, err, "create expired certificate")
	return certPEM, keyPEM
}

// Helper function to create a certificate signed by a different CA
func createCertSignedByDifferentCA(t *testing.T, name string, ips []string, dns []string) (string, string) {
	t.Helper()
	// Create a different CA
	differentCAPEM, differentCAKeyPEM, err := tlsutil.GenerateCaCertificate("different-ca")
	require.NoError(t, err, "generate different CA")

	// Create a fake client to parse the CA
	cl := fake.NewSimpleClientset()
	_, err = cl.CoreV1().Secrets("default").Create(context.TODO(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "temp-ca", Namespace: "default"},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": []byte(differentCAPEM),
			"tls.key": []byte(differentCAKeyPEM),
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err, "create temp CA secret")

	differentCA, err := tlsutil.TLSCertFromSecret(context.TODO(), cl, "default", "temp-ca")
	require.NoError(t, err, "read different CA")
	differentCASigner, err := x509.ParseCertificate(differentCA.Certificate[0])
	require.NoError(t, err, "parse different CA")

	// Create certificate signed by different CA
	ipAddresses := []net.IP{}
	for _, ip := range ips {
		addr := net.ParseIP(ip)
		if addr != nil {
			ipAddresses = append(ipAddresses, addr)
		}
	}

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   name,
			Organization: []string{"DO NOT USE IN PRODUCTION"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 6, 0),
		IsCA:                  false,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ipAddresses,
	}

	certPEM, keyPEM, err := tlsutil.GenerateCertificate(cert, differentCASigner, differentCA.PrivateKey)
	require.NoError(t, err, "create cert signed by different CA")

	return certPEM, keyPEM
}

// Helper to create a certificate with empty CN
func createCertWithEmptyCN(t *testing.T, signerCert *x509.Certificate, signerKey crypto.PrivateKey) (string, string) {
	t.Helper()
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "", // Empty CN
			Organization: []string{"DO NOT USE IN PRODUCTION"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 6, 0),
		IsCA:                  false,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		BasicConstraintsValid: true,
	}

	certPEM, keyPEM, err := tlsutil.GenerateCertificate(cert, signerCert, signerKey)
	require.NoError(t, err, "create cert with empty CN")
	return certPEM, keyPEM
}

// Helper that returns an unstructured that represents a fake application
func createFakeApp() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]interface{}{
				"name":      "fake-app",
				"namespace": "argocd",
			},
			"spec": map[string]interface{}{
				"destination": map[string]interface{}{
					"server":    "https://kubernetes.default.svc",
					"namespace": "default",
				},
			},
		},
	}
}

// Helper to create a fake cluster secret with some options
func createFakeSecret(name, server string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd-agent.argoproj-labs.io/agent-name": name,
				"argocd.argoproj.io/secret-type":           "cluster",
			},
			Annotations: map[string]string{
				"managed-by": "argocd-agent",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"config": []byte(`{"tlsClientConfig":{"insecure":false}}`),
			"name":   []byte(name),
			"server": []byte(server),
		},
	}
}

func TestCheckConfigPrincipal(t *testing.T) {
	t.Run("Valid configuration", func(t *testing.T) {
		principalNS := "argocd"
		cl := fake.NewSimpleClientset()

		// Create a scheme and register ArgoCD CRD
		scheme := runtime.NewScheme()
		// Create dynamic client with the registered types
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
			{Group: "route.openshift.io", Version: "v1", Resource: "routes"}:      "RouteList",
		})

		// Create namespace
		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create namespace")

		// Create KubernetesClient wrapper
		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		// CA
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")

		// Principal TLS
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		// Parse CA for issuing
		caCert, err := tlsutil.TLSCertFromSecret(context.TODO(), cl, principalNS, config.SecretNamePrincipalCA)
		require.NoError(t, err, "read CA")
		signer, err := x509.ParseCertificate(caCert.Certificate[0])
		require.NoError(t, err, "parse CA")
		pCertPEM, pKeyPEM, err := tlsutil.GenerateServerCertificate("principal", signer, caCert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"})
		require.NoError(t, err, "gen principal cert")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalTLS, pCertPEM, pKeyPEM)

		// Resource proxy TLS
		rpCertPEM, rpKeyPEM, err := tlsutil.GenerateServerCertificate("resource-proxy", signer, caCert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"})
		require.NoError(t, err, "gen rp cert")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNameProxyTLS, rpCertPEM, rpKeyPEM)

		// JWT secret (PKCS8 RSA)
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err, "gen jwt key")
		pkcs8, err := x509.MarshalPKCS8PrivateKey(rsaKey)
		require.NoError(t, err, "marshal pkcs8")
		block := &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}
		jwtPem := pem.EncodeToMemory(block)
		_, err = cl.CoreV1().Secrets(principalNS).Create(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: config.SecretNameJWT, Namespace: principalNS},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"jwt.key": jwtPem,
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create jwt secret")

		// Create argocd-cm ConfigMap to indicate cluster-scoped mode (no application.namespaces key)
		_, err = cl.CoreV1().ConfigMaps(principalNS).Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-cm", Namespace: principalNS},
			Data:       map[string]string{},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create argocd-cm configmap")

		// Create an OpenShift Route that matches the principal TLS cert IPS/DNS
		route := &unstructured.Unstructured{}
		route.SetAPIVersion("route.openshift.io/v1")
		route.SetKind("Route")
		route.SetName("argocd-server")
		route.SetNamespace(principalNS)
		route.Object["spec"] = map[string]interface{}{
			"host": "localhost", // matches the cert IPS/DNS
		}
		_, err = dynCl.Resource(schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}).Namespace(principalNS).Create(context.TODO(), route, metav1.CreateOptions{})
		require.NoError(t, err, "create route")

		res := RunPrincipalChecks(context.TODO(), kubeClient, principalNS)
		for _, r := range res {
			require.NoError(t, r.err, "check failed: %s", r.name)
		}
	})

	t.Run("Missing CA secret", func(t *testing.T) {
		principalNS := "argocd"
		cl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		})

		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create namespace")

		// Create argocd-cm ConfigMap
		_, err = cl.CoreV1().ConfigMaps(principalNS).Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-cm", Namespace: principalNS},
			Data:       map[string]string{},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create argocd-cm configmap")

		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		// Don't create CA secret, should return error
		res := RunPrincipalChecks(context.TODO(), kubeClient, principalNS)
		hasCAError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "CA certificate") {
				hasCAError = true
				require.NotEmpty(t, r.err.Error(), "error message should not be empty")
			}
		}
		require.True(t, hasCAError, "expected error when CA secret is missing")
	})

	t.Run("Missing Principal TLS secret", func(t *testing.T) {
		principalNS := "argocd"
		cl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		})

		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create namespace")

		// Create CA only
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		// Create argocd-cm ConfigMap
		_, err = cl.CoreV1().ConfigMaps(principalNS).Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-cm", Namespace: principalNS},
			Data:       map[string]string{},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create argocd-cm configmap")

		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		// Don't create Principal TLS secret - should error
		res := RunPrincipalChecks(context.TODO(), kubeClient, principalNS)
		hasTLSError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "principal gRPC TLS") {
				hasTLSError = true
				require.NotEmpty(t, r.err.Error(), "error message should not be empty")
			}
		}
		require.True(t, hasTLSError, "expected error when Principal TLS secret is missing")
	})

	t.Run("Missing Proxy TLS secret", func(t *testing.T) {
		principalNS := "argocd"
		cl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		})

		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create namespace")

		// Create CA and Principal TLS
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		caCert, err := tlsutil.TLSCertFromSecret(context.TODO(), cl, principalNS, config.SecretNamePrincipalCA)
		require.NoError(t, err, "read CA")
		signer, err := x509.ParseCertificate(caCert.Certificate[0])
		require.NoError(t, err, "parse CA")
		pCertPEM, pKeyPEM, err := tlsutil.GenerateServerCertificate("principal", signer, caCert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"})
		require.NoError(t, err, "gen principal cert")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalTLS, pCertPEM, pKeyPEM)

		// Create argocd-cm ConfigMap
		_, err = cl.CoreV1().ConfigMaps(principalNS).Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-cm", Namespace: principalNS},
			Data:       map[string]string{},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create argocd-cm configmap")

		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		// Don't create Proxy TLS secret - should error
		res := RunPrincipalChecks(context.TODO(), kubeClient, principalNS)
		hasProxyTLSError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "resource proxy TLS") {
				hasProxyTLSError = true
				require.NotEmpty(t, r.err.Error(), "error message should not be empty")
			}
		}
		require.True(t, hasProxyTLSError, "expected error when Resource Proxy TLS secret is missing")
	})

	t.Run("Missing JWT secret", func(t *testing.T) {
		principalNS := "argocd"
		cl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		})

		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create namespace")

		// Create CA and TLS secrets
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		caCert, err := tlsutil.TLSCertFromSecret(context.TODO(), cl, principalNS, config.SecretNamePrincipalCA)
		require.NoError(t, err, "read CA")
		signer, err := x509.ParseCertificate(caCert.Certificate[0])
		require.NoError(t, err, "parse CA")
		pCertPEM, pKeyPEM, err := tlsutil.GenerateServerCertificate("principal", signer, caCert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"})
		require.NoError(t, err, "gen principal cert")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalTLS, pCertPEM, pKeyPEM)

		rpCertPEM, rpKeyPEM, err := tlsutil.GenerateServerCertificate("resource-proxy", signer, caCert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"})
		require.NoError(t, err, "gen rp cert")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNameProxyTLS, rpCertPEM, rpKeyPEM)

		// Create argocd-cm ConfigMap
		_, err = cl.CoreV1().ConfigMaps(principalNS).Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-cm", Namespace: principalNS},
			Data:       map[string]string{},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create argocd-cm configmap")

		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		// Don't create JWT secret, should return error
		res := RunPrincipalChecks(context.TODO(), kubeClient, principalNS)
		hasJWTError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "JWT signing key") {
				hasJWTError = true
				require.NotEmpty(t, r.err.Error(), "error message should not be empty")
			}
		}
		require.True(t, hasJWTError, "expected error when JWT secret is missing")
	})

	t.Run("Expired Principal TLS certificate", func(t *testing.T) {
		principalNS := "argocd"
		cl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		})

		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create namespace")

		// Create CA
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		caCert, err := tlsutil.TLSCertFromSecret(context.TODO(), cl, principalNS, config.SecretNamePrincipalCA)
		require.NoError(t, err, "read CA")
		signer, err := x509.ParseCertificate(caCert.Certificate[0])
		require.NoError(t, err, "parse CA")

		// Create expired Principal TLS certificate
		expiredCertPEM, expiredKeyPEM := createExpiredCertificate(t, "principal", signer, caCert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"})
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalTLS, expiredCertPEM, expiredKeyPEM)

		// Create argocd-cm ConfigMap
		_, err = cl.CoreV1().ConfigMaps(principalNS).Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-cm", Namespace: principalNS},
			Data:       map[string]string{},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create argocd-cm configmap")

		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		res := RunPrincipalChecks(context.TODO(), kubeClient, principalNS)
		hasExpiredError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "principal gRPC TLS") {
				hasExpiredError = true
				require.Contains(t, r.err.Error(), "expired", "error should mention certificate expired")
			}
		}
		require.True(t, hasExpiredError, "expected error when Principal TLS certificate is expired")
	})

	t.Run("Expired Proxy TLS certificate", func(t *testing.T) {
		principalNS := "argocd"
		cl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		})

		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create namespace")

		// Create CA
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		caCert, err := tlsutil.TLSCertFromSecret(context.TODO(), cl, principalNS, config.SecretNamePrincipalCA)
		require.NoError(t, err, "read CA")
		signer, err := x509.ParseCertificate(caCert.Certificate[0])
		require.NoError(t, err, "parse CA")

		// Create valid Principal TLS
		pCertPEM, pKeyPEM, err := tlsutil.GenerateServerCertificate("principal", signer, caCert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"})
		require.NoError(t, err, "gen principal cert")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalTLS, pCertPEM, pKeyPEM)

		// Create expired Resource Proxy TLS certificate
		expiredCertPEM, expiredKeyPEM := createExpiredCertificate(t, "resource-proxy", signer, caCert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"})
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNameProxyTLS, expiredCertPEM, expiredKeyPEM)

		// Create argocd-cm ConfigMap
		_, err = cl.CoreV1().ConfigMaps(principalNS).Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-cm", Namespace: principalNS},
			Data:       map[string]string{},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create argocd-cm configmap")

		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		res := RunPrincipalChecks(context.TODO(), kubeClient, principalNS)
		hasExpiredError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "resource proxy TLS") {
				hasExpiredError = true
				require.Contains(t, r.err.Error(), "expired", "error should mention certificate expired")
			}
		}
		require.True(t, hasExpiredError, "expected error when Resource Proxy TLS certificate is expired")
	})

	t.Run("Namespaced mode configuration", func(t *testing.T) {
		principalNS := "argocd"
		cl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		})

		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create namespace")

		// Create argocd-cm ConfigMap with application.namespaces set (namespaced mode)
		_, err = cl.CoreV1().ConfigMaps(principalNS).Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-cm", Namespace: principalNS},
			Data: map[string]string{
				"application.namespaces": "default,argocd", // Namespaced mode
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create argocd-cm configmap")

		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		res := RunPrincipalChecks(context.TODO(), kubeClient, principalNS)
		hasNamespacedError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "cluster-scoped mode") {
				hasNamespacedError = true
				require.Contains(t, r.err.Error(), "namespaced mode", "error should mention namespaced mode")
			}
		}
		require.True(t, hasNamespacedError, "expected error when Argo CD is in namespaced mode")
	})

	t.Run("Invalid JWT key type", func(t *testing.T) {
		principalNS := "argocd"
		cl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		})

		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create namespace")

		// Create CA and TLS secrets
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		caCert, err := tlsutil.TLSCertFromSecret(context.TODO(), cl, principalNS, config.SecretNamePrincipalCA)
		require.NoError(t, err, "read CA")
		signer, err := x509.ParseCertificate(caCert.Certificate[0])
		require.NoError(t, err, "parse CA")
		pCertPEM, pKeyPEM, err := tlsutil.GenerateServerCertificate("principal", signer, caCert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"})
		require.NoError(t, err, "gen principal cert")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalTLS, pCertPEM, pKeyPEM)

		rpCertPEM, rpKeyPEM, err := tlsutil.GenerateServerCertificate("resource-proxy", signer, caCert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"})
		require.NoError(t, err, "gen rp cert")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNameProxyTLS, rpCertPEM, rpKeyPEM)

		// Create JWT secret with ECDSA key (non-RSA), should return error
		ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err, "gen ecdsa key")
		ecdsaPKCS8, err := x509.MarshalPKCS8PrivateKey(ecdsaKey)
		require.NoError(t, err, "marshal ecdsa pkcs8")
		block := &pem.Block{Type: "PRIVATE KEY", Bytes: ecdsaPKCS8}
		jwtPem := pem.EncodeToMemory(block)
		_, err = cl.CoreV1().Secrets(principalNS).Create(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: config.SecretNameJWT, Namespace: principalNS},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"jwt.key": jwtPem,
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create jwt secret with ECDSA")

		// Create argocd-cm ConfigMap
		_, err = cl.CoreV1().ConfigMaps(principalNS).Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-cm", Namespace: principalNS},
			Data:       map[string]string{},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create argocd-cm configmap")

		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		res := RunPrincipalChecks(context.TODO(), kubeClient, principalNS)
		hasJWTKeyTypeError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "JWT signing key") {
				hasJWTKeyTypeError = true
				require.Contains(t, r.err.Error(), "RSA", "error should mention RSA requirement")
			}
		}
		require.True(t, hasJWTKeyTypeError, "expected error when JWT key is not RSA")
	})

	t.Run("Route host mismatch", func(t *testing.T) {
		principalNS := "argocd"
		cl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
			{Group: "route.openshift.io", Version: "v1", Resource: "routes"}:      "RouteList",
		})

		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create namespace")

		// Create CA and TLS secrets
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		caCert, err := tlsutil.TLSCertFromSecret(context.TODO(), cl, principalNS, config.SecretNamePrincipalCA)
		require.NoError(t, err, "read CA")
		signer, err := x509.ParseCertificate(caCert.Certificate[0])
		require.NoError(t, err, "parse CA")
		// Certificate with DNS: localhost, but route will have different host
		pCertPEM, pKeyPEM, err := tlsutil.GenerateServerCertificate("principal", signer, caCert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"})
		require.NoError(t, err, "gen principal cert")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNamePrincipalTLS, pCertPEM, pKeyPEM)

		rpCertPEM, rpKeyPEM, err := tlsutil.GenerateServerCertificate("resource-proxy", signer, caCert.PrivateKey, []string{"127.0.0.1"}, []string{"localhost"})
		require.NoError(t, err, "gen rp cert")
		mustCreateTLSSecret(t, cl, principalNS, config.SecretNameProxyTLS, rpCertPEM, rpKeyPEM)

		// JWT secret
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err, "gen jwt key")
		pkcs8, err := x509.MarshalPKCS8PrivateKey(rsaKey)
		require.NoError(t, err, "marshal pkcs8")
		block := &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}
		jwtPem := pem.EncodeToMemory(block)
		_, err = cl.CoreV1().Secrets(principalNS).Create(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: config.SecretNameJWT, Namespace: principalNS},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"jwt.key": jwtPem,
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create jwt secret")

		// Create argocd-cm ConfigMap
		_, err = cl.CoreV1().ConfigMaps(principalNS).Create(context.TODO(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "argocd-cm", Namespace: principalNS},
			Data:       map[string]string{},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create argocd-cm configmap")

		// Create OpenShift Route with hostname that doesn't match cert DNS
		route := &unstructured.Unstructured{}
		route.SetAPIVersion("route.openshift.io/v1")
		route.SetKind("Route")
		route.SetName("argocd-server")
		route.SetNamespace(principalNS)
		route.Object["spec"] = map[string]interface{}{
			"host": "mismatched-host.example.com", // Doesn't match cert DNS
		}
		_, err = dynCl.Resource(schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}).Namespace(principalNS).Create(context.TODO(), route, metav1.CreateOptions{})
		require.NoError(t, err, "create route")

		// Mock the discovery API to include the Route API so routeAPIExists returns true
		fakeDiscovery := cl.Discovery().(*fakediscovery.FakeDiscovery)
		routeGroupVersion := schema.GroupVersion{Group: "route.openshift.io", Version: "v1"}
		routeResourceList := &metav1.APIResourceList{
			GroupVersion: routeGroupVersion.String(),
			APIResources: []metav1.APIResource{
				{Group: "route.openshift.io", Version: "v1", Name: "routes", SingularName: "route", Namespaced: true, Kind: "Route", Verbs: []string{"get", "list", "watch", "create", "update", "delete"}},
			},
		}
		fakeDiscovery.Resources = append(fakeDiscovery.Resources, routeResourceList)

		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		res := RunPrincipalChecks(context.TODO(), kubeClient, principalNS)
		hasRouteMismatchError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "Route host") {
				hasRouteMismatchError = true
				require.NotEmpty(t, r.err.Error(), "error message should not be empty")
			}
		}
		require.True(t, hasRouteMismatchError, "expected error when Route host doesn't match certificate DNS")
	})

	t.Run("Argo CD namespace contains an application", func(t *testing.T) {
		principalNS := "argocd"
		cl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		app := createFakeApp()
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		}, app)

		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "error creating namespace")

		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		res := RunPrincipalChecks(context.Background(), kubeClient, principalNS)
		hasApps := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "no Application CRs defined") {
				hasApps = true
				require.NotEmpty(t, r.err.Error(), "error message should not be empty")
			}
		}
		require.True(t, hasApps, "expected error when Application CRs exist in principal namespace")
	})

	t.Run("Cluster secrets server URL includes agentName and https", func(t *testing.T) {
		principalNS := "argocd"

		validSecret := createFakeSecret("fake-agent", "https://fake-url.io?agentName=fake-agent")
		invalidSecret := createFakeSecret("invalid-agent", "https://fake-url.io?invalidField=fake-agent")
		httpSecret := createFakeSecret("http-agent", "http://fake-url.io?agentName=http-agent")
		wrongSecret := createFakeSecret("wrong-agent", "http://fake-url.io?invalidField=wrong-agent")

		cl := fake.NewSimpleClientset(validSecret, invalidSecret, httpSecret, wrongSecret)
		scheme := runtime.NewScheme()
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		})

		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "error creating namespace")

		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		res := RunPrincipalChecks(context.Background(), kubeClient, principalNS)
		caughtOnlyInvalid := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "query parameter on server url") {
				caughtOnlyInvalid = !strings.Contains(r.err.Error(), "fake-agent") &&
					strings.Contains(r.err.Error(), "invalid-agent") &&
					strings.Contains(r.err.Error(), "http-agent") &&
					strings.Count(r.err.Error(), "wrong-agent") == 2
			}
		}
		require.True(t, caughtOnlyInvalid)
	})

	t.Run("Cluster secrets include skip-reconcile annotation", func(t *testing.T) {
		principalNS := "argocd"

		validSecret := createFakeSecret("fake-agent", "")
		validSecret.SetAnnotations(map[string]string{
			common.AnnotationKeyAppSkipReconcile: "true",
			"managed-by":                         "argocd-agent",
		})
		invalidSecret := createFakeSecret("invalid-agent", "")
		invalidSecret.SetAnnotations(map[string]string{
			common.AnnotationKeyAppSkipReconcile: "false",
			"managed-by":                         "argocd-agent",
		})
		missingAnnotationSecret := createFakeSecret("missing-agent", "")

		cl := fake.NewSimpleClientset(validSecret, invalidSecret, missingAnnotationSecret)
		scheme := runtime.NewScheme()
		dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}:       "ArgoCDList",
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		})

		_, err := cl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: principalNS},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "error creating namespace")

		kubeClient := &kube.KubernetesClient{
			Clientset:     cl,
			DynamicClient: dynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		res := RunPrincipalChecks(context.Background(), kubeClient, principalNS)
		caughtOnlyInvalid := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "skip-reconcile annotation") {
				fmt.Println(r.err.Error())
				caughtOnlyInvalid = !strings.Contains(r.err.Error(), "fake-agent") &&
					strings.Contains(r.err.Error(), "invalid-agent") &&
					strings.Contains(r.err.Error(), "missing-agent")
			}
		}
		require.True(t, caughtOnlyInvalid)
	})
}

func TestCheckConfigAgent(t *testing.T) {
	t.Run("Valid configuration", func(t *testing.T) {
		agentNS := "argocd"
		principalNS := "argocd"
		agentCl := fake.NewSimpleClientset()
		principalCl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		agentDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})
		principalDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})

		agentKubeClient := &kube.KubernetesClient{
			Clientset:     agentCl,
			DynamicClient: agentDynCl,
			Context:       context.TODO(),
			Namespace:     agentNS,
		}
		principalKubeClient := &kube.KubernetesClient{
			Clientset:     principalCl,
			DynamicClient: principalDynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		// Create principal CA
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, principalCl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		// Create agent CA secret on agent cluster (opaque with ca.crt)
		_, err = agentCl.CoreV1().Secrets(agentNS).Create(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: config.SecretNameAgentCA, Namespace: agentNS},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"ca.crt": []byte(caCertPEM),
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create agent ca secret")

		// Issue a client cert for agent subject
		caCert, err := tlsutil.TLSCertFromSecret(context.TODO(), principalCl, principalNS, config.SecretNamePrincipalCA)
		require.NoError(t, err, "read CA")
		signer, err := x509.ParseCertificate(caCert.Certificate[0])
		require.NoError(t, err, "parse CA")
		agentName := "test-cluster"
		cCert, cKey, err := tlsutil.GenerateClientCertificate(agentName, signer, caCert.PrivateKey)
		require.NoError(t, err, "gen client cert")
		mustCreateTLSSecret(t, agentCl, agentNS, config.SecretNameAgentClientCert, cCert, cKey)

		// Ensure matching namespace exists on principal
		_, err = principalCl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: agentName}}, metav1.CreateOptions{})
		require.NoError(t, err, "create ns")

		res := RunAgentChecks(context.TODO(), agentKubeClient, agentNS, principalKubeClient, principalNS)
		for _, r := range res {
			require.NoError(t, r.err, "check failed: %s", r.name)
		}
	})

	t.Run("Missing agent CA secret", func(t *testing.T) {
		agentNS := "argocd"
		principalNS := "argocd"
		agentCl := fake.NewSimpleClientset()
		principalCl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		agentDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})
		principalDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})

		agentKubeClient := &kube.KubernetesClient{
			Clientset:     agentCl,
			DynamicClient: agentDynCl,
			Context:       context.TODO(),
			Namespace:     agentNS,
		}
		principalKubeClient := &kube.KubernetesClient{
			Clientset:     principalCl,
			DynamicClient: principalDynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		// Create principal CA (required for principal checks)
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, principalCl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		// Don't create agent CA secret, should return error
		res := RunAgentChecks(context.TODO(), agentKubeClient, agentNS, principalKubeClient, principalNS)
		hasErrors := false
		for _, r := range res {
			if r.err != nil {
				hasErrors = true
				require.NotEmpty(t, r.err.Error(), "error message should not be empty for: %s", r.name)
			}
		}
		require.True(t, hasErrors, "expected checks to fail when agent CA secret is missing")
	})

	t.Run("Missing namespace on principal", func(t *testing.T) {
		agentNS := "argocd"
		principalNS := "argocd"
		agentCl := fake.NewSimpleClientset()
		principalCl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		agentDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})
		principalDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})

		agentKubeClient := &kube.KubernetesClient{
			Clientset:     agentCl,
			DynamicClient: agentDynCl,
			Context:       context.TODO(),
			Namespace:     agentNS,
		}
		principalKubeClient := &kube.KubernetesClient{
			Clientset:     principalCl,
			DynamicClient: principalDynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		// Create principal CA
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, principalCl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		// Create agent CA secret
		_, err = agentCl.CoreV1().Secrets(agentNS).Create(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: config.SecretNameAgentCA, Namespace: agentNS},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"ca.crt": []byte(caCertPEM),
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create agent ca secret")

		// Issue a client cert for agent subject
		caCert, err := tlsutil.TLSCertFromSecret(context.TODO(), principalCl, principalNS, config.SecretNamePrincipalCA)
		require.NoError(t, err, "read CA")
		signer, err := x509.ParseCertificate(caCert.Certificate[0])
		require.NoError(t, err, "parse CA")
		agentName := "non-existent-namespace"
		cCert, cKey, err := tlsutil.GenerateClientCertificate(agentName, signer, caCert.PrivateKey)
		require.NoError(t, err, "gen client cert")
		mustCreateTLSSecret(t, agentCl, agentNS, config.SecretNameAgentClientCert, cCert, cKey)

		// Don't create the namespace on principal, should return error
		res := RunAgentChecks(context.TODO(), agentKubeClient, agentNS, principalKubeClient, principalNS)
		hasNamespaceError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "namespace") {
				hasNamespaceError = true
				require.Contains(t, r.err.Error(), "not found", "error should mention namespace not found")
			}
		}
		require.True(t, hasNamespaceError, "expected error when namespace doesn't exist on principal")
	})

	t.Run("Missing client cert secret", func(t *testing.T) {
		agentNS := "argocd"
		principalNS := "argocd"
		agentCl := fake.NewSimpleClientset()
		principalCl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		agentDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})
		principalDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})

		agentKubeClient := &kube.KubernetesClient{
			Clientset:     agentCl,
			DynamicClient: agentDynCl,
			Context:       context.TODO(),
			Namespace:     agentNS,
		}
		principalKubeClient := &kube.KubernetesClient{
			Clientset:     principalCl,
			DynamicClient: principalDynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		// Create principal CA
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, principalCl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		// Create agent CA secret
		_, err = agentCl.CoreV1().Secrets(agentNS).Create(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: config.SecretNameAgentCA, Namespace: agentNS},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"ca.crt": []byte(caCertPEM),
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create agent ca secret")

		// Don't create agent client cert secret, should return error
		res := RunAgentChecks(context.TODO(), agentKubeClient, agentNS, principalKubeClient, principalNS)
		hasClientCertError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "mTLS certificate") {
				hasClientCertError = true
				require.NotEmpty(t, r.err.Error(), "error message should not be empty")
			}
		}
		require.True(t, hasClientCertError, "expected error when agent client cert secret is missing")
	})

	t.Run("Expired client certificate", func(t *testing.T) {
		agentNS := "argocd"
		principalNS := "argocd"
		agentCl := fake.NewSimpleClientset()
		principalCl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		agentDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})
		principalDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})

		agentKubeClient := &kube.KubernetesClient{
			Clientset:     agentCl,
			DynamicClient: agentDynCl,
			Context:       context.TODO(),
			Namespace:     agentNS,
		}
		principalKubeClient := &kube.KubernetesClient{
			Clientset:     principalCl,
			DynamicClient: principalDynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		// Create principal CA
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, principalCl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		// Create agent CA secret
		_, err = agentCl.CoreV1().Secrets(agentNS).Create(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: config.SecretNameAgentCA, Namespace: agentNS},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"ca.crt": []byte(caCertPEM),
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create agent ca secret")

		// Create expired client cert
		caCert, err := tlsutil.TLSCertFromSecret(context.TODO(), principalCl, principalNS, config.SecretNamePrincipalCA)
		require.NoError(t, err, "read CA")
		signer, err := x509.ParseCertificate(caCert.Certificate[0])
		require.NoError(t, err, "parse CA")
		agentName := "test-cluster"
		expiredCertPEM, expiredKeyPEM := createExpiredCertificate(t, agentName, signer, caCert.PrivateKey, []string{}, []string{})
		mustCreateTLSSecret(t, agentCl, agentNS, config.SecretNameAgentClientCert, expiredCertPEM, expiredKeyPEM)

		res := RunAgentChecks(context.TODO(), agentKubeClient, agentNS, principalKubeClient, principalNS)
		hasExpiredError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "mTLS certificate") {
				hasExpiredError = true
				require.Contains(t, r.err.Error(), "expired", "error should mention certificate expired")
			}
		}
		require.True(t, hasExpiredError, "expected error when agent client cert is expired")
	})

	t.Run("Client cert not signed by principal CA", func(t *testing.T) {
		agentNS := "argocd"
		principalNS := "argocd"
		agentCl := fake.NewSimpleClientset()
		principalCl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		agentDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})
		principalDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})

		agentKubeClient := &kube.KubernetesClient{
			Clientset:     agentCl,
			DynamicClient: agentDynCl,
			Context:       context.TODO(),
			Namespace:     agentNS,
		}
		principalKubeClient := &kube.KubernetesClient{
			Clientset:     principalCl,
			DynamicClient: principalDynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		// Create principal CA
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, principalCl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		// Create agent CA secret (with principal CA)
		_, err = agentCl.CoreV1().Secrets(agentNS).Create(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: config.SecretNameAgentCA, Namespace: agentNS},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"ca.crt": []byte(caCertPEM),
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create agent ca secret")

		// Create client cert signed by different CA
		agentName := "test-cluster"
		wrongCertPEM, wrongKeyPEM := createCertSignedByDifferentCA(t, agentName, []string{}, []string{})
		mustCreateTLSSecret(t, agentCl, agentNS, config.SecretNameAgentClientCert, wrongCertPEM, wrongKeyPEM)

		// Create namespace on principal
		_, err = principalCl.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: agentName}}, metav1.CreateOptions{})
		require.NoError(t, err, "create ns")

		res := RunAgentChecks(context.TODO(), agentKubeClient, agentNS, principalKubeClient, principalNS)
		hasSignatureError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "signed by principal CA") {
				hasSignatureError = true
				require.Contains(t, r.err.Error(), "not signed", "error should mention certificate not signed by principal CA")
			}
		}
		require.True(t, hasSignatureError, "expected error when agent cert is not signed by principal CA")
	})

	t.Run("Client cert with empty CN", func(t *testing.T) {
		agentNS := "argocd"
		principalNS := "argocd"
		agentCl := fake.NewSimpleClientset()
		principalCl := fake.NewSimpleClientset()

		scheme := runtime.NewScheme()
		agentDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})
		principalDynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}: "ArgoCDList",
		})

		agentKubeClient := &kube.KubernetesClient{
			Clientset:     agentCl,
			DynamicClient: agentDynCl,
			Context:       context.TODO(),
			Namespace:     agentNS,
		}
		principalKubeClient := &kube.KubernetesClient{
			Clientset:     principalCl,
			DynamicClient: principalDynCl,
			Context:       context.TODO(),
			Namespace:     principalNS,
		}

		// Create principal CA
		caCertPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate(config.SecretNamePrincipalCA)
		require.NoError(t, err, "generate CA")
		mustCreateTLSSecret(t, principalCl, principalNS, config.SecretNamePrincipalCA, caCertPEM, caKeyPEM)

		// Create agent CA secret
		_, err = agentCl.CoreV1().Secrets(agentNS).Create(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: config.SecretNameAgentCA, Namespace: agentNS},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"ca.crt": []byte(caCertPEM),
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err, "create agent ca secret")

		// Create client cert with empty CN
		caCert, err := tlsutil.TLSCertFromSecret(context.TODO(), principalCl, principalNS, config.SecretNamePrincipalCA)
		require.NoError(t, err, "read CA")
		signer, err := x509.ParseCertificate(caCert.Certificate[0])
		require.NoError(t, err, "parse CA")
		emptyCNCertPEM, emptyCNKeyPEM := createCertWithEmptyCN(t, signer, caCert.PrivateKey)
		mustCreateTLSSecret(t, agentCl, agentNS, config.SecretNameAgentClientCert, emptyCNCertPEM, emptyCNKeyPEM)

		res := RunAgentChecks(context.TODO(), agentKubeClient, agentNS, principalKubeClient, principalNS)
		hasEmptyCNError := false
		for _, r := range res {
			if r.err != nil && strings.Contains(r.name, "namespace") {
				hasEmptyCNError = true
				require.Contains(t, r.err.Error(), "empty", "error should mention empty CN")
			}
		}
		require.True(t, hasEmptyCNError, "expected error when agent cert has empty CN")
	})
}
