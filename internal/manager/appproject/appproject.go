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
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/util/glob"
	"github.com/sirupsen/logrus"
	"github.com/wI2L/jsondiff"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

type updateTransformer func(existing, incoming *v1alpha1.AppProject)
type patchTransformer func(existing, incoming *v1alpha1.AppProject) (jsondiff.Patch, error)

// LastUpdatedAnnotation is a label put on AppProject which contains the time
// when an update was last received for this AppProject
const LastUpdatedAnnotation = "argocd-agent.argoproj.io/last-updated"

// AppProjectManager manages Argo CD AppProject resources on a given backend.
//
// It provides primitives to create, update, upsert and delete AppProjects.
type AppProjectManager struct {
	allowUpsert       bool
	appprojectBackend backend.AppProject
	metrics           *metrics.AppProjectClientMetrics
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

// WithMetrics sets the metrics provider for the Manager
func WithMetrics(m *metrics.AppProjectClientMetrics) AppProjectManagerOption {
	return func(mgr *AppProjectManager) {
		mgr.metrics = m
	}
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
func (m *AppProjectManager) StartBackend(ctx context.Context) {
	m.appprojectBackend.StartInformer(ctx)
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

	// If the AppProject has a source namespace, we only allow the agent to manage the AppProject
	// if the source namespace matches the agent's namespace
	if m.role == manager.ManagerRoleAgent {
		fmt.Printf("Agent Namespace %v\n", m.namespace)
		if len(project.Spec.SourceNamespaces) > 0 {
			for _, ns := range project.Spec.SourceNamespaces {
				if glob.Match(ns, m.namespace) {
					created, err := createAppProject(ctx, m, project)
					if err != nil {
						return nil, err
					}
					return created, nil
				}
			}
		}
		return nil, fmt.Errorf("cannot create appproject: agent is not allowed to manage this namespace")
	}

	//	Principal can create AppProject in any namespace
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
		if m.metrics != nil {
			m.metrics.AppProjectsCreated.WithLabelValues(project.Namespace).Inc()
		}
		return created, nil
	} else {
		if m.metrics != nil {
			m.metrics.ProjectClientErrors.Inc()
		}
	}
	return nil, nil
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

	incoming.SetNamespace(m.namespace)

	updated, err = m.update(ctx, m.allowUpsert, incoming, func(existing, incoming *v1alpha1.AppProject) {
		existing.ObjectMeta.Annotations = incoming.ObjectMeta.Annotations
		existing.ObjectMeta.Labels = incoming.ObjectMeta.Labels
		existing.ObjectMeta.Finalizers = incoming.ObjectMeta.Finalizers
		existing.Spec = *incoming.Spec.DeepCopy()
		existing.Status = *incoming.Status.DeepCopy()
	}, func(existing, incoming *v1alpha1.AppProject) (jsondiff.Patch, error) {
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
		if m.metrics != nil {
			m.metrics.AppProjectsUpdated.WithLabelValues(incoming.Namespace).Inc()
		}
	} else {
		if m.metrics != nil {
			m.metrics.ProjectClientErrors.Inc()
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
func (m *AppProjectManager) Delete(ctx context.Context, namespace string, incoming *v1alpha1.AppProject, deletionPropagation *backend.DeletionPropagation) error {
	removeFinalizer := false
	logCtx := log().WithFields(logrus.Fields{
		"component":       "DeleteOperation",
		"appProject":      incoming.Name,
		"resourceVersion": incoming.ResourceVersion,
	})
	if m.role.IsPrincipal() {
		removeFinalizer = true
		incoming.SetNamespace(namespace)
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

	err = m.appprojectBackend.Delete(ctx, incoming.Name, incoming.Namespace, deletionPropagation)

	return err
}

// RemoveFinalizers will remove finalizers on an existing app project.
func (m *AppProjectManager) RemoveFinalizers(ctx context.Context, incoming *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
	updated, err := m.update(ctx, false, incoming, func(existing, incoming *v1alpha1.AppProject) {
		existing.ObjectMeta.Finalizers = nil
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

func log() *logrus.Entry {
	return logrus.WithField("component", "AppManager")
}
