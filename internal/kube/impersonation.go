// Copyright 2026 The argocd-agent Authors
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
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ParseServiceAccountUser splits a Kubernetes service account user name of the
// form system:serviceaccount:<namespace>:<name> into its namespace and name.
//
// It is deliberately strict. This is the only form of identity Argo CD asks
// for when it impersonates, and anything else is refused rather than passed on
// to the Kubernetes API.
func ParseServiceAccountUser(user string) (namespace, name string, err error) {
	parts := strings.Split(user, ":")
	if len(parts) != 4 || parts[0] != "system" || parts[1] != "serviceaccount" {
		return "", "", fmt.Errorf("%q is not a service account user of the form system:serviceaccount:<namespace>:<name>", user)
	}
	if errs := validation.IsDNS1123Label(parts[2]); len(errs) > 0 {
		return "", "", fmt.Errorf("invalid service account namespace %q: %s", parts[2], strings.Join(errs, "; "))
	}
	if errs := validation.IsDNS1123Subdomain(parts[3]); len(errs) > 0 {
		return "", "", fmt.Errorf("invalid service account name %q: %s", parts[3], strings.Join(errs, "; "))
	}
	return parts[2], parts[3], nil
}

// Impersonate returns a copy of kc whose clients send every request on behalf
// of user, which must be a service account user as accepted by
// ParseServiceAccountUser.
//
// Any impersonation already set on kc's REST configuration is replaced, not
// extended, and kc itself is not modified. The copy has no
// ApplicationsClientset, so it cannot be used where the agent's own is meant.
func (kc *KubernetesClient) Impersonate(user string) (*KubernetesClient, error) {
	if _, _, err := ParseServiceAccountUser(user); err != nil {
		return nil, err
	}
	if kc.RestConfig == nil {
		return nil, errors.New("client has no REST configuration to impersonate with")
	}

	cfg := rest.CopyConfig(kc.RestConfig)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	imp := NewKubernetesClient(kc.Context, clientset, nil, kc.Namespace)
	imp.DynamicClient = dyn
	imp.RestConfig = cfg
	return imp, nil
}
