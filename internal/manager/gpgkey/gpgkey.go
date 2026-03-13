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
	"reflect"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/cache"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

type GPGKeyManager struct {
	namespace string
	backend   backend.GPGKey

	manager.ManagedResources
	manager.ObservedResources
}

func NewManager(be backend.GPGKey, namespace string) *GPGKeyManager {
	return &GPGKeyManager{
		namespace:         namespace,
		backend:           be,
		ManagedResources:  manager.NewManagedResources(),
		ObservedResources: manager.NewObservedResources(),
	}
}

func (m *GPGKeyManager) StartBackend(ctx context.Context) error {
	return m.backend.StartInformer(ctx)
}

func (m *GPGKeyManager) EnsureSynced(timeout time.Duration) error {
	return m.backend.EnsureSynced(timeout)
}

func (m *GPGKeyManager) Get(ctx context.Context, name string, namespace string) (*corev1.ConfigMap, error) {
	return m.backend.Get(ctx, name, namespace)
}

func (m *GPGKeyManager) CompareSourceUID(ctx context.Context, incoming *corev1.ConfigMap) (bool, bool, error) {
	existing, err := m.backend.Get(ctx, incoming.Name, incoming.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, false, nil
		}
		return false, false, err
	}

	sourceUID, exists := existing.Annotations[manager.SourceUIDAnnotation]
	if !exists {
		return true, false, nil
	}

	return true, string(incoming.UID) == sourceUID, nil
}

func (m *GPGKeyManager) Create(ctx context.Context, cm *corev1.ConfigMap) (*corev1.ConfigMap, error) {
	cm.ResourceVersion = ""
	cm.Generation = 0

	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}
	cm.Annotations[manager.SourceUIDAnnotation] = string(cm.UID)

	created, err := m.backend.Create(ctx, cm)
	if err == nil {
		if err := m.Manage(created.Name); err != nil {
			log().Warnf("Could not manage GPG keys configmap %s: %v", created.Name, err)
		}
		if err := m.IgnoreChange(created.Name, created.ResourceVersion); err != nil {
			log().Warnf("Could not ignore change %s for GPG keys configmap %s: %v", created.ResourceVersion, created.Name, err)
		}
		return created, nil
	}
	return nil, err
}

func (m *GPGKeyManager) Delete(ctx context.Context, name, namespace string, deletionPropagation *backend.DeletionPropagation) error {
	return m.backend.Delete(ctx, name, namespace, deletionPropagation)
}

func (m *GPGKeyManager) UpdateManagedGPGKey(ctx context.Context, incoming *corev1.ConfigMap) (*corev1.ConfigMap, error) {
	logCtx := log().WithFields(logrus.Fields{
		"component": "UpdateManagedGPGKey",
		"name":      incoming.Name,
	})

	existing, err := m.backend.Get(ctx, incoming.Name, incoming.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return m.Create(ctx, incoming)
		}
		return nil, err
	}

	if v, ok := existing.Annotations[manager.SourceUIDAnnotation]; ok {
		if incoming.Annotations == nil {
			incoming.Annotations = make(map[string]string)
		}
		incoming.Annotations[manager.SourceUIDAnnotation] = v
	}
	existing.Data = incoming.Data
	existing.Annotations = incoming.Annotations
	existing.Labels = incoming.Labels

	updated, err := m.backend.Update(ctx, existing)
	if err == nil {
		if updated.Generation == 1 {
			logCtx.Infof("Created GPG keys ConfigMap")
		} else {
			logCtx.Infof("Updated GPG keys ConfigMap")
		}
		if err := m.IgnoreChange(updated.Name, updated.ResourceVersion); err != nil {
			logCtx.Warnf("Could not ignore change %s for GPG keys ConfigMap %s: %v", updated.ResourceVersion, updated.Name, err)
		}
	}
	return updated, err
}

func (m *GPGKeyManager) RevertGPGKeyChanges(ctx context.Context, cm *corev1.ConfigMap, gpgKeyCache *cache.ResourceCache[map[string]string]) bool {
	logCtx := log().WithFields(logrus.Fields{
		"component": "RevertGPGKeyChanges",
		"name":      cm.Name,
	})

	sourceUID, exists := cm.Annotations[manager.SourceUIDAnnotation]
	if !exists {
		return false
	}

	if cachedData, ok := gpgKeyCache.Get(types.UID(sourceUID)); ok {
		logCtx.Debugf("GPG keys ConfigMap is available in agent cache")

		if !reflect.DeepEqual(cachedData, cm.Data) {
			cm.Data = cachedData
			logCtx.Infof("Reverting modifications done to GPG keys ConfigMap")
			if _, err := m.UpdateManagedGPGKey(ctx, cm); err != nil {
				logCtx.Errorf("Unable to revert modifications done in GPG keys ConfigMap. Error: %v", err)
				return false
			}
			return true
		}
		logCtx.Debugf("GPG keys ConfigMap is already in sync with source cache")
	} else {
		logCtx.Errorf("GPG keys ConfigMap is not available in agent cache")
	}

	return false
}

func log() *logrus.Entry {
	return logrus.WithField("component", "GPGKeyManager")
}
