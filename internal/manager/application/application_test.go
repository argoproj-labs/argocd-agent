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
	"github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/application"
	appmock "github.com/argoproj-labs/argocd-agent/internal/backend/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	fakeappclient "github.com/argoproj/argo-cd/v3/pkg/client/clientset/versioned/fake"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/argoproj-labs/argocd-agent/internal/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ktypes "k8s.io/apimachinery/pkg/types"
)

var appExistsError = errors.NewAlreadyExists(schema.GroupResource{Group: "argoproj.io", Resource: "application"}, "existing")
var appNotFoundError = errors.NewNotFound(schema.GroupResource{Group: "argoproj.io", Resource: "application"}, "existing")

func fakeInformer(t *testing.T, namespace string, objects ...runtime.Object) (*fakeappclient.Clientset, *informer.Informer[*v1alpha1.Application]) {
	t.Helper()
	appC := fakeappclient.NewSimpleClientset(objects...)
	informer, err := informer.NewInformer[*v1alpha1.Application](context.Background(),
		informer.WithListHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
			return appC.ArgoprojV1alpha1().Applications(namespace).List(ctx, opts)
		}),
		informer.WithWatchHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
			return appC.ArgoprojV1alpha1().Applications(namespace).Watch(ctx, opts)
		}),
		informer.WithNamespaceScope[*v1alpha1.Application](namespace),
		informer.WithGroupResource[*v1alpha1.Application]("argoproj.io", "applications"),
	)
	require.NoError(t, err)

	go func() {
		err = informer.Start(context.Background())
		if err != nil {
			t.Fatalf("failed to start informer: %v", err)
		}
	}()

	if err = informer.WaitForSync(context.Background()); err != nil {
		t.Fatalf("failed to wait for informer sync: %v", err)
	}

	return appC, informer
}

func fakeAppManager(t *testing.T, objects ...runtime.Object) (*fakeappclient.Clientset, *ApplicationManager) {
	t.Helper()
	appC, informer := fakeInformer(t, "", objects...)
	be := application.NewKubernetesBackend(appC, "", informer, true)

	am, err := NewApplicationManager(be, "argocd")
	assert.NoError(t, err)

	return appC, am
}

func Test_ManagerOptions(t *testing.T) {
	t.Run("NewManager with default options", func(t *testing.T) {
		m, err := NewApplicationManager(nil, "")
		require.NoError(t, err)
		assert.Equal(t, false, m.allowUpsert)
	})

	t.Run("NewManager with upsert enabled", func(t *testing.T) {
		m, err := NewApplicationManager(nil, "", WithAllowUpsert(true))
		require.NoError(t, err)
		assert.True(t, m.allowUpsert)
	})
}

func Test_ManagerCreate(t *testing.T) {
	t.Run("Create an application that exists", func(t *testing.T) {
		mockedBackend := appmock.NewApplication(t)
		mockedBackend.On("Create", mock.Anything, mock.Anything, mock.Anything).
			Return(func(ctx context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error) {
				if app.Name == "existing" {
					return nil, appExistsError
				} else {
					return nil, nil
				}
			})
		m, err := NewApplicationManager(mockedBackend, "")
		require.NoError(t, err)
		_, err = m.Create(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "existing", Namespace: "default"}})
		assert.ErrorIs(t, err, appExistsError)
	})

	t.Run("Create a new application", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
				UID:       ktypes.UID("sample"),
			},
		}
		mockedBackend := appmock.NewApplication(t)
		m, err := NewApplicationManager(mockedBackend, "")
		require.NoError(t, err)
		mockedBackend.On("Create", mock.Anything, mock.Anything).Return(app, nil)
		rapp, err := m.Create(context.TODO(), app)
		assert.NoError(t, err)
		assert.Equal(t, "test", rapp.Name)
		assert.Equal(t, string(app.UID), rapp.Annotations[manager.SourceUIDAnnotation])
	})
}

func prettyPrint(app *v1alpha1.Application) {
	b, err := json.MarshalIndent(app, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s", b)
}

func Test_ManagerUpdateManaged(t *testing.T) {
	t.Run("Update spec", func(t *testing.T) {
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "cluster-1",
				Labels: map[string]string{
					"foo": "bar",
				},
				Annotations: map[string]string{
					"bar":                       "foo",
					manager.SourceUIDAnnotation: "random_uid",
				},
				Finalizers: []string{"test-finalizer"},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           "kustomize-guestbook",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Status: v1alpha1.ApplicationStatus{
				Sync: v1alpha1.SyncStatus{
					Status: v1alpha1.SyncStatusCodeOutOfSync,
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "admin"},
			},
		}
		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"bar":  "foo",
					"some": "other",
				},
				Annotations: map[string]string{
					"bar":                        "bar",
					"argocd.argoproj.io/refresh": "normal",
					manager.SourceUIDAnnotation:  "old_uid",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Status: v1alpha1.ApplicationStatus{
				Sync: v1alpha1.SyncStatus{
					Status: v1alpha1.SyncStatusCodeSynced,
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "hello"},
			},
		}

		// We are on the agent
		// appC := fakeappclient.NewSimpleClientset(existing)
		// ai, err := appinformer.NewAppInformer(context.Background(), appC, "argocd")
		// require.NoError(t, err)
		appC, ai := fakeInformer(t, "", existing)
		be := application.NewKubernetesBackend(appC, "", ai, true)
		mgr, err := NewApplicationManager(be, "argocd", WithMode(manager.ManagerModeManaged), WithRole(manager.ManagerRoleAgent))
		require.NoError(t, err)

		updated, err := mgr.UpdateManagedApp(context.Background(), incoming)

		require.NoError(t, err)
		require.NotNil(t, updated)

		// Application must have agent's namespace
		require.Equal(t, updated.Namespace, "argocd")
		// Refresh annotation should only be overwritten by the controller, not
		// by the incoming app
		require.Contains(t, updated.Annotations, "argocd.argoproj.io/refresh")

		// Source UID annotation should not be overwritten by the incoming app
		require.Equal(t, existing.Annotations[manager.SourceUIDAnnotation], updated.Annotations[manager.SourceUIDAnnotation])

		// Labels and annotations must be in sync with incoming
		require.Equal(t, incoming.Labels, updated.Labels)

		// Finalizers must be in sync
		require.Equal(t, incoming.Finalizers, updated.Finalizers)

		// Non-refresh annotations must be in sync with incoming
		require.Equal(t, incoming.Annotations["bar"], updated.Annotations["bar"])
		// Refresh annotation must not be removed
		require.Contains(t, updated.Annotations, "argocd.argoproj.io/refresh")
		// Status must not have been touched
		require.Equal(t, existing.Status, updated.Status)
		// Spec must be in sync with incoming
		require.Equal(t, incoming.Spec, updated.Spec)
	})

}

func Test_ManagerUpdateStatus(t *testing.T) {
	t.Run("Update spec", func(t *testing.T) {
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"foo": "bar",
				},
				Annotations: map[string]string{
					"bar": "foo",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           "kustomize-guestbook",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Status: v1alpha1.ApplicationStatus{
				Sync: v1alpha1.SyncStatus{
					Status: v1alpha1.SyncStatusCodeOutOfSync,
				},
			},
		}
		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "cluster-1",
				Labels: map[string]string{
					"bar":  "foo",
					"some": "other",
				},
				Annotations: map[string]string{
					"bar":                        "foo",
					"argocd.argoproj.io/refresh": "normal",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "hello"},
			},
		}

		// appC := fakeappclient.NewSimpleClientset(existing)
		// ai, err := appinformer.NewAppInformer(context.Background(), appC, "argocd")
		// require.NoError(t, err)
		appC, ai := fakeInformer(t, "", existing)
		be := application.NewKubernetesBackend(appC, "", ai, true)
		mgr, err := NewApplicationManager(be, "argocd")
		require.NoError(t, err)
		mgr.mode = manager.ManagerModeManaged
		mgr.role = manager.ManagerRolePrincipal
		updated, err := mgr.UpdateStatus(context.Background(), "cluster-1", incoming)
		require.NoError(t, err)
		b, err := json.MarshalIndent(updated, "", " ")
		fmt.Printf("%s\n", b)
		require.NoError(t, err)
		require.NotNil(t, updated)
		require.Equal(t, "cluster-1", updated.Namespace)
		require.NotContains(t, updated.Annotations, "argocd.argoproj.io/refresh")
		require.NotContains(t, updated.Labels, "foo")
		require.Contains(t, updated.Labels, "bar")
		require.Contains(t, updated.Labels, "some")
		require.Equal(t, updated.Spec.Source.Path, ".")
		require.Nil(t, updated.Operation)
	})
}

func Test_ManagerUpdateAutonomous(t *testing.T) {
	t.Run("Update status", func(t *testing.T) {
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"foo": "bar",
				},
				Annotations: map[string]string{
					"bar": "foo",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
		}
		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "cluster-1",
				Labels: map[string]string{
					"foo": "bar",
				},
				Annotations: map[string]string{
					"bar":                        "foo",
					"argocd.argoproj.io/refresh": "normal",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "hello"},
			},
		}

		// appC := fakeappclient.NewSimpleClientset(existing)
		// ai, err := appinformer.NewAppInformer(context.Background(), appC, "argocd")
		// require.NoError(t, err)
		appC, ai := fakeInformer(t, "", existing)
		be := application.NewKubernetesBackend(appC, "", ai, true)
		mgr, err := NewApplicationManager(be, "argocd")
		require.NoError(t, err)
		mgr.role = manager.ManagerRolePrincipal
		updated, err := mgr.UpdateAutonomousApp(context.TODO(), "cluster-1", incoming)
		require.NoError(t, err)
		require.NotNil(t, updated)
		assert.Equal(t, "cluster-1", updated.Namespace)
		require.NotContains(t, updated.ObjectMeta.Annotations, "argocd.argoproj.io/refresh")
		require.Equal(t, map[string]string{"foo": "bar"}, updated.Labels)
	})
}

func Test_ManagerUpdateOperation(t *testing.T) {
	t.Run("Update status", func(t *testing.T) {
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"foo": "bar",
				},
				Annotations: map[string]string{
					"argocd.argoproj.io/refresh": "normal",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "hello"},
			},
		}
		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"foo": "bar",
					"bar": "foo",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "foobar"},
			},
		}

		// appC := fakeappclient.NewSimpleClientset(existing)
		// informer := appinformer.NewAppInformer(context.Background(), appC, "argocd")
		// be := kubernetes.NewKubernetesBackend(appC, "", informer, true)
		// mgr := NewApplicationManager(be, "argocd")
		_, mgr := fakeAppManager(t, existing)
		mgr.mode = manager.ManagerModeAutonomous
		mgr.role = manager.ManagerRoleAgent
		updated, err := mgr.UpdateOperation(context.TODO(), incoming)
		require.NoError(t, err)
		require.NotNil(t, updated)
	})
}

func Test_DeleteApp(t *testing.T) {
	t.Run("Delete without finalizer", func(t *testing.T) {
		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"foo": "bar",
					"bar": "foo",
				},
				Finalizers: []string{"resource-finalizer.argoproj.io"},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "foobar"},
			},
		}
		appC, mgr := fakeAppManager(t, existing)
		app, err := appC.ArgoprojV1alpha1().Applications("argocd").Get(context.TODO(), "foobar", v1.GetOptions{})
		assert.NoError(t, err)
		assert.NotNil(t, app)
		deletionPropagation := backend.DeletePropagationForeground
		err = mgr.Delete(context.TODO(), "argocd", existing, &deletionPropagation)
		assert.NoError(t, err)
		app, err = appC.ArgoprojV1alpha1().Applications("argocd").Get(context.TODO(), "foobar", v1.GetOptions{})
		assert.True(t, errors.IsNotFound(err))
		assert.Equal(t, &v1alpha1.Application{}, app)
	})
	t.Run("Remove finalizers", func(t *testing.T) {
		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"foo": "bar",
					"bar": "foo",
				},
				Finalizers: []string{"resource-finalizer.argoproj.io"},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "foobar"},
			},
		}
		appC, mgr := fakeAppManager(t, existing)
		app, err := appC.ArgoprojV1alpha1().Applications("argocd").Get(context.TODO(), "foobar", v1.GetOptions{})
		assert.NoError(t, err)
		assert.NotNil(t, app)
		app, err = mgr.RemoveFinalizers(context.TODO(), app)
		assert.NoError(t, err)
		assert.Empty(t, app.ObjectMeta.Finalizers)
	})
}

func Test_ManageApp(t *testing.T) {
	t.Run("Mark app as managed", func(t *testing.T) {
		appm, err := NewApplicationManager(nil, "")
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

	t.Run("Mark app as unmanaged", func(t *testing.T) {
		appm, err := NewApplicationManager(nil, "")
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
		appm, err := NewApplicationManager(nil, "")
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
		appm, err := NewApplicationManager(nil, "")
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
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foo",
				Namespace: "bar",
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "baz",
			},
		}
		stampLastUpdated(app)
		assert.Contains(t, app.Annotations, LastUpdatedAnnotation)
		assert.Len(t, app.Annotations, 1)
	})
	t.Run("Stamp app with existing annotations", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foo",
				Namespace: "bar",
				Annotations: map[string]string{
					"foo": "bar",
					"bar": "baz",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "baz",
			},
		}
		stampLastUpdated(app)
		assert.Contains(t, app.Annotations, LastUpdatedAnnotation)
		assert.Len(t, app.Annotations, 3)
	})
}

func Test_CompareSourceUIDForApp(t *testing.T) {
	oldApp := &v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test",
			Namespace: "argocd",
			Annotations: map[string]string{
				manager.SourceUIDAnnotation: "old_uid",
			},
		},
	}

	mockedBackend := appmock.NewApplication(t)
	getMock := mockedBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(oldApp, nil)
	m, err := NewApplicationManager(mockedBackend, "")
	require.Nil(t, err)
	ctx := context.Background()

	t.Cleanup(func() {
		getMock.Unset()
	})

	t.Run("should return true if the UID matches", func(t *testing.T) {
		incoming := oldApp.DeepCopy()
		incoming.UID = ktypes.UID("old_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.True(t, exists)
		require.Nil(t, err)
		require.True(t, uidMatch)
	})

	t.Run("should return false if the UID doesn't match", func(t *testing.T) {
		incoming := oldApp.DeepCopy()
		incoming.UID = ktypes.UID("new_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.True(t, exists)
		require.Nil(t, err)
		require.False(t, uidMatch)
	})

	t.Run("should return an error if there is no UID annotation", func(t *testing.T) {
		oldApp.Annotations = map[string]string{}
		mockedBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(oldApp, nil)
		m, err := NewApplicationManager(mockedBackend, "")
		require.Nil(t, err)
		ctx := context.Background()

		incoming := oldApp.DeepCopy()
		incoming.UID = ktypes.UID("new_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.True(t, exists)
		require.NotNil(t, err)
		require.EqualError(t, err, "source UID Annotation is not found for app: test")
		require.False(t, uidMatch)
	})

	t.Run("should return False if the app doesn't exist", func(t *testing.T) {
		expectedErr := errors.NewNotFound(schema.GroupResource{Group: "argoproj.io", Resource: "application"},
			oldApp.Name)
		getMock.Unset()
		getMock = mockedBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedErr)
		m, err := NewApplicationManager(mockedBackend, "")
		require.Nil(t, err)
		ctx := context.Background()

		app, err := m.applicationBackend.Get(ctx, oldApp.Name, oldApp.Namespace)
		require.Nil(t, app)
		require.True(t, errors.IsNotFound(err))

		incoming := oldApp.DeepCopy()
		incoming.UID = ktypes.UID("new_uid")
		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.False(t, exists)
		require.False(t, uidMatch)
		require.Nil(t, err)
	})

	t.Run("should override incoming namespace", func(t *testing.T) {
		oldApp.Annotations = map[string]string{manager.SourceUIDAnnotation: "old_uid"}
		getMock.Unset()
		getMock = mockedBackend.On("Get", mock.Anything, mock.Anything, "argocd").Return(oldApp, nil)
		m, err := NewApplicationManager(mockedBackend, oldApp.Namespace)
		require.Nil(t, err)
		ctx := context.Background()

		incoming := oldApp.DeepCopy()
		incoming.Namespace = "foobar"
		incoming.UID = ktypes.UID("old_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.True(t, exists)
		require.Nil(t, err)
		require.True(t, uidMatch)
	})
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}

func Test_RevertManagedAppChanges(t *testing.T) {
	t.Run("Revert spec changes", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Annotations: map[string]string{
					manager.SourceUIDAnnotation: "some_uid",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "test",
			},
		}

		appC, ai := fakeInformer(t, "", app)
		be := application.NewKubernetesBackend(appC, "", ai, true)
		mgr, err := NewApplicationManager(be, "argocd", WithMode(manager.ManagerModeManaged), WithRole(manager.ManagerRoleAgent))
		require.NoError(t, err)

		sourceCache := cache.NewSourceCache()

		// Store app spec in cache
		sourceCache.Application.Set("some_uid", app.Spec)

		reverted := mgr.RevertManagedAppChanges(context.Background(), app, sourceCache.Application)
		require.False(t, reverted)

		// Update app spec
		app.Spec.Project = "test1"

		reverted = mgr.RevertManagedAppChanges(context.Background(), app, sourceCache.Application)
		require.True(t, reverted)
	})
}
