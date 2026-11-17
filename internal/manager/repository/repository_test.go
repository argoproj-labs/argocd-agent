// Copyright 2025 The argocd-agent Authors
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

package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	appmock "github.com/argoproj-labs/argocd-agent/internal/backend/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/cache"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

const testNamespace = "argocd"

func newTestRepository(name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			UID:       types.UID(name + "-uid"),
		},
		Data: map[string][]byte{"url": []byte("https://github.com/example/repo.git")},
	}
}

// newExistingRepository is a repository as it exists on the agent, with the
// source UID annotation that Create adds.
func newExistingRepository() *corev1.Secret {
	repo := newTestRepository("test-repo")
	repo.ResourceVersion = "5"
	repo.Labels = map[string]string{"app": "old"}
	repo.Annotations = map[string]string{manager.SourceUIDAnnotation: "src-1", "keep": "me"}
	repo.Data = map[string][]byte{"url": []byte("https://old")}
	return repo
}

func Test_CompareSourceUIDForRepository(t *testing.T) {
	oldRepository := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "argocd",
			Annotations: map[string]string{
				manager.SourceUIDAnnotation: "old_uid",
			},
		},
		Data: map[string][]byte{
			"project": []byte("test-project"),
			"url":     []byte("https://github.com/example/repo.git"),
		},
	}

	mockedBackend := appmock.NewRepository(t)
	getMock := mockedBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(oldRepository, nil)
	m := &RepositoryManager{backend: mockedBackend}
	ctx := context.Background()

	t.Cleanup(func() {
		getMock.Unset()
	})

	t.Run("should return true if the UID matches", func(t *testing.T) {
		incoming := oldRepository.DeepCopy()
		incoming.UID = types.UID("old_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.True(t, exists)
		require.Nil(t, err)
		require.True(t, uidMatch)
	})

	t.Run("should return false if the UID doesn't match", func(t *testing.T) {
		incoming := oldRepository.DeepCopy()
		incoming.UID = types.UID("new_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.True(t, exists)
		require.Nil(t, err)
		require.False(t, uidMatch)
	})

	t.Run("should return an error if there is no UID annotation", func(t *testing.T) {
		oldRepository.Annotations = map[string]string{}
		mockedBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(oldRepository, nil)
		m := &RepositoryManager{backend: mockedBackend}
		ctx := context.Background()

		incoming := oldRepository.DeepCopy()
		incoming.UID = types.UID("new_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.NotNil(t, err)
		require.True(t, exists)
		require.EqualError(t, err, "source UID Annotation is not found for repository: test-repo")
		require.False(t, uidMatch)
	})

	t.Run("should return the error of the backend when it is not a not found error", func(t *testing.T) {
		getErr := errors.New("get failed")
		be := appmock.NewRepository(t)
		be.EXPECT().Get(mock.Anything, oldRepository.Name, oldRepository.Namespace).Return(nil, getErr).Once()
		m := &RepositoryManager{backend: be}

		exists, uidMatch, err := m.CompareSourceUID(ctx, oldRepository.DeepCopy())

		require.ErrorIs(t, err, getErr)
		require.False(t, exists)
		require.False(t, uidMatch)
	})

	t.Run("should return False if the repository doesn't exist", func(t *testing.T) {
		expectedErr := k8serrors.NewNotFound(schema.GroupResource{Group: "", Resource: "secret"},
			oldRepository.Name)
		getMock.Unset()
		getMock = mockedBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedErr)
		m := &RepositoryManager{backend: mockedBackend}
		ctx := context.Background()

		repo, err := m.backend.Get(ctx, oldRepository.Name, oldRepository.Namespace)
		require.Nil(t, repo)
		require.True(t, k8serrors.IsNotFound(err))

		incoming := oldRepository.DeepCopy()
		incoming.UID = types.UID("new_uid")
		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.False(t, exists)
		require.False(t, uidMatch)
		require.Nil(t, err)
	})
}

func Test_RepositoryManagerCreate(t *testing.T) {
	t.Run("Create a repository that already exists", func(t *testing.T) {
		repo := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-repo",
				Namespace: "argocd",
				UID:       "test-uid",
			},
			Data: map[string][]byte{
				"project": []byte("test-project"),
				"url":     []byte("https://github.com/example/repo.git"),
			},
		}

		existsError := k8serrors.NewAlreadyExists(schema.GroupResource{Group: "", Resource: "secret"}, "test-repo")
		mockedBackend := appmock.NewRepository(t)
		mockedBackend.On("Create", mock.Anything, mock.Anything).Return(nil, existsError)

		m := &RepositoryManager{
			backend:           mockedBackend,
			ManagedResources:  manager.NewManagedResources(),
			ObservedResources: manager.NewObservedResources(),
		}
		_, err := m.Create(context.Background(), repo, true)
		require.Error(t, err)
		require.True(t, k8serrors.IsAlreadyExists(err))
	})

	t.Run("Create a new repository successfully", func(t *testing.T) {
		repo := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-repo",
				Namespace: "argocd",
				UID:       "test-uid",
			},
			Data: map[string][]byte{
				"project": []byte("test-project"),
				"url":     []byte("https://github.com/example/repo.git"),
			},
		}

		expectedRepo := repo.DeepCopy()
		expectedRepo.ResourceVersion = "1"
		expectedRepo.Annotations = map[string]string{
			manager.SourceUIDAnnotation: string(repo.UID),
		}

		mockedBackend := appmock.NewRepository(t)
		mockedBackend.On("Create", mock.Anything, mock.MatchedBy(func(r *corev1.Secret) bool {
			// Verify that ResourceVersion and Generation are cleared
			return r.ResourceVersion == "" && r.Generation == 0 &&
				r.Annotations[manager.SourceUIDAnnotation] == string(repo.UID)
		})).Return(expectedRepo, nil)

		m := &RepositoryManager{
			backend:           mockedBackend,
			ManagedResources:  manager.NewManagedResources(),
			ObservedResources: manager.NewObservedResources(),
		}

		createdRepo, err := m.Create(context.Background(), repo, true)
		require.NoError(t, err)
		require.NotNil(t, createdRepo)
		require.Equal(t, "test-repo", createdRepo.Name)
		require.Equal(t, string(repo.UID), createdRepo.Annotations[manager.SourceUIDAnnotation])
		require.Equal(t, "1", createdRepo.ResourceVersion)
	})

	t.Run("Create repository clears ResourceVersion and Generation", func(t *testing.T) {
		repo := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-repo",
				Namespace:       "argocd",
				UID:             "test-uid",
				ResourceVersion: "123", // Should be cleared
				Generation:      5,     // Should be cleared
			},
			Data: map[string][]byte{
				"project": []byte("test-project"),
				"url":     []byte("https://github.com/example/repo.git"),
			},
		}

		expectedRepo := repo.DeepCopy()
		expectedRepo.ResourceVersion = "1"
		expectedRepo.Generation = 1
		expectedRepo.Annotations = map[string]string{
			manager.SourceUIDAnnotation: string(repo.UID),
		}

		mockedBackend := appmock.NewRepository(t)
		mockedBackend.On("Create", mock.Anything, mock.MatchedBy(func(r *corev1.Secret) bool {
			return r.ResourceVersion == "" && r.Generation == 0
		})).Return(expectedRepo, nil)

		m := &RepositoryManager{
			backend:           mockedBackend,
			ManagedResources:  manager.NewManagedResources(),
			ObservedResources: manager.NewObservedResources(),
		}

		createdRepo, err := m.Create(context.Background(), repo, true)
		require.NoError(t, err)
		require.Equal(t, "", repo.ResourceVersion)         // Original should be modified
		require.Equal(t, int64(0), repo.Generation)        // Original should be modified
		require.Equal(t, "1", createdRepo.ResourceVersion) // Returned should have backend value
		require.Equal(t, string(repo.UID), createdRepo.Annotations[manager.SourceUIDAnnotation])
	})

	t.Run("a created repository is managed, and its change is ignored only if requested", func(t *testing.T) {
		tests := []struct {
			name         string
			ignoreChange bool
		}{
			{name: "ignoreChange is true", ignoreChange: true},
			{name: "ignoreChange is false", ignoreChange: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				created := newTestRepository("test-repo")
				created.ResourceVersion = "7"
				be := appmock.NewRepository(t)
				be.EXPECT().Create(mock.Anything, mock.Anything).Return(created, nil).Once()
				m := NewManager(be, testNamespace, false)

				_, err := m.Create(context.Background(), newTestRepository("test-repo"), tt.ignoreChange)

				require.NoError(t, err)
				require.True(t, m.IsManaged("test-repo"))
				require.Equal(t, tt.ignoreChange, m.IsChangeIgnored("test-repo", "7"))
			})
		}
	})

	t.Run("the annotations of the repository are kept and the source UID is added", func(t *testing.T) {
		repo := newTestRepository("test-repo")
		repo.Annotations = map[string]string{"keep": "me"}
		var sent *corev1.Secret
		be := appmock.NewRepository(t)
		be.EXPECT().Create(mock.Anything, mock.Anything).RunAndReturn(
			func(_ context.Context, r *corev1.Secret) (*corev1.Secret, error) {
				sent = r.DeepCopy()
				return r, nil
			}).Once()

		_, err := NewManager(be, testNamespace, false).Create(context.Background(), repo, true)

		require.NoError(t, err)
		require.Equal(t, map[string]string{"keep": "me", manager.SourceUIDAnnotation: "test-repo-uid"}, sent.Annotations)
	})

	t.Run("a repository that was already managed is still created and returned", func(t *testing.T) {
		be := appmock.NewRepository(t)
		be.EXPECT().Create(mock.Anything, mock.Anything).Return(newTestRepository("test-repo"), nil).Once()
		m := NewManager(be, testNamespace, false)
		require.NoError(t, m.Manage("test-repo"))

		created, err := m.Create(context.Background(), newTestRepository("test-repo"), true)

		require.NoError(t, err)
		require.Equal(t, "test-repo", created.Name)
	})

	t.Run("a repository whose change was already ignored is still created and returned", func(t *testing.T) {
		created := newTestRepository("test-repo")
		created.ResourceVersion = "7"
		be := appmock.NewRepository(t)
		be.EXPECT().Create(mock.Anything, mock.Anything).Return(created, nil).Once()
		m := NewManager(be, testNamespace, false)
		require.NoError(t, m.IgnoreChange("test-repo", "7"))

		got, err := m.Create(context.Background(), newTestRepository("test-repo"), true)

		require.NoError(t, err)
		require.Same(t, created, got)
		require.True(t, m.IsChangeIgnored("test-repo", "7"))
	})

	t.Run("a repository that could not be created is not managed", func(t *testing.T) {
		createErr := errors.New("create failed")
		be := appmock.NewRepository(t)
		be.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, createErr).Once()
		m := NewManager(be, testNamespace, false)

		got, err := m.Create(context.Background(), newTestRepository("test-repo"), true)

		require.ErrorIs(t, err, createErr)
		require.Nil(t, got)
		require.False(t, m.IsManaged("test-repo"))
	})
}

func Test_RepositoryManagerDelete(t *testing.T) {
	foreground := backend.DeletePropagationForeground
	tests := []struct {
		name        string
		propagation *backend.DeletionPropagation
		err         error
	}{
		{name: "passes the deletion propagation to the backend", propagation: &foreground},
		{name: "deletes without a deletion propagation"},
		{
			name: "returns the error of the backend",
			err:  k8serrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "test-repo"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			be := appmock.NewRepository(t)
			be.EXPECT().Delete(ctx, "test-repo", testNamespace, tt.propagation).Return(tt.err).Once()

			err := NewManager(be, testNamespace, false).Delete(ctx, "test-repo", testNamespace, tt.propagation)

			require.ErrorIs(t, err, tt.err)
		})
	}
}

func Test_RepositoryManagerList(t *testing.T) {
	selector := backend.RepositorySelector{Namespace: testNamespace, Labels: map[string]string{"team": "a"}}
	listErr := errors.New("list failed")
	tests := []struct {
		name   string
		result []corev1.Secret
		err    error
	}{
		{name: "returns the repositories of the backend", result: []corev1.Secret{*newTestRepository("one"), *newTestRepository("two")}},
		{name: "returns no repositories when there are none"},
		{name: "returns the error of the backend", err: listErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			be := appmock.NewRepository(t)
			be.EXPECT().List(ctx, selector).Return(tt.result, tt.err).Once()

			got, err := NewManager(be, testNamespace, false).List(ctx, selector)

			require.ErrorIs(t, err, tt.err)
			require.Equal(t, tt.result, got)
		})
	}
}

func Test_RepositoryManagerGet(t *testing.T) {
	repo := newTestRepository("test-repo")
	getErr := k8serrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "test-repo")
	tests := []struct {
		name   string
		result *corev1.Secret
		err    error
	}{
		{name: "returns the repository of the backend", result: repo},
		{name: "returns the error of the backend", err: getErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			be := appmock.NewRepository(t)
			be.EXPECT().Get(ctx, "test-repo", testNamespace).Return(tt.result, tt.err).Once()

			got, err := NewManager(be, testNamespace, false).Get(ctx, "test-repo", testNamespace)

			require.ErrorIs(t, err, tt.err)
			require.Same(t, tt.result, got)
		})
	}
}

func Test_RepositoryManagerUpdateManagedRepository(t *testing.T) {
	ctx := context.Background()

	t.Run("updates the repository when the backend does not support patch", func(t *testing.T) {
		incoming := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-repo",
				Namespace:   testNamespace,
				Labels:      map[string]string{"app": "new"},
				Annotations: map[string]string{"extra": "yes"},
				Finalizers:  []string{"example.com/finalizer"},
			},
			Data: map[string][]byte{"url": []byte("https://new")},
		}
		updated := newExistingRepository()
		updated.ResourceVersion = "6"
		var sent *corev1.Secret
		be := appmock.NewRepository(t)
		be.EXPECT().Get(ctx, "test-repo", testNamespace).Return(newExistingRepository(), nil).Once()
		be.EXPECT().SupportsPatch().Return(false).Once()
		be.EXPECT().Update(ctx, mock.Anything).RunAndReturn(
			func(_ context.Context, s *corev1.Secret) (*corev1.Secret, error) {
				sent = s.DeepCopy()
				return updated, nil
			}).Once()
		m := NewManager(be, testNamespace, false)

		got, err := m.UpdateManagedRepository(ctx, incoming)

		require.NoError(t, err)
		require.Same(t, updated, got)
		require.Equal(t, map[string]string{"app": "new"}, sent.Labels)
		require.Equal(t, []string{"example.com/finalizer"}, sent.Finalizers)
		require.Equal(t, map[string][]byte{"url": []byte("https://new")}, sent.Data)
		require.True(t, m.IsChangeIgnored("test-repo", "6"))
	})

	t.Run("the source UID annotation of the existing repository is preserved on update", func(t *testing.T) {
		tests := []struct {
			name                string
			existingAnnotations map[string]string
			incomingAnnotations map[string]string
			want                map[string]string
		}{
			{
				name:                "added to the annotations of the incoming repository",
				existingAnnotations: map[string]string{manager.SourceUIDAnnotation: "src-1"},
				incomingAnnotations: map[string]string{"extra": "yes"},
				want:                map[string]string{manager.SourceUIDAnnotation: "src-1", "extra": "yes"},
			},
			{
				name:                "added when the incoming repository has no annotations",
				existingAnnotations: map[string]string{manager.SourceUIDAnnotation: "src-1"},
				want:                map[string]string{manager.SourceUIDAnnotation: "src-1"},
			},
			{
				name:                "not invented when the existing repository has none",
				incomingAnnotations: map[string]string{"extra": "yes"},
				want:                map[string]string{"extra": "yes"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				existing := newExistingRepository()
				existing.Annotations = tt.existingAnnotations
				incoming := newTestRepository("test-repo")
				incoming.Annotations = tt.incomingAnnotations
				var sent *corev1.Secret
				be := appmock.NewRepository(t)
				be.EXPECT().Get(ctx, "test-repo", testNamespace).Return(existing, nil).Once()
				be.EXPECT().SupportsPatch().Return(false).Once()
				be.EXPECT().Update(ctx, mock.Anything).RunAndReturn(
					func(_ context.Context, s *corev1.Secret) (*corev1.Secret, error) {
						sent = s.DeepCopy()
						return s, nil
					}).Once()

				_, err := NewManager(be, testNamespace, false).UpdateManagedRepository(ctx, incoming)

				require.NoError(t, err)
				require.Equal(t, tt.want, sent.Annotations)
			})
		}
	})

	t.Run("patches the repository when the backend supports patch", func(t *testing.T) {
		existing := newExistingRepository()
		existing.Annotations = map[string]string{manager.SourceUIDAnnotation: "src-1"}
		incoming := newTestRepository("test-repo")
		incoming.Labels = map[string]string{"app": "old"}
		incoming.Data = map[string][]byte{"url": []byte("https://new")}
		updated := newExistingRepository()
		updated.ResourceVersion = "6"
		var patch []byte
		be := appmock.NewRepository(t)
		be.EXPECT().Get(ctx, "test-repo", testNamespace).Return(existing, nil).Once()
		be.EXPECT().SupportsPatch().Return(true).Once()
		be.EXPECT().Patch(ctx, "test-repo", testNamespace, mock.Anything).RunAndReturn(
			func(_ context.Context, _, _ string, p []byte) (*corev1.Secret, error) {
				patch = p
				return updated, nil
			}).Once()
		m := NewManager(be, testNamespace, false)

		got, err := m.UpdateManagedRepository(ctx, incoming)

		require.NoError(t, err)
		require.Same(t, updated, got)
		// Only the data differs: the source UID annotation was kept in the target.
		type operation struct {
			Op   string `json:"op"`
			Path string `json:"path"`
		}
		var operations []operation
		require.NoError(t, json.Unmarshal(patch, &operations))
		require.Equal(t, []operation{{Op: "replace", Path: "/data/url"}}, operations)
		require.True(t, m.IsChangeIgnored("test-repo", "6"))
	})

	t.Run("creates the repository when it does not exist and upsert is allowed", func(t *testing.T) {
		created := newTestRepository("test-repo")
		created.ResourceVersion = "1"
		be := appmock.NewRepository(t)
		be.EXPECT().Get(ctx, "test-repo", testNamespace).
			Return(nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "test-repo")).Once()
		be.EXPECT().Create(ctx, mock.Anything).Return(created, nil).Once()
		m := NewManager(be, testNamespace, true)

		got, err := m.UpdateManagedRepository(ctx, newTestRepository("test-repo"))

		require.NoError(t, err)
		require.Same(t, created, got)
		require.True(t, m.IsManaged("test-repo"))
	})

	t.Run("returns an error without writing when the repository cannot be fetched", func(t *testing.T) {
		getErr := errors.New("get failed")
		tests := []struct {
			name        string
			allowUpsert bool
			err         error
		}{
			{
				name: "not found and upsert is not allowed",
				err:  k8serrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "test-repo"),
			},
			{name: "another error and upsert is allowed", allowUpsert: true, err: getErr},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				be := appmock.NewRepository(t)
				be.EXPECT().Get(ctx, "test-repo", testNamespace).Return(nil, tt.err).Once()
				m := NewManager(be, testNamespace, tt.allowUpsert)

				got, err := m.UpdateManagedRepository(ctx, newTestRepository("test-repo"))

				require.ErrorIs(t, err, tt.err)
				require.ErrorContains(t, err, "error updating repository test-repo")
				require.Nil(t, got)
			})
		}
	})

	t.Run("a failed write returns the error and does not ignore the change", func(t *testing.T) {
		writeErr := errors.New("write failed")
		tests := []struct {
			name     string
			patching bool
			expect   func(be *appmock.Repository)
		}{
			{
				name:     "patch",
				patching: true,
				expect: func(be *appmock.Repository) {
					be.EXPECT().Patch(ctx, "test-repo", testNamespace, mock.Anything).Return(nil, writeErr).Once()
				},
			},
			{
				name: "update",
				expect: func(be *appmock.Repository) {
					be.EXPECT().Update(ctx, mock.Anything).Return(nil, writeErr).Once()
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				be := appmock.NewRepository(t)
				be.EXPECT().Get(ctx, "test-repo", testNamespace).Return(newExistingRepository(), nil).Once()
				be.EXPECT().SupportsPatch().Return(tt.patching).Once()
				tt.expect(be)

				got, err := NewManager(be, testNamespace, false).UpdateManagedRepository(ctx, newTestRepository("test-repo"))

				require.ErrorIs(t, err, writeErr)
				require.Nil(t, got)
			})
		}
	})

	t.Run("retries the update after a conflict", func(t *testing.T) {
		conflict := k8serrors.NewConflict(schema.GroupResource{Resource: "secrets"}, "test-repo", errors.New("the object was modified"))
		updated := newExistingRepository()
		updated.ResourceVersion = "6"
		be := appmock.NewRepository(t)
		be.EXPECT().Get(ctx, "test-repo", testNamespace).Return(newExistingRepository(), nil).Twice()
		be.EXPECT().SupportsPatch().Return(false).Twice()
		be.EXPECT().Update(ctx, mock.Anything).Return(nil, conflict).Once()
		be.EXPECT().Update(ctx, mock.Anything).Return(updated, nil).Once()

		got, err := NewManager(be, testNamespace, false).UpdateManagedRepository(ctx, newTestRepository("test-repo"))

		require.NoError(t, err)
		require.Same(t, updated, got)
	})
}

func Test_RepositoryManagerRevertRepositoryChanges(t *testing.T) {
	source := map[string][]byte{"url": []byte("https://source")}
	tampered := map[string][]byte{"url": []byte("https://tampered")}
	sourceCached := map[types.UID]map[string][]byte{"src-1": source}
	withSourceUID := map[string]string{manager.SourceUIDAnnotation: "src-1"}

	// expectUpdate sets up the backend for the update that reverts the data.
	expectUpdate := func(result *corev1.Secret, err error) func(be *appmock.Repository) {
		return func(be *appmock.Repository) {
			existing := newExistingRepository()
			existing.Data = tampered
			be.EXPECT().Get(mock.Anything, "test-repo", testNamespace).Return(existing, nil).Once()
			be.EXPECT().SupportsPatch().Return(false).Once()
			be.EXPECT().Update(mock.Anything, mock.MatchedBy(func(s *corev1.Secret) bool {
				return maps.EqualFunc(s.Data, source, bytes.Equal)
			})).Return(result, err).Once()
		}
	}

	tests := []struct {
		name        string
		annotations map[string]string
		cached      map[types.UID]map[string][]byte
		data        map[string][]byte
		setup       func(be *appmock.Repository)
		want        bool
		wantData    map[string][]byte
	}{
		{
			name:     "does nothing without a source UID annotation",
			cached:   sourceCached,
			data:     tampered,
			wantData: tampered,
		},
		{
			name:        "does nothing when the repository is not in the cache",
			annotations: withSourceUID,
			data:        tampered,
			wantData:    tampered,
		},
		{
			name:        "does nothing when the data matches the cache",
			annotations: withSourceUID,
			cached:      sourceCached,
			data:        source,
			wantData:    source,
		},
		{
			name:        "reverts the data to the cached one",
			annotations: withSourceUID,
			cached:      sourceCached,
			data:        tampered,
			setup:       expectUpdate(newExistingRepository(), nil),
			want:        true,
			wantData:    source,
		},
		{
			name:        "reports a failure when the revert cannot be written",
			annotations: withSourceUID,
			cached:      sourceCached,
			data:        tampered,
			setup:       expectUpdate(nil, errors.New("update failed")),
			want:        false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			be := appmock.NewRepository(t)
			if tt.setup != nil {
				tt.setup(be)
			}
			repoCache := cache.NewSourceCache().Repository
			for uid, data := range tt.cached {
				repoCache.Set(uid, data)
			}
			repo := newTestRepository("test-repo")
			repo.Annotations = tt.annotations
			repo.Data = tt.data

			got := NewManager(be, testNamespace, false).RevertRepositoryChanges(context.Background(), repo, repoCache)

			require.Equal(t, tt.want, got)
			if tt.wantData != nil {
				require.Equal(t, tt.wantData, repo.Data)
			}
		})
	}
}

func Test_UpdateManagedRepository(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "argocd",
			Annotations: map[string]string{
				manager.SourceUIDAnnotation: "source-uid",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "external-secrets.io/v1",
					Kind:       "ExternalSecret",
					Name:       "test-repo",
					UID:        "owner-uid",
				},
			},
		},
		Data: map[string][]byte{
			"url": []byte("https://github.com/example/repo.git"),
		},
	}
	incoming := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "argocd",
		},
		Data: map[string][]byte{
			"url": []byte("https://github.com/example/repo.git"),
		},
	}

	newManager := func(be *appmock.Repository) *RepositoryManager {
		return &RepositoryManager{
			backend:           be,
			ManagedResources:  manager.NewManagedResources(),
			ObservedResources: manager.NewObservedResources(),
		}
	}

	t.Run("Update removes stale owner references", func(t *testing.T) {
		be := appmock.NewRepository(t)
		be.On("Get", mock.Anything, "test-repo", "argocd").Return(existing.DeepCopy(), nil)
		be.On("SupportsPatch").Return(false)
		be.On("Update", mock.Anything, mock.Anything).Return(func(_ context.Context, s *corev1.Secret) (*corev1.Secret, error) {
			return s, nil
		}, nil)

		updated, err := newManager(be).UpdateManagedRepository(context.Background(), incoming.DeepCopy())
		require.NoError(t, err)
		require.Empty(t, updated.OwnerReferences)
		require.Equal(t, "source-uid", updated.Annotations[manager.SourceUIDAnnotation])
	})

	t.Run("Patch removes stale owner references", func(t *testing.T) {
		be := appmock.NewRepository(t)
		be.On("Get", mock.Anything, "test-repo", "argocd").Return(existing.DeepCopy(), nil)
		be.On("SupportsPatch").Return(true)
		var patch []byte
		be.On("Patch", mock.Anything, "test-repo", "argocd", mock.Anything).Run(func(args mock.Arguments) {
			patch = args.Get(3).([]byte)
		}).Return(&corev1.Secret{}, nil)

		_, err := newManager(be).UpdateManagedRepository(context.Background(), incoming.DeepCopy())
		require.NoError(t, err)
		require.Contains(t, string(patch), `{"op":"remove","path":"/metadata/ownerReferences"}`)
	})

	ownedIncoming := incoming.DeepCopy()
	ownedIncoming.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Name:       "owner",
			UID:        "new-owner-uid",
		},
	}

	t.Run("Update applies incoming owner references", func(t *testing.T) {
		be := appmock.NewRepository(t)
		be.On("Get", mock.Anything, "test-repo", "argocd").Return(existing.DeepCopy(), nil)
		be.On("SupportsPatch").Return(false)
		be.On("Update", mock.Anything, mock.Anything).Return(func(_ context.Context, s *corev1.Secret) (*corev1.Secret, error) {
			return s, nil
		}, nil)

		updated, err := newManager(be).UpdateManagedRepository(context.Background(), ownedIncoming.DeepCopy())
		require.NoError(t, err)
		require.Equal(t, ownedIncoming.OwnerReferences, updated.OwnerReferences)
	})

	t.Run("Patch applies incoming owner references", func(t *testing.T) {
		be := appmock.NewRepository(t)
		be.On("Get", mock.Anything, "test-repo", "argocd").Return(existing.DeepCopy(), nil)
		be.On("SupportsPatch").Return(true)
		var patch []byte
		be.On("Patch", mock.Anything, "test-repo", "argocd", mock.Anything).Run(func(args mock.Arguments) {
			patch = args.Get(3).([]byte)
		}).Return(&corev1.Secret{}, nil)

		_, err := newManager(be).UpdateManagedRepository(context.Background(), ownedIncoming.DeepCopy())
		require.NoError(t, err)
		require.Contains(t, string(patch), `{"value":"new-owner-uid","op":"replace","path":"/metadata/ownerReferences/0/uid"}`)
	})

	t.Run("Patch removes stale finalizers", func(t *testing.T) {
		withFinalizers := existing.DeepCopy()
		withFinalizers.Finalizers = []string{"example.com/finalizer"}
		be := appmock.NewRepository(t)
		be.On("Get", mock.Anything, "test-repo", "argocd").Return(withFinalizers, nil)
		be.On("SupportsPatch").Return(true)
		var patch []byte
		be.On("Patch", mock.Anything, "test-repo", "argocd", mock.Anything).Run(func(args mock.Arguments) {
			patch = args.Get(3).([]byte)
		}).Return(&corev1.Secret{}, nil)

		_, err := newManager(be).UpdateManagedRepository(context.Background(), incoming.DeepCopy())
		require.NoError(t, err)
		require.Contains(t, string(patch), `{"op":"remove","path":"/metadata/finalizers"}`)
	})
}
