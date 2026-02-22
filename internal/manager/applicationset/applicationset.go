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
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

// ApplicationSetManager manages Argo CD ApplicationSet resources on a given backend.
type ApplicationSetManager struct {
	appSetBackend backend.ApplicationSet
	namespace     string
}

// NewApplicationSetManager initializes and returns a new ApplicationSetManager.
func NewApplicationSetManager(be backend.ApplicationSet, namespace string) *ApplicationSetManager {
	return &ApplicationSetManager{
		appSetBackend: be,
		namespace:     namespace,
	}
}

// Create creates the ApplicationSet, stamping source-uid annotation and clearing
// ResourceVersion/Generation so the replica gets a clean create.
func (m *ApplicationSetManager) Create(ctx context.Context, appSet *v1alpha1.ApplicationSet) (*v1alpha1.ApplicationSet, error) {
	appSet.ResourceVersion = ""
	appSet.Generation = 0

	if appSet.Annotations == nil {
		appSet.Annotations = make(map[string]string)
	}
	appSet.Annotations[manager.SourceUIDAnnotation] = string(appSet.UID)

	created, err := m.appSetBackend.Create(ctx, appSet)
	if err != nil {
		return nil, err
	}
	log().WithField("applicationset", appSet.Name).Debug("Created ApplicationSet")
	return created, nil
}

// Upsert creates the ApplicationSet or updates it if it already exists.
// Preserves the source-uid annotation from the existing object on update.
func (m *ApplicationSetManager) Upsert(ctx context.Context, appSet *v1alpha1.ApplicationSet) (*v1alpha1.ApplicationSet, error) {
	created, err := m.Create(ctx, appSet)
	if err == nil {
		return created, nil
	}
	if !k8serrors.IsAlreadyExists(err) {
		return nil, err
	}

	existing, err := m.appSetBackend.Get(ctx, appSet.Name, appSet.Namespace)
	if err != nil {
		return nil, fmt.Errorf("get existing applicationset for upsert: %w", err)
	}

	appSet.UID = existing.UID
	appSet.ResourceVersion = existing.ResourceVersion
	appSet.Generation = existing.Generation

	if v, ok := existing.Annotations[manager.SourceUIDAnnotation]; ok {
		if appSet.Annotations == nil {
			appSet.Annotations = make(map[string]string)
		}
		appSet.Annotations[manager.SourceUIDAnnotation] = v
	}

	updated, err := m.appSetBackend.Update(ctx, appSet)
	if err != nil {
		return nil, err
	}
	log().WithField("applicationset", appSet.Name).Debug("Updated ApplicationSet")
	return updated, nil
}

// Get retrieves an ApplicationSet by name and namespace.
func (m *ApplicationSetManager) Get(ctx context.Context, name, namespace string) (*v1alpha1.ApplicationSet, error) {
	return m.appSetBackend.Get(ctx, name, namespace)
}

// Delete deletes an ApplicationSet.
func (m *ApplicationSetManager) Delete(ctx context.Context, namespace string, appSet *v1alpha1.ApplicationSet, opts *backend.DeletionPropagation) error {
	return m.appSetBackend.Delete(ctx, appSet.Name, namespace, opts)
}

// List lists all ApplicationSets across all namespaces.
func (m *ApplicationSetManager) List(ctx context.Context) ([]v1alpha1.ApplicationSet, error) {
	return m.appSetBackend.List(ctx, "")
}

// EnsureSynced waits until the backend is synced.
func (m *ApplicationSetManager) EnsureSynced(duration time.Duration) error {
	return m.appSetBackend.EnsureSynced(duration)
}

// StartBackend starts the backend informer.
func (m *ApplicationSetManager) StartBackend(ctx context.Context) error {
	return m.appSetBackend.StartInformer(ctx)
}

func log() *logrus.Entry {
	return logging.GetDefaultLogger().ComponentLogger("ApplicationSetManager")
}
