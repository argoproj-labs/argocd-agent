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

package kube

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
)

type KubernetesClient struct {
	Clientset             kubernetes.Interface
	DynamicClient         dynamic.Interface
	DiscoveryClient       discovery.DiscoveryInterface
	ApplicationsClientset versioned.Interface
	Context               context.Context
	Namespace             string
	RestConfig            *rest.Config
}

func NewKubernetesClient(ctx context.Context, client kubernetes.Interface, applicationsClientset versioned.Interface, namespace string) *KubernetesClient {
	kc := &KubernetesClient{}
	kc.Context = ctx
	kc.Clientset = client
	kc.ApplicationsClientset = applicationsClientset
	kc.Namespace = namespace
	return kc
}

// NewKubernetesClient creates a new Kubernetes client object from given
// configuration file. If configuration file is the empty string, in-cluster
// client will be created.
func NewKubernetesClientFromConfig(ctx context.Context, namespace string, kubeconfig string, kubecontext string) (*KubernetesClient, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.DefaultClientConfig = &clientcmd.DefaultClientConfig
	loadingRules.ExplicitPath = kubeconfig
	overrides := clientcmd.ConfigOverrides{}
	if kubecontext != "" {
		overrides.CurrentContext = kubecontext
	}
	clientConfig := clientcmd.NewInteractiveDeferredLoadingClientConfig(loadingRules, &overrides, os.Stdin)

	config, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	if namespace == "" {
		namespace, _, err = clientConfig.Namespace()
		if err != nil {
			return nil, err
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	applicationsClientset, err := versioned.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	cl := NewKubernetesClient(ctx, clientset, applicationsClientset, namespace)
	cl.RestConfig = config
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	cl.DynamicClient = dyn
	return cl, nil
}

func NewRestConfig(config string, context string) (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.DefaultClientConfig = &clientcmd.DefaultClientConfig
	loadingRules.ExplicitPath = config
	overrides := clientcmd.ConfigOverrides{}
	if context != "" {
		overrides.CurrentContext = context
	}
	clientConfig := clientcmd.NewInteractiveDeferredLoadingClientConfig(loadingRules, &overrides, os.Stdin)

	restCfg, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	return restCfg, nil
}

// IsRetryableError is a helper method to see whether an error returned from the dynamic client
// is potentially retryable. If the error is retryable we could retry again with exponential backoff.
// This code is directly copied from: https://github.com/argoproj/argo-cd/controller/cache/cache.go
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	return kerrors.IsInternalError(err) ||
		kerrors.IsInvalid(err) ||
		kerrors.IsTooManyRequests(err) ||
		kerrors.IsServerTimeout(err) ||
		kerrors.IsServiceUnavailable(err) ||
		kerrors.IsTimeout(err) ||
		kerrors.IsUnexpectedObjectError(err) ||
		kerrors.IsUnexpectedServerError(err) ||
		isResourceQuotaConflictErr(err) ||
		isTransientNetworkErr(err) ||
		isExceededQuotaErr(err) ||
		isHTTP2GoawayErr(err) ||
		errors.Is(err, syscall.ECONNRESET)
}

func isHTTP2GoawayErr(err error) bool {
	return strings.Contains(err.Error(), "http2: server sent GOAWAY and closed the connection")
}

func isExceededQuotaErr(err error) bool {
	return kerrors.IsForbidden(err) && strings.Contains(err.Error(), "exceeded quota")
}

func isResourceQuotaConflictErr(err error) bool {
	return kerrors.IsConflict(err) && strings.Contains(err.Error(), "Operation cannot be fulfilled on resourcequota")
}

func isTransientNetworkErr(err error) bool {
	var netErr net.Error
	switch {
	case errors.As(err, &netErr):
		var dnsErr *net.DNSError
		var opErr *net.OpError
		var unknownNetworkErr net.UnknownNetworkError
		var urlErr *url.Error
		switch {
		case errors.As(err, &dnsErr), errors.As(err, &opErr), errors.As(err, &unknownNetworkErr):
			return true
		case errors.As(err, &urlErr):
			// For a URL error, where it replies "connection closed"
			// retry again.
			return strings.Contains(err.Error(), "Connection closed by foreign host")
		}
	}

	errorString := err.Error()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		errorString = fmt.Sprintf("%s %s", errorString, exitErr.Stderr)
	}
	if strings.Contains(errorString, "net/http: TLS handshake timeout") ||
		strings.Contains(errorString, "i/o timeout") ||
		strings.Contains(errorString, "connection timed out") ||
		strings.Contains(errorString, "connection reset by peer") {
		return true
	}
	return false
}
