package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

func TestCheckConfigPrincipal_RequiredSecretsAndConfig_Valid(t *testing.T) {
	principalNS := "argocd"
	cl := fake.NewSimpleClientset()

	// Create a scheme and register ArgoCD CRD
	scheme := runtime.NewScheme()
	// Create dynamic client with the registered types
	dynCl := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Group: "argoproj.io", Version: "v1alpha1", Resource: "argocds"}: "ArgoCDList",
		{Group: "route.openshift.io", Version: "v1", Resource: "routes"}: "RouteList",
	})

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
}

func TestCheckConfigAgent_RequiredPrincipalNamespaceAndCASignature(t *testing.T) {
	agentNS := "argocd"
	principalNS := "argocd"
	agentCl := fake.NewSimpleClientset()
	principalCl := fake.NewSimpleClientset()

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

	res := RunAgentChecks(context.TODO(), agentCl, agentNS, principalCl, principalNS)
	for _, r := range res {
		require.NoError(t, r.err, "check failed: %s", r.name)
	}
}
