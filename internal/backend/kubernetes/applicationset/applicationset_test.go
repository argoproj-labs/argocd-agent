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

package applicationset

import (
	"context"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	fakeappclient "github.com/argoproj/argo-cd/v3/pkg/client/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stesting "k8s.io/client-go/testing"
)

func Test_NewKubernetesBackend(t *testing.T) {
	t.Run("New with label selector option", func(t *testing.T) {
		k := NewKubernetesBackend(nil, "argocd", nil, WithLabelSelector("env=prod"))
		assert.NotNil(t, k)
		assert.Equal(t, "env=prod", k.labelSelector)
	})
	t.Run("New with empty label selector option", func(t *testing.T) {
		k := NewKubernetesBackend(nil, "argocd", nil, WithLabelSelector(""))
		assert.NotNil(t, k)
		assert.Empty(t, k.labelSelector)
	})
	t.Run("New without options", func(t *testing.T) {
		k := NewKubernetesBackend(nil, "argocd", nil)
		assert.NotNil(t, k)
		assert.Empty(t, k.labelSelector)
	})
}

func Test_List(t *testing.T) {
	t.Run("List uses default label selector when no custom selector", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset()
		be := NewKubernetesBackend(fakeAppC, "argocd", nil)
		_, err := be.List(context.TODO(), backend.ApplicationSetSelector{})
		require.NoError(t, err)
		actions := fakeAppC.Actions()
		require.NotEmpty(t, actions)
		listAction, ok := actions[0].(k8stesting.ListAction)
		require.True(t, ok)
		restrictions := listAction.GetListRestrictions()
		expected := config.LabelSelector("")
		assert.Equal(t, expected.LabelSelector, restrictions.Labels.String())
	})
	t.Run("List uses custom label selector", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset()
		be := NewKubernetesBackend(fakeAppC, "argocd", nil, WithLabelSelector("env=prod"))
		_, err := be.List(context.TODO(), backend.ApplicationSetSelector{})
		require.NoError(t, err)
		actions := fakeAppC.Actions()
		require.NotEmpty(t, actions)
		listAction, ok := actions[0].(k8stesting.ListAction)
		require.True(t, ok)
		restrictions := listAction.GetListRestrictions()
		expected := config.LabelSelector("env=prod")
		assert.Equal(t, expected.LabelSelector, restrictions.Labels.String())
	})
}
