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
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/backend/kubernetes/application"
	appmock "github.com/argoproj-labs/argocd-agent/internal/backend/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/sirupsen/logrus"
	"github.com/wI2L/jsondiff"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/utils/ptr"

	synccommon "github.com/argoproj/argo-cd/gitops-engine/pkg/sync/common"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	fakeappclient "github.com/argoproj/argo-cd/v3/pkg/client/clientset/versioned/fake"
	"github.com/argoproj/argo-cd/v3/util/settings"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/argoproj-labs/argocd-agent/internal/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ktypes "k8s.io/apimachinery/pkg/types"
)

var (
	appExistsError   = errors.NewAlreadyExists(schema.GroupResource{Group: "argoproj.io", Resource: "application"}, "existing")
	appNotFoundError = errors.NewNotFound(schema.GroupResource{Group: "argoproj.io", Resource: "application"}, "existing")
)

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
		startErr := informer.Start(context.Background())
		if startErr != nil {
			t.Errorf("failed to start informer: %v", startErr)
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
		_, err = m.Create(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "existing", Namespace: "default"}}, true)
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
		rapp, err := m.Create(context.TODO(), app, true)
		assert.NoError(t, err)
		assert.Equal(t, "test", rapp.Name)
		assert.Equal(t, string(app.UID), rapp.Annotations[manager.SourceUIDAnnotation])
	})

	t.Run("Create preserves incoming source uid when already resolved", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
				UID:       ktypes.UID("replica-uid"),
				Annotations: map[string]string{
					manager.SourceUIDAnnotation: "primary-uid",
				},
			},
		}
		mockedBackend := appmock.NewApplication(t)
		m, err := NewApplicationManager(mockedBackend, "")
		require.NoError(t, err)
		mockedBackend.On("Create", mock.Anything, mock.Anything).Return(app, nil)

		rapp, err := m.Create(context.TODO(), app, true)
		assert.NoError(t, err)
		assert.Equal(t, "primary-uid", rapp.Annotations[manager.SourceUIDAnnotation])
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

		updated, err := mgr.UpdateManagedApp(context.Background(), incoming, ManagedIdentity{})

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

	t.Run("Explicit managed identity overrides are stamped", func(t *testing.T) {
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				UID:       ktypes.UID("incoming-uid"),
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
		}
		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Annotations: map[string]string{
					manager.SourceUIDAnnotation:    "old-source-uid",
					manager.PrincipalUIDAnnotation: "principal-A",
				},
			},
		}

		appC, ai := fakeInformer(t, "", existing)
		be := application.NewKubernetesBackend(appC, "", ai, true)
		mgr, err := NewApplicationManager(be, "argocd", WithMode(manager.ManagerModeManaged), WithRole(manager.ManagerRoleAgent))
		require.NoError(t, err)

		updated, err := mgr.UpdateManagedApp(context.Background(), incoming, ManagedIdentity{
			SourceUID:    "resolved-source-uid",
			PrincipalUID: "principal-B",
		})

		require.NoError(t, err)
		require.NotNil(t, updated)
		require.Equal(t, "resolved-source-uid", updated.Annotations[manager.SourceUIDAnnotation])
		require.Equal(t, "principal-B", updated.Annotations[manager.PrincipalUIDAnnotation])
	})

	t.Run("Remove finalizers with patch backend", func(t *testing.T) {
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
			},
		}
		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:       "foobar",
				Namespace:  "argocd",
				Finalizers: []string{"resources-finalizer.argocd.argoproj.io"},
				Annotations: map[string]string{
					manager.SourceUIDAnnotation: "source-uid",
				},
			},
		}

		appC, ai := fakeInformer(t, "", existing)
		be := application.NewKubernetesBackend(appC, "", ai, true)
		mgr, err := NewApplicationManager(be, "argocd", WithMode(manager.ManagerModeManaged), WithRole(manager.ManagerRoleAgent))
		require.NoError(t, err)

		updated, err := mgr.UpdateManagedApp(context.Background(), incoming, ManagedIdentity{})

		require.NoError(t, err)
		require.NotNil(t, updated)
		require.Empty(t, updated.Finalizers)
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
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "incoming"},
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
		require.NotNil(t, updated.Operation)
		require.Equal(t, incoming.Operation, updated.Operation)
	})

	t.Run("Retain principal-owned annotations across an agent status update", func(t *testing.T) {
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
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
				Annotations: map[string]string{
					// State written on the principal's copy only: the agent's
					// incoming application does not carry these annotations.
					manager.SourceUIDAnnotation: "12345",
					manager.NotifiedAnnotation:  `{"cd6b8a91cf6b8a4b1e0b4d0e:app-deployed":{"state":"delivered"}}`,
					// A stale value the agent has since changed: the incoming
					// value must win for annotations the agent owns.
					"bar": "baz",
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

		appC, ai := fakeInformer(t, "", existing)
		// usePatch=false exercises the update path, which inherits the
		// incoming annotation set and is where preservation must happen; the
		// patch path never touches annotations except the refresh marker.
		be := application.NewKubernetesBackend(appC, "", ai, false)
		mgr, err := NewApplicationManager(be, "argocd")
		require.NoError(t, err)
		mgr.mode = manager.ManagerModeManaged
		mgr.role = manager.ManagerRolePrincipal
		updated, err := mgr.UpdateStatus(context.Background(), "cluster-1", incoming)
		require.NoError(t, err)
		require.NotNil(t, updated)
		assert.Equal(t, "12345", updated.Annotations[manager.SourceUIDAnnotation])
		assert.Equal(t, `{"cd6b8a91cf6b8a4b1e0b4d0e:app-deployed":{"state":"delivered"}}`, updated.Annotations[manager.NotifiedAnnotation])
		assert.Equal(t, "foo", updated.Annotations["bar"])
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
	t.Run("Retain principal-owned annotations across an agent update", func(t *testing.T) {
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
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
				Annotations: map[string]string{
					// State written on the principal's copy only: the agent's
					// incoming application does not carry these annotations.
					manager.SourceUIDAnnotation: "12345",
					manager.NotifiedAnnotation:  `{"cd6b8a91cf6b8a4b1e0b4d0e:app-deployed":{"state":"delivered"}}`,
					// A stale value the agent has since changed: the incoming
					// value must win for annotations the agent owns.
					"bar": "baz",
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

		appC, ai := fakeInformer(t, "", existing)
		be := application.NewKubernetesBackend(appC, "", ai, true)
		mgr, err := NewApplicationManager(be, "argocd")
		require.NoError(t, err)
		mgr.role = manager.ManagerRolePrincipal
		updated, err := mgr.UpdateAutonomousApp(context.TODO(), "cluster-1", incoming)
		require.NoError(t, err)
		require.NotNil(t, updated)
		assert.Equal(t, "12345", updated.Annotations[manager.SourceUIDAnnotation])
		assert.Equal(t, `{"cd6b8a91cf6b8a4b1e0b4d0e:app-deployed":{"state":"delivered"}}`, updated.Annotations[manager.NotifiedAnnotation])
		assert.Equal(t, "foo", updated.Annotations["bar"])
	})

	t.Run("Retain principal-owned annotations without patch support", func(t *testing.T) {
		// The non-patch update path inherits the incoming annotation set the
		// same way; the incoming application carries no annotations at all,
		// which also exercises the nil-map initialization.
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
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
				Annotations: map[string]string{
					manager.SourceUIDAnnotation: "12345",
					manager.NotifiedAnnotation:  `{"cd6b8a91cf6b8a4b1e0b4d0e:app-deployed":{"state":"delivered"}}`,
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

		appC, ai := fakeInformer(t, "", existing)
		be := application.NewKubernetesBackend(appC, "", ai, false)
		mgr, err := NewApplicationManager(be, "argocd")
		require.NoError(t, err)
		mgr.role = manager.ManagerRolePrincipal
		updated, err := mgr.UpdateAutonomousApp(context.TODO(), "cluster-1", incoming)
		require.NoError(t, err)
		require.NotNil(t, updated)
		assert.Equal(t, "12345", updated.Annotations[manager.SourceUIDAnnotation])
		assert.Equal(t, `{"cd6b8a91cf6b8a4b1e0b4d0e:app-deployed":{"state":"delivered"}}`, updated.Annotations[manager.NotifiedAnnotation])
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

func Test_TerminateOperation(t *testing.T) {
	t.Run("terminate the app by patching the status.operationState", func(t *testing.T) {
		mockedBackend := appmock.NewApplication(t)

		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
			},
			Status: v1alpha1.ApplicationStatus{
				OperationState: &v1alpha1.OperationState{
					Phase: synccommon.OperationRunning,
				},
			},
		}

		patched := existing.DeepCopy()
		patched.Status.OperationState = &v1alpha1.OperationState{
			Phase: synccommon.OperationTerminating,
		}

		matcher := mock.MatchedBy(func(b []byte) bool {
			var patch jsondiff.Patch
			if err := json.Unmarshal(b, &patch); err != nil {
				return false
			}
			if len(patch) != 1 {
				return false
			}

			if patch[0].Type != "add" {
				return false
			}

			if patch[0].Path != "/status/operationState/phase" {
				return false
			}

			return patch[0].Value == string(synccommon.OperationTerminating)
		})

		mockedBackend.On("Get", mock.Anything, "foobar", "argocd").Return(existing, nil)
		mockedBackend.On("Patch", mock.Anything, "foobar", "argocd", matcher).Return(patched, nil)

		mgr, err := NewApplicationManager(mockedBackend, "argocd", WithRole(manager.ManagerRoleAgent), WithMode(manager.ManagerModeManaged))
		require.NoError(t, err)

		incoming := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "foobar"}}
		updated, err := mgr.TerminateOperation(context.TODO(), incoming)
		require.NoError(t, err)
		require.NotNil(t, updated)
		require.NotNil(t, updated.Status.OperationState)
		require.Equal(t, synccommon.OperationTerminating, updated.Status.OperationState.Phase)
	})

	t.Run("should be called only by an agent", func(t *testing.T) {
		mockedBackend := appmock.NewApplication(t)
		mgr, err := NewApplicationManager(mockedBackend, "argocd", WithRole(manager.ManagerRolePrincipal))
		require.NoError(t, err)

		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "foobar"}}

		_, err = mgr.TerminateOperation(context.TODO(), app)
		require.Error(t, err)
		require.ErrorContains(t, err, "TerminateOperation should only be called by an agent")
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

func Test_CompareIdentity(t *testing.T) {
	existingApp := &v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test",
			Namespace: "argocd",
			Annotations: map[string]string{
				manager.SourceUIDAnnotation:    "source-1",
				manager.PrincipalUIDAnnotation: "principal-A",
			},
		},
	}

	t.Run("same principal, same source-uid → update", func(t *testing.T) {
		be := appmock.NewApplication(t)
		be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(existingApp, nil)
		m, err := NewApplicationManager(be, "")
		require.NoError(t, err)

		incoming := existingApp.DeepCopy()
		incoming.UID = ktypes.UID("source-1")

		result, err := m.CompareIdentity(context.Background(), incoming, "principal-A")
		require.NoError(t, err)
		assert.True(t, result.Exists)
		assert.True(t, result.SourceUIDMatch)
		assert.True(t, result.PrincipalUIDMatch)
		assert.False(t, result.PrincipalTransition)
		assert.False(t, result.MissingSourceUID)
	})

	t.Run("same principal, different source-uid → delete/recreate", func(t *testing.T) {
		be := appmock.NewApplication(t)
		be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(existingApp, nil)
		m, err := NewApplicationManager(be, "")
		require.NoError(t, err)

		incoming := existingApp.DeepCopy()
		incoming.UID = ktypes.UID("source-NEW")
		delete(incoming.Annotations, manager.SourceUIDAnnotation)

		result, err := m.CompareIdentity(context.Background(), incoming, "principal-A")
		require.NoError(t, err)
		assert.True(t, result.Exists)
		assert.False(t, result.SourceUIDMatch)
		assert.True(t, result.PrincipalUIDMatch)
		assert.False(t, result.PrincipalTransition)
	})

	t.Run("same principal, missing source-uid on incoming → stamp", func(t *testing.T) {
		be := appmock.NewApplication(t)
		be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(existingApp, nil)
		m, err := NewApplicationManager(be, "")
		require.NoError(t, err)

		incoming := existingApp.DeepCopy()
		incoming.UID = ""
		incoming.Annotations = map[string]string{}

		result, err := m.CompareIdentity(context.Background(), incoming, "principal-A")
		require.NoError(t, err)
		assert.True(t, result.Exists)
		assert.True(t, result.MissingSourceUID)
		assert.True(t, result.PrincipalUIDMatch)
		assert.False(t, result.PrincipalTransition)
	})

	t.Run("different principal → transition", func(t *testing.T) {
		be := appmock.NewApplication(t)
		be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(existingApp, nil)
		m, err := NewApplicationManager(be, "")
		require.NoError(t, err)

		incoming := existingApp.DeepCopy()
		incoming.UID = ktypes.UID("source-NEW")
		delete(incoming.Annotations, manager.SourceUIDAnnotation)

		result, err := m.CompareIdentity(context.Background(), incoming, "principal-B")
		require.NoError(t, err)
		assert.True(t, result.Exists)
		assert.False(t, result.PrincipalUIDMatch)
		assert.True(t, result.PrincipalTransition)
	})

	t.Run("missing principal-uid in event (backward compat) → same principal", func(t *testing.T) {
		be := appmock.NewApplication(t)
		be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(existingApp, nil)
		m, err := NewApplicationManager(be, "")
		require.NoError(t, err)

		incoming := existingApp.DeepCopy()
		incoming.UID = ktypes.UID("source-1")

		result, err := m.CompareIdentity(context.Background(), incoming, "")
		require.NoError(t, err)
		assert.True(t, result.Exists)
		assert.True(t, result.SourceUIDMatch)
		assert.True(t, result.PrincipalUIDMatch)
		assert.False(t, result.PrincipalTransition)
		assert.True(t, result.MissingPrincipalUID)
	})

	t.Run("first event with principal-uid on pre-upgrade resource → adoption", func(t *testing.T) {
		preUpgradeApp := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "argocd",
				Annotations: map[string]string{
					manager.SourceUIDAnnotation: "source-1",
				},
			},
		}
		be := appmock.NewApplication(t)
		be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(preUpgradeApp, nil)
		m, err := NewApplicationManager(be, "")
		require.NoError(t, err)

		incoming := preUpgradeApp.DeepCopy()
		incoming.UID = ktypes.UID("source-1")

		result, err := m.CompareIdentity(context.Background(), incoming, "principal-A")
		require.NoError(t, err)
		assert.True(t, result.Exists)
		assert.True(t, result.SourceUIDMatch)
		assert.True(t, result.PrincipalUIDMatch, "should adopt: existing has no principal-uid")
		assert.False(t, result.PrincipalTransition)
		assert.True(t, result.AdoptedPrincipalUID)
	})

	t.Run("app does not exist", func(t *testing.T) {
		expectedErr := errors.NewNotFound(schema.GroupResource{Group: "argoproj.io", Resource: "application"}, "test")
		be := appmock.NewApplication(t)
		be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedErr)
		m, err := NewApplicationManager(be, "")
		require.NoError(t, err)

		incoming := existingApp.DeepCopy()
		incoming.UID = ktypes.UID("source-1")

		result, err := m.CompareIdentity(context.Background(), incoming, "principal-A")
		require.NoError(t, err)
		assert.False(t, result.Exists)
	})
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
	settings.ConfigureGoClientFeatures()
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

func Test_Upsert_CopiesExistingUID(t *testing.T) {
	existing := &v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{
			Name:            "test-app",
			Namespace:       "default",
			UID:             ktypes.UID("replica-uid"),
			ResourceVersion: "rv-1",
		},
	}
	incoming := &v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			UID:       ktypes.UID("primary-uid"),
		},
	}

	mockedBackend := appmock.NewApplication(t)
	mockedBackend.On("Create", mock.Anything, mock.Anything).Return(nil, appExistsError)
	mockedBackend.On("Get", mock.Anything, "test-app", "default").Return(existing, nil)
	mockedBackend.On("Update", mock.Anything, mock.MatchedBy(func(app *v1alpha1.Application) bool {
		return app.UID == "replica-uid" && app.ResourceVersion == "rv-1"
	})).Return(incoming, nil)
	mockedBackend.On("UpdateIgnoreChange", mock.Anything, mock.Anything).Return(nil).Maybe()

	m, err := NewApplicationManager(mockedBackend, "", WithAllowUpsert(true))
	require.NoError(t, err)
	_, err = m.Upsert(context.Background(), incoming)
	require.NoError(t, err)
	mockedBackend.AssertExpectations(t)
}

func Test_ClearOperationState(t *testing.T) {
	t.Run("Successfully clears operationState", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "guestbook",
				Namespace: "argocd",
			},
			Status: v1alpha1.ApplicationStatus{
				OperationState: &v1alpha1.OperationState{
					Phase: synccommon.OperationSucceeded,
				},
			},
		}
		updatedApp := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "guestbook",
				Namespace: "argocd",
			},
			Status: v1alpha1.ApplicationStatus{},
		}

		mockedBackend := appmock.NewApplication(t)
		mockedBackend.On("Get", mock.Anything, "guestbook", "argocd").Return(app, nil)
		mockedBackend.On("Patch", mock.Anything, "guestbook", "argocd", []byte(`[{"op":"replace","path":"/status/operationState","value":null}]`)).Return(updatedApp, nil)

		m, err := NewApplicationManager(mockedBackend, "argocd")
		require.NoError(t, err)

		err = m.ClearOperationState(context.TODO(), app)
		assert.NoError(t, err)
		mockedBackend.AssertExpectations(t)
	})

	t.Run("Returns error when patch fails", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "guestbook",
				Namespace: "argocd",
			},
		}

		mockedBackend := appmock.NewApplication(t)
		mockedBackend.On("Get", mock.Anything, "guestbook", "argocd").Return(app, nil)
		mockedBackend.On("Patch", mock.Anything, "guestbook", "argocd", []byte(`[{"op":"replace","path":"/status/operationState","value":null}]`)).Return(nil, fmt.Errorf("patch failed"))

		m, err := NewApplicationManager(mockedBackend, "argocd")
		require.NoError(t, err)

		err = m.ClearOperationState(context.TODO(), app)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "patch failed")
		mockedBackend.AssertExpectations(t)
	})
}

// The tests below use a mocked backend, so that every call the manager makes,
// and every object it sends, is checked.

const testNamespace = "argocd"

var errBackend = fmt.Errorf("backend failure")

func principalOpts() []ApplicationManagerOption {
	return []ApplicationManagerOption{WithRole(manager.ManagerRolePrincipal)}
}

func managedAgentOpts() []ApplicationManagerOption {
	return []ApplicationManagerOption{WithRole(manager.ManagerRoleAgent), WithMode(manager.ManagerModeManaged)}
}

func autonomousAgentOpts() []ApplicationManagerOption {
	return []ApplicationManagerOption{WithRole(manager.ManagerRoleAgent), WithMode(manager.ManagerModeAutonomous)}
}

func newTestApp(name string) *v1alpha1.Application {
	return &v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        "https://example.com/repo.git",
				TargetRevision: "HEAD",
				Path:           ".",
			},
			Destination: v1alpha1.ApplicationDestination{Server: "in-cluster", Namespace: "guestbook"},
		},
	}
}

func newMockedAppManager(t *testing.T, opts ...ApplicationManagerOption) (*appmock.Application, *ApplicationManager) {
	t.Helper()
	be := appmock.NewApplication(t)
	m, err := NewApplicationManager(be, testNamespace, opts...)
	require.NoError(t, err)
	return be, m
}

// capturePatch returns the function to give RunAndReturn for a Patch call, which
// stores the patch it was given and answers with the given application.
func capturePatch(sent *[]byte, answer *v1alpha1.Application) func(context.Context, string, string, []byte) (*v1alpha1.Application, error) {
	return func(_ context.Context, _, _ string, patch []byte) (*v1alpha1.Application, error) {
		*sent = patch
		return answer, nil
	}
}

// Fixtures of the Revert tests: an application whose spec drifted from the one
// in the cache of its source.
var revertCachedSpec = v1alpha1.ApplicationSpec{Project: "cached"}

func newDriftedApp() *v1alpha1.Application {
	app := newTestApp("app")
	app.Annotations = map[string]string{manager.SourceUIDAnnotation: "source-uid"}
	app.Spec = v1alpha1.ApplicationSpec{Project: "modified"}
	return app
}

func newSpecCache(spec *v1alpha1.ApplicationSpec) *cache.ResourceCache[v1alpha1.ApplicationSpec] {
	c := cache.NewSourceCache()
	if spec != nil {
		c.Application.Set("source-uid", *spec)
	}
	return c.Application
}

// captureCreate is capturePatch for Create calls, answering with the application
// it was given when answer is nil.
func captureCreate(sent **v1alpha1.Application, answer *v1alpha1.Application) func(context.Context, *v1alpha1.Application) (*v1alpha1.Application, error) {
	return func(_ context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error) {
		*sent = app.DeepCopy()
		if answer == nil {
			return app, nil
		}
		return answer, nil
	}
}

// captureUpdate is capturePatch for Update calls.
func captureUpdate(sent **v1alpha1.Application, answer *v1alpha1.Application) func(context.Context, *v1alpha1.Application) (*v1alpha1.Application, error) {
	return func(_ context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error) {
		*sent = app.DeepCopy()
		return answer, nil
	}
}

// withoutMapOps drops the ops that add a whole labels or annotations map. Some
// patches leave those maps out of what they compare against, so they always
// send the incoming maps in full, which says nothing about what changed.
func withoutMapOps(ops []string) []string {
	var res []string
	for _, op := range ops {
		if op != "add /metadata/labels" && op != "add /metadata/annotations" {
			res = append(res, op)
		}
	}
	return res
}

// patchOps decodes a JSON patch into "<op> <path>" strings.
func patchOps(t *testing.T, patch []byte) []string {
	t.Helper()
	var ops []struct {
		Op   string `json:"op"`
		Path string `json:"path"`
	}
	require.NoError(t, json.Unmarshal(patch, &ops))
	res := make([]string, 0, len(ops))
	for _, op := range ops {
		res = append(res, op.Op+" "+op.Path)
	}
	return res
}

// applyPatch applies a JSON patch to the application and returns what comes
// out of it. A patch that does not apply, such as one that adds below a path
// that is not there, fails the test.
func applyPatch(t *testing.T, app *v1alpha1.Application, patch []byte) *v1alpha1.Application {
	t.Helper()
	appC := fakeappclient.NewSimpleClientset(app)
	patched, err := appC.ArgoprojV1alpha1().Applications(app.Namespace).Patch(context.Background(), app.Name, ktypes.JSONPatchType, patch, v1.PatchOptions{})
	require.NoError(t, err)
	return patched
}

func Test_NewApplicationManager_RoleAndMode(t *testing.T) {
	tests := []struct {
		name    string
		opts    []ApplicationManagerOption
		wantErr bool
	}{
		{name: "principal without a mode", opts: principalOpts()},
		{name: "principal with managed mode", opts: []ApplicationManagerOption{WithRole(manager.ManagerRolePrincipal), WithMode(manager.ManagerModeManaged)}, wantErr: true},
		{name: "principal with autonomous mode", opts: []ApplicationManagerOption{WithRole(manager.ManagerRolePrincipal), WithMode(manager.ManagerModeAutonomous)}, wantErr: true},
		{name: "managed agent", opts: managedAgentOpts()},
		{name: "autonomous agent", opts: autonomousAgentOpts()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewApplicationManager(appmock.NewApplication(t), testNamespace, tt.opts...)

			if tt.wantErr {
				require.ErrorContains(t, err, "mode should be unset")
				assert.Nil(t, m)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, m)
		})
	}
}

// Destination based mapping keeps the namespace of the incoming application,
// otherwise the manager looks it up in its own namespace.
func Test_ApplicationManager_DestinationBasedMapping(t *testing.T) {
	calls := []struct {
		name        string
		opts        []ApplicationManagerOption
		call        func(m *ApplicationManager, app *v1alpha1.Application) error
		wantWhenOff string // namespace looked up when the option is off, when it is not the manager's
	}{
		{
			name: "CompareIdentity",
			call: func(m *ApplicationManager, app *v1alpha1.Application) error {
				_, err := m.CompareIdentity(context.Background(), app, "")
				return err
			},
		},
		{
			name: "UpdateManagedApp",
			opts: managedAgentOpts(),
			call: func(m *ApplicationManager, app *v1alpha1.Application) error {
				_, err := m.UpdateManagedApp(context.Background(), app, ManagedIdentity{})
				return err
			},
		},
		{
			name: "SetOperation",
			opts: managedAgentOpts(),
			call: func(m *ApplicationManager, app *v1alpha1.Application) error {
				_, err := m.SetOperation(context.Background(), app)
				return err
			},
		},
		{
			name: "TerminateOperation",
			opts: managedAgentOpts(),
			call: func(m *ApplicationManager, app *v1alpha1.Application) error {
				_, err := m.TerminateOperation(context.Background(), app)
				return err
			},
		},
		{
			name:        "UpdateStatus",
			opts:        principalOpts(),
			wantWhenOff: "namespace-of-the-agent",
			call: func(m *ApplicationManager, app *v1alpha1.Application) error {
				_, err := m.UpdateStatus(context.Background(), "namespace-of-the-agent", app)
				return err
			},
		},
	}
	for _, tc := range calls {
		for _, enabled := range []bool{true, false} {
			name := fmt.Sprintf("%s with the option %t", tc.name, enabled)
			t.Run(name, func(t *testing.T) {
				wantNamespace := "namespace-of-the-app"
				if !enabled {
					wantNamespace = testNamespace
					if tc.wantWhenOff != "" {
						wantNamespace = tc.wantWhenOff
					}
				}
				be, m := newMockedAppManager(t, append([]ApplicationManagerOption{WithDestinationBasedMapping(enabled)}, tc.opts...)...)
				be.EXPECT().Get(mock.Anything, "app", wantNamespace).Return(nil, errBackend).Once()
				app := newTestApp("app")
				app.Namespace = "namespace-of-the-app"

				err := tc.call(m, app)

				require.ErrorIs(t, err, errBackend)
			})
		}
	}

	// What is written and deleted must go to the namespace that was looked up.
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("UpdateManagedApp writes and deletes with the option %t", enabled), func(t *testing.T) {
			wantNamespace := "namespace-of-the-app"
			if !enabled {
				wantNamespace = testNamespace
			}
			be, m := newMockedAppManager(t, append([]ApplicationManagerOption{WithDestinationBasedMapping(enabled)}, managedAgentOpts()...)...)
			existing := newTestApp("app")
			existing.Namespace = wantNamespace
			app := newTestApp("app")
			app.Namespace = "namespace-of-the-app"
			app.DeletionTimestamp = ptr.To(v1.Now())
			be.EXPECT().Get(mock.Anything, "app", wantNamespace).Return(existing, nil).Once()
			be.EXPECT().SupportsPatch().Return(true).Once()
			be.EXPECT().Patch(mock.Anything, "app", wantNamespace, mock.Anything).Return(existing, nil).Once()
			be.EXPECT().Delete(mock.Anything, "app", wantNamespace, mock.Anything).Return(nil).Once()

			_, err := m.UpdateManagedApp(context.Background(), app, ManagedIdentity{})

			require.NoError(t, err)
		})
	}
}

func Test_ApplicationManager_Create_Details(t *testing.T) {
	newIncoming := func() *v1alpha1.Application {
		app := newTestApp("app")
		app.UID = "uid-1"
		app.ResourceVersion = "7"
		app.Generation = 3
		app.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "admin"}}
		return app
	}

	t.Run("a new application starts without a resource version and generation", func(t *testing.T) {
		be, m := newMockedAppManager(t)
		var sent *v1alpha1.Application
		be.EXPECT().Create(mock.Anything, mock.Anything).RunAndReturn(captureCreate(&sent, nil)).Once()

		_, err := m.Create(context.Background(), newIncoming(), true)

		require.NoError(t, err)
		assert.Empty(t, sent.ResourceVersion)
		assert.Zero(t, sent.Generation)
		assert.Equal(t, "uid-1", sent.Annotations[manager.SourceUIDAnnotation])
	})

	t.Run("on the principal the operation is dropped and the last update is stamped", func(t *testing.T) {
		be, m := newMockedAppManager(t, principalOpts()...)
		var sent *v1alpha1.Application
		be.EXPECT().Create(mock.Anything, mock.Anything).RunAndReturn(captureCreate(&sent, nil)).Once()

		_, err := m.Create(context.Background(), newIncoming(), true)

		require.NoError(t, err)
		assert.Nil(t, sent.Operation)
		require.Contains(t, sent.Annotations, LastUpdatedAnnotation)
		_, err = time.Parse(time.RFC3339Nano, sent.Annotations[LastUpdatedAnnotation])
		assert.NoError(t, err)
	})

	t.Run("on an agent the operation is kept and nothing is stamped", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		var sent *v1alpha1.Application
		be.EXPECT().Create(mock.Anything, mock.Anything).RunAndReturn(captureCreate(&sent, nil)).Once()

		_, err := m.Create(context.Background(), newIncoming(), true)

		require.NoError(t, err)
		require.NotNil(t, sent.Operation)
		assert.Equal(t, "admin", sent.Operation.InitiatedBy.Username)
		assert.NotContains(t, sent.Annotations, LastUpdatedAnnotation)
	})

	t.Run("the created application is managed, and its change ignored only when asked", func(t *testing.T) {
		for _, ignore := range []bool{true, false} {
			t.Run(fmt.Sprintf("ignore the change: %t", ignore), func(t *testing.T) {
				be, m := newMockedAppManager(t)
				created := newTestApp("app")
				created.ResourceVersion = "9"
				be.EXPECT().Create(mock.Anything, mock.Anything).Return(created, nil).Once()

				got, err := m.Create(context.Background(), newTestApp("app"), ignore)

				require.NoError(t, err)
				assert.Same(t, created, got)
				assert.True(t, m.IsManaged("argocd/app"))
				assert.Equal(t, ignore, m.IsChangeIgnored("argocd/app", "9"))
			})
		}
	})

	t.Run("a failed create leaves nothing managed or ignored", func(t *testing.T) {
		be, m := newMockedAppManager(t)
		be.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, errBackend).Once()

		got, err := m.Create(context.Background(), newTestApp("app"), true)

		require.ErrorIs(t, err, errBackend)
		assert.Nil(t, got)
		assert.False(t, m.IsManaged("argocd/app"))
		assert.Zero(t, m.ObservedResources.Len())
	})
}

func Test_ApplicationManager_CreateWithPrincipalUID(t *testing.T) {
	tests := []struct {
		name        string
		principal   string
		annotations map[string]string
		want        map[string]string
	}{
		{
			name:      "the principal uid is stamped next to the source uid",
			principal: "principal-1",
			want: map[string]string{
				manager.PrincipalUIDAnnotation: "principal-1",
				manager.SourceUIDAnnotation:    "uid-1",
			},
		},
		{
			name:        "existing annotations are kept",
			principal:   "principal-1",
			annotations: map[string]string{"keep": "me"},
			want: map[string]string{
				"keep":                         "me",
				manager.PrincipalUIDAnnotation: "principal-1",
				manager.SourceUIDAnnotation:    "uid-1",
			},
		},
		{
			name:        "without a principal uid no annotation is added",
			annotations: map[string]string{"keep": "me"},
			want:        map[string]string{"keep": "me", manager.SourceUIDAnnotation: "uid-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			be, m := newMockedAppManager(t, managedAgentOpts()...)
			var sent *v1alpha1.Application
			created := newTestApp("app")
			created.ResourceVersion = "4"
			be.EXPECT().Create(mock.Anything, mock.Anything).RunAndReturn(captureCreate(&sent, created)).Once()
			app := newTestApp("app")
			app.UID = "uid-1"
			app.Annotations = tt.annotations

			_, err := m.CreateWithPrincipalUID(context.Background(), app, tt.principal)

			require.NoError(t, err)
			assert.Equal(t, tt.want, sent.Annotations)
			assert.True(t, m.IsChangeIgnored("argocd/app", "4"), "the change of a created application is ignored")
		})
	}
}

func Test_ApplicationManager_Upsert_Details(t *testing.T) {
	existsOnCluster := func() *v1alpha1.Application {
		existing := newTestApp("app")
		existing.UID = "replica-uid"
		existing.ResourceVersion = "rv-1"
		existing.Annotations = map[string]string{manager.SourceUIDAnnotation: "stale-uid"}
		return existing
	}
	incoming := func() *v1alpha1.Application {
		app := newTestApp("app")
		app.UID = "primary-uid"
		app.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "admin"}}
		return app
	}

	t.Run("a new application is created and nothing else is called", func(t *testing.T) {
		be, m := newMockedAppManager(t)
		created := newTestApp("app")
		created.ResourceVersion = "3"
		be.EXPECT().Create(mock.Anything, mock.Anything).Return(created, nil).Once()

		got, err := m.Upsert(context.Background(), incoming())

		require.NoError(t, err)
		assert.Same(t, created, got)
		assert.True(t, m.IsChangeIgnored("argocd/app", "3"))
	})

	t.Run("an error other than already exists is returned without updating", func(t *testing.T) {
		be, m := newMockedAppManager(t)
		be.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, errBackend).Once()

		got, err := m.Upsert(context.Background(), incoming())

		require.ErrorIs(t, err, errBackend)
		assert.Nil(t, got)
	})

	t.Run("an application that exists is updated with the primary source uid, not the stale one", func(t *testing.T) {
		be, m := newMockedAppManager(t)
		var sent *v1alpha1.Application
		updated := newTestApp("app")
		updated.ResourceVersion = "rv-2"
		be.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, appExistsError).Once()
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existsOnCluster(), nil).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, updated)).Once()

		got, err := m.Upsert(context.Background(), incoming())

		require.NoError(t, err)
		assert.Same(t, updated, got)
		assert.Equal(t, "primary-uid", sent.Annotations[manager.SourceUIDAnnotation])
		assert.True(t, m.IsChangeIgnored("argocd/app", "rv-2"))
	})

	// Create already drops the operation and stamps the application on the
	// principal before it reports that the application exists, so this checks
	// what reaches the backend, not which of the two does it.
	t.Run("the operation is not sent to the backend by the principal, but is by an agent", func(t *testing.T) {
		tests := []struct {
			name          string
			opts          []ApplicationManagerOption
			wantOperation bool
		}{
			{name: "principal", opts: principalOpts()},
			{name: "agent", opts: managedAgentOpts(), wantOperation: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				be, m := newMockedAppManager(t, tt.opts...)
				var sent *v1alpha1.Application
				be.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, appExistsError).Once()
				be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existsOnCluster(), nil).Once()
				be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, newTestApp("app"))).Once()

				_, err := m.Upsert(context.Background(), incoming())

				require.NoError(t, err)
				assert.Equal(t, tt.wantOperation, sent.Operation != nil)
				assert.Equal(t, !tt.wantOperation, sent.Annotations[LastUpdatedAnnotation] != "", "only the principal stamps")
			})
		}
	})

	t.Run("a failure to read the existing application is returned", func(t *testing.T) {
		be, m := newMockedAppManager(t)
		be.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, appExistsError).Once()
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(nil, errBackend).Once()

		got, err := m.Upsert(context.Background(), incoming())

		require.ErrorIs(t, err, errBackend)
		assert.Nil(t, got)
	})

	t.Run("a failed update is returned and its change is not ignored", func(t *testing.T) {
		be, m := newMockedAppManager(t)
		be.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, appExistsError).Once()
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existsOnCluster(), nil).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).Return(nil, errBackend).Once()

		got, err := m.Upsert(context.Background(), incoming())

		require.ErrorIs(t, err, errBackend)
		assert.Nil(t, got)
		assert.Zero(t, m.ObservedResources.Len())
	})

	t.Run("the update is retried after a conflict, with the new resource version", func(t *testing.T) {
		be, m := newMockedAppManager(t)
		conflict := errors.NewConflict(schema.GroupResource{Group: "argoproj.io", Resource: "applications"}, "app", fmt.Errorf("the object was modified"))
		first, second := existsOnCluster(), existsOnCluster()
		second.ResourceVersion = "rv-2"
		var versions []string
		be.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, appExistsError).Once()
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(first, nil).Once()
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(second, nil).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(
			func(_ context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error) {
				versions = append(versions, app.ResourceVersion)
				if len(versions) == 1 {
					return nil, conflict
				}
				return app, nil
			}).Twice()

		_, err := m.Upsert(context.Background(), incoming())

		require.NoError(t, err)
		assert.Equal(t, []string{"rv-1", "rv-2"}, versions)
	})
}

func Test_ApplicationManager_UpdateManagedApp_Details(t *testing.T) {
	newExisting := func() *v1alpha1.Application {
		app := newTestApp("app")
		app.Labels = map[string]string{"a": "1"}
		app.Annotations = map[string]string{
			"argocd.argoproj.io/refresh": "hard",
			manager.SourceUIDAnnotation:  "source-uid",
			"old":                        "annotation",
		}
		app.Finalizers = []string{"old-finalizer"}
		app.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "old"}}
		app.Status.Sync.Status = v1alpha1.SyncStatusCodeSynced
		return app
	}
	forUpdate := mock.MatchedBy(func(ctx context.Context) bool {
		v, _ := ctx.Value(backend.ForUpdateContextKey).(bool)
		return v
	})

	t.Run("only a managed agent can update a managed application", func(t *testing.T) {
		tests := []struct {
			name string
			opts []ApplicationManagerOption
		}{
			{name: "principal", opts: principalOpts()},
			{name: "autonomous agent", opts: autonomousAgentOpts()},
			{name: "no role", opts: nil},
			{name: "managed mode without a role", opts: []ApplicationManagerOption{WithMode(manager.ManagerModeManaged)}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, m := newMockedAppManager(t, tt.opts...)

				got, err := m.UpdateManagedApp(context.Background(), newTestApp("app"), ManagedIdentity{})

				require.ErrorContains(t, err, "managed agent")
				assert.Nil(t, got)
			})
		}
	})

	t.Run("without patch support the application takes over what the incoming one carries", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		incoming := newTestApp("app")
		incoming.Labels = map[string]string{"new": "label"}
		incoming.Annotations = map[string]string{"new": "annotation"}
		incoming.Finalizers = []string{"new-finalizer"}
		incoming.Spec.Project = "other"
		incoming.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "new"}}
		incoming.Status.Sync.Status = v1alpha1.SyncStatusCodeOutOfSync
		var sent *v1alpha1.Application
		updated := newTestApp("app")
		updated.ResourceVersion = "8"
		be.EXPECT().Get(forUpdate, "app", testNamespace).Return(newExisting(), nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, updated)).Once()

		got, err := m.UpdateManagedApp(context.Background(), incoming, ManagedIdentity{})

		require.NoError(t, err)
		assert.Same(t, updated, got)
		assert.Equal(t, map[string]string{"new": "label"}, sent.Labels)
		assert.Equal(t, []string{"new-finalizer"}, sent.Finalizers)
		assert.Equal(t, "other", sent.Spec.Project)
		require.NotNil(t, sent.Operation)
		assert.Equal(t, "new", sent.Operation.InitiatedBy.Username)
		assert.Equal(t, map[string]string{
			"new":                        "annotation",
			"argocd.argoproj.io/refresh": "hard",       // removed by the controller, not by us
			manager.SourceUIDAnnotation:  "source-uid", // never taken from the incoming application
		}, sent.Annotations)
		assert.Equal(t, v1alpha1.SyncStatusCodeSynced, sent.Status.Sync.Status, "the status belongs to the agent")
		assert.True(t, m.IsChangeIgnored("argocd/app", "8"))
	})

	t.Run("with patch support only what changed is patched", func(t *testing.T) {
		tests := []struct {
			name    string
			mutate  func(incoming *v1alpha1.Application)
			wantOps []string
		}{
			{
				name:    "labels",
				mutate:  func(a *v1alpha1.Application) { a.Labels["a"] = "2" },
				wantOps: []string{"replace /metadata/labels/a"},
			},
			{
				name:    "spec",
				mutate:  func(a *v1alpha1.Application) { a.Spec.Project = "other" },
				wantOps: []string{"replace /spec/project"},
			},
			{
				name:    "finalizers",
				mutate:  func(a *v1alpha1.Application) { a.Finalizers = []string{"new-finalizer"} },
				wantOps: []string{"replace /metadata/finalizers/0"},
			},
			{
				name: "operation",
				mutate: func(a *v1alpha1.Application) {
					a.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "new"}}
				},
				wantOps: []string{"replace /operation/initiatedBy/username"},
			},
			{
				name:    "annotations",
				mutate:  func(a *v1alpha1.Application) { a.Annotations["new"] = "annotation" },
				wantOps: []string{"add /metadata/annotations/new"},
			},
			{
				name:   "the status is never patched",
				mutate: func(a *v1alpha1.Application) { a.Status.Sync.Status = v1alpha1.SyncStatusCodeOutOfSync },
			},
			{
				name:   "the source uid on the cluster wins over the incoming one",
				mutate: func(a *v1alpha1.Application) { a.Annotations[manager.SourceUIDAnnotation] = "other-uid" },
			},
			{
				name:   "the refresh annotation on the cluster is kept",
				mutate: func(a *v1alpha1.Application) { delete(a.Annotations, "argocd.argoproj.io/refresh") },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				be, m := newMockedAppManager(t, managedAgentOpts()...)
				incoming := newExisting()
				tt.mutate(incoming)
				var sent []byte
				updated := newTestApp("app")
				updated.ResourceVersion = "8"
				be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newExisting(), nil).Once()
				be.EXPECT().SupportsPatch().Return(true).Once()
				be.EXPECT().Patch(mock.Anything, "app", testNamespace, mock.Anything).RunAndReturn(capturePatch(&sent, updated)).Once()

				got, err := m.UpdateManagedApp(context.Background(), incoming, ManagedIdentity{})

				require.NoError(t, err)
				assert.Same(t, updated, got)
				assert.ElementsMatch(t, tt.wantOps, patchOps(t, sent))
				// A patch must carry the incoming values over, and no patch must
				// leave the application as it is on the cluster.
				want := newExisting()
				if tt.wantOps != nil {
					tt.mutate(want)
				}
				assert.Equal(t, want, applyPatch(t, newExisting(), sent))
				assert.True(t, m.IsChangeIgnored("argocd/app", "8"))
			})
		}
	})

	t.Run("the identity annotations are resolved without a patch", func(t *testing.T) {
		tests := []struct {
			name     string
			existing map[string]string
			incoming map[string]string
			identity ManagedIdentity
			want     map[string]string
		}{
			{
				name:     "an explicit identity wins over what the applications carry",
				existing: map[string]string{manager.SourceUIDAnnotation: "existing-source", manager.PrincipalUIDAnnotation: "existing-principal"},
				incoming: map[string]string{manager.SourceUIDAnnotation: "incoming-source", manager.PrincipalUIDAnnotation: "incoming-principal"},
				identity: ManagedIdentity{SourceUID: "explicit-source", PrincipalUID: "explicit-principal"},
				want:     map[string]string{manager.SourceUIDAnnotation: "explicit-source", manager.PrincipalUIDAnnotation: "explicit-principal"},
			},
			{
				name:     "the existing application wins over the incoming one",
				existing: map[string]string{manager.SourceUIDAnnotation: "existing-source", manager.PrincipalUIDAnnotation: "existing-principal"},
				incoming: map[string]string{manager.SourceUIDAnnotation: "incoming-source", manager.PrincipalUIDAnnotation: "incoming-principal"},
				want:     map[string]string{manager.SourceUIDAnnotation: "existing-source", manager.PrincipalUIDAnnotation: "existing-principal"},
			},
			{
				name:     "the incoming application fills in what the existing one lacks",
				incoming: map[string]string{manager.SourceUIDAnnotation: "incoming-source", manager.PrincipalUIDAnnotation: "incoming-principal"},
				want:     map[string]string{manager.SourceUIDAnnotation: "incoming-source", manager.PrincipalUIDAnnotation: "incoming-principal"},
			},
			{
				name: "nothing is stamped when nothing is known",
				want: map[string]string{},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				be, m := newMockedAppManager(t, managedAgentOpts()...)
				existing, incoming := newExisting(), newTestApp("app")
				existing.Annotations = tt.existing
				incoming.Annotations = tt.incoming
				var sent *v1alpha1.Application
				be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existing, nil).Once()
				be.EXPECT().SupportsPatch().Return(false).Once()
				be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, newTestApp("app"))).Once()

				_, err := m.UpdateManagedApp(context.Background(), incoming, tt.identity)

				require.NoError(t, err)
				assert.Equal(t, tt.want, sent.Annotations)
			})
		}
	})

	t.Run("an application that is deleted on the principal is deleted in the foreground", func(t *testing.T) {
		now := v1.Now()
		foreground := mock.MatchedBy(func(p *backend.DeletionPropagation) bool {
			return p != nil && *p == backend.DeletePropagationForeground
		})
		for _, patching := range []bool{false, true} {
			t.Run(fmt.Sprintf("patch support: %t", patching), func(t *testing.T) {
				tracker := manager.NewDeletionTracker()
				be, m := newMockedAppManager(t, append([]ApplicationManagerOption{WithDeletionTracker(tracker)}, managedAgentOpts()...)...)
				incoming := newExisting()
				incoming.DeletionTimestamp = &now
				updated := newExisting()
				be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newExisting(), nil).Once()
				be.EXPECT().SupportsPatch().Return(patching).Once()
				if patching {
					be.EXPECT().Patch(mock.Anything, "app", testNamespace, mock.Anything).Return(updated, nil).Once()
				} else {
					be.EXPECT().Update(mock.Anything, mock.Anything).Return(updated, nil).Once()
				}
				be.EXPECT().Delete(mock.Anything, "app", testNamespace, foreground).Return(nil).Once()

				got, err := m.UpdateManagedApp(context.Background(), incoming, ManagedIdentity{})

				require.NoError(t, err)
				assert.Same(t, updated, got)
				assert.True(t, tracker.RemoveExpected("source-uid"), "the deletion must be marked as expected")
			})
		}

		t.Run("it works without a deletion tracker", func(t *testing.T) {
			be, m := newMockedAppManager(t, managedAgentOpts()...)
			incoming := newExisting()
			incoming.DeletionTimestamp = &now
			be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newExisting(), nil).Once()
			be.EXPECT().SupportsPatch().Return(false).Once()
			be.EXPECT().Update(mock.Anything, mock.Anything).Return(newExisting(), nil).Once()
			be.EXPECT().Delete(mock.Anything, "app", testNamespace, foreground).Return(nil).Once()

			_, err := m.UpdateManagedApp(context.Background(), incoming, ManagedIdentity{})

			require.NoError(t, err)
		})

		for _, patching := range []bool{false, true} {
			t.Run(fmt.Sprintf("a failed write is returned and the application still deleted, patch support: %t", patching), func(t *testing.T) {
				be, m := newMockedAppManager(t, managedAgentOpts()...)
				incoming := newExisting()
				incoming.DeletionTimestamp = &now
				be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newExisting(), nil).Once()
				be.EXPECT().SupportsPatch().Return(patching).Once()
				if patching {
					be.EXPECT().Patch(mock.Anything, "app", testNamespace, mock.Anything).Return(nil, errBackend).Once()
				} else {
					be.EXPECT().Update(mock.Anything, mock.Anything).Return(nil, errBackend).Once()
				}
				be.EXPECT().Delete(mock.Anything, "app", testNamespace, foreground).Return(nil).Once()

				got, err := m.UpdateManagedApp(context.Background(), incoming, ManagedIdentity{})

				require.ErrorIs(t, err, errBackend)
				assert.Nil(t, got)
				assert.Zero(t, m.ObservedResources.Len())
			})
		}

		t.Run("an application that is already being deleted is not deleted again", func(t *testing.T) {
			be, m := newMockedAppManager(t, managedAgentOpts()...)
			incoming := newExisting()
			incoming.DeletionTimestamp = &now
			existing := newExisting()
			existing.DeletionTimestamp = &now
			be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existing, nil).Once()
			be.EXPECT().SupportsPatch().Return(false).Once()
			be.EXPECT().Update(mock.Anything, mock.Anything).Return(newExisting(), nil).Once()

			_, err := m.UpdateManagedApp(context.Background(), incoming, ManagedIdentity{})

			require.NoError(t, err)
		})

		t.Run("a failed delete is returned", func(t *testing.T) {
			be, m := newMockedAppManager(t, managedAgentOpts()...)
			incoming := newExisting()
			incoming.DeletionTimestamp = &now
			be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newExisting(), nil).Once()
			be.EXPECT().SupportsPatch().Return(false).Once()
			be.EXPECT().Update(mock.Anything, mock.Anything).Return(newExisting(), nil).Once()
			be.EXPECT().Delete(mock.Anything, "app", testNamespace, foreground).Return(errBackend).Once()

			got, err := m.UpdateManagedApp(context.Background(), incoming, ManagedIdentity{})

			require.ErrorIs(t, err, errBackend)
			assert.Nil(t, got)
		})
	})

	t.Run("an application that does not exist", func(t *testing.T) {
		t.Run("is an error when upsert is not allowed", func(t *testing.T) {
			be, m := newMockedAppManager(t, managedAgentOpts()...)
			be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(nil, appNotFoundError).Once()

			got, err := m.UpdateManagedApp(context.Background(), newTestApp("app"), ManagedIdentity{})

			require.ErrorIs(t, err, appNotFoundError)
			assert.Nil(t, got)
		})

		t.Run("is created when upsert is allowed", func(t *testing.T) {
			be, m := newMockedAppManager(t, append([]ApplicationManagerOption{WithAllowUpsert(true)}, managedAgentOpts()...)...)
			created := newTestApp("app")
			created.ResourceVersion = "1"
			be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(nil, appNotFoundError).Once()
			be.EXPECT().Create(mock.Anything, mock.Anything).Return(created, nil).Once()

			got, err := m.UpdateManagedApp(context.Background(), newTestApp("app"), ManagedIdentity{})

			require.NoError(t, err)
			assert.Same(t, created, got)
			assert.True(t, m.IsChangeIgnored("argocd/app", "1"))
		})
	})

	t.Run("the update is retried after a conflict", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		conflict := errors.NewConflict(schema.GroupResource{Group: "argoproj.io", Resource: "applications"}, "app", fmt.Errorf("the object was modified"))
		updated := newTestApp("app")
		updated.ResourceVersion = "9"
		first, second := newExisting(), newExisting()
		first.ResourceVersion, second.ResourceVersion = "rv-1", "rv-2"
		var versions []string
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(first, nil).Once()
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(second, nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Twice()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(
			func(_ context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error) {
				versions = append(versions, app.ResourceVersion)
				if len(versions) == 1 {
					return nil, conflict
				}
				return updated, nil
			}).Twice()

		got, err := m.UpdateManagedApp(context.Background(), newTestApp("app"), ManagedIdentity{})

		require.NoError(t, err)
		assert.Same(t, updated, got)
		assert.Equal(t, []string{"rv-1", "rv-2"}, versions)
	})

	t.Run("a failed update is returned and its change not ignored", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newExisting(), nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).Return(nil, errBackend).Once()

		got, err := m.UpdateManagedApp(context.Background(), newTestApp("app"), ManagedIdentity{})

		require.ErrorIs(t, err, errBackend)
		assert.Nil(t, got)
		assert.Zero(t, m.ObservedResources.Len())
	})
}

func Test_ApplicationManager_CompareIdentity_Details(t *testing.T) {
	t.Run("a backend failure is returned", func(t *testing.T) {
		be, m := newMockedAppManager(t)
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(nil, errBackend).Once()

		got, err := m.CompareIdentity(context.Background(), newTestApp("app"), "principal-1")

		require.ErrorIs(t, err, errBackend)
		assert.Nil(t, got)
	})

	t.Run("an existing application without a source uid is reported as such", func(t *testing.T) {
		be, m := newMockedAppManager(t)
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newTestApp("app"), nil).Once()

		got, err := m.CompareIdentity(context.Background(), newTestApp("app"), "")

		require.NoError(t, err)
		assert.Equal(t, &IdentityCompareResult{
			Exists:                   true,
			ExistingMissingSourceUID: true,
			MissingPrincipalUID:      true,
		}, got)
	})
}

func Test_preservePrincipalAnnotations(t *testing.T) {
	tests := []struct {
		name     string
		existing map[string]string
		incoming map[string]string
		want     map[string]string
	}{
		{
			name: "the annotations are added when the incoming application has none",
			existing: map[string]string{
				manager.SourceUIDAnnotation: "source-uid",
				manager.NotifiedAnnotation:  "notified",
			},
			want: map[string]string{
				manager.SourceUIDAnnotation: "source-uid",
				manager.NotifiedAnnotation:  "notified",
			},
		},
		{
			name:     "the value on the principal wins over the one of the agent",
			existing: map[string]string{manager.SourceUIDAnnotation: "principal-value"},
			incoming: map[string]string{manager.SourceUIDAnnotation: "agent-value", "other": "kept"},
			want:     map[string]string{manager.SourceUIDAnnotation: "principal-value", "other": "kept"},
		},
		{
			name:     "other annotations of the existing application are not carried over",
			existing: map[string]string{"unrelated": "value"},
			incoming: map[string]string{"other": "kept"},
			want:     map[string]string{"other": "kept"},
		},
		{
			name:     "nothing is added when the existing application has none",
			incoming: map[string]string{"other": "kept"},
			want:     map[string]string{"other": "kept"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing, incoming := newTestApp("app"), newTestApp("app")
			existing.Annotations = tt.existing
			incoming.Annotations = tt.incoming

			preservePrincipalAnnotations(existing, incoming)

			assert.Equal(t, tt.want, incoming.Annotations)
		})
	}
}

func Test_ApplicationManager_UpdateAutonomousApp_Details(t *testing.T) {
	deletedAt := v1.NewTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	laterDeletedAt := v1.NewTime(deletedAt.Add(time.Hour))

	t.Run("only the principal can update an autonomous application", func(t *testing.T) {
		for name, opts := range map[string][]ApplicationManagerOption{"managed agent": managedAgentOpts(), "no role": nil} {
			t.Run(name, func(t *testing.T) {
				_, m := newMockedAppManager(t, opts...)

				got, err := m.UpdateAutonomousApp(context.Background(), "agent-ns", newTestApp("app"))

				require.ErrorContains(t, err, "only be called from principal")
				assert.Nil(t, got)
			})
		}
	})

	t.Run("the application is looked up in the namespace of the agent and created when missing", func(t *testing.T) {
		be, m := newMockedAppManager(t, principalOpts()...)
		created := newTestApp("app")
		created.Namespace = "agent-ns"
		created.ResourceVersion = "1"
		var sent *v1alpha1.Application
		be.EXPECT().Get(mock.Anything, "app", "agent-ns").Return(nil, appNotFoundError).Once()
		be.EXPECT().Create(mock.Anything, mock.Anything).RunAndReturn(captureCreate(&sent, created)).Once()

		got, err := m.UpdateAutonomousApp(context.Background(), "agent-ns", newTestApp("app"))

		require.NoError(t, err)
		assert.Same(t, created, got)
		assert.Equal(t, "agent-ns", sent.Namespace)
		assert.True(t, m.IsChangeIgnored("agent-ns/app", "1"))
	})

	t.Run("without patch support", func(t *testing.T) {
		tests := []struct {
			name           string
			existingDelete *v1.Time
			incomingDelete *v1.Time
			wantDelete     *v1.Time
		}{
			{name: "the deletion of the agent is taken over", incomingDelete: &deletedAt, wantDelete: &deletedAt},
			{name: "a deletion that is already underway is not postponed", existingDelete: &deletedAt, incomingDelete: &laterDeletedAt, wantDelete: &deletedAt},
			{name: "a deletion underway is not undone", existingDelete: &deletedAt, wantDelete: &deletedAt},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				be, m := newMockedAppManager(t, principalOpts()...)
				existing, incoming := newTestApp("app"), newTestApp("app")
				existing.DeletionTimestamp = tt.existingDelete
				incoming.DeletionTimestamp = tt.incomingDelete
				var sent *v1alpha1.Application
				be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existing, nil).Once()
				be.EXPECT().SupportsPatch().Return(false).Once()
				be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, newTestApp("app"))).Once()

				_, err := m.UpdateAutonomousApp(context.Background(), testNamespace, incoming)

				require.NoError(t, err)
				assert.Equal(t, tt.wantDelete, sent.DeletionTimestamp)
			})
		}
	})

	t.Run("with patch support the deletion of the agent is patched in", func(t *testing.T) {
		be, m := newMockedAppManager(t, principalOpts()...)
		incoming := newTestApp("app")
		incoming.DeletionTimestamp = &deletedAt
		incoming.DeletionGracePeriodSeconds = ptr.To(int64(30))
		var sent []byte
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newTestApp("app"), nil).Once()
		be.EXPECT().SupportsPatch().Return(true).Once()
		be.EXPECT().Patch(mock.Anything, "app", testNamespace, mock.Anything).RunAndReturn(capturePatch(&sent, newTestApp("app"))).Once()

		_, err := m.UpdateAutonomousApp(context.Background(), testNamespace, incoming)

		require.NoError(t, err)
		ops := patchOps(t, sent)
		assert.Contains(t, ops, "add /metadata/deletionTimestamp")
		assert.Contains(t, ops, "add /metadata/deletionGracePeriodSeconds")
	})
}

func Test_ApplicationManager_UpdateAutonomousApp_Takeover(t *testing.T) {
	deletedAt := v1.NewTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	newExisting := func() *v1alpha1.Application {
		app := newTestApp("app")
		app.Labels = map[string]string{"old": "label"}
		app.Annotations = map[string]string{
			manager.SourceUIDAnnotation: "principal-source",
			manager.NotifiedAnnotation:  "notified",
			"old":                       "annotation",
		}
		app.Finalizers = []string{"old-finalizer"}
		app.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "old"}}
		app.Status.Sync.Status = v1alpha1.SyncStatusCodeSynced
		return app
	}
	newIncoming := func() *v1alpha1.Application {
		app := newTestApp("app")
		app.Labels = map[string]string{"new": "label"}
		app.Annotations = map[string]string{"new": "annotation"}
		app.Finalizers = []string{"new-finalizer"}
		app.Spec.Project = "other"
		app.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "new"}}
		app.Status.Sync.Status = v1alpha1.SyncStatusCodeOutOfSync
		return app
	}

	t.Run("without patch support the application takes over what the agent reports", func(t *testing.T) {
		be, m := newMockedAppManager(t, principalOpts()...)
		incoming := newIncoming()
		incoming.DeletionTimestamp = &deletedAt
		incoming.DeletionGracePeriodSeconds = ptr.To(int64(30))
		var sent *v1alpha1.Application
		updated := newTestApp("app")
		updated.ResourceVersion = "7"
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newExisting(), nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, updated)).Once()

		got, err := m.UpdateAutonomousApp(context.Background(), testNamespace, incoming)

		require.NoError(t, err)
		assert.Same(t, updated, got)
		assert.Equal(t, map[string]string{"new": "label"}, sent.Labels)
		assert.Equal(t, []string{"new-finalizer"}, sent.Finalizers)
		assert.Equal(t, "other", sent.Spec.Project)
		assert.Equal(t, v1alpha1.SyncStatusCodeOutOfSync, sent.Status.Sync.Status)
		require.NotNil(t, sent.Operation)
		assert.Equal(t, "new", sent.Operation.InitiatedBy.Username)
		assert.Equal(t, &deletedAt, sent.DeletionTimestamp)
		assert.Equal(t, ptr.To(int64(30)), sent.DeletionGracePeriodSeconds)
		assert.Equal(t, "annotation", sent.Annotations["new"])
		assert.NotContains(t, sent.Annotations, "old")
		assert.Equal(t, "principal-source", sent.Annotations[manager.SourceUIDAnnotation], "kept from the principal")
		assert.Equal(t, "notified", sent.Annotations[manager.NotifiedAnnotation], "kept from the principal")
		assert.Contains(t, sent.Annotations, LastUpdatedAnnotation)
		assert.True(t, m.IsChangeIgnored("argocd/app", "7"))
	})

	t.Run("a grace period that is already running is not extended", func(t *testing.T) {
		be, m := newMockedAppManager(t, principalOpts()...)
		existing := newExisting()
		existing.DeletionTimestamp = &deletedAt
		existing.DeletionGracePeriodSeconds = ptr.To(int64(10))
		incoming := newIncoming()
		incoming.DeletionTimestamp = &deletedAt
		incoming.DeletionGracePeriodSeconds = ptr.To(int64(30))
		var sent *v1alpha1.Application
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existing, nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, newTestApp("app"))).Once()

		_, err := m.UpdateAutonomousApp(context.Background(), testNamespace, incoming)

		require.NoError(t, err)
		assert.Equal(t, ptr.To(int64(10)), sent.DeletionGracePeriodSeconds)
	})

	t.Run("with patch support only what the agent changed is patched", func(t *testing.T) {
		tests := []struct {
			name    string
			mutate  func(incoming *v1alpha1.Application)
			wantOps []string
		}{
			{
				name: "an application that did not change",
				mutate: func(a *v1alpha1.Application) {
					*a = *newExisting()
					delete(a.Annotations, manager.SourceUIDAnnotation)
					delete(a.Annotations, manager.NotifiedAnnotation)
				},
				wantOps: nil,
			},
			{
				name: "labels and annotations",
				mutate: func(a *v1alpha1.Application) {
					a.Labels = map[string]string{"new": "label"}
					a.Annotations = map[string]string{"new": "annotation"}
				},
				wantOps: nil,
			},
			{
				name:    "finalizers",
				mutate:  func(a *v1alpha1.Application) { a.Finalizers = []string{"new-finalizer"} },
				wantOps: []string{"replace /metadata/finalizers/0"},
			},
			{
				name:    "spec",
				mutate:  func(a *v1alpha1.Application) { a.Spec.Project = "other" },
				wantOps: []string{"replace /spec/project"},
			},
			{
				name:    "status",
				mutate:  func(a *v1alpha1.Application) { a.Status.Sync.Status = v1alpha1.SyncStatusCodeOutOfSync },
				wantOps: []string{"replace /status/sync/status"},
			},
			{
				name: "operation",
				mutate: func(a *v1alpha1.Application) {
					a.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "new"}}
				},
				wantOps: []string{"replace /operation/initiatedBy/username"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				be, m := newMockedAppManager(t, principalOpts()...)
				incoming := newExisting()
				// What the agent reports does not know the annotations of the principal.
				delete(incoming.Annotations, manager.SourceUIDAnnotation)
				delete(incoming.Annotations, manager.NotifiedAnnotation)
				tt.mutate(incoming)
				var sent []byte
				be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newExisting(), nil).Once()
				be.EXPECT().SupportsPatch().Return(true).Once()
				be.EXPECT().Patch(mock.Anything, "app", testNamespace, mock.Anything).RunAndReturn(capturePatch(&sent, newTestApp("app"))).Once()

				_, err := m.UpdateAutonomousApp(context.Background(), testNamespace, incoming)

				require.NoError(t, err)
				assert.ElementsMatch(t, tt.wantOps, withoutMapOps(patchOps(t, sent)))
				patched := applyPatch(t, newExisting(), sent)
				assert.Equal(t, incoming.Labels, patched.Labels)
				assert.Equal(t, incoming.Annotations, patched.Annotations)
				assert.Equal(t, "principal-source", patched.Annotations[manager.SourceUIDAnnotation], "kept from the principal")
				assert.Equal(t, "notified", patched.Annotations[manager.NotifiedAnnotation], "kept from the principal")
				assert.Equal(t, incoming.Finalizers, patched.Finalizers)
				assert.Equal(t, incoming.Spec, patched.Spec)
				assert.Equal(t, incoming.Status, patched.Status)
				assert.Equal(t, incoming.Operation, patched.Operation)
			})
		}
	})

	t.Run("a failure other than not found does not create the application", func(t *testing.T) {
		be, m := newMockedAppManager(t, principalOpts()...)
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(nil, errBackend).Once()

		got, err := m.UpdateAutonomousApp(context.Background(), testNamespace, newIncoming())

		require.ErrorIs(t, err, errBackend)
		assert.Nil(t, got)
	})
}

func Test_ApplicationManager_UpdateStatus_Details(t *testing.T) {
	const refreshPath = "/metadata/annotations/argocd.argoproj.io~1refresh"
	syncedStatus := func(status v1alpha1.SyncStatusCode) v1alpha1.ApplicationStatus {
		return v1alpha1.ApplicationStatus{Sync: v1alpha1.SyncStatus{Status: status}}
	}

	t.Run("only the principal can update the status", func(t *testing.T) {
		_, m := newMockedAppManager(t, managedAgentOpts()...)

		got, err := m.UpdateStatus(context.Background(), testNamespace, newTestApp("app"))

		require.ErrorContains(t, err, "only be called on principal")
		assert.Nil(t, got)
	})

	t.Run("an application that does not exist is not created", func(t *testing.T) {
		be, m := newMockedAppManager(t, principalOpts()...)
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(nil, appNotFoundError).Once()

		got, err := m.UpdateStatus(context.Background(), testNamespace, newTestApp("app"))

		require.ErrorIs(t, err, appNotFoundError)
		assert.Nil(t, got)
	})

	t.Run("without patch support the status is taken over and the operation cleared", func(t *testing.T) {
		be, m := newMockedAppManager(t, principalOpts()...)
		existing := newTestApp("app")
		existing.Annotations = map[string]string{manager.SourceUIDAnnotation: "principal-source"}
		existing.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "admin"}}
		existing.Status = syncedStatus(v1alpha1.SyncStatusCodeSynced)
		incoming := newTestApp("app")
		incoming.Spec.Project = "other"
		incoming.Labels = map[string]string{"new": "label"}
		incoming.Status = syncedStatus(v1alpha1.SyncStatusCodeOutOfSync)
		var sent *v1alpha1.Application
		updated := newTestApp("app")
		updated.ResourceVersion = "5"
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existing, nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, updated)).Once()

		got, err := m.UpdateStatus(context.Background(), testNamespace, incoming)

		require.NoError(t, err)
		assert.Same(t, updated, got)
		assert.Equal(t, v1alpha1.SyncStatusCodeOutOfSync, sent.Status.Sync.Status)
		assert.Nil(t, sent.Operation)
		assert.Equal(t, map[string]string{"new": "label"}, sent.Labels)
		assert.Equal(t, "default", sent.Spec.Project, "the spec is not changed by a status update")
		assert.Contains(t, sent.Annotations, LastUpdatedAnnotation)
		assert.Equal(t, "principal-source", sent.Annotations[manager.SourceUIDAnnotation], "kept from the principal")
		assert.True(t, m.IsChangeIgnored("argocd/app", "5"))
	})

	t.Run("with patch support", func(t *testing.T) {
		tests := []struct {
			name             string
			existingRefresh  bool
			incomingRefresh  bool
			existingStatus   v1alpha1.ApplicationStatus
			incomingStatus   v1alpha1.ApplicationStatus
			wantOps          []string
			wantFirstReplace bool
		}{
			{
				name:             "a new application gets its status initialized first",
				incomingRefresh:  true,
				incomingStatus:   syncedStatus(v1alpha1.SyncStatusCodeOutOfSync),
				wantOps:          []string{"replace /status", "add " + refreshPath, "replace /status/sync/status"},
				wantFirstReplace: true,
			},
			{
				name:            "the refresh annotation is removed when the incoming application has none",
				existingRefresh: true,
				existingStatus:  syncedStatus(v1alpha1.SyncStatusCodeSynced),
				incomingStatus:  syncedStatus(v1alpha1.SyncStatusCodeSynced),
				wantOps:         []string{"remove " + refreshPath},
			},
			{
				name:            "the refresh annotation is left alone when both have it",
				existingRefresh: true,
				incomingRefresh: true,
				existingStatus:  syncedStatus(v1alpha1.SyncStatusCodeSynced),
				incomingStatus:  syncedStatus(v1alpha1.SyncStatusCodeSynced),
			},
			{
				name:           "only a changed status is patched",
				existingStatus: syncedStatus(v1alpha1.SyncStatusCodeSynced),
				incomingStatus: syncedStatus(v1alpha1.SyncStatusCodeOutOfSync),
				wantOps:        []string{"replace /status/sync/status"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				be, m := newMockedAppManager(t, principalOpts()...)
				existing, incoming := newTestApp("app"), newTestApp("app")
				existing.Status, incoming.Status = tt.existingStatus, tt.incomingStatus
				// The refresh annotation is added below /metadata/annotations, which
				// only applies to an application that has annotations already.
				existing.Annotations = map[string]string{"some": "annotation"}
				if tt.existingRefresh {
					existing.Annotations["argocd.argoproj.io/refresh"] = "normal"
				}
				if tt.incomingRefresh {
					incoming.Annotations = map[string]string{"argocd.argoproj.io/refresh": "normal"}
				}
				var sent []byte
				be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existing.DeepCopy(), nil).Once()
				be.EXPECT().SupportsPatch().Return(true).Once()
				be.EXPECT().Patch(mock.Anything, "app", testNamespace, mock.Anything).RunAndReturn(capturePatch(&sent, newTestApp("app"))).Once()

				_, err := m.UpdateStatus(context.Background(), testNamespace, incoming)

				require.NoError(t, err)
				ops := patchOps(t, sent)
				assert.ElementsMatch(t, tt.wantOps, ops)
				if tt.wantFirstReplace {
					assert.Equal(t, "replace /status", ops[0], "the status must be initialized before it is patched")
				}
				patched := applyPatch(t, existing, sent)
				assert.Equal(t, tt.incomingStatus, patched.Status)
				wantAnnotations := map[string]string{"some": "annotation"}
				if tt.incomingRefresh {
					wantAnnotations["argocd.argoproj.io/refresh"] = "normal"
				}
				assert.Equal(t, wantAnnotations, patched.Annotations)
			})
		}
	})
}

func Test_ApplicationManager_UpdateOperation_Details(t *testing.T) {
	newExisting := func() *v1alpha1.Application {
		app := newTestApp("app")
		app.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "old"}}
		return app
	}
	newIncoming := func() *v1alpha1.Application {
		app := newTestApp("app")
		app.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "new"}}
		return app
	}

	t.Run("only an autonomous agent can update the operation", func(t *testing.T) {
		tests := []struct {
			name string
			opts []ApplicationManagerOption
		}{
			{name: "principal", opts: principalOpts()},
			{name: "managed agent", opts: managedAgentOpts()},
			{name: "no role", opts: nil},
			{name: "autonomous mode without a role", opts: []ApplicationManagerOption{WithMode(manager.ManagerModeAutonomous)}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, m := newMockedAppManager(t, tt.opts...)

				got, err := m.UpdateOperation(context.Background(), newIncoming())

				require.ErrorContains(t, err, "only be called by an agent in autonomous mode")
				assert.Nil(t, got)
			})
		}
	})

	t.Run("without patch support the operation, labels, annotations and status are taken over", func(t *testing.T) {
		be, m := newMockedAppManager(t, autonomousAgentOpts()...)
		incoming := newIncoming()
		incoming.Labels = map[string]string{"new": "label"}
		incoming.Annotations = map[string]string{"argocd.argoproj.io/refresh": "normal"}
		incoming.Status.Sync.Status = v1alpha1.SyncStatusCodeOutOfSync
		var sent *v1alpha1.Application
		updated := newTestApp("app")
		updated.ResourceVersion = "6"
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newExisting(), nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, updated)).Once()

		got, err := m.UpdateOperation(context.Background(), incoming)

		require.NoError(t, err)
		assert.Same(t, updated, got)
		require.NotNil(t, sent.Operation)
		assert.Equal(t, "new", sent.Operation.InitiatedBy.Username)
		assert.Equal(t, incoming.Labels, sent.Labels)
		assert.Equal(t, incoming.Annotations, sent.Annotations)
		assert.Equal(t, v1alpha1.SyncStatusCodeOutOfSync, sent.Status.Sync.Status)
		assert.True(t, m.IsChangeIgnored("argocd/app", "6"))
	})

	t.Run("the operation of an application that is terminating is kept", func(t *testing.T) {
		be, m := newMockedAppManager(t, autonomousAgentOpts()...)
		existing := newExisting()
		existing.Status.OperationState = &v1alpha1.OperationState{Phase: synccommon.OperationTerminating}
		var sent *v1alpha1.Application
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existing, nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, newTestApp("app"))).Once()

		_, err := m.UpdateOperation(context.Background(), newIncoming())

		require.NoError(t, err)
		assert.Equal(t, "old", sent.Operation.InitiatedBy.Username)
	})

	t.Run("with patch support", func(t *testing.T) {
		tests := []struct {
			name        string
			mutate      func(incoming *v1alpha1.Application)
			wantOps     []string
			wantInPatch string
		}{
			{
				name:    "a new operation is patched in",
				mutate:  func(*v1alpha1.Application) {},
				wantOps: []string{"replace /operation/initiatedBy/username"},
			},
			{
				name: "a refresh annotation is added",
				mutate: func(a *v1alpha1.Application) {
					a.Annotations = map[string]string{"argocd.argoproj.io/refresh": "normal"}
				},
				wantOps:     []string{"replace /operation/initiatedBy/username"},
				wantInPatch: `"argocd.argoproj.io/refresh":"normal"`,
			},
			{
				name:   "an unchanged operation needs no patch",
				mutate: func(a *v1alpha1.Application) { a.Operation = newExisting().Operation },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				be, m := newMockedAppManager(t, autonomousAgentOpts()...)
				incoming := newIncoming()
				tt.mutate(incoming)
				var sent []byte
				be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newExisting(), nil).Once()
				be.EXPECT().SupportsPatch().Return(true).Once()
				be.EXPECT().Patch(mock.Anything, "app", testNamespace, mock.Anything).RunAndReturn(capturePatch(&sent, newTestApp("app"))).Once()

				_, err := m.UpdateOperation(context.Background(), incoming)

				require.NoError(t, err)
				assert.ElementsMatch(t, tt.wantOps, withoutMapOps(patchOps(t, sent)))
				if tt.wantInPatch != "" {
					assert.Contains(t, string(sent), tt.wantInPatch)
				}
			})
		}
	})
}

func Test_ApplicationManager_SetOperation(t *testing.T) {
	newExisting := func() *v1alpha1.Application {
		app := newTestApp("app")
		app.Labels = map[string]string{"keep": "label"}
		app.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "old"}}
		return app
	}
	newIncoming := func() *v1alpha1.Application {
		app := newTestApp("app")
		app.Labels = map[string]string{"other": "label"}
		app.Spec.Project = "other"
		app.Operation = &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "new"}}
		return app
	}

	t.Run("only an agent can set an operation", func(t *testing.T) {
		_, m := newMockedAppManager(t, principalOpts()...)

		got, err := m.SetOperation(context.Background(), newIncoming())

		require.ErrorContains(t, err, "only be called by an agent")
		assert.Nil(t, got)
	})

	t.Run("without patch support only the operation changes", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		var sent *v1alpha1.Application
		updated := newTestApp("app")
		updated.ResourceVersion = "3"
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newExisting(), nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, updated)).Once()

		got, err := m.SetOperation(context.Background(), newIncoming())

		require.NoError(t, err)
		assert.Same(t, updated, got)
		assert.Equal(t, "new", sent.Operation.InitiatedBy.Username)
		assert.Equal(t, "default", sent.Spec.Project, "the spec must not change")
		assert.Equal(t, map[string]string{"keep": "label"}, sent.Labels, "the labels must not change")
		assert.True(t, m.IsChangeIgnored("argocd/app", "3"))
	})

	t.Run("with patch support only the operation is patched", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		var sent []byte
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newExisting(), nil).Once()
		be.EXPECT().SupportsPatch().Return(true).Once()
		be.EXPECT().Patch(mock.Anything, "app", testNamespace, mock.Anything).RunAndReturn(capturePatch(&sent, newTestApp("app"))).Once()

		_, err := m.SetOperation(context.Background(), newIncoming())

		require.NoError(t, err)
		assert.Equal(t, []string{"replace /operation/initiatedBy/username"}, patchOps(t, sent))
	})

	t.Run("the operation of an application that is terminating is kept", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		existing := newExisting()
		existing.Status.OperationState = &v1alpha1.OperationState{Phase: synccommon.OperationTerminating}
		var sent *v1alpha1.Application
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existing, nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, newTestApp("app"))).Once()

		_, err := m.SetOperation(context.Background(), newIncoming())

		require.NoError(t, err)
		assert.Equal(t, "old", sent.Operation.InitiatedBy.Username)
	})

	t.Run("an application that does not exist is not created", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(nil, appNotFoundError).Once()

		got, err := m.SetOperation(context.Background(), newIncoming())

		require.ErrorIs(t, err, appNotFoundError)
		assert.Nil(t, got)
	})
}

func Test_operationToUse(t *testing.T) {
	existingOp := func() *v1alpha1.Operation {
		return &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "existing"}}
	}
	incomingOp := func() *v1alpha1.Operation {
		return &v1alpha1.Operation{InitiatedBy: v1alpha1.OperationInitiator{Username: "incoming"}}
	}
	withPhase := func(phase synccommon.OperationPhase) v1alpha1.ApplicationStatus {
		return v1alpha1.ApplicationStatus{OperationState: &v1alpha1.OperationState{Phase: phase}}
	}

	tests := []struct {
		name         string
		incoming     *v1alpha1.Operation
		status       v1alpha1.ApplicationStatus
		wantExisting bool
	}{
		{name: "the existing operation is kept when the incoming one is empty", wantExisting: true},
		{name: "the operation of an application that is terminating is not replaced", incoming: incomingOp(), status: withPhase(synccommon.OperationTerminating), wantExisting: true},
		{name: "the incoming operation replaces the one of a running operation", incoming: incomingOp(), status: withPhase(synccommon.OperationRunning)},
		{name: "the incoming operation is used when there is no operation state", incoming: incomingOp()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing, incoming := newTestApp("app"), newTestApp("app")
			existing.Operation = existingOp()
			existing.Status = tt.status
			incoming.Operation = tt.incoming

			got := operationToUse(existing, incoming)

			if tt.wantExisting {
				assert.Same(t, existing.Operation, got)
				return
			}
			assert.Equal(t, incomingOp(), got)
			assert.NotSame(t, incoming.Operation, got, "the incoming operation must be copied")
		})
	}
}

func Test_ApplicationManager_TerminateOperation_Errors(t *testing.T) {
	t.Run("the principal cannot terminate an operation", func(t *testing.T) {
		_, m := newMockedAppManager(t, principalOpts()...)

		got, err := m.TerminateOperation(context.Background(), newTestApp("app"))

		require.ErrorContains(t, err, "only be called by an agent")
		assert.Nil(t, got)
	})

	t.Run("a failure to read the application is returned", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(nil, errBackend).Once()

		got, err := m.TerminateOperation(context.Background(), newTestApp("app"))

		require.ErrorIs(t, err, errBackend)
		require.ErrorContains(t, err, "argocd/app")
		assert.Nil(t, got)
	})

	t.Run("an application without an operation state cannot be terminated", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newTestApp("app"), nil).Once()

		got, err := m.TerminateOperation(context.Background(), newTestApp("app"))

		require.ErrorContains(t, err, "has no operation state")
		assert.Nil(t, got)
	})

	t.Run("a failed patch is returned", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		existing := newTestApp("app")
		existing.Status.OperationState = &v1alpha1.OperationState{Phase: synccommon.OperationRunning}
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existing, nil).Once()
		be.EXPECT().Patch(mock.Anything, "app", testNamespace, mock.Anything).Return(nil, errBackend).Once()

		got, err := m.TerminateOperation(context.Background(), newTestApp("app"))

		require.ErrorIs(t, err, errBackend)
		require.ErrorContains(t, err, "failed to terminate operation")
		assert.Nil(t, got)
	})
}

func Test_ApplicationManager_Delete_Details(t *testing.T) {
	propagation := backend.DeletePropagationBackground

	t.Run("the principal removes the finalizers and then deletes", func(t *testing.T) {
		be, m := newMockedAppManager(t, principalOpts()...)
		existing := newTestApp("app")
		existing.Finalizers = []string{"resources-finalizer.argocd.argoproj.io"}
		var sent *v1alpha1.Application
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(existing, nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		updating := be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, newTestApp("app"))).Once()
		be.EXPECT().Delete(mock.Anything, "app", testNamespace, &propagation).Return(nil).Once().NotBefore(updating)

		err := m.Delete(context.Background(), testNamespace, newTestApp("app"), &propagation)

		require.NoError(t, err)
		assert.Empty(t, sent.Finalizers)
	})

	t.Run("the application is not deleted when its finalizers cannot be removed", func(t *testing.T) {
		be, m := newMockedAppManager(t, principalOpts()...)
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(nil, errBackend).Once()

		err := m.Delete(context.Background(), testNamespace, newTestApp("app"), &propagation)

		require.ErrorIs(t, err, errBackend)
		require.ErrorContains(t, err, "error removing finalizer")
	})

	t.Run("an agent deletes without touching the finalizers", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		be.EXPECT().Delete(mock.Anything, "app", testNamespace, &propagation).Return(nil).Once()

		err := m.Delete(context.Background(), testNamespace, newTestApp("app"), &propagation)

		require.NoError(t, err)
	})

	t.Run("a failed delete is returned", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		be.EXPECT().Delete(mock.Anything, "app", testNamespace, (*backend.DeletionPropagation)(nil)).Return(errBackend).Once()

		err := m.Delete(context.Background(), testNamespace, newTestApp("app"), nil)

		require.ErrorIs(t, err, errBackend)
	})
}

func Test_ApplicationManager_ClearOperationState_Details(t *testing.T) {
	t.Run("a failure to read the application does not stop the patch", func(t *testing.T) {
		be, m := newMockedAppManager(t)
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(nil, errBackend).Once()
		be.EXPECT().Patch(mock.Anything, "app", testNamespace,
			[]byte(`[{"op":"replace","path":"/status/operationState","value":null}]`)).Return(newTestApp("app"), nil).Once()

		err := m.ClearOperationState(context.Background(), newTestApp("app"))

		require.NoError(t, err)
	})
}

func Test_ApplicationManager_RevertManagedAppChanges_Details(t *testing.T) {

	// Cases in which nothing is touched on the backend.
	noops := []struct {
		name   string
		opts   []ApplicationManagerOption
		app    func() *v1alpha1.Application
		cached *v1alpha1.ApplicationSpec
	}{
		{name: "an autonomous agent does not revert", opts: autonomousAgentOpts(), app: newDriftedApp, cached: &revertCachedSpec},
		{name: "an application without a source uid is not reverted", opts: managedAgentOpts(), cached: &revertCachedSpec, app: func() *v1alpha1.Application {
			app := newDriftedApp()
			app.Annotations = nil
			return app
		}},
		{name: "an application that is not in the cache is not reverted", opts: managedAgentOpts(), app: newDriftedApp},
		{name: "an application that is in sync is not reverted", opts: managedAgentOpts(), cached: &revertCachedSpec, app: func() *v1alpha1.Application {
			app := newDriftedApp()
			app.Spec = revertCachedSpec
			return app
		}},
	}
	for _, tt := range noops {
		t.Run(tt.name, func(t *testing.T) {
			_, m := newMockedAppManager(t, tt.opts...)

			assert.False(t, m.RevertManagedAppChanges(context.Background(), tt.app(), newSpecCache(tt.cached)))
		})
	}

	t.Run("a modified application gets the cached spec back", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		var sent *v1alpha1.Application
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newDriftedApp(), nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, newTestApp("app"))).Once()

		reverted := m.RevertManagedAppChanges(context.Background(), newDriftedApp(), newSpecCache(&revertCachedSpec))

		assert.True(t, reverted)
		assert.Equal(t, revertCachedSpec, sent.Spec)
	})

	t.Run("a failed revert is reported", func(t *testing.T) {
		be, m := newMockedAppManager(t, managedAgentOpts()...)
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newDriftedApp(), nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).Return(nil, errBackend).Once()

		assert.False(t, m.RevertManagedAppChanges(context.Background(), newDriftedApp(), newSpecCache(&revertCachedSpec)))
	})
}

func Test_ApplicationManager_RevertAutonomousAppChanges_Details(t *testing.T) {

	// Cases in which nothing is touched on the backend.
	noops := []struct {
		name   string
		app    func() *v1alpha1.Application
		cached *v1alpha1.ApplicationSpec
	}{
		{name: "an application without a source uid is not reverted", cached: &revertCachedSpec, app: func() *v1alpha1.Application {
			app := newDriftedApp()
			app.Annotations = nil
			return app
		}},
		{name: "an application that is not in the cache is not reverted", app: newDriftedApp},
		{name: "an application that is in sync is not reverted", cached: &revertCachedSpec, app: func() *v1alpha1.Application {
			app := newDriftedApp()
			app.Spec = revertCachedSpec
			return app
		}},
	}
	for _, tt := range noops {
		t.Run(tt.name, func(t *testing.T) {
			_, m := newMockedAppManager(t, principalOpts()...)

			assert.False(t, m.RevertAutonomousAppChanges(context.Background(), tt.app(), newSpecCache(tt.cached)))
		})
	}

	t.Run("a modified application gets the cached spec back", func(t *testing.T) {
		be, m := newMockedAppManager(t, principalOpts()...)
		var sent *v1alpha1.Application
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newDriftedApp(), nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(captureUpdate(&sent, newTestApp("app"))).Once()

		reverted := m.RevertAutonomousAppChanges(context.Background(), newDriftedApp(), newSpecCache(&revertCachedSpec))

		assert.True(t, reverted)
		assert.Equal(t, revertCachedSpec, sent.Spec)
	})

	t.Run("a failed revert is reported", func(t *testing.T) {
		be, m := newMockedAppManager(t, principalOpts()...)
		be.EXPECT().Get(mock.Anything, "app", testNamespace).Return(newDriftedApp(), nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(mock.Anything, mock.Anything).Return(nil, errBackend).Once()

		assert.False(t, m.RevertAutonomousAppChanges(context.Background(), newDriftedApp(), newSpecCache(&revertCachedSpec)))
	})

	t.Run("only the principal can revert an autonomous application", func(t *testing.T) {
		_, m := newMockedAppManager(t, managedAgentOpts()...)

		assert.False(t, m.RevertAutonomousAppChanges(context.Background(), newDriftedApp(), newSpecCache(&revertCachedSpec)))
	})
}
