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

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/pkg/client"
	fakekube "github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_WithSourceUIDMismatchPolicy(t *testing.T) {
	newTestAgent := func(t *testing.T, policy string) (*Agent, error) {
		t.Helper()
		kubec := fakekube.NewKubernetesFakeClientWithApps("argocd")
		remote, err := client.NewRemote("127.0.0.1", 8080)
		require.NoError(t, err)
		return NewAgent(context.TODO(), kubec, "argocd",
			WithRemote(remote),
			WithCacheRefreshInterval(10*time.Second),
			WithInformerSyncTimeout(10*time.Second),
			WithSourceUIDMismatchPolicy(policy),
		)
	}

	t.Run("recreate is accepted", func(t *testing.T) {
		a, err := newTestAgent(t, "recreate")
		require.NoError(t, err)
		assert.Equal(t, manager.MismatchPolicyRecreate, a.mismatchPolicy)
	})

	t.Run("upsert is accepted", func(t *testing.T) {
		a, err := newTestAgent(t, "upsert")
		require.NoError(t, err)
		assert.Equal(t, manager.MismatchPolicyUpsert, a.mismatchPolicy)
	})

	t.Run("unknown value returns error", func(t *testing.T) {
		_, err := newTestAgent(t, "block")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown source-uid-mismatch-policy")
	})

	t.Run("default is recreate when option not set", func(t *testing.T) {
		kubec := fakekube.NewKubernetesFakeClientWithApps("argocd")
		remote, _ := client.NewRemote("127.0.0.1", 8080)
		a, err := NewAgent(context.TODO(), kubec, "argocd",
			WithRemote(remote),
			WithCacheRefreshInterval(10*time.Second),
			WithInformerSyncTimeout(10*time.Second),
		)
		require.NoError(t, err)
		assert.Equal(t, manager.MismatchPolicyRecreate, a.mismatchPolicy)
	})
}

func Test_effectiveMismatchPolicy(t *testing.T) {
	makeAgent := func(t *testing.T, globalPolicy manager.SourceUIDMismatchPolicy) *Agent {
		t.Helper()
		kubec := fakekube.NewKubernetesFakeClientWithApps("argocd")
		remote, _ := client.NewRemote("127.0.0.1", 8080)
		a, err := NewAgent(context.TODO(), kubec, "argocd", WithRemote(remote), WithCacheRefreshInterval(10*time.Second), WithInformerSyncTimeout(10*time.Second))
		require.NoError(t, err)
		a.mismatchPolicy = globalPolicy
		return a
	}

	t.Run("no annotation uses global policy", func(t *testing.T) {
		a := makeAgent(t, manager.MismatchPolicyRecreate)
		obj := &metav1.ObjectMeta{}
		assert.Equal(t, manager.MismatchPolicyRecreate, a.effectiveMismatchPolicy(obj))
	})

	t.Run("valid annotation overrides global policy", func(t *testing.T) {
		a := makeAgent(t, manager.MismatchPolicyRecreate)
		obj := &metav1.ObjectMeta{
			Annotations: map[string]string{
				manager.MismatchPolicyAnnotation: "upsert",
			},
		}
		assert.Equal(t, manager.MismatchPolicyUpsert, a.effectiveMismatchPolicy(obj))
	})

	t.Run("unknown annotation value falls back to global policy", func(t *testing.T) {
		a := makeAgent(t, manager.MismatchPolicyUpsert)
		obj := &metav1.ObjectMeta{
			Annotations: map[string]string{
				manager.MismatchPolicyAnnotation: "block",
			},
		}
		assert.Equal(t, manager.MismatchPolicyUpsert, a.effectiveMismatchPolicy(obj))
	})
}

func Test_WithRecreateAction(t *testing.T) {
	newTestAgent := func(t *testing.T, action string) (*Agent, error) {
		t.Helper()
		kubec := fakekube.NewKubernetesFakeClientWithApps("argocd")
		remote, err := client.NewRemote("127.0.0.1", 8080)
		require.NoError(t, err)
		return NewAgent(context.TODO(), kubec, "argocd",
			WithRemote(remote),
			WithCacheRefreshInterval(10*time.Second),
			WithInformerSyncTimeout(10*time.Second),
			WithRecreateAction(action),
		)
	}

	t.Run("ignore is accepted", func(t *testing.T) {
		a, err := newTestAgent(t, "ignore")
		require.NoError(t, err)
		assert.Equal(t, manager.RecreateActionIgnore, a.recreateAction)
	})

	t.Run("clear-status is accepted", func(t *testing.T) {
		a, err := newTestAgent(t, "clear-status")
		require.NoError(t, err)
		assert.Equal(t, manager.RecreateActionClearStatus, a.recreateAction)
	})

	t.Run("resync is accepted", func(t *testing.T) {
		a, err := newTestAgent(t, "resync")
		require.NoError(t, err)
		assert.Equal(t, manager.RecreateActionResync, a.recreateAction)
	})

	t.Run("unknown value returns error", func(t *testing.T) {
		_, err := newTestAgent(t, "block")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown on-application-recreate")
	})

	t.Run("default is ignore when option not set", func(t *testing.T) {
		kubec := fakekube.NewKubernetesFakeClientWithApps("argocd")
		remote, _ := client.NewRemote("127.0.0.1", 8080)
		a, err := NewAgent(context.TODO(), kubec, "argocd",
			WithRemote(remote),
			WithCacheRefreshInterval(10*time.Second),
			WithInformerSyncTimeout(10*time.Second),
		)
		require.NoError(t, err)
		assert.Equal(t, manager.RecreateActionIgnore, a.recreateAction)
	})
}

func Test_WithInformerSyncTimeout(t *testing.T) {
	newTestAgent := func(t *testing.T, opts ...AgentOption) (*Agent, error) {
		t.Helper()
		kubec := fakekube.NewKubernetesFakeClientWithApps("argocd")
		remote, err := client.NewRemote("127.0.0.1", 8080)
		require.NoError(t, err)
		baseOpts := []AgentOption{WithRemote(remote), WithCacheRefreshInterval(10 * time.Second)}
		return NewAgent(context.TODO(), kubec, "argocd", append(baseOpts, opts...)...)
	}

	t.Run("positive value is accepted", func(t *testing.T) {
		a, err := newTestAgent(t, WithInformerSyncTimeout(30*time.Second))
		require.NoError(t, err)
		assert.Equal(t, 30*time.Second, a.informerSyncTimeout)
	})

	t.Run("zero value returns error", func(t *testing.T) {
		_, err := newTestAgent(t, WithInformerSyncTimeout(0))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "informer sync timeout must be greater than 0")
	})

	t.Run("negative value returns error", func(t *testing.T) {
		_, err := newTestAgent(t, WithInformerSyncTimeout(-1*time.Second))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "informer sync timeout must be greater than 0")
	})

	t.Run("missing option returns error", func(t *testing.T) {
		_, err := newTestAgent(t)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "informer sync timeout must be greater than 0")
	})
}

func Test_WithResyncInterval(t *testing.T) {
	newTestAgent := func(t *testing.T, opts ...AgentOption) (*Agent, error) {
		t.Helper()
		kubec := fakekube.NewKubernetesFakeClientWithApps("argocd")
		remote, err := client.NewRemote("127.0.0.1", 8080)
		require.NoError(t, err)
		baseOpts := []AgentOption{
			WithRemote(remote),
			WithCacheRefreshInterval(10 * time.Second),
			WithInformerSyncTimeout(10 * time.Second),
		}
		return NewAgent(context.TODO(), kubec, "argocd", append(baseOpts, opts...)...)
	}

	t.Run("positive interval is accepted", func(t *testing.T) {
		a, err := newTestAgent(t, WithResyncInterval(5*time.Minute))
		require.NoError(t, err)
		assert.Equal(t, 5*time.Minute, a.resyncInterval)
	})

	t.Run("zero interval disables periodic resync", func(t *testing.T) {
		a, err := newTestAgent(t, WithResyncInterval(0))
		require.NoError(t, err)
		assert.Zero(t, a.resyncInterval)
	})

	t.Run("negative interval returns error", func(t *testing.T) {
		_, err := newTestAgent(t, WithResyncInterval(-time.Second))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "resync interval must be non-negative")
	})
}
