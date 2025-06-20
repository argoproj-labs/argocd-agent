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

package application

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	appCache "github.com/argoproj-labs/argocd-agent/internal/cache"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/wI2L/jsondiff"
	ty "k8s.io/apimachinery/pkg/types"
)

type updateTransformer func(existing, incoming *v1alpha1.Application)
type patchTransformer func(existing, incoming *v1alpha1.Application) (jsondiff.Patch, error)

// LastUpdatedAnnotation is a label put on applications which contains the time
// when an update was last received for this Application
const LastUpdatedAnnotation = "argocd-agent.argoproj.io/last-updated"

// ApplicationManager manages Argo CD application resources on a given backend.
//
// It provides primitives to create, update, upsert and delete applications.
type ApplicationManager struct {
	allowUpsert        bool
	applicationBackend backend.Application
	role               manager.ManagerRole
	// mode is only set when Role is ManagerRoleAgent
	mode manager.ManagerMode
	// namespace is not guaranteed to have a value in all cases. For instance, this value is empty for principal when the principal is running on cluster.
	namespace string
	// managedApps is a list of apps we manage, key is qualified name in form '(namespace of Application CR)/(name of Application CR)', value is not used.
	// - acquire 'lock' before accessing
	managedApps map[string]bool
	// observedApp, key is qualified name of the application, value is the Application's .metadata.resourceValue field
	// - acquire 'lock' before accessing
	observedApp map[string]string
	// lock should be acquired before accessing managedApps/observedApps
	lock sync.RWMutex
}

// ApplicationManagerOption is a callback function to set an option to the Application
// manager
type ApplicationManagerOption func(*ApplicationManager)

// WithAllowUpsert sets the upsert operations allowed flag
func WithAllowUpsert(upsert bool) ApplicationManagerOption {
	return func(m *ApplicationManager) {
		m.allowUpsert = upsert
	}
}

// WithRole sets the role of the Application manager
func WithRole(role manager.ManagerRole) ApplicationManagerOption {
	return func(m *ApplicationManager) {
		m.role = role
	}
}

// WithMode sets the mode of the Application manager
func WithMode(mode manager.ManagerMode) ApplicationManagerOption {
	return func(m *ApplicationManager) {
		m.mode = mode
	}
}

// NewApplicationManager initializes and returns a new Manager with the given backend and
// options.
func NewApplicationManager(be backend.Application, namespace string, opts ...ApplicationManagerOption) (*ApplicationManager, error) {
	m := &ApplicationManager{}
	for _, o := range opts {
		o(m)
	}
	m.applicationBackend = be
	m.observedApp = make(map[string]string)
	m.managedApps = make(map[string]bool)
	m.namespace = namespace

	if m.role == manager.ManagerRolePrincipal && m.mode != manager.ManagerModeUnset {
		return nil, fmt.Errorf("mode should be unset when role is principal")
	}

	return m, nil
}

// StartBackend informs the backend to run startup logic, which usually means beginning to listen for events.
// For example, in the case of the Kubernetes backend, the shared informer is started, which will listen for Application events from the watch api of the K8s cluster.
func (m *ApplicationManager) StartBackend(ctx context.Context) error {
	return m.applicationBackend.StartInformer(ctx)
}

// EnsureSynced waits until either the backend indicates it has fully synced, or the
// timeout has been reached (which will return an error)
func (m *ApplicationManager) EnsureSynced(duration time.Duration) error {
	return m.applicationBackend.EnsureSynced(duration)
}

// stampLastUpdated "stamps" an application with the last updated label
func stampLastUpdated(app *v1alpha1.Application) {
	if app.Annotations == nil {
		app.Annotations = make(map[string]string)
	}
	app.Annotations[LastUpdatedAnnotation] = time.Now().Format(time.RFC3339)
}

// Create creates the application app using the Manager's application backend.
func (m *ApplicationManager) Create(ctx context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error) {

	// A new Application must neither specify ResourceVersion nor Generation
	app.ResourceVersion = ""
	app.Generation = 0

	// We never want Operation to be set on the principal's side.
	if m.role == manager.ManagerRolePrincipal {
		app.Operation = nil
		stampLastUpdated(app)
	}

	if app.Annotations == nil {
		app.Annotations = make(map[string]string)
	}
	app.Annotations[manager.SourceUIDAnnotation] = string(app.UID)

	created, err := m.applicationBackend.Create(ctx, app)
	if err == nil {
		if err := m.Manage(created.QualifiedName()); err != nil {
			log().Warnf("Could not manage app %s: %v", created.QualifiedName(), err)
		}
		if err := m.IgnoreChange(created.QualifiedName(), created.ResourceVersion); err != nil {
			log().Warnf("Could not ignore change %s for app %s: %v", created.ResourceVersion, created.QualifiedName(), err)
		}
	}

	return created, err
}

func (m *ApplicationManager) Get(ctx context.Context, name, namespace string) (*v1alpha1.Application, error) {
	return m.applicationBackend.Get(ctx, name, namespace)
}

// UpdateManagedApp updates the Application resource on the agent when it is in
// managed mode.
//
// The app on the agent will inherit labels and annotations as well as the spec
// and any operation field of the incoming application. A possibly existing
// refresh annotation on the agent's app will be retained, because it will be
// removed by the agent's application controller.
func (m *ApplicationManager) UpdateManagedApp(ctx context.Context, incoming *v1alpha1.Application) (*v1alpha1.Application, error) {
	logCtx := log().WithFields(logrus.Fields{
		"component":       "UpdateManaged",
		"application":     incoming.QualifiedName(),
		"resourceVersion": incoming.ResourceVersion,
	})

	var updated *v1alpha1.Application
	var err error

	incoming.SetNamespace(m.namespace)

	if m.role != manager.ManagerRoleAgent || m.mode != manager.ManagerModeManaged {
		return nil, fmt.Errorf("updatedManagedApp should be called on a managed agent, only")
	}

	updated, err = m.update(ctx, m.allowUpsert, incoming, func(existing, incoming *v1alpha1.Application) {
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
		existing.Operation = incoming.Operation.DeepCopy()
		existing.Status = *incoming.Status.DeepCopy()
	}, func(existing, incoming *v1alpha1.Application) (jsondiff.Patch, error) {
		// We need to keep the refresh label if it is set on the existing app
		if v, ok := existing.Annotations["argocd.argoproj.io/refresh"]; ok {
			if incoming.Annotations == nil {
				incoming.Annotations = make(map[string]string)
			}
			incoming.Annotations["argocd.argoproj.io/refresh"] = v
		}

		if v, ok := existing.Annotations[manager.SourceUIDAnnotation]; ok {
			if incoming.Annotations == nil {
				incoming.Annotations = make(map[string]string)
			}
			incoming.Annotations[manager.SourceUIDAnnotation] = v
		}

		target := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Annotations: incoming.Annotations,
				Labels:      incoming.Labels,
				Finalizers:  incoming.Finalizers,
			},
			Spec:      incoming.Spec,
			Operation: incoming.Operation,
		}
		source := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Annotations: existing.Annotations,
				Labels:      existing.Labels,
			},
			Spec:      existing.Spec,
			Operation: existing.Operation,
		}
		patch, err := jsondiff.Compare(source, target)
		if err != nil {
			return nil, err
		}
		return patch, err
	})
	if err == nil {
		if updated.Generation == 1 {
			logCtx.Infof("Created application")
		} else {
			logCtx.Infof("Updated application")
		}
		if err := m.IgnoreChange(updated.QualifiedName(), updated.ResourceVersion); err != nil {
			logCtx.Warnf("Couldn't unignore change %s for app %s: %v", updated.ResourceVersion, updated.QualifiedName(), err)
		}
	}
	return updated, err
}

// CompareSourceUID checks for an existing app with the same name/namespace and compare its source UID with the incoming app.
func (m *ApplicationManager) CompareSourceUID(ctx context.Context, incoming *v1alpha1.Application) (bool, bool, error) {
	incoming.SetNamespace(m.namespace)

	existing, err := m.applicationBackend.Get(ctx, incoming.Name, incoming.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, false, nil
		}
		return false, false, err
	}

	// If there is an existing app with the same name/namespace, compare its source UID with the incoming app.
	sourceUID, exists := existing.Annotations[manager.SourceUIDAnnotation]
	if !exists {
		return true, false, fmt.Errorf("source UID Annotation is not found for app: %s", incoming.Name)
	}

	return true, string(incoming.UID) == sourceUID, nil
}

// UpdateAutonomousApp updates the Application resource on the control plane side
// when the agent is in autonomous mode. It will update changes to .spec and
// .status fields along with syncing labels and annotations.
//
// Additionally, it will remove any .operation field from the incoming resource
// before the resource is being updated on the control plane.
//
// This method is usually only executed by the control plane for updates that
// are received by agents in autonomous mode.
func (m *ApplicationManager) UpdateAutonomousApp(ctx context.Context, namespace string, incoming *v1alpha1.Application) (*v1alpha1.Application, error) {
	logCtx := log().WithFields(logrus.Fields{
		"component":       "UpdateAutonomous",
		"application":     incoming.QualifiedName(),
		"resourceVersion": incoming.ResourceVersion,
	})

	var updated *v1alpha1.Application
	var err error
	incoming.SetNamespace(namespace)
	if m.role == manager.ManagerRolePrincipal {
		stampLastUpdated(incoming)
	} else {
		return nil, fmt.Errorf("UpdateAutonomousApp should only be called from principal")
	}

	updated, err = m.update(ctx, true, incoming, func(existing, incoming *v1alpha1.Application) {
		if v, ok := existing.Annotations[manager.SourceUIDAnnotation]; ok {
			if incoming.Annotations == nil {
				incoming.Annotations = make(map[string]string)
			}
			incoming.Annotations[manager.SourceUIDAnnotation] = v
		}

		existing.Annotations = incoming.Annotations
		existing.Labels = incoming.Labels
		existing.DeletionTimestamp = incoming.DeletionTimestamp
		existing.DeletionGracePeriodSeconds = incoming.DeletionGracePeriodSeconds
		existing.Finalizers = incoming.Finalizers
		existing.Spec = incoming.Spec
		existing.Status = *incoming.Status.DeepCopy()
		existing.Operation = nil
		logCtx.Infof("Updating")
	}, func(existing, incoming *v1alpha1.Application) (jsondiff.Patch, error) {
		if v, ok := existing.Annotations[manager.SourceUIDAnnotation]; ok {
			if incoming.Annotations == nil {
				incoming.Annotations = make(map[string]string)
			}
			incoming.Annotations[manager.SourceUIDAnnotation] = v
		}

		target := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Labels:                     incoming.Labels,
				Annotations:                incoming.Annotations,
				DeletionTimestamp:          incoming.DeletionTimestamp,
				DeletionGracePeriodSeconds: incoming.DeletionGracePeriodSeconds,
				Finalizers:                 incoming.Finalizers,
			},
			Spec:   incoming.Spec,
			Status: incoming.Status,
		}
		source := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				DeletionTimestamp:          existing.DeletionTimestamp,
				DeletionGracePeriodSeconds: existing.DeletionGracePeriodSeconds,
				Finalizers:                 existing.Finalizers,
			},
			Spec:   existing.Spec,
			Status: existing.Status,
		}
		patch, err := jsondiff.Compare(source, target)
		if err != nil {
			return nil, err
		}

		// Append remove operation for operation field if it exists. We neither
		// want nor need it on the control plane's resource.
		if existing.Operation != nil {
			patch = append(patch, jsondiff.Operation{Type: "remove", Path: "/operation"})
		}

		return patch, nil
	})
	if err == nil {
		if err := m.IgnoreChange(updated.QualifiedName(), updated.ResourceVersion); err != nil {
			logCtx.Warnf("Could not unignore change %s for app %s: %v", updated.ResourceVersion, updated.QualifiedName(), err)
		}
		logCtx.WithField("newResourceVersion", updated.ResourceVersion).Infof("Updated application status")
	}
	return updated, err
}

// UpdateStatus updates the application on the server for updates sent by an
// agent that operates in managed mode.
//
// The app on the server will inherit the status field of the incoming app.
// Additionally, if a refresh annotation exists on the app on the app of the
// server, but not in the incoming app, the annotation will be removed. Any
// operation field on the existing resource will be removed as well.
func (m *ApplicationManager) UpdateStatus(ctx context.Context, namespace string, incoming *v1alpha1.Application) (*v1alpha1.Application, error) {
	logCtx := log().WithFields(logrus.Fields{
		"component":       "UpdateStatus",
		"application":     incoming.QualifiedName(),
		"resourceVersion": incoming.ResourceVersion,
	})

	var updated *v1alpha1.Application
	var err error
	incoming.SetNamespace(namespace)
	if m.role == manager.ManagerRolePrincipal {
		stampLastUpdated(incoming)
	} else {
		return nil, fmt.Errorf("UpdateStatus should only be called on principal")
	}

	updated, err = m.update(ctx, false, incoming, func(existing, incoming *v1alpha1.Application) {
		existing.Annotations = incoming.Annotations
		existing.Labels = incoming.Labels
		existing.Status = *incoming.Status.DeepCopy()
	}, func(existing, incoming *v1alpha1.Application) (jsondiff.Patch, error) {
		refresh, incomingRefresh := incoming.Annotations["argocd.argoproj.io/refresh"]
		_, existingRefresh := existing.Annotations["argocd.argoproj.io/refresh"]
		target := &v1alpha1.Application{
			Status: incoming.Status,
		}
		source := &v1alpha1.Application{
			Status: existing.Status,
		}
		patch, err := jsondiff.Compare(source, target)
		if err != nil {
			return nil, err
		}

		// We are not interested at keeping .operation on the control plane,
		// because there's no controller to handle it.
		if existing.Operation != nil {
			patch = append(patch, jsondiff.Operation{Type: "remove", Path: "/operation"})
		}

		// If the incoming app doesn't have the refresh annotation set, we need
		// to make sure that we remove it from the version stored on the server
		// as well.
		if existingRefresh && !incomingRefresh {
			patch = append(patch, jsondiff.Operation{Type: "remove", Path: "/metadata/annotations/argocd.argoproj.io~1refresh"})
		} else if !existingRefresh && incomingRefresh {
			patch = append(patch, jsondiff.Operation{Type: "add", Path: "/metadata/annotations/argocd.argoproj.io~1refresh", Value: refresh})
		}

		// If there is no status yet on our application (this happens when the
		// application was just created), we need to make sure to initialize
		// it properly.
		if reflect.DeepEqual(existing.Status, v1alpha1.ApplicationStatus{}) {
			patch = append([]jsondiff.Operation{{Type: "replace", Path: "/status", Value: v1alpha1.ApplicationStatus{}}}, patch...)
		}

		return patch, err
	})
	if err == nil {
		if err := m.IgnoreChange(updated.QualifiedName(), updated.ResourceVersion); err != nil {
			logCtx.Warnf("Could not ignore change %s for app %s: %v", updated.ResourceVersion, updated.QualifiedName(), err)
		}
		logCtx.WithField("newResourceVersion", updated.ResourceVersion).Infof("Updated application status")
	}
	return updated, err
}

// UpdateOperation is used to update the .operation field of the application
// resource to initiate a sync. Additionally, any labels and annotations that
// are used to trigger an action (such as, refresh) will be set on the target
// resource.
//
// This method is usually executed only by an agent in autonomous mode, because
// it has the leading version of the resource and we are not supposed to change
// its Application manifests.
func (m *ApplicationManager) UpdateOperation(ctx context.Context, incoming *v1alpha1.Application) (*v1alpha1.Application, error) {
	logCtx := log().WithFields(logrus.Fields{
		"component":       "UpdateOperation",
		"application":     incoming.QualifiedName(),
		"resourceVersion": incoming.ResourceVersion,
	})

	var updated *v1alpha1.Application
	var err error

	if !m.role.IsAgent() || !m.mode.IsAutonomous() {
		return nil, fmt.Errorf("UpdateOperation should only be called by an agent in autonomous mode: %v %v", m.role, m.mode)
	}

	updated, err = m.update(ctx, false, incoming, func(existing, incoming *v1alpha1.Application) {
		existing.Annotations = incoming.Annotations
		existing.Labels = incoming.Labels
		existing.Status = *incoming.Status.DeepCopy()
	}, func(existing, incoming *v1alpha1.Application) (jsondiff.Patch, error) {
		annotations := make(map[string]string)
		for k, v := range incoming.Annotations {
			if k != "argocd.argoproj.io/refresh" {
				annotations[k] = v
			}
		}
		target := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Annotations: incoming.Annotations,
			},
			Operation: incoming.Operation,
		}
		source := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Annotations: annotations,
			},
			Operation: existing.Operation,
		}
		patch, err := jsondiff.Compare(source, target, jsondiff.SkipCompact())
		return patch, err
	})
	if err == nil {
		if err := m.IgnoreChange(updated.QualifiedName(), updated.ResourceVersion); err != nil {
			logCtx.Warnf("Could not ignore change %s for app %s: %v", updated.ResourceVersion, updated.QualifiedName(), err)
		}
		logCtx.WithField("newResourceVersion", updated.ResourceVersion).Infof("Updated application status")
	}
	return updated, err
}

// Delete will delete an application resource. If Delete is called by the
// principal, any existing finalizers will be removed before deletion is
// attempted.
// 'deletionPropagation' follows the corresponding K8s behaviour, defaulting to Foreground if nil.
func (m *ApplicationManager) Delete(ctx context.Context, namespace string, incoming *v1alpha1.Application, deletionPropagation *backend.DeletionPropagation) error {
	removeFinalizer := false
	logCtx := log().WithFields(logrus.Fields{
		"component":       "DeleteOperation",
		"application":     incoming.QualifiedName(),
		"resourceVersion": incoming.ResourceVersion,
	})
	if m.role.IsPrincipal() {
		removeFinalizer = true
		incoming.SetNamespace(namespace)
	}
	var err error
	var updated *v1alpha1.Application

	if removeFinalizer {
		updated, err = m.RemoveFinalizers(ctx, incoming)
		if err == nil {
			logCtx.Debugf("Removed finalizer for app %s", updated.QualifiedName())
		} else {
			return fmt.Errorf("error removing finalizer: %w", err)
		}
	}

	return m.applicationBackend.Delete(ctx, incoming.Name, incoming.Namespace, deletionPropagation)
}

// update updates an existing Application resource on the Manager m's backend
// to match the incoming resource. If the backend supports patch, the existing
// resource will be patched, otherwise it will be updated.
//
// For a patch operation, patchFn is executed to calculate the patch that will
// be applied. Likewise, for an update operation, updateFn is executed to
// determine the final resource state to be updated.
//
// If upsert is set to true, and the Application resource does not yet exist
// on the backend, it will be created.
//
// The updated application will be returned on success, otherwise an error will
// be returned.
func (m *ApplicationManager) update(ctx context.Context, upsert bool, incoming *v1alpha1.Application, updateFn updateTransformer, patchFn patchTransformer) (*v1alpha1.Application, error) {
	var updated *v1alpha1.Application
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		existing, ierr := m.applicationBackend.Get(ctx, incoming.Name, incoming.Namespace)
		if ierr != nil {
			if errors.IsNotFound(ierr) && upsert {
				updated, ierr = m.Create(ctx, incoming)
				return ierr
			} else {
				return fmt.Errorf("error updating application %s: %w", incoming.QualifiedName(), ierr)
			}
		} else {
			if m.applicationBackend.SupportsPatch() && patchFn != nil {
				patch, err := patchFn(existing, incoming)
				if err != nil {
					return fmt.Errorf("could not create patch: %w", err)
				}
				jsonpatch, err := json.Marshal(patch)
				if err != nil {
					return fmt.Errorf("could not marshal jsonpatch: %w", err)
				}
				updated, ierr = m.applicationBackend.Patch(ctx, incoming.Name, incoming.Namespace, jsonpatch)
			} else {
				if updateFn != nil {
					updateFn(existing, incoming)
				}
				updated, ierr = m.applicationBackend.Update(ctx, existing)
			}
		}
		return ierr
	})
	return updated, err
}

// RemoveFinalizers will remove finalizers on an existing application
func (m *ApplicationManager) RemoveFinalizers(ctx context.Context, incoming *v1alpha1.Application) (*v1alpha1.Application, error) {
	updated, err := m.update(ctx, false, incoming, func(existing, incoming *v1alpha1.Application) {
		existing.Finalizers = nil
	}, func(existing, incoming *v1alpha1.Application) (jsondiff.Patch, error) {
		var err error
		var patch jsondiff.Patch
		target := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Finalizers: nil,
			},
		}
		source := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Finalizers: existing.Finalizers,
			},
		}
		patch, err = jsondiff.Compare(source, target, jsondiff.SkipCompact())
		return patch, err
	})
	return updated, err
}

func (m *ApplicationManager) List(ctx context.Context, selector backend.ApplicationSelector) ([]v1alpha1.Application, error) {
	return m.applicationBackend.List(ctx, selector)
}

// RevertManagedAppChanges compares the actual spec with expected spec stored in cache,
// if actual spec doesn't match with cache, then it is reverted to be in sync with cache, which is same as principal.
func (m *ApplicationManager) RevertManagedAppChanges(ctx context.Context, app *v1alpha1.Application) bool {
	logCtx := log().WithFields(logrus.Fields{
		"component":       "RevertManagedAppChanges",
		"application":     app.QualifiedName(),
		"resourceVersion": app.ResourceVersion,
	})

	sourceUID, exists := app.Annotations[manager.SourceUIDAnnotation]
	if exists && m.mode == manager.ManagerModeManaged {
		if cachedAppSpec, ok := appCache.GetApplicationSpec(ty.UID(sourceUID), logCtx); ok {
			logCtx.Debugf("Application: %s is available in agent cache", app.Name)

			if diff := reflect.DeepEqual(cachedAppSpec, app.Spec); !diff {
				app.Spec = cachedAppSpec
				logCtx.Infof("Reverting modifications done in application: %s", app.Name)
				if _, err := m.UpdateManagedApp(ctx, app); err != nil {
					logCtx.Errorf("Unable to revert modifications done in application: %s. Error: %v", app.Name, err)
					return false
				}
				return true
			}
		}
		logCtx.Debugf("Application: %s is not available in agent cache", app.Name)
	}
	return false
}

func log() *logrus.Entry {
	return logrus.WithField("component", "AppManager")
}
