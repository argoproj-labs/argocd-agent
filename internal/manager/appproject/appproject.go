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
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/cache"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/util/glob"
	"github.com/sirupsen/logrus"
	"github.com/wI2L/jsondiff"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

type (
	updateTransformer func(existing, incoming *v1alpha1.AppProject)
	patchTransformer  func(existing, incoming *v1alpha1.AppProject) (jsondiff.Patch, error)
)

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

	// ManagedResources is a list of AppProjects we manage, key is name of AppProjects CR, value is not used.
	manager.ManagedResources
	// ObservedResources, key is qualified name of the AppProject, value is the AppProjects's .metadata.resourceValue field
	manager.ObservedResources
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
	m.ObservedResources = manager.NewObservedResources()
	m.ManagedResources = manager.NewManagedResources()
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

func (m *AppProjectManager) List(ctx context.Context, selector backend.AppProjectSelector) ([]v1alpha1.AppProject, error) {
	return m.appprojectBackend.List(ctx, selector)
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
		return true, false, nil
	}

	return true, string(incoming.UID) == sourceUID, nil
}

// RevertAppProjectChanges compares the actual spec with expected spec stored in cache,
// if actual spec doesn't match with cache, then it is reverted to be in sync with cache, which is same as the source cluster.
// Returns an error if the update operation fails, and a boolean indicating if the changes were reverted.
func (m *AppProjectManager) RevertAppProjectChanges(ctx context.Context, project *v1alpha1.AppProject, projectCache *cache.ResourceCache[v1alpha1.AppProjectSpec]) (bool, error) {
	logCtx := log().WithFields(logrus.Fields{
		"component":       "RevertAppProjectChanges",
		"appProject":      project.Name,
		"resourceVersion": project.ResourceVersion,
	})

	sourceUID, exists := project.Annotations[manager.SourceUIDAnnotation]
	if !exists {
		return false, fmt.Errorf("source UID annotation not found for resource")
	}

	if cachedSpec, ok := projectCache.Get(types.UID(sourceUID)); ok {
		logCtx.Debugf("AppProject %s is available in agent cache", project.Name)

		if isEqual := reflect.DeepEqual(cachedSpec, project.Spec); !isEqual {
			project.Spec = cachedSpec
			logCtx.Infof("Reverting modifications done in appProject: %s", project.Name)
			if _, err := m.UpdateAppProject(ctx, project); err != nil {
				return false, err
			}
			return true, nil
		} else {
			logCtx.Debugf("AppProject %s is already in sync with source cache", project.Name)
		}
	} else {
		logCtx.Errorf("AppProject %s is not available in agent cache", project.Name)
	}

	return false, nil
}

// DoesAgentMatchWithProject checks if the agent name matches the given AppProject.
// We match the agent to an AppProject if:
// 1. The agent name matches any one of the destination names OR
// 2. The agent name is empty but the agent name is present in the server URL parameter AND
// 3. The agent name is not denied by any of the destination names
// Ref: https://github.com/argoproj/argo-cd/blob/master/pkg/apis/application/v1alpha1/app_project_types.go#L477
func DoesAgentMatchWithProject(agentName string, appProject v1alpha1.AppProject) bool {
	destinationMatched := false

	for _, dst := range appProject.Spec.Destinations {
		// Return immediately if the agent name is denied by any of the destination names
		if dst.Name != "" && isDenyPattern(dst.Name) && glob.Match(dst.Name[1:], agentName) {
			return false
		}

		// Some AppProjects (e.g. default) may not always have a name so we need to check the server URL
		if dst.Name == "" && dst.Server != "" {
			if dst.Server == "*" {
				destinationMatched = true
				continue
			}

			// Server URL will be the resource proxy URL https://<rp-hostname>:<port>?agentName=<agent-name>
			server, err := url.Parse(dst.Server)
			if err != nil {
				continue
			}

			serverAgentName := server.Query().Get("agentName")
			if serverAgentName == "" {
				continue
			}

			if glob.Match(serverAgentName, agentName) {
				destinationMatched = true
			}
		}

		// Match the agent name to the destination name and continue looking for deny patterns
		if dst.Name != "" && glob.Match(dst.Name, agentName) {
			destinationMatched = true
		}
	}

	// Must match both destination and source namespace requirements
	return destinationMatched &&
		glob.MatchStringInList(appProject.Spec.SourceNamespaces, agentName, glob.REGEXP)
}

func isDenyPattern(pattern string) bool {
	return strings.HasPrefix(pattern, "!")
}

// AgentSpecificAppProject returns an agent specific version of the given AppProject
// We don't have to check for deny patterns because we only construct the agent specific AppProject
// if the agent name matches the AppProject's destinations.
func AgentSpecificAppProject(appProject v1alpha1.AppProject, agent string, dstMapping bool) v1alpha1.AppProject {
	// Only keep the destinations that are relevant to the given agent
	filteredDst := []v1alpha1.ApplicationDestination{}
	for _, dst := range appProject.Spec.Destinations {
		nameMatches := dst.Name != "" && glob.Match(dst.Name, agent)
		serverMatches := false

		// Handle server-only destinations (like default project)
		if dst.Name == "" && dst.Server != "" {
			if dst.Server == "*" {
				serverMatches = true
			} else if server, err := url.Parse(dst.Server); err == nil {
				serverAgentName := server.Query().Get("agentName")
				serverMatches = serverAgentName != "" && glob.Match(serverAgentName, agent)
			}
		}

		if nameMatches || serverMatches {
			dst.Name = "in-cluster"
			dst.Server = "https://kubernetes.default.svc"
			filteredDst = append(filteredDst, dst)
		}
	}
	appProject.Spec.Destinations = filteredDst

	// Preserve sourceNamespaces to allow apps in any namespace on the workload cluster
	// This is required for destination-based mapping where Applications can be created
	// in namespaces other than argocd.
	if !dstMapping {
		appProject.Spec.SourceNamespaces = nil
	}

	// Remove the roles since they are not relevant on the workload cluster
	appProject.Spec.Roles = []v1alpha1.ProjectRole{}

	return appProject
}

func log() *logrus.Entry {
	return logging.GetDefaultLogger().ComponentLogger("AppProjectManager")
}
