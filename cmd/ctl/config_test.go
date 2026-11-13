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
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestDetermineConfigs(t *testing.T) {
	localCfg := &localConfig{
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
					KubeContext: "kind-test-agent",
					Namespace:   "argocd",
				},
				"test-agent-on-prem": {
					KubeContext: "test-agent",
					Namespace:   "argocd",
				},
			},
		},
		DefaultPrincipal: "test-principal-aws",
	}

	tests := []struct {
		name                 string
		principal            string
		principalNS          string
		principalKC          string
		agent                string
		agentNS              string
		agentKC              string
		expectedPrincipalCfg componentConfig
		expectedAgentCfg     componentConfig
	}{
		{
			name:        "follows order with principal and agent context and namespace being highest",
			principal:   "test-principal",
			principalNS: "not-argocd",
			principalKC: "a-principal-not-in-config",
			agent:       "test-agent",
			agentNS:     "not-argocd",
			agentKC:     "an-agent-not-in-config",
			expectedPrincipalCfg: componentConfig{
				KubeContext: "a-principal-not-in-config",
				Namespace:   "not-argocd",
			},
			expectedAgentCfg: componentConfig{
				KubeContext: "an-agent-not-in-config",
				Namespace:   "not-argocd",
			},
		},
		{
			name:        "follows order if no context or namespace flags",
			principal:   "test-principal",
			principalNS: "",
			principalKC: "",
			agent:       "test-agent",
			agentNS:     "",
			agentKC:     "",
			expectedPrincipalCfg: componentConfig{
				KubeContext: "kind-test-principal",
				Namespace:   "argocd",
			},
			expectedAgentCfg: componentConfig{
				KubeContext: "kind-test-agent",
				Namespace:   "argocd",
			},
		},
		{
			name:        "follows order if no flags are present, agent will be blank",
			principal:   "",
			principalNS: "",
			principalKC: "",
			agent:       "",
			agentNS:     "",
			agentKC:     "",
			expectedPrincipalCfg: componentConfig{
				KubeContext: "eks-test-principal",
				Namespace:   "argocd",
			},
			expectedAgentCfg: componentConfig{
				KubeContext: "",
				Namespace:   "",
			},
		},
		{
			name:        "principal and agent are independent",
			principal:   "test-principal",
			principalNS: "",
			principalKC: "",
			agent:       "test-agent",
			agentNS:     "not-argocd",
			agentKC:     "an-agent-not-in-config",
			expectedPrincipalCfg: componentConfig{
				KubeContext: "kind-test-principal",
				Namespace:   "argocd",
			},
			expectedAgentCfg: componentConfig{
				KubeContext: "an-agent-not-in-config",
				Namespace:   "not-argocd",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &GlobalFlags{}
			opts.principal = tt.principal
			opts.principalContext = tt.principalKC
			opts.principalNamespace = tt.principalNS
			opts.agent = tt.agent
			opts.agentContext = tt.agentKC
			opts.agentNamespace = tt.agentNS

			principalCfg, agentCfg := determineConfigs(opts, localCfg)
			require.Equal(t, tt.expectedPrincipalCfg.KubeContext, principalCfg.KubeContext, "Principal kubecontext is not equal")
			require.Equal(t, tt.expectedPrincipalCfg.Namespace, principalCfg.Namespace, "Principal namespace is not equal")
			require.Equal(t, tt.expectedAgentCfg.KubeContext, agentCfg.KubeContext, "Agent kubecontext is not equal")
			require.Equal(t, tt.expectedAgentCfg.Namespace, agentCfg.Namespace, "Agent namespace is not equal")
		})
	}
}

// writeSeedConfig writes a config with two principals (prod is the default)
// and one agent.
func writeSeedConfig(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, writeLocalConfig(&localConfig{
		Contexts: contexts{
			Principals: map[string]componentConfig{
				"prod": {KubeContext: "prod-ctx", Namespace: "argocd"},
				"dev":  {KubeContext: "dev-ctx", Namespace: "argocd-dev"},
			},
			Agents: map[string]componentConfig{
				"agent-1": {KubeContext: "a1-ctx", Namespace: "agent-ns"},
			},
		},
		DefaultPrincipal: "prod",
	}, path))
}

func seedConfig(t *testing.T) string {
	t.Helper()
	path := useConfigPath(t)
	writeSeedConfig(t, path)
	return path
}

func mustReadConfig(t *testing.T, path string) *localConfig {
	t.Helper()
	cfg, err := readLocalConfig(path)
	require.NoError(t, err)
	return cfg
}

func Test_ConfigAddCommands(t *testing.T) {
	t.Run("add principal", func(t *testing.T) {
		path := seedConfig(t)
		out := runCommand(t, NewConfigAddPrincipalCommand(),
			"--name", "staging", "--context", "stg-ctx", "--namespace", "argocd-stg")
		assert.Equal(t, "New principal staging added successfully\n", out)
		cfg := mustReadConfig(t, path)
		assert.Equal(t, componentConfig{KubeContext: "stg-ctx", Namespace: "argocd-stg"}, cfg.Contexts.Principals["staging"])
		assert.Len(t, cfg.Contexts.Principals, 3)
		assert.Equal(t, "prod", cfg.DefaultPrincipal)
	})
	t.Run("add principal as default", func(t *testing.T) {
		path := seedConfig(t)
		runCommand(t, NewConfigAddPrincipalCommand(),
			"--name", "staging", "--context", "stg-ctx", "--namespace", "argocd-stg", "--set-default")
		assert.Equal(t, "staging", mustReadConfig(t, path).DefaultPrincipal)
	})
	t.Run("add principal to an empty config file", func(t *testing.T) {
		path := useConfigPath(t)
		require.NoError(t, os.WriteFile(path, nil, 0o600))
		runCommand(t, NewConfigAddPrincipalCommand(),
			"--name", "first", "--context", "c", "--namespace", "n", "--set-default")
		cfg := mustReadConfig(t, path)
		assert.Equal(t, "first", cfg.DefaultPrincipal)
		assert.Equal(t, map[string]componentConfig{"first": {KubeContext: "c", Namespace: "n"}}, cfg.Contexts.Principals)
	})
	t.Run("add agent", func(t *testing.T) {
		path := seedConfig(t)
		out := runCommand(t, NewConfigAddAgentCommand(),
			"--name", "agent-2", "--context", "a2-ctx", "--namespace", "agent-2-ns")
		assert.Equal(t, "New agent agent-2 added successfully\n", out)
		cfg := mustReadConfig(t, path)
		assert.Equal(t, componentConfig{KubeContext: "a2-ctx", Namespace: "agent-2-ns"}, cfg.Contexts.Agents["agent-2"])
		assert.Len(t, cfg.Contexts.Agents, 2)
		assert.Equal(t, "prod", cfg.DefaultPrincipal)
	})
}

func Test_ConfigEditCommands(t *testing.T) {
	t.Run("edit principal context and namespace", func(t *testing.T) {
		path := seedConfig(t)
		out := runCommand(t, NewConfigEditPrincipalCommand(),
			"--name", "dev", "--context", "new-ctx", "--namespace", "new-ns")
		assert.Equal(t, "Principal dev updated successfully\n", out)
		cfg := mustReadConfig(t, path)
		assert.Equal(t, componentConfig{KubeContext: "new-ctx", Namespace: "new-ns"}, cfg.Contexts.Principals["dev"])
		assert.Equal(t, componentConfig{KubeContext: "prod-ctx", Namespace: "argocd"}, cfg.Contexts.Principals["prod"])
		assert.Equal(t, "prod", cfg.DefaultPrincipal)
	})
	t.Run("edit principal only changes the fields that are passed", func(t *testing.T) {
		path := seedConfig(t)
		runCommand(t, NewConfigEditPrincipalCommand(), "--name", "dev", "--namespace", "only-ns")
		runCommand(t, NewConfigEditPrincipalCommand(), "--name", "prod", "--context", "only-ctx")
		cfg := mustReadConfig(t, path)
		assert.Equal(t, componentConfig{KubeContext: "dev-ctx", Namespace: "only-ns"}, cfg.Contexts.Principals["dev"])
		assert.Equal(t, componentConfig{KubeContext: "only-ctx", Namespace: "argocd"}, cfg.Contexts.Principals["prod"])
	})
	t.Run("edit principal can make it the default", func(t *testing.T) {
		path := seedConfig(t)
		runCommand(t, NewConfigEditPrincipalCommand(), "--name", "dev", "--set-default")
		cfg := mustReadConfig(t, path)
		assert.Equal(t, "dev", cfg.DefaultPrincipal)
		assert.Equal(t, componentConfig{KubeContext: "dev-ctx", Namespace: "argocd-dev"}, cfg.Contexts.Principals["dev"])
	})
	t.Run("edit agent only changes the fields that are passed", func(t *testing.T) {
		path := seedConfig(t)
		out := runCommand(t, NewConfigEditAgentCommand(), "--name", "agent-1", "--namespace", "other-ns")
		assert.Equal(t, "Agent agent-1 updated successfully\n", out)
		runCommand(t, NewConfigEditAgentCommand(), "--name", "agent-1", "--context", "other-ctx")
		cfg := mustReadConfig(t, path)
		assert.Equal(t, componentConfig{KubeContext: "other-ctx", Namespace: "other-ns"}, cfg.Contexts.Agents["agent-1"])
	})
}

func Test_ConfigDeleteCommands(t *testing.T) {
	t.Run("delete principal", func(t *testing.T) {
		path := seedConfig(t)
		out := runCommand(t, NewConfigDeletePrincipalCommand(), "--name", "dev")
		assert.Equal(t, "Principal dev deleted successfully\n", out)
		cfg := mustReadConfig(t, path)
		assert.NotContains(t, cfg.Contexts.Principals, "dev")
		assert.Contains(t, cfg.Contexts.Principals, "prod")
		assert.Contains(t, cfg.Contexts.Agents, "agent-1")
	})
	t.Run("delete agent", func(t *testing.T) {
		path := seedConfig(t)
		out := runCommand(t, NewConfigDeleteAgentCommand(), "--name", "agent-1")
		assert.Equal(t, "Agent agent-1 deleted successfully\n", out)
		cfg := mustReadConfig(t, path)
		assert.Empty(t, cfg.Contexts.Agents)
		assert.Len(t, cfg.Contexts.Principals, 2)
	})
}

func Test_ConfigListCommands(t *testing.T) {
	t.Run("list everything", func(t *testing.T) {
		seedConfig(t)
		out := runCommand(t, NewConfigListCommand())
		assert.Regexp(t, `(?m)^COMPONENT +NAME +CONTEXT +NAMESPACE$`, out)
		assert.Regexp(t, `(?m)^principal +prod +prod-ctx +argocd$`, out)
		assert.Regexp(t, `(?m)^principal +dev +dev-ctx +argocd-dev$`, out)
		assert.Regexp(t, `(?m)^agent +agent-1 +a1-ctx +agent-ns$`, out)
	})
	t.Run("list principals marks the default", func(t *testing.T) {
		seedConfig(t)
		out := runCommand(t, NewConfigListCommand(), "principal")
		assert.Regexp(t, `(?m)^NAME +CONTEXT +NAMESPACE +DEFAULT$`, out)
		assert.Regexp(t, `(?m)^prod +prod-ctx +argocd +X$`, out)
		assert.Regexp(t, `(?m)^dev +dev-ctx +argocd-dev *$`, out)
		assert.NotContains(t, out, "agent-1")
	})
	t.Run("list agents", func(t *testing.T) {
		seedConfig(t)
		out := runCommand(t, NewConfigListCommand(), "agent")
		assert.Regexp(t, `(?m)^NAME +CONTEXT +NAMESPACE$`, out)
		assert.Regexp(t, `(?m)^agent-1 +a1-ctx +agent-ns$`, out)
		assert.NotContains(t, out, "prod-ctx")
	})
	t.Run("list on an empty config only prints the header", func(t *testing.T) {
		path := useConfigPath(t)
		require.NoError(t, os.WriteFile(path, nil, 0o600))
		for _, args := range [][]string{{}, {"principal"}, {"agent"}} {
			out := runCommand(t, NewConfigListCommand(), args...)
			assert.Equal(t, 1, strings.Count(out, "\n"), "args %v: %q", args, out)
		}
	})
}

// maxPrompts bounds a scripted session. ReadFromTerm retries forever on
// invalid input, so a prompt that is not understood would otherwise hang the
// test until the global timeout.
const maxPrompts = 50

// runInteractive runs fn with stdin and stdout replaced by pipes so that the
// prompts written by cmdutil.ReadFromTerm can be answered. answer is called
// with every prompt and returns the line to type in response.
//
// Every call to ReadFromTerm creates its own buffered reader on stdin, so an
// answer may only be written after its prompt has been printed; otherwise the
// first reader would swallow all of them.
//
// It returns everything fn printed and the prompts it asked.
func runInteractive(t *testing.T, answer func(prompt string) string, fn func()) (string, []string) {
	t.Helper()
	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW

	var (
		transcript bytes.Buffer
		prompts    []string
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var line []byte
		chunk := make([]byte, 1)
		for {
			n, err := outR.Read(chunk)
			if n > 0 {
				transcript.Write(chunk[:n])
				line = append(line, chunk[0])
				switch {
				case chunk[0] == '\n':
					line = nil
				case bytes.HasSuffix(line, []byte(": ")):
					prompts = append(prompts, string(line))
					if len(prompts) > maxPrompts {
						panic(fmt.Sprintf("more than %d prompts, last one: %q", maxPrompts, line))
					}
					_, _ = inW.WriteString(answer(string(line)) + "\n")
					line = nil
				}
			}
			if err != nil {
				return
			}
		}
	}()

	func() {
		defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
		fn()
	}()
	require.NoError(t, outW.Close())
	<-done
	_ = inW.Close()
	_ = inR.Close()
	_ = outR.Close()
	return transcript.String(), prompts
}

func Test_CreateLocalConfig(t *testing.T) {
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(kubeconfig, []byte(`apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: https://127.0.0.1:6443
users:
- name: u
  user: {}
contexts:
- name: prod-ctx
  context: {cluster: c, user: u}
- name: prod2-ctx
  context: {cluster: c, user: u}
- name: edge-ctx
  context: {cluster: c, user: u}
- name: other-ctx
  context: {cluster: c, user: u}
current-context: prod-ctx
`), 0o600))

	output := filepath.Join(t.TempDir(), "out", "ctl.conf")
	badNamespaceSent := false
	principalNames := 0
	answer := func(prompt string) string {
		switch {
		case strings.HasPrefix(prompt, "Found context prod-ctx"), strings.HasPrefix(prompt, "Found context prod2-ctx"):
			return "principal"
		case strings.HasPrefix(prompt, "Found context edge-ctx"):
			return "agent"
		case strings.HasPrefix(prompt, "Found context other-ctx"):
			return ""
		case strings.HasPrefix(prompt, "Which namespace is the principal"):
			// the first answer is rejected because it contains a space
			if !badNamespaceSent {
				badNamespaceSent = true
				return "not valid"
			}
			return "  argocd "
		case strings.HasPrefix(prompt, "Which namespace is the agent"):
			return " agent-ns"
		case strings.HasPrefix(prompt, "What name would you like to use for this principal"):
			// the second principal first tries the name of the first one,
			// which is already taken, and then picks another
			principalNames++
			if principalNames == 3 {
				return "second"
			}
			return " main "
		case strings.HasPrefix(prompt, "What name would you like to use for this agent"):
			return "edge "
		case strings.HasPrefix(prompt, "Which of the above principals"):
			return " main"
		}
		t.Errorf("unexpected prompt %q", prompt)
		return ""
	}

	transcript, prompts := runInteractive(t, answer, func() {
		CreateLocalConfig(kubeconfig, output)
	})

	cfg := mustReadConfig(t, output)
	// which of the two contexts gets which name depends on the map order of
	// the kubeconfig
	require.Len(t, cfg.Contexts.Principals, 2)
	assert.Equal(t, "argocd", cfg.Contexts.Principals["main"].Namespace)
	assert.Equal(t, "argocd", cfg.Contexts.Principals["second"].Namespace)
	assert.ElementsMatch(t, []string{"prod-ctx", "prod2-ctx"},
		[]string{cfg.Contexts.Principals["main"].KubeContext, cfg.Contexts.Principals["second"].KubeContext})
	assert.Equal(t, map[string]componentConfig{"edge": {KubeContext: "edge-ctx", Namespace: "agent-ns"}}, cfg.Contexts.Agents)
	assert.Equal(t, "main", cfg.DefaultPrincipal)
	assert.Contains(t, transcript, "> main")
	assert.Contains(t, transcript, "Successfully wrote config to "+output)

	var namespacePrompts int
	for _, p := range prompts {
		if strings.HasPrefix(p, "Which namespace is the principal") {
			namespacePrompts++
		}
	}
	assert.Equal(t, 3, namespacePrompts, "the invalid namespace should have been asked for again")
	assert.Equal(t, 3, principalNames, "the duplicate name should have been asked for again")
}

func Test_CreateLocalConfig_nothingSelected(t *testing.T) {
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(kubeconfig, []byte(`apiVersion: v1
kind: Config
contexts:
- name: only-ctx
  context: {cluster: c, user: u}
`), 0o600))
	output := filepath.Join(t.TempDir(), "ctl.conf")

	_, prompts := runInteractive(t, func(string) string { return "" }, func() {
		CreateLocalConfig(kubeconfig, output)
	})
	assert.Len(t, prompts, 1, "no follow-up questions for a skipped context, and no default to pick")

	cfg := mustReadConfig(t, output)
	assert.Empty(t, cfg.Contexts.Principals)
	assert.Empty(t, cfg.Contexts.Agents)
	assert.Empty(t, cfg.DefaultPrincipal)
}

func Test_getDefaultKubeConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	assert.Equal(t, filepath.Join(home, ".kube", "config"), getDefaultKubeConfig())
}

func Test_getDefaultCfgPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	assert.Equal(t, filepath.Join(home, ".config", "argocd-agent", "ctl.conf"), getDefaultCfgPath())
}

func Test_writeAndReadLocalConfig(t *testing.T) {
	t.Run("round trip creates missing directories with a private file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "dir", "ctl.conf")
		in := &localConfig{
			Contexts: contexts{
				Principals: map[string]componentConfig{"p": {KubeContext: "c", Namespace: "n"}},
				Agents:     map[string]componentConfig{"a": {KubeContext: "c2", Namespace: "n2"}},
			},
			DefaultPrincipal: "p",
		}
		require.NoError(t, writeLocalConfig(in, path))

		st, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())

		out, err := readLocalConfig(path)
		require.NoError(t, err)
		assert.Equal(t, in, out)
	})
	t.Run("writing replaces the previous content", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ctl.conf")
		require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("# stale\n", 100)), 0o600))
		require.NoError(t, writeLocalConfig(&localConfig{DefaultPrincipal: "p"}, path))
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "stale")
		out, err := readLocalConfig(path)
		require.NoError(t, err)
		assert.Equal(t, "p", out.DefaultPrincipal)
	})
	t.Run("empty file yields initialised maps", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.conf")
		require.NoError(t, os.WriteFile(path, nil, 0o600))
		cfg, err := readLocalConfig(path)
		require.NoError(t, err)
		assert.NotNil(t, cfg.Contexts.Principals)
		assert.NotNil(t, cfg.Contexts.Agents)
	})
	t.Run("missing file is reported as not existing", func(t *testing.T) {
		_, err := readLocalConfig(filepath.Join(t.TempDir(), "nope.conf"))
		require.Error(t, err)
		assert.True(t, os.IsNotExist(err))
	})
	t.Run("invalid yaml is an error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.conf")
		require.NoError(t, os.WriteFile(path, []byte("contexts: [unterminated"), 0o600))
		_, err := readLocalConfig(path)
		assert.Error(t, err)
	})
	t.Run("write fails when the parent is a file", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(parent, nil, 0o600))
		assert.Error(t, writeLocalConfig(&localConfig{}, filepath.Join(parent, "ctl.conf")))
	})
}

func Test_loadLocalConfig(t *testing.T) {
	t.Run("missing config falls back to empty configs", func(t *testing.T) {
		useConfigPath(t)
		principal, agent := loadLocalConfig()
		assert.Equal(t, componentConfig{}, principal)
		assert.Equal(t, componentConfig{}, agent)
	})
	t.Run("default principal is selected", func(t *testing.T) {
		seedConfig(t)
		principal, agent := loadLocalConfig()
		assert.Equal(t, componentConfig{KubeContext: "prod-ctx", Namespace: "argocd"}, principal)
		assert.Equal(t, componentConfig{}, agent)
	})
	t.Run("named principal and agent are looked up", func(t *testing.T) {
		seedConfig(t)
		globalOpts.principal = "dev"
		globalOpts.agent = "agent-1"
		principal, agent := loadLocalConfig()
		assert.Equal(t, componentConfig{KubeContext: "dev-ctx", Namespace: "argocd-dev"}, principal)
		assert.Equal(t, componentConfig{KubeContext: "a1-ctx", Namespace: "agent-ns"}, agent)
	})
	t.Run("flags win over the config file", func(t *testing.T) {
		seedConfig(t)
		globalOpts.principal = "dev"
		globalOpts.principalContext = "flag-ctx"
		globalOpts.principalNamespace = "flag-ns"
		principal, _ := loadLocalConfig()
		assert.Equal(t, componentConfig{KubeContext: "flag-ctx", Namespace: "flag-ns"}, principal)
	})
	t.Run("an invalid config is ignored", func(t *testing.T) {
		path := useConfigPath(t)
		// The default principal exists, but its namespace is blank, which
		// makes the whole config invalid. Using it would return the entry.
		require.NoError(t, writeLocalConfig(&localConfig{
			Contexts:         contexts{Principals: map[string]componentConfig{"p": {KubeContext: "c"}}},
			DefaultPrincipal: "p",
		}, path))
		principal, agent := loadLocalConfig()
		assert.Equal(t, componentConfig{}, principal)
		assert.Equal(t, componentConfig{}, agent)
	})
}

func Test_ConfigCommands_failures(t *testing.T) {
	const (
		seeded  = "seeded"
		missing = "missing"
		invalid = "invalid"

		// cmdutil.Fatal exits with 1; an error returned by cobra, such as a
		// missing required flag, makes the helper process exit with 2.
		fatal = "[FATAL]: "
	)
	tests := []struct {
		name     string
		config   string
		args     []string
		wantCode int
		wantErr  string
	}{
		{
			name: "add a principal that already exists", config: seeded,
			args:     []string{"config", "add", "principal", "--name", "prod", "--context", "c", "--namespace", "n"},
			wantCode: 1, wantErr: fatal + "A principal with the name prod already exists",
		},
		{
			name: "add an agent that already exists", config: seeded,
			args:     []string{"config", "add", "agent", "--name", "agent-1", "--context", "c", "--namespace", "n"},
			wantCode: 1, wantErr: fatal + "An agent with the name agent-1 already exists",
		},
		{
			name: "edit a principal that does not exist", config: seeded,
			args:     []string{"config", "edit", "principal", "--name", "nope", "--context", "c"},
			wantCode: 1, wantErr: fatal + "The principal nope does not exist in the config",
		},
		{
			name: "edit an agent that does not exist", config: seeded,
			args:     []string{"config", "edit", "agent", "--name", "nope", "--context", "c"},
			wantCode: 1, wantErr: fatal + "The agent nope does not exist in the config",
		},
		{
			name: "delete a principal that does not exist", config: seeded,
			args:     []string{"config", "delete", "principal", "--name", "nope"},
			wantCode: 1, wantErr: fatal + "The principal nope does not exist in the config",
		},
		{
			name: "delete an agent that does not exist", config: seeded,
			args:     []string{"config", "delete", "agent", "--name", "nope"},
			wantCode: 1, wantErr: fatal + "The agent nope does not exist in the config",
		},
		{
			name: "delete the default principal", config: seeded,
			args:     []string{"config", "delete", "principal", "--name", "prod"},
			wantCode: 1, wantErr: fatal + "Cannot delete the default principal",
		},
		{
			name: "select a principal that is not in the config", config: seeded,
			args:     []string{"--principal", "nope", "config", "list"},
			wantCode: 1, wantErr: fatal + "Principal nope does not exist in config",
		},
		{
			name: "select an agent that is not in the config", config: seeded,
			args:     []string{"--agent", "nope", "config", "list"},
			wantCode: 1, wantErr: fatal + "Agent nope does not exist in config",
		},
		{
			name: "add to a config file that does not exist", config: missing,
			args:     []string{"config", "add", "principal", "--name", "p", "--context", "c", "--namespace", "n"},
			wantCode: 1, wantErr: fatal + "Failed to read config",
		},
		{
			name: "list a config file that does not exist", config: missing,
			args:     []string{"config", "list"},
			wantCode: 1, wantErr: fatal + "Failed to read config",
		},
		{
			name: "any command with an unparsable config file", config: invalid,
			args:     []string{"config", "list"},
			wantCode: 1, wantErr: fatal + "An error occurred while reading config",
		},
		{
			name: "add principal without a name", config: seeded,
			args:     []string{"config", "add", "principal", "--context", "c", "--namespace", "n"},
			wantCode: 2, wantErr: `required flag(s) "name" not set`,
		},
		{
			name: "add principal without a context", config: seeded,
			args:     []string{"config", "add", "principal", "--name", "x", "--namespace", "n"},
			wantCode: 2, wantErr: `required flag(s) "context" not set`,
		},
		{
			name: "add principal without a namespace", config: seeded,
			args:     []string{"config", "add", "principal", "--name", "x", "--context", "c"},
			wantCode: 2, wantErr: `required flag(s) "namespace" not set`,
		},
		{
			name: "add agent without a name", config: seeded,
			args:     []string{"config", "add", "agent", "--context", "c", "--namespace", "n"},
			wantCode: 2, wantErr: `required flag(s) "name" not set`,
		},
		{
			name: "add agent without a context", config: seeded,
			args:     []string{"config", "add", "agent", "--name", "x", "--namespace", "n"},
			wantCode: 2, wantErr: `required flag(s) "context" not set`,
		},
		{
			name: "add agent without a namespace", config: seeded,
			args:     []string{"config", "add", "agent", "--name", "x", "--context", "c"},
			wantCode: 2, wantErr: `required flag(s) "namespace" not set`,
		},
		{
			name: "edit principal without a name", config: seeded,
			args:     []string{"config", "edit", "principal", "--context", "c"},
			wantCode: 2, wantErr: `required flag(s) "name" not set`,
		},
		{
			name: "edit agent without a name", config: seeded,
			args:     []string{"config", "edit", "agent", "--context", "c"},
			wantCode: 2, wantErr: `required flag(s) "name" not set`,
		},
		{
			name: "delete principal without a name", config: seeded,
			args:     []string{"config", "delete", "principal"},
			wantCode: 2, wantErr: `required flag(s) "name" not set`,
		},
		{
			name: "delete agent without a name", config: seeded,
			args:     []string{"config", "delete", "agent"},
			wantCode: 2, wantErr: `required flag(s) "name" not set`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "ctl.conf")
			switch tt.config {
			case seeded:
				writeSeedConfig(t, path)
			case invalid:
				require.NoError(t, os.WriteFile(path, []byte("contexts: [unterminated"), 0o600))
			}
			before, readErr := os.ReadFile(path)

			stdout, stderr, code := runCtl(t, append([]string{"--config", path}, tt.args...)...)

			assert.Equal(t, tt.wantCode, code, "stderr: %s", stderr)
			assert.Contains(t, stderr, tt.wantErr)
			assert.Empty(t, stdout, "a failing command must not report success")
			// a failing command must never touch the config file
			if tt.config == missing {
				assert.NoFileExists(t, path)
				return
			}
			require.NoError(t, readErr)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after))
		})
	}
}
