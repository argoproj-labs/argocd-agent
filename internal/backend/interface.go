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

/*
Package backend provides the interface for implementing Application providers.
*/
package backend

import (
	"context"
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
)

type ApplicationSelector struct {

	// Labels is not currently implemented.
	Labels map[string]string

	// Names is not currently implemented.
	Names []string

	// Namespaces is used by the 'List' Application interface function to restrict the list of Applications returned to a specific set of Namespaces.
	Namespaces []string

	// Projects is not currently implemented.
	Projects []string
}

type AppProjectSelector struct {
	// Labels is not currently implemented.
	Labels map[string]string

	// Namespaces is used by the 'List' AppProject interface function to restrict the list of AppProjects returned to a specific set of Namespaces.
	Namespaces []string

	// Names is not currently implemented.
	Names []string
}

// DeletionPropagation is based on the kubernetes DeletionPropagation API, and follows its behaviours for deletion propagation,
// specifically that it controls how deletion will propagate to the dependents of the object (and corresponding GC behaviour)
type DeletionPropagation string

const (
	// Orphans the dependents: the object is deleted, the dependents are not.
	DeletePropagationOrphan DeletionPropagation = "Orphan"

	// The object is deleted, and any dependent objects are deleted in the background.
	DeletePropagationBackground DeletionPropagation = "Background"

	// The object continues to exist until all dependents are deleted. This same behaviour cascades to dependent objects.
	DeletePropagationForeground DeletionPropagation = "Foreground"
)

// Application defines a generic interface to store/track Argo CD Application state, via ApplicationManager.
//
// As of this writing (August 2024), the only implementation is a Kubernetes-based backend (KubernetesBackend in 'internal/backend/kubernetes/application') but other backends (e.g. RDBMS-backed) could be implemented in the future.
type Application interface {
	List(ctx context.Context, selector ApplicationSelector) ([]v1alpha1.Application, error)
	Create(ctx context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error)
	Get(ctx context.Context, name string, namespace string) (*v1alpha1.Application, error)
	Delete(ctx context.Context, name string, namespace string, deletionPropagation *DeletionPropagation) error
	Update(ctx context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error)
	Patch(ctx context.Context, name string, namespace string, patch []byte) (*v1alpha1.Application, error)
	SupportsPatch() bool
	StartInformer(ctx context.Context) error
	EnsureSynced(duration time.Duration) error
}

// AppProject defines a generic interface to store/track Argo CD AppProject state, via AppProjectManager.
//
// As of this writing (August 2024), the only implementation is a Kubernetes-based backend (KubernetesBackend in 'internal/backend/kubernetes/appproject') but other backends (e.g. RDBMS-backed) could be implemented in the future.
type AppProject interface {
	List(ctx context.Context, selector AppProjectSelector) ([]v1alpha1.AppProject, error)
	Create(ctx context.Context, app *v1alpha1.AppProject) (*v1alpha1.AppProject, error)
	Get(ctx context.Context, name string, namespace string) (*v1alpha1.AppProject, error)
	Delete(ctx context.Context, name string, namespace string, deletionPropagation *DeletionPropagation) error
	Update(ctx context.Context, app *v1alpha1.AppProject) (*v1alpha1.AppProject, error)
	Patch(ctx context.Context, name string, namespace string, patch []byte) (*v1alpha1.AppProject, error)
	SupportsPatch() bool
	StartInformer(ctx context.Context) error
	EnsureSynced(duration time.Duration) error
}
