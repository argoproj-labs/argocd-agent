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

package settings

import (
	"context"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgofeatures "k8s.io/client-go/features"
	clientgofeaturetesting "k8s.io/client-go/features/testing"
	"k8s.io/client-go/kubernetes/fake"
)

// newTestSubLoggers returns a fresh set of subsystem loggers for tests.
func newTestSubLoggers() *cmdutil.SubSystemLoggers {
	return &cmdutil.SubSystemLoggers{
		ResourceProxyLogger:       logrus.New(),
		RedisProxyLogger:          logrus.New(),
		GrpcEventLogger:           logrus.New(),
		InformerEventBufferLogger: logrus.New(),
	}
}

// pKey returns the principal-prefixed form of a ConfigMap key.
func pKey(base string) string { return PrincipalKeyPrefix + base }

func defaultDefaultConfig() DefaultConfig {
	return DefaultConfig{
		LogLevels:    []string{"info"},
		FullDetail:   nil,
		PprofEnabled: false,
	}
}

func TestManager_PprofEnabled(t *testing.T) {
	t.Run("baseline disabled by default", func(t *testing.T) {
		m := NewManager(PrincipalKeyPrefix, defaultDefaultConfig(), newTestSubLoggers())
		assert.False(t, m.PprofEnabled())
	})

	t.Run("baseline enabled", func(t *testing.T) {
		b := defaultDefaultConfig()
		b.PprofEnabled = true
		m := NewManager(PrincipalKeyPrefix, b, newTestSubLoggers())
		assert.True(t, m.PprofEnabled())
	})

	t.Run("configmap enables and disables", func(t *testing.T) {
		m := NewManager(PrincipalKeyPrefix, defaultDefaultConfig(), newTestSubLoggers())

		m.Apply(map[string]string{pKey(KeyPprofEnabled): "true"})
		assert.True(t, m.PprofEnabled())

		m.Apply(map[string]string{pKey(KeyPprofEnabled): "false"})
		assert.False(t, m.PprofEnabled())
	})

	t.Run("absent key reverts to baseline", func(t *testing.T) {
		b := defaultDefaultConfig()
		b.PprofEnabled = true
		m := NewManager(PrincipalKeyPrefix, b, newTestSubLoggers())

		m.Apply(map[string]string{pKey(KeyPprofEnabled): "false"})
		assert.False(t, m.PprofEnabled())

		// A ConfigMap that no longer carries the key must fall back to baseline.
		m.Apply(map[string]string{})
		assert.True(t, m.PprofEnabled())
	})

	t.Run("invalid value is ignored, keeps baseline", func(t *testing.T) {
		b := defaultDefaultConfig()
		b.PprofEnabled = true
		m := NewManager(PrincipalKeyPrefix, b, newTestSubLoggers())

		m.Apply(map[string]string{pKey(KeyPprofEnabled): "not-a-bool"})
		assert.True(t, m.PprofEnabled())
	})

}

func TestManager_LogLevel(t *testing.T) {
	t.Run("override sets global and subsystem levels", func(t *testing.T) {
		ss := newTestSubLoggers()
		m := NewManager(PrincipalKeyPrefix, defaultDefaultConfig(), ss)

		m.Apply(map[string]string{pKey(KeyLogLevel): "debug"})
		assert.Equal(t, logrus.DebugLevel, logrus.GetLevel())
		assert.Equal(t, logrus.DebugLevel, ss.ResourceProxyLogger.GetLevel())
		assert.Equal(t, logrus.DebugLevel, ss.RedisProxyLogger.GetLevel())
	})

	t.Run("per-subsystem override", func(t *testing.T) {
		ss := newTestSubLoggers()
		m := NewManager(PrincipalKeyPrefix, defaultDefaultConfig(), ss)

		m.Apply(map[string]string{pKey(KeyLogLevel): "warning,resource-proxy=trace"})
		assert.Equal(t, logrus.WarnLevel, logrus.GetLevel())
		assert.Equal(t, logrus.TraceLevel, ss.ResourceProxyLogger.GetLevel())
		assert.Equal(t, logrus.WarnLevel, ss.RedisProxyLogger.GetLevel())
	})

	t.Run("removing override reverts to baseline", func(t *testing.T) {
		ss := newTestSubLoggers()
		b := defaultDefaultConfig()
		b.LogLevels = []string{"warning"}
		m := NewManager(PrincipalKeyPrefix, b, ss)

		m.Apply(map[string]string{pKey(KeyLogLevel): "trace"})
		assert.Equal(t, logrus.TraceLevel, logrus.GetLevel())

		// Absent key -> baseline.
		m.Apply(map[string]string{})
		assert.Equal(t, logrus.WarnLevel, logrus.GetLevel())
		assert.Equal(t, logrus.WarnLevel, ss.ResourceProxyLogger.GetLevel())
	})

	t.Run("subsystem override does not leak after removal", func(t *testing.T) {
		ss := newTestSubLoggers()
		b := defaultDefaultConfig()
		b.LogLevels = []string{"info"}
		m := NewManager(PrincipalKeyPrefix, b, ss)

		// Turn a single subsystem to trace via the ConfigMap.
		m.Apply(map[string]string{pKey(KeyLogLevel): "info,grpc-event=trace"})
		assert.Equal(t, logrus.TraceLevel, ss.GrpcEventLogger.GetLevel())

		// Remove the override entirely; the subsystem must return to baseline
		// (info), not stay at trace.
		m.Apply(map[string]string{})
		assert.Equal(t, logrus.InfoLevel, ss.GrpcEventLogger.GetLevel())
	})

	t.Run("invalid level ignored, keeps baseline", func(t *testing.T) {
		ss := newTestSubLoggers()
		b := defaultDefaultConfig()
		b.LogLevels = []string{"warning"}
		m := NewManager(PrincipalKeyPrefix, b, ss)

		m.Apply(map[string]string{pKey(KeyLogLevel): "not-a-level"})
		// Baseline stays in effect; the invalid override is dropped whole.
		assert.Equal(t, logrus.WarnLevel, logrus.GetLevel())
	})
}

func TestManager_FullDetail(t *testing.T) {
	t.Run("override enables categories", func(t *testing.T) {
		m := NewManager(PrincipalKeyPrefix, defaultDefaultConfig(), newTestSubLoggers())

		m.Apply(map[string]string{pKey(KeyFullDetail): "actions,events"})
		cfg := logging.GetFullDetailConfig()
		assert.True(t, cfg.Actions)
		assert.True(t, cfg.Events)
		assert.False(t, cfg.Informers)
	})

	t.Run("all enables everything", func(t *testing.T) {
		m := NewManager(PrincipalKeyPrefix, defaultDefaultConfig(), newTestSubLoggers())

		m.Apply(map[string]string{pKey(KeyFullDetail): "all"})
		cfg := logging.GetFullDetailConfig()
		assert.True(t, cfg.Actions)
		assert.True(t, cfg.Events)
		assert.True(t, cfg.Informers)
	})

	t.Run("removing override reverts to default", func(t *testing.T) {
		b := defaultDefaultConfig()
		b.FullDetail = []string{"informers"}
		m := NewManager(PrincipalKeyPrefix, b, newTestSubLoggers())

		m.Apply(map[string]string{pKey(KeyFullDetail): "all"})
		require.True(t, logging.GetFullDetailConfig().Actions)

		m.Apply(map[string]string{})
		cfg := logging.GetFullDetailConfig()
		assert.False(t, cfg.Actions)
		assert.False(t, cfg.Events)
		assert.True(t, cfg.Informers)
	})

	// An empty value is an override in its own right: without it, full detail
	// enabled by --full-detail at startup could never be switched off again
	// without restarting the component.
	t.Run("empty value switches full detail off", func(t *testing.T) {
		b := defaultDefaultConfig()
		b.FullDetail = []string{"actions"}
		m := NewManager(PrincipalKeyPrefix, b, newTestSubLoggers())

		m.Apply(nil)
		require.True(t, logging.GetFullDetailConfig().Actions)

		m.Apply(map[string]string{pKey(KeyFullDetail): ""})
		cfg := logging.GetFullDetailConfig()
		assert.False(t, cfg.Actions)
		assert.False(t, cfg.Events)
		assert.False(t, cfg.Informers)

		// Removing the key again reverts to the startup value.
		m.Apply(map[string]string{})
		assert.True(t, logging.GetFullDetailConfig().Actions)
	})
}

func TestManager_ApplyNilData(t *testing.T) {
	// Apply(nil) must not panic and must revert to baseline.
	ss := newTestSubLoggers()
	b := defaultDefaultConfig()
	b.LogLevels = []string{"error"}
	m := NewManager(PrincipalKeyPrefix, b, ss)

	assert.NotPanics(t, func() { m.Apply(nil) })
	assert.Equal(t, logrus.ErrorLevel, logrus.GetLevel())
}

func TestManager_NilSubLoggers(t *testing.T) {
	// A nil subsystem-logger set must be tolerated (only the standard logger is
	// affected).
	m := NewManager(PrincipalKeyPrefix, defaultDefaultConfig(), nil)
	assert.NotPanics(t, func() {
		m.Apply(map[string]string{pKey(KeyLogLevel): "debug"})
	})
	assert.Equal(t, logrus.DebugLevel, logrus.GetLevel())
}

// The principal and the agent share one ConfigMap, so each must act only on its
// own prefix. Without this, an agent-only log level would also reconfigure a
// principal running in the same namespace.
func TestManager_KeyPrefixIsolation(t *testing.T) {
	ss := newTestSubLoggers()
	b := defaultDefaultConfig()
	b.LogLevels = []string{"warning"}
	m := NewManager(PrincipalKeyPrefix, b, ss)

	m.Apply(map[string]string{
		AgentKeyPrefix + KeyLogLevel:     "trace",
		AgentKeyPrefix + KeyPprofEnabled: "true",
	})
	assert.Equal(t, logrus.WarnLevel, logrus.GetLevel())
	assert.False(t, m.PprofEnabled())

	// The unprefixed key belongs to neither component and must be ignored too.
	m.Apply(map[string]string{KeyLogLevel: "trace"})
	assert.Equal(t, logrus.WarnLevel, logrus.GetLevel())

	// The agent manager reads the very same data and does react.
	am := NewManager(AgentKeyPrefix, b, ss)
	am.Apply(map[string]string{AgentKeyPrefix + KeyLogLevel: "trace"})
	assert.Equal(t, logrus.TraceLevel, logrus.GetLevel())
}

func TestSplitList(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, splitList("a,b,c"))
	assert.Equal(t, []string{"a", "b"}, splitList(" a , b "))
	assert.Equal(t, []string{"a"}, splitList("a,,"))
	assert.Empty(t, splitList(""))
	assert.Empty(t, splitList("  ,  "))
}

func TestManager_StartWatcher(t *testing.T) {
	const namespace = "argocd"

	// The reflector otherwise asks for a streaming list, which the fake
	// clientset's watch does not implement, so no event is ever delivered.
	clientgofeaturetesting.SetFeatureDuringTest(t, clientgofeatures.WatchListClient, false)

	client := fake.NewSimpleClientset()
	kubeClient := &kube.KubernetesClient{Clientset: client}

	b := defaultDefaultConfig()
	b.LogLevels = []string{"warning"}
	m := NewManager(PrincipalKeyPrefix, b, newTestSubLoggers())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The ConfigMap exists before the watcher starts, as it does on a restart:
	// the informer's initial list must be applied just like a later change.
	cm := &corev1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{Name: DefaultConfigMapName, Namespace: namespace},
		Data: map[string]string{
			pKey(KeyLogLevel):     "debug",
			pKey(KeyPprofEnabled): "true",
		},
	}
	_, err := client.CoreV1().ConfigMaps(namespace).Create(ctx, cm, v1.CreateOptions{})
	require.NoError(t, err)

	m.StartWatcher(ctx, kubeClient, namespace, DefaultConfigMapName)
	assert.Eventually(t, func() bool {
		return logrus.GetLevel() == logrus.DebugLevel && m.PprofEnabled()
	}, 5*time.Second, 10*time.Millisecond, "existing ConfigMap was not applied")

	cm.Data[pKey(KeyLogLevel)] = "trace"
	delete(cm.Data, pKey(KeyPprofEnabled))
	// Re-issue the update until it is observed: the watch is established some
	// time after the initial list, and an event sent before that is lost.
	assert.Eventually(t, func() bool {
		_, err := client.CoreV1().ConfigMaps(namespace).Update(ctx, cm, v1.UpdateOptions{})
		require.NoError(t, err)
		return logrus.GetLevel() == logrus.TraceLevel && !m.PprofEnabled()
	}, 5*time.Second, 100*time.Millisecond, "ConfigMap update was not applied")

	err = client.CoreV1().ConfigMaps(namespace).Delete(ctx, DefaultConfigMapName, v1.DeleteOptions{})
	require.NoError(t, err)
	assert.Eventually(t, func() bool {
		return logrus.GetLevel() == logrus.WarnLevel
	}, 5*time.Second, 10*time.Millisecond, "ConfigMap deletion did not revert to the default configuration")
}
