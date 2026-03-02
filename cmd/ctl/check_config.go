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
	"net/url"
	"strings"
	"time"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj/argo-cd/v3/common"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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
			results = append(results, RunAgentChecks(ctx, agentClt, globalOpts.agentNamespace, principalClt, globalOpts.principalNamespace)...)
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

// getArgoCDCR retrieves the ArgoCD CR from the specified namespace, if it exists.
func getArgoCDCR(ctx context.Context, kc *kube.KubernetesClient, ns string) (*unstructured.Unstructured, bool, error) {
	if kc.DynamicClient == nil {
		return nil, false, fmt.Errorf("dynamic client is not available")
	}
	gvr := schema.GroupVersionResource{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}
	list, err := kc.DynamicClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		// If CR doesn't exist, return found=false with no error
		if k8serrors.IsNotFound(err) {
			return nil, false, nil
		}
		// For other errors, return the error
		return nil, false, fmt.Errorf("failed to list ArgoCD CRs: %w", err)
	}
	if list == nil || len(list.Items) == 0 {
		return nil, false, nil // CR not found, use defaults
	}
	// Use the ArgoCD CR found in the namespace
	return &list.Items[0], true, nil
}

// getPrincipalSecretNames reads secret names from ArgoCD CR if available, otherwise returns defaults.
func getPrincipalSecretNames(ctx context.Context, kc *kube.KubernetesClient, ns string) (string, string, string, string, error) {
	caSecretName := config.SecretNamePrincipalCA
	tlsSecretName := config.SecretNamePrincipalTLS
	proxyTLSSecretName := config.SecretNameProxyTLS
	jwtSecretName := config.SecretNameJWT

	// Try to read from ArgoCD CR
	cr, found, err := getArgoCDCR(ctx, kc, ns)
	if err != nil {
		// Return error for real failures (permission denied, network issues, etc.)
		return "", "", "", "", fmt.Errorf("failed to access ArgoCD CR in namespace %s: %w", ns, err)
	}
	if found {
		// Read principal.tls.rootCASecretName
		if val, found, err := unstructured.NestedString(cr.Object, "spec", "argoCDAgent", "principal", "tls", "rootCASecretName"); err != nil {
			return "", "", "", "", fmt.Errorf("failed to read principal.tls.rootCASecretName from ArgoCD CR: %w", err)
		} else if found && val != "" {
			caSecretName = val
		}
		// Read principal.tls.secretName
		if val, found, err := unstructured.NestedString(cr.Object, "spec", "argoCDAgent", "principal", "tls", "secretName"); err != nil {
			return "", "", "", "", fmt.Errorf("failed to read principal.tls.secretName from ArgoCD CR: %w", err)
		} else if found && val != "" {
			tlsSecretName = val
		}
		// Read principal.resourceProxy.tls.secretName
		if val, found, err := unstructured.NestedString(cr.Object, "spec", "argoCDAgent", "principal", "resourceProxy", "tls", "secretName"); err != nil {
			return "", "", "", "", fmt.Errorf("failed to read principal.resourceProxy.tls.secretName from ArgoCD CR: %w", err)
		} else if found && val != "" {
			proxyTLSSecretName = val
		}
		// Read principal.jwt.secretName
		if val, found, err := unstructured.NestedString(cr.Object, "spec", "argoCDAgent", "principal", "jwt", "secretName"); err != nil {
			return "", "", "", "", fmt.Errorf("failed to read principal.jwt.secretName from ArgoCD CR: %w", err)
		} else if found && val != "" {
			jwtSecretName = val
		}
	}
	return caSecretName, tlsSecretName, proxyTLSSecretName, jwtSecretName, nil
}

// getAgentSecretNames reads secret names from ArgoCD CR if available, otherwise returns defaults.
func getAgentSecretNames(ctx context.Context, kc *kube.KubernetesClient, ns string) (string, string, error) {
	caSecretName := config.SecretNameAgentCA
	clientCertSecretName := config.SecretNameAgentClientCert

	// Try to read from ArgoCD CR
	cr, found, err := getArgoCDCR(ctx, kc, ns)
	if err != nil {
		// Return error for real failures (permission denied, network issues, etc.)
		return "", "", fmt.Errorf("failed to access ArgoCD CR in namespace %s: %w", ns, err)
	}
	if found {
		// Read agent.tls.rootCASecretName
		if val, found, err := unstructured.NestedString(cr.Object, "spec", "argoCDAgent", "agent", "tls", "rootCASecretName"); err != nil {
			return "", "", fmt.Errorf("failed to read agent.tls.rootCASecretName from ArgoCD CR: %w", err)
		} else if found && val != "" {
			caSecretName = val
		}
		// Read agent.tls.secretName
		if val, found, err := unstructured.NestedString(cr.Object, "spec", "argoCDAgent", "agent", "tls", "secretName"); err != nil {
			return "", "", fmt.Errorf("failed to read agent.tls.secretName from ArgoCD CR: %w", err)
		} else if found && val != "" {
			clientCertSecretName = val
		}
	}
	return caSecretName, clientCertSecretName, nil
}

// RunPrincipalChecks validates the principal installation and related security assets.
func RunPrincipalChecks(ctx context.Context, kubeClient *kube.KubernetesClient, principalNS string) []checkResult {
	out := []checkResult{}

	// Get secret names from ArgoCD CR or use defaults
	caSecretName, tlsSecretName, proxyTLSSecretName, jwtSecretName, err := getPrincipalSecretNames(ctx, kubeClient, principalNS)
	if err != nil {
		out = append(out, checkResult{
			name: "Reading principal secret names from ArgoCD CR",
			err:  err,
		})
		// Use defaults if CR access failed
		caSecretName = config.SecretNamePrincipalCA
		tlsSecretName = config.SecretNamePrincipalTLS
		proxyTLSSecretName = config.SecretNameProxyTLS
		jwtSecretName = config.SecretNameJWT
	}

	// Ensure Argo CD is running in cluster-scoped mode
	out = append(out, checkResult{
		name: "Verifying Argo CD is running in cluster-scoped mode",
		err:  verifyArgoCDClusterScoped(ctx, kubeClient, principalNS),
	})

	// CA secret exists in principal namespace and is a valid TLS secret
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying principal public CA certificate exists and is valid (%s/%s)", principalNS, caSecretName),
		err:  principalCheckCA(ctx, kubeClient.Clientset, principalNS, caSecretName),
	})

	// Principal gRPC TLS secret exists in principal namespace and is valid
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying principal gRPC TLS certificate exists and is valid (%s/%s)", principalNS, tlsSecretName),
		err:  certSecretValid(ctx, kubeClient.Clientset, principalNS, tlsSecretName),
	})

	// Resource proxy TLS secret exists and is valid
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying resource proxy TLS certificate exists and is valid (%s/%s)", principalNS, proxyTLSSecretName),
		err:  certSecretValid(ctx, kubeClient.Clientset, principalNS, proxyTLSSecretName),
	})

	// JWT signing key exists
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying JWT signing key exists and is parseable (%s/%s)", principalNS, jwtSecretName),
		err:  jwtKeyValid(ctx, kubeClient.Clientset, principalNS, jwtSecretName),
	})

	// Route host matches TLS SANs (OpenShift-only)
	exists, err := routeAPIExists(kubeClient)
	switch {
	case err != nil:
		out = append(out, checkResult{
			name: "Checking for OpenShift Route API availability",
			err:  err,
		})
	case exists:
		hasRoutes, err := routesExist(ctx, kubeClient, principalNS)
		if err != nil {
			out = append(out, checkResult{
				name: "Checking for OpenShift Routes in namespace",
				err:  fmt.Errorf("failed to check for OpenShift Routes in namespace %s: %w", principalNS, err),
			})
		} else if hasRoutes {
			out = append(out, checkResult{
				name: "Verifying principal TLS secret ips/dns match Route host (OpenShift)",
				err:  verifyRouteHostMatchesCert(ctx, kubeClient, principalNS, tlsSecretName),
			})
		}
	}

	// No Application CRs defined within the principal's Argo CD namespace
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying no Application CRs defined within the principal's Argo CD namespace: %s", principalNS),
		err:  principalNoApplicationCRs(ctx, kubeClient, principalNS),
	})

	// Cluster secrets for agents are https and have the agentName query param
	out = append(out, checkResult{
		name: "Verifying agent cluster secrets contain correct query parameter on server url",
		err:  principalVerifyClusterSecretServer(ctx, kubeClient, principalNS),
	})

	// Cluster secrets for agents have the skip-reconcile annotation set to true
	out = append(out, checkResult{
		name: "Verifying agent cluster secrets contain the skip-reconcile annotation and that it's set to true",
		err:  principalVerifyClusterSecretAnnotation(ctx, kubeClient, principalNS),
	})

	// Argo CD redis network policy allows ingress from principal
	out = append(out, checkResult{
		name: "Verifying Argo CD network policy allows for ingress from principal",
		err:  bothVerifyRedisNetworkPolicy(ctx, kubeClient, principalNS, "principal"),
	})

	// Ensures correct components are deployed on the principal
	out = append(out, checkResult{
		name: "Verifying deployed Argo CD components on principal",
		err:  principalVerifyDeployedComponents(ctx, kubeClient, principalNS),
	})

	return out
}

// RunAgentChecks validates agent-side security assets and cross-validates them
// against the principal cluster.
func RunAgentChecks(ctx context.Context, agentKubeClient *kube.KubernetesClient, agentNS string, principalKubeClient *kube.KubernetesClient, principalNS string) []checkResult {
	out := []checkResult{}

	// Get secret names from ArgoCD CR or use defaults
	agentCASecretName, agentClientCertSecretName, err := getAgentSecretNames(ctx, agentKubeClient, agentNS)
	if err != nil {
		out = append(out, checkResult{
			name: "Reading agent secret names from ArgoCD CR",
			err:  err,
		})
		// Use defaults if CR access failed
		agentCASecretName = config.SecretNameAgentCA
		agentClientCertSecretName = config.SecretNameAgentClientCert
	}
	principalCASecretName, _, _, _, err := getPrincipalSecretNames(ctx, principalKubeClient, principalNS)
	if err != nil {
		out = append(out, checkResult{
			name: "Reading principal secret names from ArgoCD CR",
			err:  err,
		})
		// Use defaults if CR access failed
		principalCASecretName = config.SecretNamePrincipalCA
	}

	// Agent CA secret exists and has CA data (opaque secret with ca.crt)
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying agent CA secret exists and contains CA cert (%s/%s)", agentNS, agentCASecretName),
		err:  agentCASecretValid(ctx, agentKubeClient.Clientset, agentNS, agentCASecretName),
	})

	// Agent client TLS secret exists and is valid and not expired
	out = append(out, checkResult{
		name: fmt.Sprintf("Verifying agent mTLS certificate exists and is not expired (%s/%s)", agentNS, agentClientCertSecretName),
		err:  clientCertNotExpired(ctx, agentKubeClient.Clientset, agentNS, agentClientCertSecretName),
	})

	// Namespace with same name as agent cert subject exists on principal cluster
	out = append(out, checkResult{
		name: "Verifying namespace on principal matches agent certificate subject",
		err:  namespaceMatchesAgentSubject(ctx, agentKubeClient.Clientset, agentNS, agentClientCertSecretName, principalKubeClient.Clientset),
	})

	// Agent client TLS is signed by principal CA
	out = append(out, checkResult{
		name: "Verifying agent mTLS certificate is signed by principal CA certificate",
		err:  clientCertSignedByPrincipalCA(ctx, agentKubeClient.Clientset, agentNS, agentClientCertSecretName, principalKubeClient.Clientset, principalNS, principalCASecretName),
	})

	// Agent Argo CD redis network policy allows for ingress from agent
	out = append(out, checkResult{
		name: "Verifying Argo CD network policy allows for ingress from agent",
		err:  bothVerifyRedisNetworkPolicy(ctx, agentKubeClient, agentNS, "agent"),
	})

	// Ensures correct components are deployed on the agent
	out = append(out, checkResult{
		name: "Verifying deployed Argo CD components on agent",
		err:  agentVerifyDeployedComponents(ctx, agentKubeClient, agentNS),
	})

	return out
}

func principalCheckCA(ctx context.Context, kubeClient kubernetes.Interface, ns, secretName string) error {
	_, err := tlsutil.TLSCertFromSecret(ctx, kubeClient, ns, secretName)
	return err
}

func certSecretValid(ctx context.Context, kubeClient kubernetes.Interface, ns, name string) error {
	parsed, err := x509FromTLSSecret(ctx, kubeClient, ns, name)
	if err != nil {
		return err
	}
	now := time.Now()
	if now.Before(parsed.NotBefore) {
		return fmt.Errorf("certificate in secret %s/%s is not yet valid (valid from %s)", ns, name, parsed.NotBefore)
	}

	if now.After(parsed.NotAfter) {
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

// verifyArgoCDClusterScoped verifies that Argo CD is running in cluster-scoped mode by:
// 1. Checking that spec.applicationNamespaces is not set (or application.namespaces ConfigMap key is unset)
// 2. Verifying that Applications can be managed across namespaces (actual cluster-scoped behavior)
func verifyArgoCDClusterScoped(ctx context.Context, kc *kube.KubernetesClient, ns string) error {
	if kc.DynamicClient == nil {
		return fmt.Errorf("dynamic client is not available, cannot verify cluster-scoped mode")
	}

	// First, verify applicationNamespaces is not set
	appNamespacesSet := false
	crNotFound := false

	// Try operator CR first: group argoproj.io, version v1beta1, resource argocds
	gvr := schema.GroupVersionResource{Group: "argoproj.io", Version: "v1beta1", Resource: "argocds"}
	list, err := kc.DynamicClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		// If NotFound, allow fallback to ConfigMap check
		if k8serrors.IsNotFound(err) {
			crNotFound = true
		} else {
			// Return all other errors immediately
			return fmt.Errorf("failed to list ArgoCD CRs: %w", err)
		}
	} else if list != nil && len(list.Items) > 0 {
		for _, it := range list.Items {
			// If applicationNamespaces is non-empty, Argo CD is in namespaced mode (not cluster-scoped)
			arr, found, err := unstructured.NestedSlice(it.Object, "spec", "applicationNamespaces")
			if err != nil {
				return fmt.Errorf("failed to read applicationNamespaces from ArgoCD CR: %w", err)
			}
			if found && len(arr) > 0 {
				appNamespacesSet = true
				break
			}
		}
	}

	// Fallback: check the core Argo CD ConfigMap (argocd-cm)
	if !appNamespacesSet {
		cm, err := kc.Clientset.CoreV1().ConfigMaps(ns).Get(ctx, "argocd-cm", metav1.GetOptions{})
		if err != nil {
			// If both CR and ConfigMap are not found, return error
			if k8serrors.IsNotFound(err) && crNotFound {
				return fmt.Errorf("neither ArgoCD CR nor argocd-cm ConfigMap found in namespace %s", ns)
			}
			// Return all other errors
			return fmt.Errorf("failed to get argocd-cm ConfigMap: %w", err)
		}
		if v := strings.TrimSpace(cm.Data["application.namespaces"]); v != "" {
			appNamespacesSet = true
		}
	}

	if appNamespacesSet {
		return fmt.Errorf("argo CD configured for namespaced mode (applicationNamespaces is set), must be configured for cluster-scoped mode")
	}

	// Verify cluster-scoped behavior by checking if Applications can be accessed across namespaces
	// In cluster-scoped mode, we should be able to list Applications from any namespace (even though
	// Applications themselves are namespace-scoped resources). This verifies that Argo CD can operate
	// in cluster-scoped mode, managing Applications across all namespaces.
	appGVR := schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

	// List Applications across all namespaces (cluster-scoped operation)
	// This is equivalent to: kubectl get applications --all-namespaces
	_, err = kc.DynamicClient.Resource(appGVR).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		// If we can't list Applications cluster-wide, return the error
		// This could indicate namespaced mode is restricting access or a permissions issue
		return fmt.Errorf("failed to list Applications cluster-wide (required for cluster-scoped mode): %w", err)
	}
	// If list succeeds, we can confirm cluster-scoped mode is working
	return nil
}

// routeAPIExists checks if the OpenShift Route API exists on the cluster.
func routeAPIExists(kc *kube.KubernetesClient) (bool, error) {
	if kc.Clientset == nil {
		return false, fmt.Errorf("kubernetes clientset is not available")
	}
	_, err := kc.Clientset.Discovery().ServerResourcesForGroupVersion("route.openshift.io/v1")
	if err != nil {
		if meta.IsNoMatchError(err) || k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to query OpenShift Route API: %w", err)
	}
	return true, nil
}

// routesExist checks if any Route resources exist in the specified namespace.
func routesExist(ctx context.Context, kc *kube.KubernetesClient, ns string) (bool, error) {
	if kc.DynamicClient == nil {
		return false, fmt.Errorf("dynamic client is not available")
	}
	gvr := schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}
	routes, err := kc.DynamicClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list OpenShift Routes: %w", err)
	}
	return routes != nil && len(routes.Items) > 0, nil
}

// verifyRouteHostMatchesCert checks OpenShift Route host in ns is present in IPS/DNS of the given TLS secret.
// This function should only be called if routeAPIExists returns true.
func verifyRouteHostMatchesCert(ctx context.Context, kc *kube.KubernetesClient, ns string, tlsSecretName string) error {
	if kc.DynamicClient == nil {
		return fmt.Errorf("dynamic client is not available")
	}

	// Route API exists, proceed with checking routes
	gvr := schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}
	routes, err := kc.DynamicClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list OpenShift Routes: %w", err)
	}

	if routes == nil {
		return nil // skip if Route API not present
	}

	if len(routes.Items) == 0 {
		return nil // skip if no routes exist
	}

	// Load cert IPS/DNS
	parsed, err := x509FromTLSSecret(ctx, kc.Clientset, ns, tlsSecretName)
	if err != nil {
		return err
	}

	// Check if any route hostname matches the certificate's SANs
	for _, route := range routes.Items {
		hostname, found, err := unstructured.NestedString(route.Object, "spec", "host")
		if err != nil {
			return fmt.Errorf("failed to read host from Route: %w", err)
		}
		if !found || hostname == "" {
			// Skip routes without hostnames
			continue
		}

		// Verify if the route hostname is in the certificate's SANs
		if err := parsed.VerifyHostname(hostname); err != nil {
			// Hostname doesn't match certificate, try next route
			continue
		}

		// Found a matching route hostname, verification successful
		return nil
	}

	// No route hostnames matched the certificate SANs
	return fmt.Errorf("no OpenShift Route host in namespace matches TLS IPS/DNS")
}

// principalNoApplicationCRs checks the principal's namespace to ensure that no applications exist in it
func principalNoApplicationCRs(ctx context.Context, kc *kube.KubernetesClient, ns string) error {
	if kc.DynamicClient == nil {
		return fmt.Errorf("dynamic client is not available, failed to check applications")
	}

	// list applications in namespace and check to see if there is any
	gvr := schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
	apps, err := kc.DynamicClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list applications in namespace %s: %v", ns, err)
	}

	if len(apps.Items) > 0 {
		return fmt.Errorf("applications exist in principal namespace %s", ns)
	}

	return nil
}

// helper function to filter secrets list to argo cd cluster secrets that are managed by argocd-agent
func filterManagedClusterSecrets(secrets *corev1.SecretList) []corev1.Secret {
	var filtered []corev1.Secret
	for _, secret := range secrets.Items {
		isManaged := false
		if manager, ok := secret.Annotations["managed-by"]; ok && manager == "argocd-agent" {
			isManaged = true
		}

		isClusterSecret := false
		if val, ok := secret.Labels[common.LabelKeySecretType]; ok && val == common.LabelValueSecretTypeCluster {
			isClusterSecret = true
		}

		if isManaged && isClusterSecret {
			filtered = append(filtered, secret)
		}
	}
	return filtered
}

// principalVerifyClusterSecretServer verifies that the server in the cluster secrets that are managed by argocd agent in the
// principal's namespace begin with https:// and include the agentName query parameter
func principalVerifyClusterSecretServer(ctx context.Context, kc *kube.KubernetesClient, ns string) error {
	if kc.Clientset == nil {
		return fmt.Errorf("client set is not available, failed to check secrets")
	}

	secrets, err := kc.Clientset.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed list secrets in namespace %s: %s", ns, err.Error())
	}

	var noHTTPS []string
	var noAgentParam []string
	filteredSecrets := filterManagedClusterSecrets(secrets)
	for _, secret := range filteredSecrets {
		// parse url to see if https and has the agentName query param
		urlBytes := string(secret.Data["server"])
		agentURL, err := url.Parse(urlBytes)
		if err != nil {
			return fmt.Errorf("failed to prase url for %s/%s: %s", ns, secret.Name, err.Error())
		}

		if !strings.HasPrefix(urlBytes, "https://") {
			noHTTPS = append(noHTTPS, secret.Name)
		}

		query := agentURL.Query()
		if strings.TrimSpace(query.Get("agentName")) == "" {
			noAgentParam = append(noAgentParam, secret.Name)
		}
	}

	// combine parse results for a verbose error on which secret was missing what
	var errParts []string
	if len(noHTTPS) > 0 {
		errParts = append(errParts, fmt.Sprintf("the following agent cluster secrets in %s are not https: %s", ns, strings.Join(noHTTPS, ", ")))
	}

	if len(noAgentParam) > 0 {
		errParts = append(errParts, fmt.Sprintf("the following agent cluster secrets in %s are missing the agentName param: %s", ns, strings.Join(noAgentParam, ", ")))
	}

	if len(errParts) > 0 {
		return fmt.Errorf("%s", strings.Join(errParts, "\n"))
	}

	return nil
}

// principalVerifyClusterSecretAnnotation verifies that each cluster secret that is managed by argocd agent
// includes the skip-reconcile annotation
func principalVerifyClusterSecretAnnotation(ctx context.Context, kc *kube.KubernetesClient, ns string) error {
	if kc.Clientset == nil {
		return fmt.Errorf("client set is not available, failed to check secrets")
	}

	secrets, err := kc.Clientset.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list secrets in namespace %s: %s", ns, err.Error())
	}

	var noAnnotation []string
	filteredSecrets := filterManagedClusterSecrets(secrets)
	for _, secret := range filteredSecrets {
		if val, ok := secret.Annotations[common.AnnotationKeyAppSkipReconcile]; !ok || val != "true" {
			noAnnotation = append(noAnnotation, secret.Name)
		}
	}

	if len(noAnnotation) > 0 {
		return fmt.Errorf("the following agent cluster secrets in %s are missing the skip-reconcile annotation or it is not set to true: %s", ns, strings.Join(noAnnotation, ", "))
	}

	return nil
}

// listDeployedArgoCDComponents parses the specified namespace for Argo CD components and returns them as a map where the key
// is the component and value is the name of the resource
func listDeployedArgoCDComponents(ctx context.Context, kc *kube.KubernetesClient, ns string) (map[string]string, error) {
	if kc.Clientset == nil {
		return nil, fmt.Errorf("client set is not available, failed to list deployed components")
	}

	components := make(map[string]string)

	deployments, err := kc.Clientset.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	// use the part-of and component labels to tell what argocd component resource is
	for _, deploy := range deployments.Items {
		if deploy.Labels["app.kubernetes.io/part-of"] != "argocd" {
			continue
		}
		component, exists := deploy.Labels["app.kubernetes.io/component"]
		if !exists {
			continue
		}
		components[component] = deploy.Name
	}

	statefulsets, err := kc.Clientset.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for _, set := range statefulsets.Items {
		if set.Labels["app.kubernetes.io/part-of"] != "argocd" {
			continue
		}
		component, exists := set.Labels["app.kubernetes.io/component"]
		if !exists {
			continue
		}
		components[component] = set.Name
	}

	return components, nil
}

// parseDeployedComponents is a helper function to format the output for the deployed component checks
func parseDeployedComponents(components map[string]string, validComponents, invalidComponents []string) string {
	var missingComponents []string
	for _, component := range validComponents {
		if _, exists := components[component]; !exists {
			missingComponents = append(missingComponents, component)
		}
	}

	var foundInvalidComponents []string
	for _, component := range invalidComponents {
		if name, exists := components[component]; exists {
			// print result out with name of resource for more information
			foundInvalidComponents = append(foundInvalidComponents, fmt.Sprintf("%s (%s)", component, name))
		}
	}

	// combine results for more informative output
	var errParts []string
	if len(missingComponents) > 0 {
		errParts = append(errParts, fmt.Sprintf("the following components were missing: %s", strings.Join(missingComponents, ", ")))
	}

	if len(foundInvalidComponents) > 0 {
		errParts = append(errParts, fmt.Sprintf("the following invalid components were found: %s", strings.Join(foundInvalidComponents, ", ")))
	}

	return strings.Join(errParts, "\n")
}

// principalVerifyDeployedComponents verifies that there is neither an application or application set
// controller on the principal and that the repo-server, dex-server, redis, and argocd-server are deployed
func principalVerifyDeployedComponents(ctx context.Context, kc *kube.KubernetesClient, ns string) error {
	if kc.Clientset == nil {
		return fmt.Errorf("client set is not available, failed to list deployed components")
	}

	components, err := listDeployedArgoCDComponents(ctx, kc, ns)
	if err != nil {
		return fmt.Errorf("failed to list Argo CD components in namespace %s: %s", ns, err.Error())
	}

	validComponents := []string{common.LabelValueComponentRepoServer, "redis", "dex-server", "server"}
	invalidComponents := []string{"application-controller", "applicationset-controller"}
	result := parseDeployedComponents(components, validComponents, invalidComponents)

	if result != "" {
		return fmt.Errorf("%s", result)
	}

	return nil
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
	now := time.Now()
	if now.Before(parsed.NotBefore) {
		return fmt.Errorf("agent certificate not yet valid (valid from %s)", parsed.NotBefore)
	}
	if now.After(parsed.NotAfter) {
		return fmt.Errorf("agent certificate expired at %s", parsed.NotAfter)
	}
	return nil
}

func clientCertSignedByPrincipalCA(ctx context.Context, agentKube kubernetes.Interface, agentNS, agentClientCertSecretName string, principalKube kubernetes.Interface, principalNS, principalCASecretName string) error {
	agentX509, err := x509FromTLSSecret(ctx, agentKube, agentNS, agentClientCertSecretName)
	if err != nil {
		return err
	}
	caX509, err := x509FromTLSSecret(ctx, principalKube, principalNS, principalCASecretName)
	if err != nil {
		return err
	}
	if err := agentX509.CheckSignatureFrom(caX509); err != nil {
		return fmt.Errorf("agent certificate not signed by principal CA: %w", err)
	}
	return nil
}

func namespaceMatchesAgentSubject(ctx context.Context, agentKube kubernetes.Interface, agentNS, agentClientCertSecretName string, principalKube kubernetes.Interface) error {
	agentX509, err := x509FromTLSSecret(ctx, agentKube, agentNS, agentClientCertSecretName)
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
	return nil
}

// x509FromTLSSecret retrieves a Kubernetes TLS secret and parses the certificate
// into an *x509.Certificate. The secret must contain exactly one certificate.
func x509FromTLSSecret(ctx context.Context, kubeClient kubernetes.Interface, ns, name string) (*x509.Certificate, error) {
	cert, err := tlsutil.TLSCertFromSecret(ctx, kubeClient, ns, name)
	if err != nil {
		return nil, err
	}
	if len(cert.Certificate) == 0 || cert.Certificate[0] == nil {
		return nil, fmt.Errorf("%s/%s: secret does not contain certificate data", ns, name)
	}
	if len(cert.Certificate) > 1 {
		return nil, fmt.Errorf("%s/%s: secret contains %d certificates, expected exactly one", ns, name, len(cert.Certificate))
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("could not parse certificate in secret %s/%s: %w", ns, name, err)
	}
	return parsed, nil
}

func agentVerifyDeployedComponents(ctx context.Context, kc *kube.KubernetesClient, ns string) error {
	if kc.Clientset == nil {
		return fmt.Errorf("client set is not available, failed to list deployed components")
	}

	components, err := listDeployedArgoCDComponents(ctx, kc, ns)
	if err != nil {
		return fmt.Errorf("failed to list Argo CD components in %s: %s", ns, err.Error())
	}

	// applicationset controller is optional so it is not checked
	validComponents := []string{common.LabelValueComponentRepoServer, "application-controller", "redis"}
	invalidComponents := []string{"server", "dex-server"}
	result := parseDeployedComponents(components, validComponents, invalidComponents)

	if result != "" {
		return fmt.Errorf("%s", result)
	}

	return nil
}

// bothVerifyRedisNetworkPolicy verifies that the network policy for Argo CD allows ingress traffic
// from either the principal or agent
func bothVerifyRedisNetworkPolicy(ctx context.Context, kc *kube.KubernetesClient, ns, component string) error {
	if kc.Clientset == nil {
		return fmt.Errorf("client set is not available, failed to check network policies")
	}

	componentName := fmt.Sprintf("argocd-agent-%s", component)

	netPolicies, err := kc.Clientset.NetworkingV1().NetworkPolicies(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list network policies in namespace %s: %s", ns, err.Error())
	}

	policyExists := false
	ingressAllowed := false
	for _, policy := range netPolicies.Items {
		// use pod selector to determine which network policy targets argocd redis
		name, exists := policy.Spec.PodSelector.MatchLabels[common.LabelKeyAppName]
		if !exists || name != common.DefaultRedisName {
			continue
		}

		policyExists = true
		for _, ingress := range policy.Spec.Ingress {
			for _, from := range ingress.From {
				if from.PodSelector == nil {
					continue
				}

				if name, exists := from.PodSelector.MatchLabels[common.LabelKeyAppName]; exists && name == componentName {
					ingressAllowed = true
				}
			}
			if ingressAllowed {
				break
			}
		}
	}

	if !policyExists {
		return fmt.Errorf("network policy for argo cd redis does not exist in %s", ns)
	}

	if !ingressAllowed {
		return fmt.Errorf("network policy for argo cd redis does not allow traffic from %s", componentName)
	}

	return nil
}
