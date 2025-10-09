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
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/cache"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/sirupsen/logrus"
	"github.com/wI2L/jsondiff"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

type updateTransformer func(existing, incoming *corev1.Secret)
type patchTransformer func(existing, incoming *corev1.Secret) (jsondiff.Patch, error)

type RepositoryManager struct {
	allowUpsert bool
	namespace   string
	backend     backend.Repository

	// ManagedResources is a list of Repositories we manage, key is Repository's name, value is not used.
	manager.ManagedResources
	// ObservedResources, key is Repository's name, value is the Repository's .metadata.resourceValue field
	manager.ObservedResources
}

func NewManager(be backend.Repository, namespace string, usePatch bool) *RepositoryManager {

	return &RepositoryManager{
		allowUpsert:       usePatch,
		namespace:         namespace,
		backend:           be,
		ManagedResources:  manager.NewManagedResources(),
		ObservedResources: manager.NewObservedResources(),
	}
}

func (m *RepositoryManager) StartBackend(ctx context.Context) error {
	return m.backend.StartInformer(ctx)
}

func (m *RepositoryManager) EnsureSynced(timeout time.Duration) error {
	return m.backend.EnsureSynced(timeout)
}

// CompareSourceUID checks for an existing repository with the same name/namespace and compare its source UID with the incoming repository.
func (m *RepositoryManager) CompareSourceUID(ctx context.Context, incoming *corev1.Secret) (bool, bool, error) {
	existing, err := m.backend.Get(ctx, incoming.Name, incoming.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, false, nil
		}
		return false, false, err
	}

	// If there is an existing repository with the same name/namespace, compare its source UID with the incoming repository.
	sourceUID, exists := existing.Annotations[manager.SourceUIDAnnotation]
	if !exists {
		return true, false, fmt.Errorf("source UID Annotation is not found for repository: %s", incoming.Name)
	}

	return true, string(incoming.UID) == sourceUID, nil
}

// Create creates the Repository using the Manager's Repository backend.
func (m *RepositoryManager) Create(ctx context.Context, repo *corev1.Secret) (*corev1.Secret, error) {

	// A new Repository must neither specify ResourceVersion nor Generation
	repo.ResourceVersion = ""
	repo.Generation = 0

	if repo.Annotations == nil {
		repo.Annotations = make(map[string]string)
	}
	repo.Annotations[manager.SourceUIDAnnotation] = string(repo.UID)

	created, err := m.backend.Create(ctx, repo)
	if err == nil {
		if err := m.Manage(created.Name); err != nil {
			log().Warnf("Could not manage repository %s: %v", created.Name, err)
		}
		if err := m.IgnoreChange(created.Name, created.ResourceVersion); err != nil {
			log().Warnf("Could not ignore change %s for repository %s: %v", created.ResourceVersion, created.Name, err)
		}
		return created, nil
	}
	return nil, err
}

func (m *RepositoryManager) Delete(ctx context.Context, name, namespace string, deletionPropagation *backend.DeletionPropagation) error {
	return m.backend.Delete(ctx, name, namespace, deletionPropagation)
}

func (m *RepositoryManager) List(ctx context.Context, selector backend.RepositorySelector) ([]corev1.Secret, error) {
	return m.backend.List(ctx, selector)
}

func (m *RepositoryManager) Get(ctx context.Context, name, namespace string) (*corev1.Secret, error) {
	return m.backend.Get(ctx, name, namespace)
}

// UpdateManagedRepository updates the Repository resource on the agent when it is in
// managed mode.
//
// The repository on the agent will inherit labels and annotations as well as the data of the incoming repository.
func (m *RepositoryManager) UpdateManagedRepository(ctx context.Context, incoming *corev1.Secret) (*corev1.Secret, error) {
	logCtx := log().WithFields(logrus.Fields{
		"component":       "UpdateManaged",
		"repository":      incoming.Name,
		"resourceVersion": incoming.ResourceVersion,
	})

	var updated *corev1.Secret
	var err error

	updated, err = m.update(ctx, m.allowUpsert, incoming, func(existing, incoming *corev1.Secret) {
		if v, ok := existing.Annotations[manager.SourceUIDAnnotation]; ok {
			if incoming.Annotations == nil {
				incoming.Annotations = make(map[string]string)
			}
			incoming.Annotations[manager.SourceUIDAnnotation] = v
		}
		existing.Annotations = incoming.Annotations
		existing.Labels = incoming.Labels
		existing.Finalizers = incoming.Finalizers
		existing.Data = incoming.Data
	}, func(existing, incoming *corev1.Secret) (jsondiff.Patch, error) {
		if v, ok := existing.Annotations[manager.SourceUIDAnnotation]; ok {
			if incoming.Annotations == nil {
				incoming.Annotations = make(map[string]string)
			}
			incoming.Annotations[manager.SourceUIDAnnotation] = v
		}

		target := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: incoming.Annotations,
				Labels:      incoming.Labels,
				Finalizers:  incoming.Finalizers,
			},
			Data: incoming.Data,
		}
		source := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: existing.Annotations,
				Labels:      existing.Labels,
			},
			Data: existing.Data,
		}
		patch, err := jsondiff.Compare(source, target)
		if err != nil {
			return nil, err
		}
		return patch, err
	})
	if err == nil {
		if updated.Generation == 1 {
			logCtx.Infof("Created Repository")
		} else {
			logCtx.Infof("Updated Repository")
		}
		if err := m.IgnoreChange(updated.Name, updated.ResourceVersion); err != nil {
			logCtx.Warnf("Couldn't unignore change %s for Repository %s: %v", updated.ResourceVersion, updated.Name, err)
		}
	}
	return updated, err
}

// update updates an existing Repository resource on the Manager m's backend
// to match the incoming resource. If the backend supports patch, the existing
// resource will be patched, otherwise it will be updated.
//
// For a patch operation, patchFn is executed to calculate the patch that will
// be applied. Likewise, for an update operation, updateFn is executed to
// determine the final resource state to be updated.
//
// If upsert is set to true, and the Repository resource does not yet exist
// on the backend, it will be created.
//
// The updated repository will be returned on success, otherwise an error will
// be returned.
func (m *RepositoryManager) update(ctx context.Context, upsert bool, incoming *corev1.Secret, updateFn updateTransformer, patchFn patchTransformer) (*corev1.Secret, error) {
	var updated *corev1.Secret
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		existing, ierr := m.backend.Get(ctx, incoming.Name, incoming.Namespace)
		if ierr != nil {
			if errors.IsNotFound(ierr) && upsert {
				updated, ierr = m.Create(ctx, incoming)
				return ierr
			} else {
				return fmt.Errorf("error updating repository %s: %w", incoming.Name, ierr)
			}
		} else {
			if m.backend.SupportsPatch() && patchFn != nil {
				patch, err := patchFn(existing, incoming)
				if err != nil {
					return fmt.Errorf("could not create patch: %w", err)
				}
				jsonpatch, err := json.Marshal(patch)
				if err != nil {
					return fmt.Errorf("could not marshal jsonpatch: %w", err)
				}
				updated, ierr = m.backend.Patch(ctx, incoming.Name, incoming.Namespace, jsonpatch)
			} else {
				if updateFn != nil {
					updateFn(existing, incoming)
				}
				updated, ierr = m.backend.Update(ctx, existing)
			}
		}
		return ierr
	})
	return updated, err
}

// RevertRepositoryChanges compares the actual spec with expected spec stored in cache,
// if actual spec doesn't match with cache, then it is reverted to be in sync with cache, which is same as the source cluster.
func (m *RepositoryManager) RevertRepositoryChanges(ctx context.Context, repo *corev1.Secret, repoCache *cache.ResourceCache[map[string][]byte]) bool {
	logCtx := log().WithFields(logrus.Fields{
		"component":       "RevertRepositoryChanges",
		"repository":      repo.Name,
		"resourceVersion": repo.ResourceVersion,
	})

	sourceUID, exists := repo.Annotations[manager.SourceUIDAnnotation]
	if !exists {
		return false
	}

	if cachedData, ok := repoCache.Get(types.UID(sourceUID)); ok {
		logCtx.Debugf("Repository is available in agent cache")

		if isEqual := reflect.DeepEqual(cachedData, repo.Data); !isEqual {
			repo.Data = cachedData
			logCtx.Infof("Reverting modifications done to repository")
			if _, err := m.UpdateManagedRepository(ctx, repo); err != nil {
				logCtx.Errorf("Unable to revert modifications done in repository. Error: %v", err)
				return false
			}
			return true
		} else {
			logCtx.Debugf("Repository is already in sync with source cache")
		}
	} else {
		logCtx.Errorf("Repository is not available in agent cache")
	}

	return false
}

func log() *logrus.Entry {
	return logrus.WithField("component", "RepositoryManager")
}
