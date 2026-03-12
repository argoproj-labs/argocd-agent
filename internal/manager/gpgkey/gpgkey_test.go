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

package gpgkey

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

func Test_CompareSourceUIDForGPGKey(t *testing.T) {
	oldCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-gpg-keys-cm",
			Namespace: "argocd",
			Annotations: map[string]string{
				manager.SourceUIDAnnotation: "old_uid",
			},
		},
		Data: map[string]string{
			"my-key": "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...",
		},
	}

	mockedBackend := appmock.NewGPGKey(t)
	getMock := mockedBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(oldCM, nil)
	m := &GPGKeyManager{backend: mockedBackend}
	ctx := context.Background()

	t.Cleanup(func() {
		getMock.Unset()
	})

	t.Run("should return true if the UID matches", func(t *testing.T) {
		incoming := oldCM.DeepCopy()
		incoming.UID = types.UID("old_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.True(t, exists)
		require.Nil(t, err)
		require.True(t, uidMatch)
	})

	t.Run("should return false if the UID doesn't match", func(t *testing.T) {
		incoming := oldCM.DeepCopy()
		incoming.UID = types.UID("new_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.True(t, exists)
		require.Nil(t, err)
		require.False(t, uidMatch)
	})

	t.Run("should return exists true and uidMatch false if there is no UID annotation", func(t *testing.T) {
		getMock.Unset()
		oldCM.Annotations = map[string]string{}
		getMock = mockedBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(oldCM, nil)
		m := &GPGKeyManager{backend: mockedBackend}
		ctx := context.Background()

		incoming := oldCM.DeepCopy()
		incoming.UID = types.UID("new_uid")

		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.Nil(t, err)
		require.True(t, exists)
		require.False(t, uidMatch)
	})

	t.Run("should return False if the configmap doesn't exist", func(t *testing.T) {
		expectedErr := k8serrors.NewNotFound(schema.GroupResource{Group: "", Resource: "configmap"},
			oldCM.Name)
		getMock.Unset()
		getMock = mockedBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedErr)
		m := &GPGKeyManager{backend: mockedBackend}
		ctx := context.Background()

		cm, err := m.backend.Get(ctx, oldCM.Name, oldCM.Namespace)
		require.Nil(t, cm)
		require.True(t, k8serrors.IsNotFound(err))

		incoming := oldCM.DeepCopy()
		incoming.UID = types.UID("new_uid")
		exists, uidMatch, err := m.CompareSourceUID(ctx, incoming)
		require.False(t, exists)
		require.False(t, uidMatch)
		require.Nil(t, err)
	})
}

func Test_GPGKeyManagerCreate(t *testing.T) {
	t.Run("Create a GPG key ConfigMap that already exists", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "argocd-gpg-keys-cm",
				Namespace: "argocd",
				UID:       "test-uid",
			},
			Data: map[string]string{
				"my-key": "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...",
			},
		}

		existsError := k8serrors.NewAlreadyExists(schema.GroupResource{Group: "", Resource: "configmap"}, "argocd-gpg-keys-cm")
		mockedBackend := appmock.NewGPGKey(t)
		mockedBackend.On("Create", mock.Anything, mock.Anything).Return(nil, existsError)

		m := &GPGKeyManager{
			backend:           mockedBackend,
			ManagedResources:  manager.NewManagedResources(),
			ObservedResources: manager.NewObservedResources(),
		}
		_, err := m.Create(context.Background(), cm)
		require.Error(t, err)
		require.True(t, k8serrors.IsAlreadyExists(err))
	})

	t.Run("Create a new GPG key ConfigMap successfully", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "argocd-gpg-keys-cm",
				Namespace: "argocd",
				UID:       "test-uid",
			},
			Data: map[string]string{
				"my-key": "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...",
			},
		}

		expectedCM := cm.DeepCopy()
		expectedCM.ResourceVersion = "1"
		expectedCM.Annotations = map[string]string{
			manager.SourceUIDAnnotation: string(cm.UID),
		}

		mockedBackend := appmock.NewGPGKey(t)
		mockedBackend.On("Create", mock.Anything, mock.MatchedBy(func(r *corev1.ConfigMap) bool {
			return r.ResourceVersion == "" && r.Generation == 0 &&
				r.Annotations[manager.SourceUIDAnnotation] == string(cm.UID)
		})).Return(expectedCM, nil)

		m := &GPGKeyManager{
			backend:           mockedBackend,
			ManagedResources:  manager.NewManagedResources(),
			ObservedResources: manager.NewObservedResources(),
		}

		createdCM, err := m.Create(context.Background(), cm)
		require.NoError(t, err)
		require.NotNil(t, createdCM)
		require.Equal(t, "argocd-gpg-keys-cm", createdCM.Name)
		require.Equal(t, string(cm.UID), createdCM.Annotations[manager.SourceUIDAnnotation])
		require.Equal(t, "1", createdCM.ResourceVersion)
	})

	t.Run("Create GPG key ConfigMap clears ResourceVersion and Generation", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "argocd-gpg-keys-cm",
				Namespace:       "argocd",
				UID:             "test-uid",
				ResourceVersion: "123",
				Generation:      5,
			},
			Data: map[string]string{
				"my-key": "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...",
			},
		}

		expectedCM := cm.DeepCopy()
		expectedCM.ResourceVersion = "1"
		expectedCM.Generation = 1
		expectedCM.Annotations = map[string]string{
			manager.SourceUIDAnnotation: string(cm.UID),
		}

		mockedBackend := appmock.NewGPGKey(t)
		mockedBackend.On("Create", mock.Anything, mock.MatchedBy(func(r *corev1.ConfigMap) bool {
			return r.ResourceVersion == "" && r.Generation == 0
		})).Return(expectedCM, nil)

		m := &GPGKeyManager{
			backend:           mockedBackend,
			ManagedResources:  manager.NewManagedResources(),
			ObservedResources: manager.NewObservedResources(),
		}

		createdCM, err := m.Create(context.Background(), cm)
		require.NoError(t, err)
		require.Equal(t, "", cm.ResourceVersion)
		require.Equal(t, int64(0), cm.Generation)
		require.Equal(t, "1", createdCM.ResourceVersion)
		require.Equal(t, string(cm.UID), createdCM.Annotations[manager.SourceUIDAnnotation])
	})
}
