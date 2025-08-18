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

package appproject

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/wI2L/jsondiff"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

type updateTransformer func(existing, incoming *v1alpha1.AppProject)
type patchTransformer func(existing, incoming *v1alpha1.AppProject) (jsondiff.Patch, error)

const (
	// LastUpdatedAnnotation is a label put on AppProject which contains the time
	// when an update was last received for this AppProject
	LastUpdatedAnnotation = "argocd-agent.argoproj.io/last-updated"

	// DefaultAppProjectName is the name of the default AppProject
	DefaultAppProjectName = "default"
)

// AppProjectManager manages Argo CD AppProject resources on a given backend.
//
// It provides primitives to create, update, upsert and delete AppProjects.
type AppProjectManager struct {
	allowUpsert       bool
	appprojectBackend backend.AppProject
	role              manager.ManagerRole
	// mode is only set when Role is ManagerRoleAgent
	mode manager.ManagerMode
	// namespace is not guaranteed to have a value in all cases. For instance, this value is empty for principal when the principal is running on cluster.
	namespace string
	// managedAppProjects is a list of apps we manage, key is name of AppProjects CR, value is not used.
	// - acquire 'lock' before accessing
	managedAppProjects map[string]bool
	// observedApp, key is qualified name of the AppProject, value is the AppProjects's .metadata.resourceValue field
	// - acquire 'lock' before accessing
	observedAppProjects map[string]string
	// lock should be acquired before accessing managedAppProjects/observedAppProjects
	lock sync.RWMutex
}

// AppProjectManagerOption is a callback function to set an option to the AppProject manager
type AppProjectManagerOption func(*AppProjectManager)

// NewAppProjectManager initializes and returns a new Manager with the given backend and
// options.
func NewAppProjectManager(be backend.AppProject, namespace string, opts ...AppProjectManagerOption) (*AppProjectManager, error) {
	m := &AppProjectManager{}
	for _, o := range opts {
		o(m)
	}
	m.namespace = namespace
	m.appprojectBackend = be
	m.observedAppProjects = make(map[string]string)
	m.managedAppProjects = make(map[string]bool)

	if m.role == manager.ManagerRolePrincipal && m.mode != manager.ManagerModeUnset {
		return nil, fmt.Errorf("mode should be unset when role is principal")
	}

	return m, nil
}

// WithAllowUpsert sets the upsert operations allowed flag
func WithAllowUpsert(upsert bool) AppProjectManagerOption {
	return func(m *AppProjectManager) {
		m.allowUpsert = upsert
	}
}

// WithRole sets the role of the AppProject manager
func WithRole(role manager.ManagerRole) AppProjectManagerOption {
	return func(m *AppProjectManager) {
		m.role = role
	}
}

// WithMode sets the mode of the AppProject manager
func WithMode(mode manager.ManagerMode) AppProjectManagerOption {
	return func(m *AppProjectManager) {
		m.mode = mode
	}
}

// stampLastUpdated "stamps" an app-project with the last updated label
func stampLastUpdated(app *v1alpha1.AppProject) {
	if app.Annotations == nil {
		app.Annotations = make(map[string]string)
	}
	app.Annotations[LastUpdatedAnnotation] = time.Now().Format(time.RFC3339)
}

// StartBackend informs the backend to run startup logic, which usually means beginning to listen for events.
// For example, in the case of the Kubernetes backend, the shared informer is started, which will listen for AppProject events from the watch api of the K8s cluster.
func (m *AppProjectManager) StartBackend(ctx context.Context) error {
	return m.appprojectBackend.StartInformer(ctx)
}

// Create creates the AppProject using the Manager's AppProject backend.
func (m *AppProjectManager) Create(ctx context.Context, project *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {

	// A new AppProject must neither specify ResourceVersion nor Generation
	project.ResourceVersion = ""
	project.Generation = 0

	// We never want Operation to be set on the principal's side.
	if m.role == manager.ManagerRolePrincipal {
		stampLastUpdated(project)
	}

	if project.Annotations == nil {
		project.Annotations = make(map[string]string)
	}
	project.Annotations[manager.SourceUIDAnnotation] = string(project.UID)

	// AppProject must be created in the agent's namespace, which should be the
	// same as ArgoCD's namespace.
	project.Namespace = m.namespace
	created, err := createAppProject(ctx, m, project)
	if err != nil {
		return nil, err
	}
	return created, nil
}

func createAppProject(ctx context.Context, m *AppProjectManager, project *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
	created, err := m.appprojectBackend.Create(ctx, project)
	if err == nil {
		if err := m.Manage(created.Name); err != nil {
			log().Warnf("Could not manage app %s: %v", created.Name, err)
		}
		if err := m.IgnoreChange(created.Name, created.ResourceVersion); err != nil {
			log().Warnf("Could not ignore change %s for app %s: %v", created.ResourceVersion, created.Name, err)
		}
		return created, nil
	}
	return nil, err
}

func (m *AppProjectManager) Get(ctx context.Context, name, namespace string) (*v1alpha1.AppProject, error) {
	return m.appprojectBackend.Get(ctx, name, namespace)
}

// UpdateAppProject updates the AppProject resource on the agent's backend.
//
// The AppProject on the agent will inherit labels and annotations as well as the spec
// of the incoming AppProject.
func (m *AppProjectManager) UpdateAppProject(ctx context.Context, incoming *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
	logCtx := log().WithFields(logrus.Fields{
		"component":       "UpdateManaged",
		"appProject":      incoming.Name,
		"resourceVersion": incoming.ResourceVersion,
	})

	var updated *v1alpha1.AppProject
	var err error

	updated, err = m.update(ctx, m.allowUpsert, incoming, func(existing, incoming *v1alpha1.AppProject) {
		if v, ok := existing.Annotations[manager.SourceUIDAnnotation]; ok {
			if incoming.Annotations == nil {
				incoming.Annotations = make(map[string]string)
			}
			incoming.Annotations[manager.SourceUIDAnnotation] = v
		}

		existing.Annotations = incoming.Annotations
		existing.Labels = incoming.Labels
		existing.Finalizers = incoming.Finalizers
		existing.Spec = *incoming.Spec.DeepCopy()
		existing.Status = *incoming.Status.DeepCopy()
	}, func(existing, incoming *v1alpha1.AppProject) (jsondiff.Patch, error) {
		if v, ok := existing.Annotations[manager.SourceUIDAnnotation]; ok {
			if incoming.Annotations == nil {
				incoming.Annotations = make(map[string]string)
			}
			incoming.Annotations[manager.SourceUIDAnnotation] = v
		}

		target := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Annotations: incoming.Annotations,
				Labels:      incoming.Labels,
				Finalizers:  incoming.Finalizers,
			},
			Spec: incoming.Spec,
		}
		source := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Annotations: existing.Annotations,
				Labels:      existing.Labels,
			},
			Spec:   existing.Spec,
			Status: existing.Status,
		}
		patch, err := jsondiff.Compare(source, target)
		if err != nil {
			return nil, err
		}
		return patch, err
	})
	if err == nil {
		if updated.Generation == 1 {
			logCtx.Infof("Created AppProject")
		} else {
			logCtx.Infof("Updated AppProject")
		}
		if err := m.IgnoreChange(updated.Name, updated.ResourceVersion); err != nil {
			logCtx.Warnf("Couldn't unignore change %s for AppProject %s: %v", updated.ResourceVersion, updated.Name, err)
		}
	}
	return updated, err
}

// update updates an existing AppProject resource on the Manager m's backend
// to match the incoming resource. If the backend supports patch, the existing
// resource will be patched, otherwise it will be updated.
//
// For a patch operation, patchFn is executed to calculate the patch that will
// be applied. Likewise, for an update operation, updateFn is executed to
// determine the final resource state to be updated.
//
// If upsert is set to true, and the AppProject resource does not yet exist
// on the backend, it will be created.
//
// The updated AppProject will be returned on success, otherwise an error will
// be returned.
func (m *AppProjectManager) update(ctx context.Context, upsert bool, incoming *v1alpha1.AppProject, updateFn updateTransformer, patchFn patchTransformer) (*v1alpha1.AppProject, error) {
	var updated *v1alpha1.AppProject
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		existing, ierr := m.appprojectBackend.Get(ctx, incoming.Name, incoming.Namespace)
		if ierr != nil {
			if errors.IsNotFound(ierr) && upsert {
				updated, ierr = m.Create(ctx, incoming)
				return ierr
			} else {
				return fmt.Errorf("error updating app-project %s: %w", incoming.Name, ierr)
			}
		} else {
			if m.appprojectBackend.SupportsPatch() && patchFn != nil {
				patch, err := patchFn(existing, incoming)
				if err != nil {
					return fmt.Errorf("could not create patch: %w", err)
				}
				jsonpatch, err := json.Marshal(patch)
				if err != nil {
					return fmt.Errorf("could not marshal jsonpatch: %w", err)
				}
				updated, ierr = m.appprojectBackend.Patch(ctx, incoming.Name, incoming.Namespace, jsonpatch)
			} else {
				if updateFn != nil {
					updateFn(existing, incoming)
				}
				updated, ierr = m.appprojectBackend.Update(ctx, existing)
			}
		}
		return ierr
	})
	return updated, err
}

// Delete will delete an AppProject resource. If Delete is called by the
// principal, any existing finalizers will be removed before deletion is
// attempted.
// 'deletionPropagation' follows the corresponding K8s behaviour, defaulting to Foreground if nil.
func (m *AppProjectManager) Delete(ctx context.Context, incoming *v1alpha1.AppProject, deletionPropagation *backend.DeletionPropagation) error {
	removeFinalizer := false
	logCtx := log().WithFields(logrus.Fields{
		"component":       "DeleteOperation",
		"appProject":      incoming.Name,
		"resourceVersion": incoming.ResourceVersion,
	})
	if m.role.IsPrincipal() {
		removeFinalizer = true
	}
	var err error
	var updated *v1alpha1.AppProject

	if removeFinalizer {
		updated, err = m.RemoveFinalizers(ctx, incoming)
		if err == nil {
			logCtx.Debugf("Removed finalizer for app %s", updated.Name)
		} else {
			return fmt.Errorf("error removing finalizer: %w", err)
		}
	}

	return m.appprojectBackend.Delete(ctx, incoming.Name, incoming.Namespace, deletionPropagation)
}

// RemoveFinalizers will remove finalizers on an existing app project.
func (m *AppProjectManager) RemoveFinalizers(ctx context.Context, incoming *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
	updated, err := m.update(ctx, false, incoming, func(existing, incoming *v1alpha1.AppProject) {
		existing.Finalizers = nil
	}, func(existing, incoming *v1alpha1.AppProject) (jsondiff.Patch, error) {
		var err error
		var patch jsondiff.Patch
		target := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Finalizers: nil,
			},
		}
		source := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Finalizers: existing.Finalizers,
			},
		}
		patch, err = jsondiff.Compare(source, target, jsondiff.SkipCompact())
		return patch, err
	})
	return updated, err
}

// EnsureSynced waits until either the backend indicates it has fully synced, or the
// timeout has been reached (which will return an error)
func (m *AppProjectManager) EnsureSynced(duration time.Duration) error {
	return m.appprojectBackend.EnsureSynced(duration)
}

// CompareSourceUID checks for an existing appProject with the same name/namespace and compare its source UID with the incoming appProject.
func (m *AppProjectManager) CompareSourceUID(ctx context.Context, incoming *v1alpha1.AppProject) (bool, bool, error) {
	existing, err := m.appprojectBackend.Get(ctx, incoming.Name, incoming.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, false, nil
		}
		return false, false, err
	}

	// If there is an existing appProject with the same name/namespace, compare its source UID with the incoming appProject.
	sourceUID, ok := existing.Annotations[manager.SourceUIDAnnotation]
	if !ok {
		return true, false, fmt.Errorf("source UID Annotation is not found for appProject: %s", incoming.Name)
	}

	return true, string(incoming.UID) == sourceUID, nil
}

func log() *logrus.Entry {
	return logging.ComponentLogger("AppProjectManager")
}
