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

package applicationset

import (
	"context"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ktypes "k8s.io/apimachinery/pkg/types"
)

// mockBackend is a minimal mock for backend.ApplicationSet
type mockBackend struct {
	mock.Mock
}

func (m *mockBackend) Create(ctx context.Context, appSet *v1alpha1.ApplicationSet) (*v1alpha1.ApplicationSet, error) {
	args := m.Called(ctx, appSet)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1alpha1.ApplicationSet), args.Error(1)
}

func (m *mockBackend) Get(ctx context.Context, name, namespace string) (*v1alpha1.ApplicationSet, error) {
	args := m.Called(ctx, name, namespace)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1alpha1.ApplicationSet), args.Error(1)
}

func (m *mockBackend) Update(ctx context.Context, appSet *v1alpha1.ApplicationSet) (*v1alpha1.ApplicationSet, error) {
	args := m.Called(ctx, appSet)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1alpha1.ApplicationSet), args.Error(1)
}

func (m *mockBackend) Delete(ctx context.Context, name, namespace string, prop *backend.DeletionPropagation) error {
	return m.Called(ctx, name, namespace, prop).Error(0)
}

func (m *mockBackend) List(ctx context.Context, namespace string) ([]v1alpha1.ApplicationSet, error) {
	args := m.Called(ctx, namespace)
	return args.Get(0).([]v1alpha1.ApplicationSet), args.Error(1)
}

func (m *mockBackend) StartInformer(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *mockBackend) EnsureSynced(d time.Duration) error {
	return m.Called(d).Error(0)
}

func newTestAppSet(name, namespace string, uid ktypes.UID) *v1alpha1.ApplicationSet {
	return &v1alpha1.ApplicationSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       uid,
		},
	}
}

func Test_Create_StampsSourceUID(t *testing.T) {
	be := new(mockBackend)
	m := NewApplicationSetManager(be, "argocd")
	ctx := context.Background()

	appSet := newTestAppSet("test", "argocd", "original-uid")
	created := newTestAppSet("test", "argocd", "replica-uid")
	created.Annotations = map[string]string{
		manager.SourceUIDAnnotation: "original-uid",
	}

	be.On("Create", ctx, mock.MatchedBy(func(a *v1alpha1.ApplicationSet) bool {
		return a.Annotations[manager.SourceUIDAnnotation] == "original-uid"
	})).Return(created, nil)

	result, err := m.Create(ctx, appSet)
	require.NoError(t, err)
	require.Equal(t, "original-uid", result.Annotations[manager.SourceUIDAnnotation])
	be.AssertExpectations(t)
}

func Test_Create_ClearsResourceVersion(t *testing.T) {
	be := new(mockBackend)
	m := NewApplicationSetManager(be, "argocd")
	ctx := context.Background()

	appSet := newTestAppSet("test", "argocd", "uid-1")
	appSet.ResourceVersion = "some-rv"
	appSet.Generation = 5

	returned := newTestAppSet("test", "argocd", "uid-1")
	returned.Annotations = map[string]string{manager.SourceUIDAnnotation: "uid-1"}

	be.On("Create", ctx, mock.MatchedBy(func(a *v1alpha1.ApplicationSet) bool {
		return a.ResourceVersion == "" && a.Generation == 0
	})).Return(returned, nil)

	_, err := m.Create(ctx, appSet)
	require.NoError(t, err)
	be.AssertExpectations(t)
}

func Test_Upsert_PreservesSourceUID(t *testing.T) {
	be := new(mockBackend)
	m := NewApplicationSetManager(be, "argocd")
	ctx := context.Background()

	existing := newTestAppSet("test", "argocd", "existing-uid")
	existing.ResourceVersion = "rv-1"
	existing.Annotations = map[string]string{
		manager.SourceUIDAnnotation: "primary-uid",
	}

	incoming := newTestAppSet("test", "argocd", "some-uid")

	// Create fails with AlreadyExists
	alreadyExists := k8serrors.NewAlreadyExists(schema.GroupResource{}, "test")
	be.On("Create", ctx, mock.Anything).Return(nil, alreadyExists)
	be.On("Get", ctx, "test", "argocd").Return(existing, nil)
	be.On("Update", ctx, mock.MatchedBy(func(a *v1alpha1.ApplicationSet) bool {
		return a.Annotations[manager.SourceUIDAnnotation] == "primary-uid"
	})).Return(existing, nil)

	result, err := m.Upsert(ctx, incoming)
	require.NoError(t, err)
	require.Equal(t, "primary-uid", result.Annotations[manager.SourceUIDAnnotation])
	be.AssertExpectations(t)
}
