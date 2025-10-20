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
		rapp, err := m.Create(context.TODO(), app)
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
		rapp, err := m.Create(context.TODO(), app)
		assert.NoError(t, err)
		assert.Equal(t, "test", rapp.Name)
		assert.Equal(t, string(app.UID), rapp.Annotations[manager.SourceUIDAnnotation])
	})
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

func TestDoesAgentMatchWithProject(t *testing.T) {
	tests := []struct {
		name       string
		agentName  string
		appProject v1alpha1.AppProject
		want       bool
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DoesAgentMatchWithProject(tt.agentName, tt.appProject)
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
	}{
		{
			name: "filters destinations for matching agent",
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
					SourceNamespaces: []string{"cluster-prod"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name: "matches multiple destinations with glob pattern",
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
					SourceNamespaces: []string{"prod-cluster-1"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name: "no matching destinations",
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
					SourceNamespaces: []string{"prod-cluster"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name: "empty destinations and roles",
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
					SourceNamespaces: []string{"test-agent"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name: "agent name matches wildcard destination",
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
					SourceNamespaces: []string{"test-agent"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AgentSpecificAppProject(tt.appProject, tt.agent)
			assert.Equal(t, tt.want, got)
		})
	}
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
