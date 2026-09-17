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

// Package settings applies runtime-switchable settings (log level,
// full-detail logging and pprof access) from the argocd-agent-params ConfigMap.
//
// Principal and agent share that ConfigMap and are told apart by a key prefix.
//
// Flags provide the default configuration. A key present in the ConfigMap overrides the
// default configuration; an absent key (or a deleted ConfigMap) reverts to the default configuration.
// Invalid values are logged and ignored, never fatal.
package settings

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

// DefaultConfigMapName is the ConfigMap the components already source their
// startup parameters from. Installations that rename it (the Helm charts derive
// the name from the release) must pass the actual name to StartWatcher.
const DefaultConfigMapName = "argocd-agent-params"

// Key prefixes distinguishing the two components inside the shared ConfigMap.
const (
	PrincipalKeyPrefix = "principal."
	AgentKeyPrefix     = "agent."
)

// Keys recognized in the settings ConfigMap, without the component prefix.
const (
	// KeyLogLevel uses the same format as --log-level: a comma-separated list of
	// "level" or "subsystem=level".
	KeyLogLevel = "log.level"
	// KeyFullDetail is a comma-separated list of full-detail categories.
	KeyFullDetail = "log.full-detail"
	// KeyPprofEnabled ("true"/"false") gates access to the pprof endpoint.
	KeyPprofEnabled = "pprof.enabled"
)

// DefaultConfig holds the flag-derived defaults used when a key is absent.
type DefaultConfig struct {
	LogLevels    []string
	FullDetail   []string
	PprofEnabled bool
}

// Manager applies debug settings to the running process. Safe for concurrent use.
type Manager struct {
	mu            sync.Mutex
	keyPrefix     string
	defaultConfig DefaultConfig
	subLoggers    *cmdutil.SubSystemLoggers

	pprofEnabled atomic.Bool

	logger *logrus.Entry
}

// NewManager returns a Manager reading the keys prefixed with keyPrefix, which
// must be either PrincipalKeyPrefix or AgentKeyPrefix.
func NewManager(keyPrefix string, dc DefaultConfig, subLoggers *cmdutil.SubSystemLoggers) *Manager {
	m := &Manager{
		keyPrefix:     keyPrefix,
		defaultConfig: dc,
		subLoggers:    subLoggers,
		logger:        logrus.WithField("module", "Settings"),
	}
	m.pprofEnabled.Store(dc.PprofEnabled)
	return m
}

// key returns the component-prefixed ConfigMap key for the given base key.
func (m *Manager) key(base string) string {
	return m.keyPrefix + base
}

// PprofEnabled reports whether the pprof endpoint should serve requests.
func (m *Manager) PprofEnabled() bool {
	return m.pprofEnabled.Load()
}

// Apply reconciles debug settings with the given ConfigMap data. A nil map
// reverts every setting to its baseline.
func (m *Manager) Apply(data map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.applyLogLevel(data)
	m.applyFullDetail(data)
	m.applyPprof(data)
}

func (m *Manager) applyLogLevel(data map[string]string) {
	// Re-establish the default configuration first so a removed key or a subsystem-only
	// override cannot leak levels from a previous revision.
	if err := cmdutil.ParseAndApplyLogLevels(m.defaultConfig.LogLevels, m.subLoggers); err != nil {
		m.logger.WithError(err).Warnf("could not apply default log level")
	}

	key := m.key(KeyLogLevel)
	levels := splitList(data[key])
	if len(levels) == 0 {
		return
	}

	// ParseAndApplyLogLevels validates before mutating, so a failure leaves the
	// default configuration in effect.
	if err := cmdutil.ParseAndApplyLogLevels(levels, m.subLoggers); err != nil {
		m.logger.WithError(err).Warnf("Ignoring invalid %q value from the params ConfigMap", key)
		return
	}

	m.logOverride(key, strings.Join(m.defaultConfig.LogLevels, ","), strings.Join(levels, ","))
}

func (m *Manager) applyFullDetail(data map[string]string) {
	key := m.key(KeyFullDetail)
	categories := m.defaultConfig.FullDetail
	// A present key is authoritative, including when it is empty: that is the
	// only way to switch full detail off again without a restart when it was
	// enabled by a flag at startup.
	if val, ok := data[key]; ok {
		categories = splitList(val)
		m.logOverride(key, strings.Join(m.defaultConfig.FullDetail, ","), strings.Join(categories, ","))
	}
	cmdutil.ParseFullDetail(categories)
}

func (m *Manager) applyPprof(data map[string]string) {
	key := m.key(KeyPprofEnabled)
	enabled := m.defaultConfig.PprofEnabled
	if val, ok := data[key]; ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(val)); err != nil {
			m.logger.WithError(err).Warnf("Ignoring invalid %q value from the params ConfigMap", key)
		} else {
			enabled = b
			m.logOverride(key, strconv.FormatBool(m.defaultConfig.PprofEnabled), strconv.FormatBool(b))
		}
	}
	m.pprofEnabled.Store(enabled)
}

// logOverride reports a ConfigMap value that takes precedence over the value the
// component was started with, so the deviation is visible in the logs rather
// than only in the ConfigMap.
func (m *Manager) logOverride(key, startup, applied string) {
	if startup == applied {
		return
	}
	m.logger.Infof("Applying %s=%q from the params ConfigMap, overriding the startup value %q", key, applied, startup)
}

// splitList splits a comma-separated value, trimming whitespace and dropping
// empty entries.
func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// StartWatcher watches the named ConfigMap in the given namespace and
// applies its contents to the settings manager on change. When the ConfigMap is
// absent or deleted, the manager reverts to the flag-derived default configuration. Errors
// are logged and the process continues with the default configuration: runtime settings
// reconfiguration is best-effort and must never block startup.
func (m *Manager) StartWatcher(ctx context.Context, kubeClient *kube.KubernetesClient, namespace, cmName string) {
	if namespace == "" {
		logrus.Errorf("Cannot watch settings ConfigMap %q: namespace is empty", cmName)
		return
	}

	fieldSelector := fmt.Sprintf("metadata.name=%s", cmName)

	opts := []informer.InformerOption[*corev1.ConfigMap]{
		informer.WithListHandler[*corev1.ConfigMap](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
			opts.FieldSelector = fieldSelector
			return kubeClient.Clientset.CoreV1().ConfigMaps(namespace).List(ctx, opts)
		}),
		informer.WithWatchHandler[*corev1.ConfigMap](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
			opts.FieldSelector = fieldSelector
			return kubeClient.Clientset.CoreV1().ConfigMaps(namespace).Watch(ctx, opts)
		}),
		informer.WithAddHandler[*corev1.ConfigMap](func(cm *corev1.ConfigMap) { m.Apply(cm.Data) }),
		informer.WithUpdateHandler[*corev1.ConfigMap](func(_, cm *corev1.ConfigMap) { m.Apply(cm.Data) }),
		informer.WithDeleteHandler[*corev1.ConfigMap](func(_ *corev1.ConfigMap) { m.Apply(nil) }),
		informer.WithGroupResource[*corev1.ConfigMap]("", "configmaps"),
	}

	settingsInformer, err := informer.NewInformer(ctx, opts...)
	if err != nil {
		logrus.WithError(err).Errorf("Could not create settings ConfigMap informer for %q", cmName)
		return
	}

	go func() {
		logrus.Infof("Watching settings ConfigMap %q in namespace %q for runtime overrides", cmName, namespace)
		if err := settingsInformer.Start(ctx); err != nil {
			logrus.WithError(err).Errorf("Settings ConfigMap informer stopped")
		}
	}()
}
