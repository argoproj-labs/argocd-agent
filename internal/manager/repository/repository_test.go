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
	"context"
	"testing"

	appmock "github.com/argoproj-labs/argocd-agent/internal/backend/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

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
		_, err := m.Create(context.Background(), repo)
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

		createdRepo, err := m.Create(context.Background(), repo)
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

		createdRepo, err := m.Create(context.Background(), repo)
		require.NoError(t, err)
		require.Equal(t, "", repo.ResourceVersion)         // Original should be modified
		require.Equal(t, int64(0), repo.Generation)        // Original should be modified
		require.Equal(t, "1", createdRepo.ResourceVersion) // Returned should have backend value
		require.Equal(t, string(repo.UID), createdRepo.Annotations[manager.SourceUIDAnnotation])
	})
}
