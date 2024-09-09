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

package appproject

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	fakeclient "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wI2L/jsondiff"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func Test_NewKubernetes(t *testing.T) {
	t.Run("New with patch capability", func(t *testing.T) {
		k := NewKubernetesBackend(nil, "", nil, true)
		assert.NotNil(t, k)
		assert.True(t, k.SupportsPatch())
	})
	t.Run("New without patch capability", func(t *testing.T) {
		k := NewKubernetesBackend(nil, "", nil, false)
		assert.NotNil(t, k)
		assert.False(t, k.SupportsPatch())
	})

}

func mkAppProjects() []runtime.Object {
	appProjects := make([]runtime.Object, 10)
	for i := 0; i < 10; i += 1 {
		appProjects[i] = runtime.Object(&v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      "app-project",
				Namespace: fmt.Sprintf("ns%d", i),
			},
		})
	}
	return appProjects
}

func Test_List(t *testing.T) {
	appProjects := mkAppProjects()
	t.Run("No appProjects", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset()
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		projects, err := k.List(context.TODO(), backend.AppProjectSelector{})
		require.NoError(t, err)
		assert.Len(t, projects, 0)
	})
	t.Run("Few appProjects", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		appProjects, err := k.List(context.TODO(), backend.AppProjectSelector{})
		require.NoError(t, err)
		assert.Len(t, appProjects, 10)
	})
	t.Run("Apps with matching selector", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		appProjects, err := k.List(context.TODO(), backend.AppProjectSelector{Namespaces: []string{"ns1", "ns2"}})
		require.NoError(t, err)
		assert.Len(t, appProjects, 2)
	})

	t.Run("Apps with non-matching selector", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		appProjects, err := k.List(context.TODO(), backend.AppProjectSelector{Namespaces: []string{"ns11", "ns12"}})
		require.NoError(t, err)
		assert.Len(t, appProjects, 0)
	})
}

func Test_Create(t *testing.T) {
	appProjects := mkAppProjects()
	t.Run("Create app project", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		project, err := k.Create(context.TODO(), &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "foo", Namespace: "bar"}})
		assert.NoError(t, err)
		assert.NotNil(t, project)
	})
	t.Run("Create existing app project", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		project, err := k.Create(context.TODO(), &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "app-project", Namespace: "ns1"}})
		assert.ErrorContains(t, err, "exists")
		assert.Nil(t, project)
	})
}

func Test_Get(t *testing.T) {
	appProjects := mkAppProjects()
	t.Run("Get existing app project", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		project, err := k.Get(context.TODO(), "app-project", "ns1")
		assert.NoError(t, err)
		assert.NotNil(t, project)
	})
	t.Run("Get non-existing app project", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		project, err := k.Get(context.TODO(), "foo", "ns1")
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, project)
	})
}

func Test_Delete(t *testing.T) {
	appProjects := mkAppProjects()
	t.Run("Delete existing app project", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		deletionPropagation := backend.DeletePropagationForeground
		err := k.Delete(context.TODO(), "app-project", "ns1", &deletionPropagation)
		assert.NoError(t, err)
	})
	t.Run("Delete non-existing app project", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		deletionPropagation := backend.DeletePropagationForeground
		err := k.Delete(context.TODO(), "app-project", "ns10", &deletionPropagation)
		assert.ErrorContains(t, err, "not found")
	})
}

func Test_Update(t *testing.T) {
	appProjects := mkAppProjects()
	t.Run("Update existing app project", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		project, err := k.Update(context.TODO(), &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "app-project", Namespace: "ns1"}})
		assert.NoError(t, err)
		assert.NotNil(t, project)
	})
	t.Run("Update non-existing app project", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		project, err := k.Update(context.TODO(), &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "app-project", Namespace: "ns10"}})
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, project)
	})
}

func Test_Patch(t *testing.T) {
	appProjects := mkAppProjects()
	t.Run("Patch existing app project", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		p := jsondiff.Patch{jsondiff.Operation{Type: "add", Path: "/foo", Value: "bar"}}
		jsonpatch, err := json.Marshal(p)
		require.NoError(t, err)
		project, err := k.Patch(context.TODO(), "app-project", "ns1", jsonpatch)
		assert.NoError(t, err)
		assert.NotNil(t, project)
	})
	t.Run("Update non-existing app project", func(t *testing.T) {
		fakeAppC := fakeclient.NewSimpleClientset(appProjects...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		project, err := k.Update(context.TODO(), &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "app-project", Namespace: "ns10"}})
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, project)
	})
}
