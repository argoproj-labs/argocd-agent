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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name  string
		cfg   localConfig
		valid bool
	}{
		{
			name: "valid configuration",
			cfg: localConfig{
				Contexts: contexts{
					Principals: map[string]componentConfig{
						"test-principal": {
							KubeContext: "kind-test-principal",
							Namespace:   "argocd",
						},
						"test-principal-aws": {
							KubeContext: "eks-test-principal",
							Namespace:   "argocd",
						},
					},
					Agents: map[string]componentConfig{
						"test-agent": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
						"test-agent-on-prem": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
					},
				},
				DefaultPrincipal: "test-principal",
			},
			valid: true,
		},
		{
			name: "blank default principal",
			cfg: localConfig{
				Contexts: contexts{
					Principals: map[string]componentConfig{
						"test-principal": {
							KubeContext: "kind-test-principal",
							Namespace:   "argocd",
						},
						"test-principal-aws": {
							KubeContext: "eks-test-principal",
							Namespace:   "argocd",
						},
					},
					Agents: map[string]componentConfig{
						"test-agent": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
						"test-agent-on-prem": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
					},
				},
			},
			valid: false,
		},
		{
			name: "blank agent kubecontext",
			cfg: localConfig{
				Contexts: contexts{
					Principals: map[string]componentConfig{
						"test-principal": {
							KubeContext: "kind-test-principal",
							Namespace:   "argocd",
						},
						"test-principal-aws": {
							KubeContext: "eks-test-principal",
							Namespace:   "argocd",
						},
					},
					Agents: map[string]componentConfig{
						"test-agent": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
						"test-agent-on-prem": {
							KubeContext: "",
							Namespace:   "argocd",
						},
					},
				},
				DefaultPrincipal: "test-principal",
			},
			valid: false,
		},
		{
			name: "blank agent namespace",
			cfg: localConfig{
				Contexts: contexts{
					Principals: map[string]componentConfig{
						"test-principal": {
							KubeContext: "kind-test-principal",
							Namespace:   "argocd",
						},
						"test-principal-aws": {
							KubeContext: "eks-test-principal",
							Namespace:   "argocd",
						},
					},
					Agents: map[string]componentConfig{
						"test-agent": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
						"test-agent-on-prem": {
							KubeContext: "test-agent",
							Namespace:   "",
						},
					},
				},
				DefaultPrincipal: "test-principal",
			},
			valid: false,
		},
		{
			name: "blank agent name",
			cfg: localConfig{
				Contexts: contexts{
					Principals: map[string]componentConfig{
						"test-principal": {
							KubeContext: "kind-test-principal",
							Namespace:   "argocd",
						},
						"test-principal-aws": {
							KubeContext: "eks-test-principal",
							Namespace:   "argocd",
						},
					},
					Agents: map[string]componentConfig{
						"test-agent": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
						"": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
					},
				},
				DefaultPrincipal: "test-principal",
			},
			valid: false,
		},
		{
			name: "blank principal kubecontext",
			cfg: localConfig{
				Contexts: contexts{
					Principals: map[string]componentConfig{
						"test-principal": {
							KubeContext: "",
							Namespace:   "argocd",
						},
						"test-principal-aws": {
							KubeContext: "eks-test-principal",
							Namespace:   "argocd",
						},
					},
					Agents: map[string]componentConfig{
						"test-agent": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
						"test-agent-on-prem": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
					},
				},
				DefaultPrincipal: "test-principal",
			},
			valid: false,
		},
		{
			name: "blank principal namespace",
			cfg: localConfig{
				Contexts: contexts{
					Principals: map[string]componentConfig{
						"test-principal": {
							KubeContext: "kind-test-principal",
							Namespace:   "",
						},
						"test-principal-aws": {
							KubeContext: "eks-test-principal",
							Namespace:   "argocd",
						},
					},
					Agents: map[string]componentConfig{
						"test-agent": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
						"test-agent-on-prem": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
					},
				},
				DefaultPrincipal: "test-principal",
			},
			valid: false,
		},
		{
			name: "blank principal name",
			cfg: localConfig{
				Contexts: contexts{
					Principals: map[string]componentConfig{
						"test-principal": {
							KubeContext: "kind-test-principal",
							Namespace:   "argocd",
						},
						"": {
							KubeContext: "eks-test-principal",
							Namespace:   "argocd",
						},
					},
					Agents: map[string]componentConfig{
						"test-agent": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
						"test-agent-on-prem": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
					},
				},
				DefaultPrincipal: "test-principal",
			},
			valid: false,
		},
		{
			name: "no principals listed",
			cfg: localConfig{
				Contexts: contexts{
					Agents: map[string]componentConfig{
						"test-agent": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
						"test-agent-on-prem": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
					},
				},
				DefaultPrincipal: "test-principal",
			},
			valid: false,
		},
		{
			name: "default principal does not exist in principal contexts",
			cfg: localConfig{
				Contexts: contexts{
					Principals: map[string]componentConfig{
						"test-principal": {
							KubeContext: "kind-test-principal",
							Namespace:   "argocd",
						},
						"test-principal-aws": {
							KubeContext: "eks-test-principal",
							Namespace:   "argocd",
						},
					},
					Agents: map[string]componentConfig{
						"test-agent": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
						"test-agent-on-prem": {
							KubeContext: "test-agent",
							Namespace:   "argocd",
						},
					},
				},
				DefaultPrincipal: "test-principal-on-some-cluster-thats-not-real",
			},
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(&tt.cfg)
			// true is valid and false is invalid
			require.Equal(t, tt.valid, err == nil, "was not the correct result")
		})
	}
}
