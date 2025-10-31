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
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"strings"
	"time"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/spf13/cobra"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
)

type checkResult struct {
	name string
	err  error
}

func (r checkResult) String() string {
	if r.err == nil {
		return fmt.Sprintf("* %s: ✅", r.name)
	}
	return fmt.Sprintf("* %s: ❌\nERROR: %v", r.name, r.err)
}

func NewCheckConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check-config",
		Short: "Validate principal and agent configuration",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
		GroupID: "config",
	}
	cmd.AddCommand(NewCheckConfigPrincipalCommand())
	cmd.AddCommand(NewCheckConfigAgentCommand())
	return cmd
}

func NewCheckConfigPrincipalCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "principal",
		Short: "Validate principal configuration",
		Run: func(cmd *cobra.Command, args []string) {
			if strings.TrimSpace(globalOpts.principalNamespace) == "" {
				cmdutil.Fatal("--principal-namespace is required")
			}
			ctx := context.TODO()
			clt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("Could not create Kubernetes client: %v", err)
			}
			results := RunPrincipalChecks(ctx, clt, globalOpts.principalNamespace)
			printResultsAndExit(results)
		},
	}
	return command
}

func NewCheckConfigAgentCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "agent",
		Short: "Validate agent configuration (and principal cross-checks)",
		Run: func(cmd *cobra.Command, args []string) {
			if strings.TrimSpace(globalOpts.agentContext) == "" ||
				strings.TrimSpace(globalOpts.agentNamespace) == "" ||
				strings.TrimSpace(globalOpts.principalContext) == "" ||
				strings.TrimSpace(globalOpts.principalNamespace) == "" {
				cmdutil.Fatal("--agent-context, --agent-namespace, --principal-context, --principal-namespace are all required")
			}
			ctx := context.TODO()
			agentClt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.agentNamespace, "", globalOpts.agentContext)
			if err != nil {
				cmdutil.Fatal("Could not create agent Kubernetes client: %v", err)
			}
			principalClt, err := kube.NewKubernetesClientFromConfig(ctx, globalOpts.principalNamespace, "", globalOpts.principalContext)
			if err != nil {
				cmdutil.Fatal("Could not create principal Kubernetes client: %v", err)
			}
			// Run principal checks as part of agent checks
			results := []checkResult{}
			results = append(results, RunPrincipalChecks(ctx, principalClt, globalOpts.principalNamespace)...)
			results = append(results, RunAgentChecks(ctx, agentClt.Clientset, globalOpts.agentNamespace, principalClt.Clientset, globalOpts.principalNamespace)...)
			printResultsAndExit(results)
		},
	}
	return command
}

func printResultsAndExit(results []checkResult) {
	hasErr := false
	fmt.Println("Configuration validation results:")
	for _, r := range results {
		fmt.Println(r.String())
		if r.err != nil {
			hasErr = true
		}
	}
	if hasErr {
		cmdutil.Fatal("one or more checks failed")
	}
}

// RunPrincipalChecks validates the principal installation and related security assets.
func RunPrincipalChecks(ctx context.Context, kubeClient *kube.KubernetesClient, principalNS string) []checkResult {
	out := []checkResult{}

	// Ensure Argo CD is cluster-scoped
	out = append(out, checkResult{
		name: "Verifying Argo CD is cluster-scoped",
		err:  verifyArgoCDClusterScoped(ctx, kubeClient, principalNS),
	})

	// CA secret exists in principal namespace and is a valid TLS secret
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying principal public CA certificate exists and is valid (%s/%s)", principalNS, config.SecretNamePrincipalCA),
		err:  principalCheckCA(ctx, kubeClient.Clientset, principalNS),
	})

	// Principal gRPC TLS secret exists in principal namespace and is valid
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying principal gRPC TLS certificate exists and is valid (%s/%s)", principalNS, config.SecretNamePrincipalTLS),
		err:  certSecretValid(ctx, kubeClient.Clientset, principalNS, config.SecretNamePrincipalTLS),
	})

	// Resource proxy TLS secret exists and is valid
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying resource proxy TLS certificate exists and is valid (%s/%s)", principalNS, config.SecretNameProxyTLS),
		err:  certSecretValid(ctx, kubeClient.Clientset, principalNS, config.SecretNameProxyTLS),
	})

	// JWT signing key exists
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying JWT signing key exists and is parseable (%s/%s)", principalNS, config.SecretNameJWT),
		err:  jwtKeyValid(ctx, kubeClient.Clientset, principalNS, config.SecretNameJWT),
	})

	// Route host matches TLS SANs (OpenShift-only)
	out = append(out, checkResult{
		name: "Verifying principal TLS secret ips/dns match Route host (OpenShift)",
		err:  verifyRouteHostMatchesCert(ctx, kubeClient, principalNS, config.SecretNamePrincipalTLS),
	})

	return out
}

// RunAgentChecks validates agent-side security assets and cross-validates them
// against the principal cluster.
func RunAgentChecks(ctx context.Context, agentKube kubernetes.Interface, agentNS string, principalKube kubernetes.Interface, principalNS string) []checkResult {
	out := []checkResult{}

	// Agent CA secret exists and has CA data (opaque secret with ca.crt)
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying agent CA secret exists and contains CA cert (%s/%s)", agentNS, config.SecretNameAgentCA),
		err:  agentCASecretValid(ctx, agentKube, agentNS, config.SecretNameAgentCA),
	})

	// Agent client TLS secret exists and is valid and not expired
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying agent mTLS certificate exists and is not expired (%s/%s)", agentNS, config.SecretNameAgentClientCert),
		err:  clientCertNotExpired(ctx, agentKube, agentNS, config.SecretNameAgentClientCert),
	})

	// Namespace with same name as agent cert subject exists on principal cluster
	out = append(out, checkResult{
		name: "Verifying namespace on principal matches agent certificate subject",
		err:  namespaceMatchesAgentSubject(ctx, agentKube, agentNS, principalKube, principalNS),
	})

	// Agent client TLS is signed by principal CA
	out = append(out, checkResult{
		name: "Verifying agent mTLS certificate is signed by principal CA certificate",
		err:  clientCertSignedByPrincipalCA(ctx, agentKube, agentNS, principalKube, principalNS),
	})

	return out
}

func principalCheckCA(ctx context.Context, kubeClient kubernetes.Interface, ns string) error {
	_, err := tlsutil.TLSCertFromSecret(ctx, kubeClient, ns, config.SecretNamePrincipalCA)
	return err
}

func certSecretValid(ctx context.Context, kubeClient kubernetes.Interface, ns, name string) error {
	parsed, err := x509FromTLSSecret(ctx, kubeClient, ns, name)
	if err != nil {
		return err
	}
	if time.Now().After(parsed.NotAfter) {
		return fmt.Errorf("certificate in secret %s/%s is expired", ns, name)
	}
	return nil
}

func jwtKeyValid(ctx context.Context, kubeClient kubernetes.Interface, ns, name string) error {
	key, err := tlsutil.JWTSigningKeyFromSecret(ctx, kubeClient, ns, name)
	if err != nil {
		return err
	}
	// Require RSA for now
	if _, ok := key.(*rsa.PrivateKey); !ok {
		return fmt.Errorf("JWT signing key is not an RSA private key")
	}
	return nil
}

// verifyArgoCDClusterScoped checks operator CR or argocd-cm to ensure no namespaced mode is configured.
func verifyArgoCDClusterScoped(ctx context.Context, kc *kube.KubernetesClient, ns string) error {
	// Try operator CR first: group argoproj.io, version v1alpha1, resource argocds
	gvr := schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "argocds"}
	// If CRD exists and an ArgoCD instance exists in ns, assert spec.applicationNamespaces is empty
	if kc.DynamicClient != nil {
		list, err := kc.DynamicClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err == nil && list != nil && len(list.Items) > 0 {
			for _, it := range list.Items {
				if arr, found, _ := unstructured.NestedSlice(it.Object, "spec", "applicationNamespaces"); found && len(arr) > 0 {
					return fmt.Errorf("argo CD configured for namespaced mode via spec.applicationNamespaces")
				}
			}
			return nil
		}
	}
	// Fallback when Operator CR is absent: check the core Argo CD ConfigMap (argocd-cm)
	// and fail if application.namespaces is set (namespaced mode). If the ConfigMap
	// does not exist or the key is empty, assume cluster-scoped and pass.
	cm, err := kc.Clientset.CoreV1().ConfigMaps(ns).Get(ctx, "argocd-cm", metav1.GetOptions{})
	if err == nil {
		if v := strings.TrimSpace(cm.Data["application.namespaces"]); v != "" {
			return fmt.Errorf("argocd-cm sets application.namespaces (%s)", v)
		}
		return nil
	}
	// If neither CR nor ConfigMap found, do not fail the check
	return nil
}

// verifyRouteHostMatchesCert checks OpenShift Route host in ns is present in IPS?DNS of the given TLS secret.
func verifyRouteHostMatchesCert(ctx context.Context, kc *kube.KubernetesClient, ns string, tlsSecretName string) error {
	// Discover route API by attempting a list via dynamic client
	if kc.DynamicClient == nil {
		return nil // skip
	}
	// Load cert IPS/DNS
	parsed, err := x509FromTLSSecret(ctx, kc.Clientset, ns, tlsSecretName)
	if err != nil {
		return err
	}

	// Try routes.route.openshift.io
	gvr := schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}
	routes, err := kc.DynamicClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil || routes == nil {
		return nil // skip if Route API not present or inaccessible
	}

	if len(routes.Items) == 0 {
		return nil // skip if no routes exist
	}

	// Pass if any route host matches SANs
	for _, r := range routes.Items {
		host, _, _ := unstructured.NestedString(r.Object, "spec", "host")
		if host == "" {
			continue
		}
		if err := parsed.VerifyHostname(host); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no OpenShift Route host in namespace matches TLS IPS/DNS")
}

func agentCASecretValid(ctx context.Context, kubeClient kubernetes.Interface, ns, name string) error {
	sec, err := kubeClient.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if len(sec.Data) == 0 {
		return fmt.Errorf("%s/%s: empty secret", ns, name)
	}
	// Expect a "ca.crt" field with PEM data
	if _, ok := sec.Data["ca.crt"]; !ok {
		return fmt.Errorf("%s/%s: missing ca.crt field", ns, name)
	}
	// Validate PEM parses into certs
	_, err = tlsutil.X509CertPoolFromSecret(ctx, kubeClient, ns, name, "ca.crt")
	return err
}

func clientCertNotExpired(ctx context.Context, kubeClient kubernetes.Interface, ns, name string) error {
	parsed, err := x509FromTLSSecret(ctx, kubeClient, ns, name)
	if err != nil {
		return err
	}
	if time.Now().After(parsed.NotAfter) {
		return fmt.Errorf("agent certificate expired at %s", parsed.NotAfter)
	}
	return nil
}

func clientCertSignedByPrincipalCA(ctx context.Context, agentKube kubernetes.Interface, agentNS string, principalKube kubernetes.Interface, principalNS string) error {
	agentX509, err := x509FromTLSSecret(ctx, agentKube, agentNS, config.SecretNameAgentClientCert)
	if err != nil {
		return err
	}
	caX509, err := x509FromTLSSecret(ctx, principalKube, principalNS, config.SecretNamePrincipalCA)
	if err != nil {
		return err
	}
	if err := agentX509.CheckSignatureFrom(caX509); err != nil {
		return fmt.Errorf("agent certificate not signed by principal CA: %w", err)
	}
	return nil
}

func namespaceMatchesAgentSubject(ctx context.Context, agentKube kubernetes.Interface, agentNS string, principalKube kubernetes.Interface, principalNS string) error {
	agentX509, err := x509FromTLSSecret(ctx, agentKube, agentNS, config.SecretNameAgentClientCert)
	if err != nil {
		return err
	}
	subj := strings.TrimSpace(agentX509.Subject.CommonName)
	if subj == "" {
		return fmt.Errorf("agent certificate subject (CN) is empty")
	}
	// Validate namespace exists on principal cluster
	_, err = principalKube.CoreV1().Namespaces().Get(ctx, subj, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return fmt.Errorf("namespace '%s' not found on principal cluster", subj)
		}
		return err
	}
	// optional: ensure agent's own namespace exists (sanity)
	_, _ = agentKube.CoreV1().Namespaces().Get(ctx, agentNS, metav1.GetOptions{})
	return nil
}

// x509FromTLSSecret retrieves a Kubernetes TLS secret and parses the first certificate
// in the chain into an *x509.Certificate.
func x509FromTLSSecret(ctx context.Context, kubeClient kubernetes.Interface, ns, name string) (*x509.Certificate, error) {
	cert, err := tlsutil.TLSCertFromSecret(ctx, kubeClient, ns, name)
	if err != nil {
		return nil, err
	}
	if len(cert.Certificate) == 0 || cert.Certificate[0] == nil {
		return nil, fmt.Errorf("%s/%s: secret does not contain certificate data", ns, name)
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("could not parse certificate in secret %s/%s: %w", ns, name, err)
	}
	return parsed, nil
}
