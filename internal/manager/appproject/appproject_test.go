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
	goerrors "errors"
	"slices"
	"strings"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	appproject "github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/appproject"
	appmock "github.com/argoproj-labs/argocd-agent/internal/backend/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/cache"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	fakeappclient "github.com/argoproj/argo-cd/v3/pkg/client/clientset/versioned/fake"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testNamespace = "argocd"

func fakeProjManager(t *testing.T, namespace string, objects ...runtime.Object) (*fakeappclient.Clientset, *AppProjectManager) {
	client := fakeappclient.NewSimpleClientset(objects...)
	informer, err := informer.NewInformer(context.Background(),
		informer.WithListHandler[*v1alpha1.AppProject](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
			return client.ArgoprojV1alpha1().AppProjects(namespace).List(ctx, opts)
		}),
		informer.WithWatchHandler[*v1alpha1.AppProject](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
			return client.ArgoprojV1alpha1().AppProjects(namespace).Watch(ctx, opts)
		}),
		informer.WithGroupResource[*v1alpha1.AppProject]("argoproj.io", "appprojects"),
	)
	assert.NoError(t, err)

	be := appproject.NewKubernetesBackend(client, "", informer, true)

	am, err := NewAppProjectManager(be, "")
	assert.NoError(t, err)

	return client, am
}

func newTestAppProject(name string) *v1alpha1.AppProject {
	return &v1alpha1.AppProject{
		ObjectMeta: v1.ObjectMeta{Name: name, Namespace: testNamespace, UID: ktypes.UID(name + "-uid")},
		Spec:       v1alpha1.AppProjectSpec{SourceRepos: []string{"https://example.com/repo.git"}},
	}
}

// newExistingAppProject is an AppProject as it exists on the agent, with the
// source UID annotation that Create adds.
func newExistingAppProject() *v1alpha1.AppProject {
	project := newTestAppProject("test-project")
	project.ResourceVersion = "5"
	project.Labels = map[string]string{"app": "old"}
	project.Annotations = map[string]string{manager.SourceUIDAnnotation: "src-1"}
	project.Spec.SourceRepos = []string{"https://old"}
	return project
}

func newMockedManager(t *testing.T, opts ...AppProjectManagerOption) (*appmock.AppProject, *AppProjectManager) {
	t.Helper()
	be := appmock.NewAppProject(t)
	m, err := NewAppProjectManager(be, testNamespace, opts...)
	require.NoError(t, err)
	return be, m
}

func Test_ManagerOptions(t *testing.T) {
	t.Run("NewManager with default options", func(t *testing.T) {
		m, err := NewAppProjectManager(nil, "")
		require.NoError(t, err)
		assert.Equal(t, false, m.allowUpsert)
	})

	t.Run("NewManager with upsert enabled", func(t *testing.T) {
		m, err := NewAppProjectManager(nil, "", WithAllowUpsert(true))
		require.NoError(t, err)
		assert.True(t, m.allowUpsert)
	})
}

func Test_NewAppProjectManagerRoleAndMode(t *testing.T) {
	tests := []struct {
		name    string
		opts    []AppProjectManagerOption
		wantErr string
	}{
		{
			name: "a principal without a mode",
			opts: []AppProjectManagerOption{WithRole(manager.ManagerRolePrincipal)},
		},
		{
			name:    "a principal with a mode is rejected",
			opts:    []AppProjectManagerOption{WithRole(manager.ManagerRolePrincipal), WithMode(manager.ManagerModeManaged)},
			wantErr: "mode should be unset when role is principal",
		},
		{
			name: "an agent with a mode",
			opts: []AppProjectManagerOption{WithRole(manager.ManagerRoleAgent), WithMode(manager.ManagerModeAutonomous)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewAppProjectManager(appmock.NewAppProject(t), testNamespace, tt.opts...)

			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				require.Nil(t, m)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, m)
		})
	}
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
		err = mgr.Delete(context.TODO(), existing, &deletionPropagation)
		assert.NoError(t, err)
		app, err = appC.ArgoprojV1alpha1().AppProjects("argocd").Get(context.TODO(), "foobar", v1.GetOptions{})
		assert.True(t, errors.IsNotFound(err))
		assert.Equal(t, &v1alpha1.AppProject{}, app)
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
		assert.Equal(t, 0, appm.ManagedResources.Len())
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
		assert.Equal(t, 0, appm.ObservedResources.Len())
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
		rapp, err := m.Create(context.TODO(), app, true)
		assert.NoError(t, err)
		assert.Equal(t, "test", rapp.Name)
	})

	t.Run("Create a new AppProject on Principal", func(t *testing.T) {
		app := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
				UID:       "new_uid",
			},
		}
		mockedBackend := appmock.NewAppProject(t)
		m, err := NewAppProjectManager(mockedBackend, "", WithRole(manager.ManagerRolePrincipal))
		require.NoError(t, err)
		mockedBackend.On("Create", mock.Anything, mock.Anything).Return(app, nil)
		rapp, err := m.Create(context.TODO(), app, true)
		assert.NoError(t, err)
		assert.Equal(t, "test", rapp.Name)
		assert.Equal(t, string(app.UID), rapp.Annotations[manager.SourceUIDAnnotation])
	})
}

func Test_AppProjectManagerUpsert(t *testing.T) {
	ctx := context.Background()
	alreadyExists := errors.NewAlreadyExists(schema.GroupResource{Group: "argoproj.io", Resource: "appprojects"}, "test-project")

	t.Run("creates the project when it does not exist", func(t *testing.T) {
		created := newTestAppProject("test-project")
		created.ResourceVersion = "1"
		var sent *v1alpha1.AppProject
		be, m := newMockedManager(t)
		be.EXPECT().Create(ctx, mock.Anything).RunAndReturn(
			func(_ context.Context, p *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
				sent = p.DeepCopy()
				return created, nil
			}).Once()

		got, err := m.Upsert(ctx, newTestAppProject("test-project"))

		require.NoError(t, err)
		require.Same(t, created, got)
		require.Equal(t, testNamespace, sent.Namespace)
		require.Equal(t, "test-project-uid", sent.Annotations[manager.SourceUIDAnnotation])
		require.True(t, m.IsManaged("test-project"))
		require.True(t, m.IsChangeIgnored("test-project", "1"))
	})

	t.Run("updates the existing project when it already exists", func(t *testing.T) {
		tests := []struct {
			name      string
			role      manager.ManagerRole
			wantStamp bool
		}{
			{name: "on the principal the update is stamped", role: manager.ManagerRolePrincipal, wantStamp: true},
			{name: "on an agent the update is not stamped", role: manager.ManagerRoleAgent, wantStamp: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// The replica assigns its own UID and resource version to the project.
				existing := newExistingAppProject()
				existing.UID = "replica-uid"
				existing.ResourceVersion = "9"
				existing.Annotations = map[string]string{manager.SourceUIDAnnotation: "stale-from-a-previous-failover"}
				updated := newExistingAppProject()
				updated.ResourceVersion = "10"
				var sent *v1alpha1.AppProject
				be, m := newMockedManager(t, WithRole(tt.role))
				be.EXPECT().Create(ctx, mock.Anything).Return(nil, alreadyExists).Once()
				be.EXPECT().Get(ctx, "test-project", testNamespace).Return(existing, nil).Once()
				be.EXPECT().Update(ctx, mock.Anything).RunAndReturn(
					func(_ context.Context, p *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
						sent = p.DeepCopy()
						return updated, nil
					}).Once()

				got, err := m.Upsert(ctx, newTestAppProject("test-project"))

				require.NoError(t, err)
				require.Same(t, updated, got)
				require.Equal(t, ktypes.UID("replica-uid"), sent.UID)
				require.Equal(t, "9", sent.ResourceVersion)
				// The source UID is the one of the primary, not the stale one on the replica.
				require.Equal(t, "test-project-uid", sent.Annotations[manager.SourceUIDAnnotation])
				_, stamped := sent.Annotations[LastUpdatedAnnotation]
				require.Equal(t, tt.wantStamp, stamped)
				require.True(t, m.IsChangeIgnored("test-project", "10"))
			})
		}
	})

	t.Run("returns the error without going further", func(t *testing.T) {
		createErr := goerrors.New("create failed")
		getErr := goerrors.New("get failed")
		updateErr := goerrors.New("update failed")
		tests := []struct {
			name    string
			expect  func(be *appmock.AppProject)
			wantErr error
			wantMsg string
		}{
			{
				name: "create fails with an error other than already exists",
				expect: func(be *appmock.AppProject) {
					be.EXPECT().Create(ctx, mock.Anything).Return(nil, createErr).Once()
				},
				wantErr: createErr,
			},
			{
				name: "the existing project cannot be fetched",
				expect: func(be *appmock.AppProject) {
					be.EXPECT().Create(ctx, mock.Anything).Return(nil, alreadyExists).Once()
					be.EXPECT().Get(ctx, "test-project", testNamespace).Return(nil, getErr).Once()
				},
				wantErr: getErr,
				wantMsg: "get existing appproject for upsert",
			},
			{
				name: "the update fails",
				expect: func(be *appmock.AppProject) {
					be.EXPECT().Create(ctx, mock.Anything).Return(nil, alreadyExists).Once()
					be.EXPECT().Get(ctx, "test-project", testNamespace).Return(newExistingAppProject(), nil).Once()
					be.EXPECT().Update(ctx, mock.Anything).Return(nil, updateErr).Once()
				},
				wantErr: updateErr,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				be, m := newMockedManager(t)
				tt.expect(be)

				got, err := m.Upsert(ctx, newTestAppProject("test-project"))

				require.ErrorIs(t, err, tt.wantErr)
				if tt.wantMsg != "" {
					require.ErrorContains(t, err, tt.wantMsg)
				}
				require.Nil(t, got)
			})
		}
	})
}

func Test_AppProjectManagerUpdateAppProject(t *testing.T) {
	ctx := context.Background()

	t.Run("updates the project when the backend does not support patch", func(t *testing.T) {
		incoming := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:        "test-project",
				Namespace:   testNamespace,
				Labels:      map[string]string{"app": "new"},
				Annotations: map[string]string{"extra": "yes"},
				Finalizers:  []string{"example.com/finalizer"},
			},
			Spec: v1alpha1.AppProjectSpec{SourceRepos: []string{"https://new"}},
			Status: v1alpha1.AppProjectStatus{
				JWTTokensByRole: map[string]v1alpha1.JWTTokens{"admin": {Items: []v1alpha1.JWTToken{{ID: "t1"}}}},
			},
		}
		updated := newExistingAppProject()
		updated.ResourceVersion = "6"
		var sent *v1alpha1.AppProject
		be, m := newMockedManager(t)
		be.EXPECT().Get(ctx, "test-project", testNamespace).Return(newExistingAppProject(), nil).Once()
		be.EXPECT().SupportsPatch().Return(false)
		be.EXPECT().Update(ctx, mock.Anything).RunAndReturn(
			func(_ context.Context, p *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
				sent = p.DeepCopy()
				return updated, nil
			}).Once()

		got, err := m.UpdateAppProject(ctx, incoming)

		require.NoError(t, err)
		require.Same(t, updated, got)
		require.Equal(t, map[string]string{"app": "new"}, sent.Labels)
		require.Equal(t, []string{"example.com/finalizer"}, sent.Finalizers)
		require.Equal(t, []string{"https://new"}, sent.Spec.SourceRepos)
		require.Equal(t, incoming.Status, sent.Status)
		// The incoming project is not stamped, so the existing source UID is kept.
		require.Equal(t, map[string]string{"extra": "yes", manager.SourceUIDAnnotation: "src-1"}, sent.Annotations)
		require.True(t, m.IsChangeIgnored("test-project", "6"))
	})

	t.Run("an incoming project that carries its own source UID replaces the existing one", func(t *testing.T) {
		incoming := newTestAppProject("test-project")
		incoming.Annotations = map[string]string{manager.SourceUIDAnnotation: "adopted"}
		var sent *v1alpha1.AppProject
		be, m := newMockedManager(t)
		be.EXPECT().Get(ctx, "test-project", testNamespace).Return(newExistingAppProject(), nil).Once()
		be.EXPECT().SupportsPatch().Return(false)
		be.EXPECT().Update(ctx, mock.Anything).RunAndReturn(
			func(_ context.Context, p *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
				sent = p.DeepCopy()
				return p, nil
			}).Once()

		_, err := m.UpdateAppProject(ctx, incoming)

		require.NoError(t, err)
		require.Equal(t, "adopted", sent.Annotations[manager.SourceUIDAnnotation])
	})

	t.Run("patches the project when the backend supports patch", func(t *testing.T) {
		incoming := newTestAppProject("test-project")
		incoming.Labels = map[string]string{"app": "old"}
		incoming.Spec.SourceRepos = []string{"https://new"}
		updated := newExistingAppProject()
		updated.ResourceVersion = "6"
		var patch []byte
		be, m := newMockedManager(t)
		be.EXPECT().Get(ctx, "test-project", testNamespace).Return(newExistingAppProject(), nil).Once()
		be.EXPECT().SupportsPatch().Return(true)
		be.EXPECT().Patch(ctx, "test-project", testNamespace, mock.Anything).RunAndReturn(
			func(_ context.Context, _, _ string, p []byte) (*v1alpha1.AppProject, error) {
				patch = p
				return updated, nil
			}).Once()

		got, err := m.UpdateAppProject(ctx, incoming)

		require.NoError(t, err)
		require.Same(t, updated, got)
		// Only the source repositories differ: the source UID annotation of the
		// existing project was kept in the target, so it is not part of the patch.
		var operations []struct {
			Op   string `json:"op"`
			Path string `json:"path"`
		}
		require.NoError(t, json.Unmarshal(patch, &operations))
		require.NotEmpty(t, operations)
		for _, operation := range operations {
			require.True(t, strings.HasPrefix(operation.Path, "/spec/sourceRepos"), "unexpected operation on %s", operation.Path)
		}
		require.True(t, m.IsChangeIgnored("test-project", "6"))
	})

	t.Run("creates the project when it does not exist and upsert is allowed", func(t *testing.T) {
		created := newTestAppProject("test-project")
		created.ResourceVersion = "1"
		be, m := newMockedManager(t, WithAllowUpsert(true))
		be.EXPECT().Get(ctx, "test-project", testNamespace).
			Return(nil, errors.NewNotFound(schema.GroupResource{Resource: "appprojects"}, "test-project")).Once()
		be.EXPECT().Create(ctx, mock.Anything).Return(created, nil).Once()

		got, err := m.UpdateAppProject(ctx, newTestAppProject("test-project"))

		require.NoError(t, err)
		require.Same(t, created, got)
		require.True(t, m.IsManaged("test-project"))
	})

	t.Run("returns an error without writing when the project cannot be fetched", func(t *testing.T) {
		getErr := goerrors.New("get failed")
		tests := []struct {
			name        string
			allowUpsert bool
			err         error
		}{
			{
				name: "not found and upsert is not allowed",
				err:  errors.NewNotFound(schema.GroupResource{Resource: "appprojects"}, "test-project"),
			},
			{name: "another error and upsert is allowed", allowUpsert: true, err: getErr},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				be, m := newMockedManager(t, WithAllowUpsert(tt.allowUpsert))
				be.EXPECT().Get(ctx, "test-project", testNamespace).Return(nil, tt.err).Once()

				got, err := m.UpdateAppProject(ctx, newTestAppProject("test-project"))

				require.ErrorIs(t, err, tt.err)
				require.ErrorContains(t, err, "error updating app-project test-project")
				require.Nil(t, got)
			})
		}
	})

	t.Run("a failed write returns the error", func(t *testing.T) {
		writeErr := goerrors.New("write failed")
		tests := []struct {
			name     string
			patching bool
			expect   func(be *appmock.AppProject)
		}{
			{
				name:     "patch",
				patching: true,
				expect: func(be *appmock.AppProject) {
					be.EXPECT().Patch(ctx, "test-project", testNamespace, mock.Anything).Return(nil, writeErr).Once()
				},
			},
			{
				name: "update",
				expect: func(be *appmock.AppProject) {
					be.EXPECT().Update(ctx, mock.Anything).Return(nil, writeErr).Once()
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				be, m := newMockedManager(t)
				be.EXPECT().Get(ctx, "test-project", testNamespace).Return(newExistingAppProject(), nil).Once()
				be.EXPECT().SupportsPatch().Return(tt.patching)
				tt.expect(be)

				got, err := m.UpdateAppProject(ctx, newTestAppProject("test-project"))

				require.ErrorIs(t, err, writeErr)
				require.Nil(t, got)
			})
		}
	})

	t.Run("retries the update after a conflict", func(t *testing.T) {
		conflict := errors.NewConflict(schema.GroupResource{Resource: "appprojects"}, "test-project", goerrors.New("the object was modified"))
		updated := newExistingAppProject()
		updated.ResourceVersion = "6"
		be, m := newMockedManager(t)
		be.EXPECT().Get(ctx, "test-project", testNamespace).RunAndReturn(
			func(context.Context, string, string) (*v1alpha1.AppProject, error) {
				return newExistingAppProject(), nil
			}).Twice()
		be.EXPECT().SupportsPatch().Return(false)
		be.EXPECT().Update(ctx, mock.Anything).Return(nil, conflict).Once()
		be.EXPECT().Update(ctx, mock.Anything).Return(updated, nil).Once()

		got, err := m.UpdateAppProject(ctx, newTestAppProject("test-project"))

		require.NoError(t, err)
		require.Same(t, updated, got)
	})
}

func Test_AppProjectManagerDelete(t *testing.T) {
	ctx := context.Background()
	foreground := backend.DeletePropagationForeground

	t.Run("the principal removes the finalizers before deleting", func(t *testing.T) {
		existing := newExistingAppProject()
		existing.Finalizers = []string{"resource-finalizer.argoproj.io"}
		var calls []string
		var finalizersSent []string
		be, m := newMockedManager(t, WithRole(manager.ManagerRolePrincipal))
		be.EXPECT().Get(ctx, "test-project", testNamespace).Return(existing, nil).Once()
		be.EXPECT().SupportsPatch().Return(false)
		be.EXPECT().Update(ctx, mock.Anything).RunAndReturn(
			func(_ context.Context, p *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
				calls = append(calls, "update")
				finalizersSent = p.Finalizers
				return p, nil
			}).Once()
		be.EXPECT().Delete(ctx, "test-project", testNamespace, &foreground).RunAndReturn(
			func(context.Context, string, string, *backend.DeletionPropagation) error {
				calls = append(calls, "delete")
				return nil
			}).Once()

		err := m.Delete(ctx, newTestAppProject("test-project"), &foreground)

		require.NoError(t, err)
		require.Equal(t, []string{"update", "delete"}, calls)
		require.Empty(t, finalizersSent)
	})

	t.Run("the principal does not delete when the finalizers cannot be removed", func(t *testing.T) {
		getErr := goerrors.New("get failed")
		be, m := newMockedManager(t, WithRole(manager.ManagerRolePrincipal))
		be.EXPECT().Get(ctx, "test-project", testNamespace).Return(nil, getErr).Once()

		err := m.Delete(ctx, newTestAppProject("test-project"), &foreground)

		require.ErrorIs(t, err, getErr)
		require.ErrorContains(t, err, "error removing finalizer")
	})

	t.Run("an agent leaves the finalizers alone", func(t *testing.T) {
		be, m := newMockedManager(t, WithRole(manager.ManagerRoleAgent))
		be.EXPECT().Delete(ctx, "test-project", testNamespace, &foreground).Return(nil).Once()

		err := m.Delete(ctx, newTestAppProject("test-project"), &foreground)

		require.NoError(t, err)
	})

	t.Run("returns the error of the backend", func(t *testing.T) {
		deleteErr := goerrors.New("delete failed")
		be, m := newMockedManager(t, WithRole(manager.ManagerRoleAgent))
		be.EXPECT().Delete(ctx, "test-project", testNamespace, (*backend.DeletionPropagation)(nil)).Return(deleteErr).Once()

		err := m.Delete(ctx, newTestAppProject("test-project"), nil)

		require.ErrorIs(t, err, deleteErr)
	})
}

func Test_preserveSourceUIDIfUnset(t *testing.T) {
	tests := []struct {
		name     string
		existing map[string]string
		incoming map[string]string
		want     map[string]string
	}{
		{
			name:     "keeps the source UID the incoming project carries",
			existing: map[string]string{manager.SourceUIDAnnotation: "existing"},
			incoming: map[string]string{manager.SourceUIDAnnotation: "incoming"},
			want:     map[string]string{manager.SourceUIDAnnotation: "incoming"},
		},
		{
			name:     "copies the existing source UID when the incoming one is empty",
			existing: map[string]string{manager.SourceUIDAnnotation: "existing"},
			incoming: map[string]string{manager.SourceUIDAnnotation: "", "keep": "me"},
			want:     map[string]string{manager.SourceUIDAnnotation: "existing", "keep": "me"},
		},
		{
			name:     "copies the existing source UID when the incoming project has no annotations",
			existing: map[string]string{manager.SourceUIDAnnotation: "existing"},
			want:     map[string]string{manager.SourceUIDAnnotation: "existing"},
		},
		{
			name:     "leaves the incoming project alone when the existing one has no source UID",
			existing: map[string]string{"other": "annotation"},
			want:     nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Annotations: tt.existing}}
			incoming := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Annotations: tt.incoming}}

			preserveSourceUIDIfUnset(existing, incoming)

			require.Equal(t, tt.want, incoming.Annotations)
		})
	}
}

func Test_CompareSourceUIDForAppProject(t *testing.T) {
	oldAppProject := &v1alpha1.AppProject{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test",
			Namespace: "argocd",
			Annotations: map[string]string{
				manager.SourceUIDAnnotation: "old_uid",
			},
		},
	}

	mockedBackend := appmock.NewAppProject(t)
	getMock := mockedBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(oldAppProject, nil)
	m, err := NewAppProjectManager(mockedBackend, "")
	require.Nil(t, err)
	ctx := context.Background()

	t.Cleanup(func() {
		getMock.Unset()
	})

	t.Run("should return true if the UID matches", func(t *testing.T) {
		incoming := oldAppProject.DeepCopy()
		incoming.UID = ktypes.UID("old_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.True(t, exists)
		require.Nil(t, err)
		require.True(t, uidMatch)
	})

	t.Run("should return false if the UID doesn't match", func(t *testing.T) {
		incoming := oldAppProject.DeepCopy()
		incoming.UID = ktypes.UID("new_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.True(t, exists)
		require.Nil(t, err)
		require.False(t, uidMatch)
	})

	t.Run("shouldn't return an error if there is no UID annotation", func(t *testing.T) {
		oldAppProject.Annotations = map[string]string{}
		mockedBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(oldAppProject, nil)
		m, err := NewAppProjectManager(mockedBackend, "")
		require.Nil(t, err)
		ctx := context.Background()

		incoming := oldAppProject.DeepCopy()
		incoming.UID = ktypes.UID("new_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.True(t, exists)
		require.Nil(t, err)
		require.False(t, uidMatch)
	})

	t.Run("should return the error of the backend when it is not a not found error", func(t *testing.T) {
		getErr := goerrors.New("get failed")
		be, m := newMockedManager(t)
		be.EXPECT().Get(ctx, oldAppProject.Name, oldAppProject.Namespace).Return(nil, getErr).Once()

		exists, uidMatch, err := m.CompareSourceUID(ctx, oldAppProject.DeepCopy())

		require.ErrorIs(t, err, getErr)
		require.False(t, exists)
		require.False(t, uidMatch)
	})

	t.Run("should return False if the appProject doesn't exist", func(t *testing.T) {
		expectedErr := errors.NewNotFound(schema.GroupResource{Group: "argoproj.io", Resource: "appproject"},
			oldAppProject.Name)
		getMock.Unset()
		getMock = mockedBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedErr)
		m, err := NewAppProjectManager(mockedBackend, "")
		require.Nil(t, err)
		ctx := context.Background()

		app, err := m.appprojectBackend.Get(ctx, oldAppProject.Name, oldAppProject.Namespace)
		require.Nil(t, app)
		require.True(t, errors.IsNotFound(err))

		incoming := oldAppProject.DeepCopy()
		incoming.UID = ktypes.UID("new_uid")
		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.False(t, exists)
		require.False(t, uidMatch)
		require.Nil(t, err)
	})
}

func Test_AppProjectManagerRevertAppProjectChanges(t *testing.T) {
	cachedSpec := v1alpha1.AppProjectSpec{SourceRepos: []string{"https://source"}}
	tamperedSpec := v1alpha1.AppProjectSpec{SourceRepos: []string{"https://tampered"}}
	cached := map[ktypes.UID]v1alpha1.AppProjectSpec{"src-1": cachedSpec}
	withSourceUID := map[string]string{manager.SourceUIDAnnotation: "src-1"}
	updateErr := goerrors.New("update failed")

	// expectUpdate sets up the backend for the update that reverts the spec.
	expectUpdate := func(result *v1alpha1.AppProject, err error) func(be *appmock.AppProject) {
		return func(be *appmock.AppProject) {
			existing := newExistingAppProject()
			existing.Spec = tamperedSpec
			be.EXPECT().Get(mock.Anything, "test-project", testNamespace).Return(existing, nil).Once()
			be.EXPECT().SupportsPatch().Return(false)
			be.EXPECT().Update(mock.Anything, mock.MatchedBy(func(p *v1alpha1.AppProject) bool {
				return slices.Equal(p.Spec.SourceRepos, cachedSpec.SourceRepos)
			})).Return(result, err).Once()
		}
	}

	tests := []struct {
		name        string
		role        manager.ManagerRole
		mode        manager.ManagerMode
		annotations map[string]string
		cached      map[ktypes.UID]v1alpha1.AppProjectSpec
		spec        v1alpha1.AppProjectSpec
		setup       func(be *appmock.AppProject)
		want        bool
		wantErr     error
		wantErrMsg  string
		wantSpec    *v1alpha1.AppProjectSpec
	}{
		{
			name:     "an autonomous agent never reverts, even without a source UID",
			role:     manager.ManagerRoleAgent,
			mode:     manager.ManagerModeAutonomous,
			cached:   cached,
			spec:     tamperedSpec,
			wantSpec: &tamperedSpec,
		},
		{
			name:       "the principal needs a source UID annotation",
			role:       manager.ManagerRolePrincipal,
			cached:     cached,
			spec:       tamperedSpec,
			wantErrMsg: "source UID annotation not found for resource",
		},
		{
			name:       "a managed agent needs a source UID annotation",
			role:       manager.ManagerRoleAgent,
			mode:       manager.ManagerModeManaged,
			cached:     cached,
			spec:       tamperedSpec,
			wantErrMsg: "source UID annotation not found for resource",
		},
		{
			name:        "does nothing when the project is not in the cache",
			role:        manager.ManagerRoleAgent,
			mode:        manager.ManagerModeManaged,
			annotations: withSourceUID,
			spec:        tamperedSpec,
			wantSpec:    &tamperedSpec,
		},
		{
			name:        "does nothing when the spec matches the cache",
			role:        manager.ManagerRoleAgent,
			mode:        manager.ManagerModeManaged,
			annotations: withSourceUID,
			cached:      cached,
			spec:        cachedSpec,
			wantSpec:    &cachedSpec,
		},
		{
			name:        "reverts the spec to the cached one",
			role:        manager.ManagerRoleAgent,
			mode:        manager.ManagerModeManaged,
			annotations: withSourceUID,
			cached:      cached,
			spec:        tamperedSpec,
			setup:       expectUpdate(newExistingAppProject(), nil),
			want:        true,
			wantSpec:    &cachedSpec,
		},
		{
			name:        "returns the error when the revert cannot be written",
			role:        manager.ManagerRolePrincipal,
			annotations: withSourceUID,
			cached:      cached,
			spec:        tamperedSpec,
			setup:       expectUpdate(nil, updateErr),
			wantErr:     updateErr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			be, m := newMockedManager(t, WithRole(tt.role), WithMode(tt.mode))
			if tt.setup != nil {
				tt.setup(be)
			}
			projectCache := cache.NewSourceCache().AppProject
			for uid, spec := range tt.cached {
				projectCache.Set(uid, spec)
			}
			project := newTestAppProject("test-project")
			project.Annotations = tt.annotations
			project.Spec = tt.spec

			got, err := m.RevertAppProjectChanges(context.Background(), project, projectCache)

			require.Equal(t, tt.want, got)
			switch {
			case tt.wantErr != nil:
				require.ErrorIs(t, err, tt.wantErr)
			case tt.wantErrMsg != "":
				require.EqualError(t, err, tt.wantErrMsg)
			default:
				require.NoError(t, err)
			}
			if tt.wantSpec != nil {
				require.Equal(t, *tt.wantSpec, project.Spec)
			}
		})
	}
}

func TestDoesAgentMatchWithProject(t *testing.T) {
	tests := []struct {
		name       string
		agentName  string
		appProject v1alpha1.AppProject
		want       bool
		dstMapping bool
	}{
		{
			name:      "agent matches destination name",
			agentName: "agent-prod",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "agent-prod", Namespace: "default"},
					},
					SourceNamespaces: []string{"agent-prod"},
				},
			},
			want: true,
		},
		{
			name:      "empty destination name with wildcard server",
			agentName: "any-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "", Server: "*", Namespace: "default"},
					},
					SourceNamespaces: []string{"any-agent"},
				},
			},
			want: true,
		},

		{
			name:      "agent matches destination name with wildcard",
			agentName: "agent-staging",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "agent-*", Namespace: "default"},
					},
					SourceNamespaces: []string{"agent-*"},
				},
			},
			want: true,
		},
		{
			name:      "deny pattern matches agent - should reject",
			agentName: "prod-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "!prod-*", Namespace: "default"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: false,
		},
		{
			name:      "deny pattern with positive pattern - deny takes precedence",
			agentName: "prod-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "*", Namespace: "default"},       // Allow all
						{Name: "!prod-*", Namespace: "default"}, // Deny prod
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: false,
		},
		{
			name:      "deny pattern with positive pattern - allow others",
			agentName: "dev-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "*", Namespace: "default"},       // Allow all
						{Name: "!prod-*", Namespace: "default"}, // Deny prod
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: true,
		},

		{
			name:      "agent matches destination but not source namespace",
			agentName: "agent-test",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "agent-test", Namespace: "default"},
					},
					SourceNamespaces: []string{"different-namespace"},
				},
			},
			want: false,
		},
		{
			name:      "only deny patterns - no positive match",
			agentName: "dev-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "!prod-*", Namespace: "default"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: false, // No positive pattern to match
		},
		{
			name:      "server URL with exact agent name match",
			agentName: "prod-cluster-1",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "", Server: "https://resource-proxy.example.com:8080?agentName=prod-cluster-1", Namespace: "default"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: true, // Agent name matches extracted query parameter
		},
		{
			name:      "server URL with agent name pattern",
			agentName: "prod-cluster-2",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "", Server: "https://resource-proxy.example.com:8080?agentName=prod-cluster-*", Namespace: "default"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: true, // Agent name matches extracted pattern
		},
		{
			name:      "server URL with non-matching agent name",
			agentName: "dev-cluster-1",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "", Server: "https://resource-proxy.example.com:8080?agentName=prod-cluster-1", Namespace: "default"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: false, // Agent name doesn't match extracted query parameter
		},
		{
			name:      "destination-based mapping - agent only matches destination name",
			agentName: "agent-prod",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "agent-prod", Namespace: "default"},
					},
					SourceNamespaces: []string{"test"},
				},
			},
			want:       true,
			dstMapping: true,
		},
		{
			name:      "destination-based mapping - nil sourceNamespaces with matching destination",
			agentName: "agent-prod",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "agent-prod", Namespace: "default"},
					},
				},
			},
			want:       true,
			dstMapping: true,
		},
		{
			name:      "destination-based mapping - destination does not match",
			agentName: "agent-prod",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "other-agent", Namespace: "default"},
					},
				},
			},
			want:       false,
			dstMapping: true,
		},
		{
			name:      "server URL that cannot be parsed is ignored",
			agentName: "agent-prod",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Server: "://not-a-url"},
					},
				},
			},
			want:       false,
			dstMapping: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DoesAgentMatchWithProject(tt.agentName, tt.appProject, tt.dstMapping)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAgentSpecificAppProject(t *testing.T) {
	tests := []struct {
		name       string
		appProject v1alpha1.AppProject
		agent      string
		want       v1alpha1.AppProject
		dstMapping bool
		mode       types.AgentMode
	}{
		{
			name:       "filters destinations for matching agent",
			dstMapping: false,
			mode:       types.AgentModeManaged,
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "cluster-prod",
							Namespace: "default",
							Server:    "https://prod-cluster.example.com",
						},
						{
							Name:      "cluster-dev",
							Namespace: "staging",
							Server:    "https://dev-cluster.example.com",
						},
					},
					SourceNamespaces: []string{"argocd", "apps"},
					Roles: []v1alpha1.ProjectRole{
						{
							Name: "admin",
							Policies: []string{
								"p, proj:test-project:admin, applications, *, test-project/*, allow",
							},
						},
					},
				},
			},
			agent: "cluster-prod",
			want: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "in-cluster",
							Namespace: "default",
							Server:    "https://kubernetes.default.svc",
						},
					},
					SourceNamespaces: nil,
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name:       "matches multiple destinations with glob pattern",
			dstMapping: false,
			mode:       types.AgentModeManaged,
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-dest-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "prod-cluster-1",
							Namespace: "app1",
							Server:    "https://prod1.example.com",
						},
						{
							Name:      "prod-cluster-1",
							Namespace: "app2",
							Server:    "https://prod2.example.com",
						},
						{
							Name:      "dev-cluster",
							Namespace: "app3",
							Server:    "https://dev.example.com",
						},
					},
					SourceNamespaces: []string{"*"},
					Roles: []v1alpha1.ProjectRole{
						{Name: "viewer"},
						{Name: "admin"},
					},
				},
			},
			agent: "prod-cluster-1",
			want: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-dest-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "in-cluster",
							Namespace: "app1",
							Server:    "https://kubernetes.default.svc",
						},
						{
							Name:      "in-cluster",
							Namespace: "app2",
							Server:    "https://kubernetes.default.svc",
						},
					},
					SourceNamespaces: nil,
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name:       "no matching destinations",
			dstMapping: false,
			mode:       types.AgentModeManaged,
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-match-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "staging-cluster",
							Namespace: "default",
							Server:    "https://staging.example.com",
						},
						{
							Name:      "test-cluster",
							Namespace: "testing",
							Server:    "https://test.example.com",
						},
					},
					SourceNamespaces: []string{"argocd", "staging"},
					Roles: []v1alpha1.ProjectRole{
						{
							Name: "operator",
							Policies: []string{
								"p, proj:no-match-project:operator, applications, get, *, allow",
							},
						},
					},
				},
			},
			agent: "prod-cluster",
			want: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-match-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations:     []v1alpha1.ApplicationDestination{},
					SourceNamespaces: nil,
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name:       "empty destinations and roles",
			dstMapping: false,
			mode:       types.AgentModeManaged,
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations:     []v1alpha1.ApplicationDestination{},
					SourceNamespaces: []string{"original-namespace"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
			agent: "test-agent",
			want: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations:     []v1alpha1.ApplicationDestination{},
					SourceNamespaces: nil,
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name:       "agent name matches wildcard destination",
			dstMapping: false,
			mode:       types.AgentModeManaged,
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "*",
							Namespace: "default",
						},
					},
					SourceNamespaces: []string{"*"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
			agent: "test-agent",
			want: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "in-cluster",
							Server:    "https://kubernetes.default.svc",
							Namespace: "default",
						},
					},
					SourceNamespaces: nil,
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name:       "preserves source namespaces for destination-based mapping",
			dstMapping: true,
			mode:       types.AgentModeManaged,
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "*",
							Namespace: "default",
						},
					},
					SourceNamespaces: []string{"agent-name,app-ns"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
			agent: "test-agent",
			want: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wildcard-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "in-cluster",
							Server:    "https://kubernetes.default.svc",
							Namespace: "default",
						},
					},
					SourceNamespaces: []string{"agent-name,app-ns"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name:       "autonomous mode preserves source namespaces and roles",
			dstMapping: false,
			mode:       types.AgentModeAutonomous,
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "auto-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "cluster-prod",
							Namespace: "default",
							Server:    "https://prod-cluster.example.com",
						},
					},
					SourceNamespaces: []string{"argocd", "apps"},
					Roles: []v1alpha1.ProjectRole{
						{
							Name: "admin",
							Policies: []string{
								"p, proj:auto-project:admin, applications, *, auto-project/*, allow",
							},
						},
					},
				},
			},
			agent: "cluster-prod",
			want: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "auto-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "in-cluster",
							Namespace: "default",
							Server:    "https://kubernetes.default.svc",
						},
					},
					SourceNamespaces: nil,
					Roles: []v1alpha1.ProjectRole{
						{
							Name: "admin",
							Policies: []string{
								"p, proj:auto-project:admin, applications, *, auto-project/*, allow",
							},
						},
					},
				},
			},
		},
		{
			name:       "filters destination service accounts for matching agent",
			dstMapping: false,
			mode:       types.AgentModeManaged,
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "cluster-prod",
							Namespace: "default",
							Server:    "https://prod-cluster.example.com",
						},
					},
					DestinationServiceAccounts: []v1alpha1.ApplicationDestinationServiceAccount{
						{
							DefaultServiceAccount: "argocd",
							Namespace:             "guestbook",
							Server:                "https://resource-proxy.argocd.svc.cluster.local:9090?agentName=cluster-prod",
						},
						{
							DefaultServiceAccount: "argocd",
							Namespace:             "guestbook",
							Server:                "https://resource-proxy.argocd.svc.cluster.local:9090?agentName=cluster-dev",
						},
					},
					SourceNamespaces: []string{"argocd"},
				},
			},
			agent: "cluster-prod",
			want: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "in-cluster",
							Namespace: "default",
							Server:    "https://kubernetes.default.svc",
						},
					},
					DestinationServiceAccounts: []v1alpha1.ApplicationDestinationServiceAccount{
						{
							DefaultServiceAccount: "argocd",
							Namespace:             "guestbook",
							Server:                "https://kubernetes.default.svc",
						},
					},
					SourceNamespaces: nil,
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name:       "server-only destination with a wildcard server is kept",
			dstMapping: true,
			mode:       types.AgentModeAutonomous,
			agent:      "agent-prod",
			appProject: v1alpha1.AppProject{Spec: v1alpha1.AppProjectSpec{Destinations: []v1alpha1.ApplicationDestination{{Server: "*", Namespace: "apps"}}}},
			want:       v1alpha1.AppProject{Spec: v1alpha1.AppProjectSpec{Destinations: []v1alpha1.ApplicationDestination{{Name: "in-cluster", Server: "https://kubernetes.default.svc", Namespace: "apps"}}}},
		},
		{
			name:       "server-only destination whose agentName matches is kept",
			dstMapping: true,
			mode:       types.AgentModeAutonomous,
			agent:      "agent-prod",
			appProject: v1alpha1.AppProject{Spec: v1alpha1.AppProjectSpec{Destinations: []v1alpha1.ApplicationDestination{{Server: "https://rp.example.com:9090?agentName=agent-prod", Namespace: "apps"}}}},
			want:       v1alpha1.AppProject{Spec: v1alpha1.AppProjectSpec{Destinations: []v1alpha1.ApplicationDestination{{Name: "in-cluster", Server: "https://kubernetes.default.svc", Namespace: "apps"}}}},
		},
		{
			name:       "server-only destination whose agentName is another agent is dropped",
			dstMapping: true,
			mode:       types.AgentModeAutonomous,
			agent:      "agent-prod",
			appProject: v1alpha1.AppProject{Spec: v1alpha1.AppProjectSpec{Destinations: []v1alpha1.ApplicationDestination{{Server: "https://rp.example.com:9090?agentName=other"}}}},
			want:       v1alpha1.AppProject{Spec: v1alpha1.AppProjectSpec{Destinations: []v1alpha1.ApplicationDestination{}}},
		},
		{
			name:       "server-only destination with a server URL that cannot be parsed is dropped",
			dstMapping: true,
			mode:       types.AgentModeAutonomous,
			agent:      "agent-prod",
			appProject: v1alpha1.AppProject{Spec: v1alpha1.AppProjectSpec{Destinations: []v1alpha1.ApplicationDestination{{Server: "://not-a-url"}}}},
			want:       v1alpha1.AppProject{Spec: v1alpha1.AppProjectSpec{Destinations: []v1alpha1.ApplicationDestination{}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AgentSpecificAppProject(tt.appProject, tt.agent, tt.dstMapping, tt.mode)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAgentSpecificDestinationServiceAccounts(t *testing.T) {
	const resourceProxy = "https://argocd-agent-principal-resource-proxy.argocd.svc.cluster.local:9090"

	tests := []struct {
		name    string
		destSAs []v1alpha1.ApplicationDestinationServiceAccount
		agent   string
		want    []v1alpha1.ApplicationDestinationServiceAccount
	}{
		{
			name:  "keeps only the entries of the given agent",
			agent: "workload-dev-local",
			destSAs: []v1alpha1.ApplicationDestinationServiceAccount{
				{DefaultServiceAccount: "argocd", Namespace: "petclinic-argocd", Server: "https://kubernetes.default.svc"},
				{DefaultServiceAccount: "argocd", Namespace: "petclinic-ci", Server: resourceProxy + "?agentName=workload-dev-local"},
				{DefaultServiceAccount: "argocd", Namespace: "petclinic-dev", Server: resourceProxy + "?agentName=workload-dev-local"},
				{DefaultServiceAccount: "argocd", Namespace: "petclinic-prod", Server: resourceProxy + "?agentName=workload-prod-local"},
				{DefaultServiceAccount: "argocd", Namespace: "petclinic-stage", Server: resourceProxy + "?agentName=workload-prod-local"},
				{DefaultServiceAccount: "argocd", Namespace: "petclinic-argocd", Server: resourceProxy + "?agentName=workload-dev-local"},
				{DefaultServiceAccount: "argocd", Namespace: "petclinic-argocd", Server: resourceProxy + "?agentName=workload-prod-local"},
			},
			want: []v1alpha1.ApplicationDestinationServiceAccount{
				{DefaultServiceAccount: "argocd", Namespace: "petclinic-ci", Server: "https://kubernetes.default.svc"},
				{DefaultServiceAccount: "argocd", Namespace: "petclinic-dev", Server: "https://kubernetes.default.svc"},
				{DefaultServiceAccount: "argocd", Namespace: "petclinic-argocd", Server: "https://kubernetes.default.svc"},
			},
		},
		{
			name:  "matches the agent name as a glob pattern",
			agent: "workload-dev-local",
			destSAs: []v1alpha1.ApplicationDestinationServiceAccount{
				{DefaultServiceAccount: "argocd", Namespace: "dev", Server: resourceProxy + "?agentName=workload-dev-*"},
				{DefaultServiceAccount: "argocd", Namespace: "prod", Server: resourceProxy + "?agentName=workload-prod-*"},
			},
			want: []v1alpha1.ApplicationDestinationServiceAccount{
				{DefaultServiceAccount: "argocd", Namespace: "dev", Server: "https://kubernetes.default.svc"},
			},
		},
		{
			name:  "keeps wildcard servers for every agent",
			agent: "workload-dev-local",
			destSAs: []v1alpha1.ApplicationDestinationServiceAccount{
				{DefaultServiceAccount: "argocd", Namespace: "guestbook", Server: "*"},
			},
			want: []v1alpha1.ApplicationDestinationServiceAccount{
				{DefaultServiceAccount: "argocd", Namespace: "guestbook", Server: "https://kubernetes.default.svc"},
			},
		},
		{
			name:  "keeps the first of the entries collapsing onto the same namespace",
			agent: "workload-dev-local",
			destSAs: []v1alpha1.ApplicationDestinationServiceAccount{
				{DefaultServiceAccount: "first", Namespace: "guestbook", Server: resourceProxy + "?agentName=workload-dev-local"},
				{DefaultServiceAccount: "second", Namespace: "guestbook", Server: resourceProxy + "?agentName=workload-dev-*"},
				{DefaultServiceAccount: "third", Namespace: "guestbook", Server: "*"},
			},
			want: []v1alpha1.ApplicationDestinationServiceAccount{
				{DefaultServiceAccount: "first", Namespace: "guestbook", Server: "https://kubernetes.default.svc"},
			},
		},
		{
			name:  "drops entries without an agent name and entries with an unparseable server",
			agent: "workload-dev-local",
			destSAs: []v1alpha1.ApplicationDestinationServiceAccount{
				{DefaultServiceAccount: "argocd", Namespace: "guestbook", Server: "https://kubernetes.default.svc"},
				{DefaultServiceAccount: "argocd", Namespace: "guestbook", Server: resourceProxy},
				{DefaultServiceAccount: "argocd", Namespace: "guestbook", Server: "https://control plane?agentName=workload-dev-local"},
				{DefaultServiceAccount: "argocd", Namespace: "guestbook", Server: ""},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentSpecificDestinationServiceAccounts(tt.destSAs, tt.agent)
			assert.Equal(t, tt.want, got)
		})
	}
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
