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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ctlEnvVars are the variables that change the defaults of the global flags.
var ctlEnvVars = []string{
	"ARGOCD_AGENT_PRINCIPAL_CONTEXT",
	"ARGOCD_AGENT_PRINCIPAL_NAMESPACE",
	"ARGOCD_AGENT_AGENT_CONTEXT",
	"ARGOCD_AGENT_AGENT_NAMESPACE",
}

// clearCtlEnv makes sure that the developer's environment cannot influence the
// flag defaults. The original values are restored when the test ends.
func clearCtlEnv(t *testing.T) {
	t.Helper()
	for _, k := range ctlEnvVars {
		t.Setenv(k, "")
		require.NoError(t, os.Unsetenv(k))
	}
}

// useConfigPath resets the global options, points them at a config file in a
// temp dir and restores the previous state when the test ends. The returned
// file does not exist yet.
func useConfigPath(t *testing.T) string {
	t.Helper()
	clearCtlEnv(t)
	// the default config path is derived from the home directory
	t.Setenv("HOME", t.TempDir())
	oldOpts := *globalOpts
	oldPrincipal, oldAgent := principalCfg, agentCfg
	t.Cleanup(func() {
		*globalOpts = oldOpts
		principalCfg, agentCfg = oldPrincipal, oldAgent
	})
	path := filepath.Join(t.TempDir(), "ctl.conf")
	*globalOpts = GlobalFlags{configPath: path}
	principalCfg, agentCfg = componentConfig{}, componentConfig{}
	return path
}

// captureStdout runs fn and returns everything it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	func() {
		defer func() { os.Stdout = old }()
		fn()
	}()
	require.NoError(t, w.Close())
	return <-done
}

// runCommand executes cmd with args and returns what it printed to stdout.
func runCommand(t *testing.T, cmd *cobra.Command, args ...string) string {
	t.Helper()
	if args == nil {
		// cobra falls back to os.Args when it is given nil
		args = []string{}
	}
	cmd.SetArgs(args)
	var err error
	out := captureStdout(t, func() { err = cmd.Execute() })
	require.NoError(t, err)
	return out
}

const ctlHelperEnv = "ARGOCD_AGENTCTL_TEST_HELPER"

// TestHelperProcess is not a real test. It is the entry point of the process
// started by runCtl, which lets the tests observe commands that end in
// cmdutil.Fatal and therefore in os.Exit.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(ctlHelperEnv) != "1" {
		t.Skip("only runs as the helper process of runCtl")
	}
	var args []string
	for i, a := range os.Args {
		if a == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	root := NewRootCommand()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

// runCtl runs argocd-agentctl with args in a separate process with a minimal
// environment and returns what it printed and its exit code.
func runCtl(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, append([]string{"-test.run=^TestHelperProcess$", "--"}, args...)...)
	cmd.Env = []string{ctlHelperEnv + "=1", "HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	err = cmd.Run()
	if err == nil {
		return outBuf.String(), errBuf.String(), 0
	}
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "stderr: %s", errBuf.String())
	return outBuf.String(), errBuf.String(), exitErr.ExitCode()
}

func Test_NewRootCommand(t *testing.T) {
	t.Run("version does not read the local config", func(t *testing.T) {
		// Any other command dies on this file, so version only works if it
		// never loads the config.
		path := filepath.Join(t.TempDir(), "ctl.conf")
		require.NoError(t, os.WriteFile(path, []byte("contexts: [unterminated"), 0o600))
		stdout, stderr, code := runCtl(t, "--config", path, "version")
		assert.Equal(t, 0, code, "stderr: %s", stderr)
		var v map[string]any
		require.NoError(t, json.Unmarshal([]byte(stdout), &v))
		assert.Equal(t, "argocd-agent", v["name"])

		_, stderr, code = runCtl(t, "--config", path, "config", "list")
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr, "An error occurred while reading config")
	})
	t.Run("other commands resolve the config from the --config flag", func(t *testing.T) {
		path := seedConfig(t)
		out := runCommand(t, NewRootCommand(), "--config", path, "config", "list", "principal")
		assert.Regexp(t, `(?m)^prod +prod-ctx +argocd +X$`, out)
		assert.Equal(t, componentConfig{KubeContext: "prod-ctx", Namespace: "argocd"}, principalCfg)
		assert.Equal(t, componentConfig{}, agentCfg)
	})
	t.Run("the principal and agent flags select entries of the config", func(t *testing.T) {
		path := seedConfig(t)
		runCommand(t, NewRootCommand(), "--config", path, "--principal", "dev", "--agent", "agent-1", "config", "list")
		assert.Equal(t, componentConfig{KubeContext: "dev-ctx", Namespace: "argocd-dev"}, principalCfg)
		assert.Equal(t, componentConfig{KubeContext: "a1-ctx", Namespace: "agent-ns"}, agentCfg)
	})
}

func Test_NewVersionCommand(t *testing.T) {
	compact := runCommand(t, NewVersionCommand())
	indented := runCommand(t, NewVersionCommand(), "-i")

	var fromCompact, fromIndented map[string]any
	require.NoError(t, json.Unmarshal([]byte(compact), &fromCompact))
	require.NoError(t, json.Unmarshal([]byte(indented), &fromIndented))
	assert.Equal(t, "argocd-agent", fromCompact["name"])
	assert.NotEmpty(t, fromCompact["version"])
	assert.Equal(t, fromCompact, fromIndented)

	assert.NotContains(t, strings.TrimSpace(compact), "\n", "compact output is a single line")
	assert.Contains(t, indented, "\n  \"name\"", "indented output uses two spaces")
}

func Test_addGlobalFlags(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		useConfigPath(t)
		opts := &GlobalFlags{}
		cmd := &cobra.Command{Use: "test"}
		addGlobalFlags(cmd, opts)
		require.NoError(t, cmd.ParseFlags(nil))

		assert.Empty(t, opts.principalContext)
		assert.Equal(t, "argocd", opts.principalNamespace)
		assert.Empty(t, opts.agentContext)
		assert.Equal(t, "argocd", opts.agentNamespace)
		assert.Empty(t, opts.principal)
		assert.Empty(t, opts.agent)
		assert.Equal(t, getDefaultCfgPath(), opts.configPath)
	})
	t.Run("environment variables provide the defaults and flags win", func(t *testing.T) {
		useConfigPath(t)
		t.Setenv("ARGOCD_AGENT_PRINCIPAL_CONTEXT", "env-principal")
		t.Setenv("ARGOCD_AGENT_PRINCIPAL_NAMESPACE", "env-principal-ns")
		t.Setenv("ARGOCD_AGENT_AGENT_CONTEXT", "env-agent")
		t.Setenv("ARGOCD_AGENT_AGENT_NAMESPACE", "env-agent-ns")
		opts := &GlobalFlags{}
		cmd := &cobra.Command{Use: "test"}
		addGlobalFlags(cmd, opts)
		require.NoError(t, cmd.ParseFlags([]string{"--agent-context", "flag-agent"}))

		assert.Equal(t, "env-principal", opts.principalContext)
		assert.Equal(t, "env-principal-ns", opts.principalNamespace)
		assert.Equal(t, "flag-agent", opts.agentContext)
		assert.Equal(t, "env-agent-ns", opts.agentNamespace)
	})
}
