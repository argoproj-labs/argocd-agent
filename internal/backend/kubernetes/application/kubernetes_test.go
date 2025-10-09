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

package application

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	fakeappclient "github.com/argoproj/argo-cd/v3/pkg/client/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wI2L/jsondiff"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
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

func mkApps() []runtime.Object {
	apps := make([]runtime.Object, 10)
	for i := 0; i < 10; i += 1 {
		apps[i] = runtime.Object(&v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "app",
				Namespace: fmt.Sprintf("ns%d", i),
			},
		})
	}
	return apps
}

func Test_List(t *testing.T) {
	apps := mkApps()
	t.Run("No apps", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset()
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		apps, err := k.List(context.TODO(), backend.ApplicationSelector{})
		require.NoError(t, err)
		assert.Len(t, apps, 0)
	})
	t.Run("Few apps", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		apps, err := k.List(context.TODO(), backend.ApplicationSelector{})
		require.NoError(t, err)
		assert.Len(t, apps, 10)
	})
	t.Run("Apps with matching selector", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		apps, err := k.List(context.TODO(), backend.ApplicationSelector{Namespaces: []string{"ns1", "ns2"}})
		require.NoError(t, err)
		assert.Len(t, apps, 2)
	})

	t.Run("Apps with non-matching selector", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		apps, err := k.List(context.TODO(), backend.ApplicationSelector{Namespaces: []string{"ns11", "ns12"}})
		require.NoError(t, err)
		assert.Len(t, apps, 0)
	})
}

func Test_Create(t *testing.T) {
	apps := mkApps()
	t.Run("Create app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		app, err := k.Create(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "foo", Namespace: "bar"}})
		assert.NoError(t, err)
		assert.NotNil(t, app)
	})
	t.Run("Create existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		app, err := k.Create(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app", Namespace: "ns1"}})
		assert.ErrorContains(t, err, "exists")
		assert.Equal(t, &v1alpha1.Application{}, app)
	})
}

func Test_Get(t *testing.T) {
	apps := mkApps()
	ctx := context.TODO()
	t.Run("Get existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)

		inf, err := informer.NewInformer[*v1alpha1.Application](
			ctx,
			informer.WithListHandler[*v1alpha1.Application](func(ctx context.Context, options v1.ListOptions) (runtime.Object, error) {
				return fakeAppC.ArgoprojV1alpha1().Applications("").List(ctx, options)
			}),
			informer.WithWatchHandler[*v1alpha1.Application](func(ctx context.Context, options v1.ListOptions) (watch.Interface, error) {
				return fakeAppC.ArgoprojV1alpha1().Applications("").Watch(ctx, options)
			}),
			informer.WithGroupResource[*v1alpha1.Application]("argoproj.io", "applications"),
		)
		require.NoError(t, err)

		go inf.Start(ctx)
		require.NoError(t, inf.WaitForSync(ctx))

		// Create the backend with the informer
		backend := NewKubernetesBackend(fakeAppC, "", inf, true)

		app, err := backend.Get(ctx, "app", "ns1")
		assert.NoError(t, err)
		assert.NotNil(t, app)
		assert.Equal(t, "app", app.Name)
		assert.Equal(t, "ns1", app.Namespace)

	})
	t.Run("Get non-existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		inf, err := informer.NewInformer[*v1alpha1.Application](
			ctx,
			informer.WithListHandler[*v1alpha1.Application](func(ctx context.Context, options v1.ListOptions) (runtime.Object, error) {
				return fakeAppC.ArgoprojV1alpha1().Applications("").List(ctx, options)
			}),
			informer.WithWatchHandler[*v1alpha1.Application](func(ctx context.Context, options v1.ListOptions) (watch.Interface, error) {
				return fakeAppC.ArgoprojV1alpha1().Applications("").Watch(ctx, options)
			}),
			informer.WithGroupResource[*v1alpha1.Application]("argoproj.io", "applications"),
		)
		require.NoError(t, err)
		go inf.Start(ctx)
		require.NoError(t, inf.WaitForSync(ctx))

		backend := NewKubernetesBackend(fakeAppC, "", inf, true)

		app, err := backend.Get(ctx, "nonexistent", "ns1")
		assert.ErrorContains(t, err, "not found")
		assert.Equal(t, &v1alpha1.Application{}, app)

	})

	t.Run("Get returns type assertion error for invalid object", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset()

		mockInf := &mockInformerWithInvalidType{}

		backend := &KubernetesBackend{
			appClient:   fakeAppC,
			appInformer: mockInf,
			appLister:   mockInf.Lister(),
		}

		app, err := backend.Get(ctx, "test", "ns1")
		require.Error(t, err)
		require.Nil(t, app)
		assert.Contains(t, err.Error(), "object is not an Application")
	})
}

type mockInformerWithInvalidType struct{}

func (m *mockInformerWithInvalidType) Start(ctx context.Context) error {
	return nil
}

func (m *mockInformerWithInvalidType) WaitForSync(ctx context.Context) error {
	return nil
}

func (m *mockInformerWithInvalidType) HasSynced() bool {
	return true
}

func (m *mockInformerWithInvalidType) Stop() error {
	return nil
}

func (m *mockInformerWithInvalidType) Lister() cache.GenericLister {
	return &mockListerWithInvalidType{}
}

type mockListerWithInvalidType struct{}

func (m *mockListerWithInvalidType) List(selector labels.Selector) ([]runtime.Object, error) {
	return nil, nil
}

func (m *mockListerWithInvalidType) Get(name string) (runtime.Object, error) {
	return &corev1.ConfigMap{}, nil
}

func (m *mockListerWithInvalidType) ByNamespace(namespace string) cache.GenericNamespaceLister {
	return &mockNamespaceListerWithInvalidType{}
}

type mockNamespaceListerWithInvalidType struct{}

func (m *mockNamespaceListerWithInvalidType) List(selector labels.Selector) ([]runtime.Object, error) {
	return nil, nil
}

func (m *mockNamespaceListerWithInvalidType) Get(name string) (runtime.Object, error) {
	return &corev1.ConfigMap{}, nil
}

func Test_Delete(t *testing.T) {
	apps := mkApps()
	t.Run("Delete existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		deletionPropagation := backend.DeletePropagationForeground
		err := k.Delete(context.TODO(), "app", "ns1", &deletionPropagation)
		assert.NoError(t, err)
	})
	t.Run("Delete non-existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		deletionPropagation := backend.DeletePropagationForeground
		err := k.Delete(context.TODO(), "app", "ns10", &deletionPropagation)
		assert.ErrorContains(t, err, "not found")
	})
}

func Test_Update(t *testing.T) {
	apps := mkApps()
	t.Run("Update existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		_, err := k.Update(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app", Namespace: "ns1"}})
		assert.NoError(t, err)
	})
	t.Run("Update non-existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		app, err := k.Update(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app", Namespace: "ns10"}})
		assert.ErrorContains(t, err, "not found")
		assert.Equal(t, &v1alpha1.Application{}, app)
	})
}

func Test_Patch(t *testing.T) {
	apps := mkApps()
	t.Run("Patch existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		p := jsondiff.Patch{jsondiff.Operation{Type: "add", Path: "/foo", Value: "bar"}}
		jsonpatch, err := json.Marshal(p)
		require.NoError(t, err)
		app, err := k.Patch(context.TODO(), "app", "ns1", jsonpatch)
		assert.NoError(t, err)
		assert.NotNil(t, app)
	})
	t.Run("Update non-existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, "", nil, true)
		app, err := k.Update(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app", Namespace: "ns10"}})
		assert.ErrorContains(t, err, "not found")
		assert.Equal(t, &v1alpha1.Application{}, app)
	})
}
