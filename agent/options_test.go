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
		a, err := NewAgent(context.TODO(), kubec, "argocd", WithRemote(remote), WithCacheRefreshInterval(10*time.Second))
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
