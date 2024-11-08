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
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	appproject "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/appproject"
	appmock "github.com/argoproj-labs/argocd-agent/internal/backend/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	fakeappclient "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func fakeProjManager(t *testing.T, namespace string, objects ...runtime.Object) (*fakeappclient.Clientset, *AppProjectManager) {
	client := fakeappclient.NewSimpleClientset(objects...)
	informer, err := informer.NewInformer(context.Background(),
		informer.WithListHandler[*v1alpha1.AppProject](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
			return client.ArgoprojV1alpha1().AppProjects(namespace).List(ctx, opts)
		}),
		informer.WithWatchHandler[*v1alpha1.AppProject](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
			return client.ArgoprojV1alpha1().AppProjects(namespace).Watch(ctx, opts)
		}),
	)
	assert.NoError(t, err)

	be := appproject.NewKubernetesBackend(client, "", informer, true)

	am, err := NewAppProjectManager(be, "")
	assert.NoError(t, err)

	return client, am
}

func Test_ManagerOptions(t *testing.T) {
	t.Run("NewManager with default options", func(t *testing.T) {
		m, err := NewAppProjectManager(nil, "")
		require.NoError(t, err)
		assert.Equal(t, false, m.allowUpsert)
		assert.Nil(t, m.metrics)
	})

	t.Run("NewManager with metrics", func(t *testing.T) {
		m, err := NewAppProjectManager(nil, "", WithMetrics(&metrics.AppProjectClientMetrics{}))
		require.NoError(t, err)
		assert.NotNil(t, m.metrics)
	})

	t.Run("NewManager with upsert enabled", func(t *testing.T) {
		m, err := NewAppProjectManager(nil, "", WithAllowUpsert(true))
		require.NoError(t, err)
		assert.True(t, m.allowUpsert)
	})
}

func Test_DeleteAppProject(t *testing.T) {
	t.Run("Delete without finalizer", func(t *testing.T) {
		existing := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"foo": "bar",
					"bar": "foo",
				},
				Finalizers: []string{"resource-finalizer.argoproj.io"},
			},
			Spec: v1alpha1.AppProjectSpec{
				SourceRepos: []string{"*"},
			},
		}
		appC, mgr := fakeProjManager(t, "argocd", existing)
		app, err := appC.ArgoprojV1alpha1().AppProjects("argocd").Get(context.TODO(), "foobar", v1.GetOptions{})
		assert.NoError(t, err)
		assert.NotNil(t, app)
		deletionPropagation := backend.DeletePropagationForeground
		err = mgr.Delete(context.TODO(), "argocd", existing, &deletionPropagation)
		assert.NoError(t, err)
		app, err = appC.ArgoprojV1alpha1().AppProjects("argocd").Get(context.TODO(), "foobar", v1.GetOptions{})
		assert.True(t, errors.IsNotFound(err))
		assert.Nil(t, app)
	})
	t.Run("Remove finalizers", func(t *testing.T) {
		existing := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"foo": "bar",
					"bar": "foo",
				},
				Finalizers: []string{"resource-finalizer.argoproj.io"},
			},
			Spec: v1alpha1.AppProjectSpec{
				SourceRepos: []string{"*"},
			},
		}
		appC, mgr := fakeProjManager(t, "argocd", existing)
		app, err := appC.ArgoprojV1alpha1().AppProjects("argocd").Get(context.TODO(), "foobar", v1.GetOptions{})
		assert.NoError(t, err)
		assert.NotNil(t, app)
		app, err = mgr.RemoveFinalizers(context.TODO(), app)
		assert.NoError(t, err)
		assert.Empty(t, app.ObjectMeta.Finalizers)
	})
}

func Test_ManageAppProjects(t *testing.T) {
	t.Run("Mark appProject as managed", func(t *testing.T) {
		appm, err := NewAppProjectManager(nil, "")
		require.NoError(t, err)
		assert.False(t, appm.IsManaged("foo"))
		err = appm.Manage("foo")
		assert.NoError(t, err)
		assert.True(t, appm.IsManaged("foo"))
		err = appm.Manage("foo")
		assert.Error(t, err)
		assert.True(t, appm.IsManaged("foo"))
		appm.ClearManaged()
		assert.False(t, appm.IsManaged("foo"))
		assert.Len(t, appm.managedAppProjects, 0)
	})

	t.Run("Mark appProject as unmanaged", func(t *testing.T) {
		appm, err := NewAppProjectManager(nil, "")
		require.NoError(t, err)
		err = appm.Manage("foo")
		assert.True(t, appm.IsManaged("foo"))
		assert.NoError(t, err)
		err = appm.Unmanage("foo")
		assert.NoError(t, err)
		assert.False(t, appm.IsManaged("foo"))
		err = appm.Unmanage("foo")
		assert.Error(t, err)
		assert.False(t, appm.IsManaged("foo"))
	})
}

func Test_IgnoreChange(t *testing.T) {
	t.Run("Ignore a change", func(t *testing.T) {
		appm, err := NewAppProjectManager(nil, "")
		require.NoError(t, err)
		assert.False(t, appm.IsChangeIgnored("foo", "1"))
		err = appm.IgnoreChange("foo", "1")
		assert.NoError(t, err)
		assert.True(t, appm.IsChangeIgnored("foo", "1"))
		err = appm.IgnoreChange("foo", "1")
		assert.Error(t, err)
		assert.True(t, appm.IsChangeIgnored("foo", "1"))
		appm.ClearIgnored()
		assert.False(t, appm.IsChangeIgnored("foo", "1"))
		assert.Len(t, appm.managedAppProjects, 0)
	})

	t.Run("Unignore a change", func(t *testing.T) {
		appm, err := NewAppProjectManager(nil, "")
		require.NoError(t, err)
		err = appm.UnignoreChange("foo")
		assert.Error(t, err)
		assert.False(t, appm.IsChangeIgnored("foo", "1"))
		err = appm.IgnoreChange("foo", "1")
		assert.NoError(t, err)
		assert.True(t, appm.IsChangeIgnored("foo", "1"))
		err = appm.UnignoreChange("foo")
		assert.NoError(t, err)
		assert.False(t, appm.IsChangeIgnored("foo", "1"))
		err = appm.UnignoreChange("foo")
		assert.Error(t, err)
		assert.False(t, appm.IsChangeIgnored("foo", "1"))
	})
}

func Test_stampLastUpdated(t *testing.T) {
	t.Run("Stamp app without labels", func(t *testing.T) {
		app := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foo",
				Namespace: "bar",
			},
			Spec: v1alpha1.AppProjectSpec{
				SourceRepos: []string{"*"},
			},
		}
		stampLastUpdated(app)
		assert.Contains(t, app.Annotations, LastUpdatedAnnotation)
		assert.Len(t, app.Annotations, 1)
	})
	t.Run("Stamp app with existing annotations", func(t *testing.T) {
		app := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foo",
				Namespace: "bar",
				Annotations: map[string]string{
					"foo": "bar",
					"bar": "baz",
				},
			},
			Spec: v1alpha1.AppProjectSpec{
				SourceRepos: []string{"*"},
			},
		}
		stampLastUpdated(app)
		assert.Contains(t, app.Annotations, LastUpdatedAnnotation)
		assert.Len(t, app.Annotations, 3)
	})
}

func TestCreateAppProject(t *testing.T) {
	t.Run("Create an appproject on Agent", func(t *testing.T) {
		app := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Spec: v1alpha1.AppProjectSpec{
				SourceNamespaces: []string{"default"},
			},
		}
		mockedBackend := appmock.NewAppProject(t)
		m, err := NewAppProjectManager(mockedBackend, "default", WithRole(manager.ManagerRoleAgent))
		require.NoError(t, err)
		mockedBackend.On("Create", mock.Anything, mock.Anything).Return(app, nil)
		rapp, err := m.Create(context.TODO(), app)
		assert.NoError(t, err)
		assert.Equal(t, "test", rapp.Name)
	})

	t.Run("Create a new AppProject on Principal", func(t *testing.T) {
		app := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
		}
		mockedBackend := appmock.NewAppProject(t)
		m, err := NewAppProjectManager(mockedBackend, "", WithRole(manager.ManagerRolePrincipal))
		require.NoError(t, err)
		mockedBackend.On("Create", mock.Anything, mock.Anything).Return(app, nil)
		rapp, err := m.Create(context.TODO(), app)
		assert.NoError(t, err)
		assert.Equal(t, "test", rapp.Name)
	})
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
