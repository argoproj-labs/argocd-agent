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

package manager

import (
	"context"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

type ManagerRole int
type ManagerMode int

const (
	ManagerRoleUnset ManagerRole = iota
	ManagerRolePrincipal
	ManagerRoleAgent
)

const (
	ManagerModeUnset ManagerMode = iota
	ManagerModeAutonomous
	ManagerModeManaged
)

const (
	// SourceUIDAnnotation is an annotation that represents the UID of the source resource.
	// It is added to the resources managed on the target.
	SourceUIDAnnotation = "argocd.argoproj.io/source-uid"
)

type Manager interface {
	SetRole(role ManagerRole)
	SetMode(role ManagerRole)
}

// IsPrincipal returns true if the manager role is principal
func (r ManagerRole) IsPrincipal() bool {
	return r == ManagerRolePrincipal
}

// IsAgent returns true if the manager role is agent
func (r ManagerRole) IsAgent() bool {
	return r == ManagerRoleAgent
}

// IsAutonomous returns true if the manager mode is autonomous
func (m ManagerMode) IsAutonomous() bool {
	return m == ManagerModeAutonomous
}

// IsManaged returns true if the manager mode is managed mode
func (m ManagerMode) IsManaged() bool {
	return m == ManagerModeManaged
}

// ManagedResources is a map of resource names that are managed by agent/principal.
// key: resource name
type ManagedResources struct {
	mu      sync.RWMutex
	managed map[string]bool
}

func NewManagedResources() ManagedResources {
	return ManagedResources{
		managed: make(map[string]bool),
	}
}

// ClearManaged clears the managed resources
func (m *ManagedResources) ClearManaged() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.managed = make(map[string]bool)
}

// IsManaged returns whether the resource name is currently managed by this agent
func (m *ManagedResources) IsManaged(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.managed[name]
	return ok
}

// Manage marks the resource name as being managed by this agent
func (m *ManagedResources) Manage(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.managed[name]
	if !ok {
		m.managed[name] = true
		return nil
	} else {
		return fmt.Errorf("resource %s is already managed", name)
	}
}

// Unmanage marks the resource name as not being managed by this agent
func (m *ManagedResources) Unmanage(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.managed[name]
	if !ok {
		return fmt.Errorf("resource %s is not managed", name)
	} else {
		delete(m.managed, name)
		return nil
	}
}

func (m *ManagedResources) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.managed)
}

// ObservedResources is a map of resource names that are observed by agent/principal.
// key: resource name
// value: resource version
type ObservedResources struct {
	mu       sync.RWMutex
	observed map[string]string
}

func NewObservedResources() ObservedResources {
	return ObservedResources{
		observed: make(map[string]string),
	}
}

// Clear clears the observed resources
func (o *ObservedResources) ClearIgnored() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.observed = make(map[string]string)
}

// IgnoreChange adds a particular version for the resource named name to
// list of changes to ignore.
func (o *ObservedResources) IgnoreChange(name string, version string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if cur, ok := o.observed[name]; ok && cur == version {
		return fmt.Errorf("version %s is already ignored for %s", version, name)
	} else {
		o.observed[name] = version
		return nil
	}
}

// IsChangeIgnored returns true if the version for name is already being
// ignored.
func (o *ObservedResources) IsChangeIgnored(name string, version string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	last, ok := o.observed[name]
	if !ok {
		return false
	}
	return last == version
}

func (o *ObservedResources) UnignoreChange(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.observed[name]; ok {
		delete(o.observed, name)
		return nil
	} else {
		return fmt.Errorf("no generation recorded for resource %s", name)
	}
}

func (o *ObservedResources) Len() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return len(o.observed)
}

type kubeResource interface {
	runtime.Object
	metav1.Object
}

type resourceManager[R kubeResource] interface {
	Create(ctx context.Context, obj R) (R, error)
}

// RevertUserInitiatedDeletion detects if a resource deletion was unauthorized and recreates the resource.
// Returns true if the resource was recreated, false otherwise.
func RevertUserInitiatedDeletion[R kubeResource](ctx context.Context,
	outbound R,
	deletions *DeletionTracker,
	mgr resourceManager[R],
	logCtx *logrus.Entry,
) (bool, error) {

	logCtx = logCtx.WithFields(logrus.Fields{
		"resource": outbound.GetName(),
		"kind":     outbound.GetObjectKind().GroupVersionKind().Kind,
	})

	sourceUID, exists := outbound.GetAnnotations()[SourceUIDAnnotation]
	if !exists {
		return false, fmt.Errorf("source UID annotation not found for resource")
	}

	// Check if this deletion is coming from the source
	if deletions.RemoveExpected(types.UID(sourceUID)) {
		logCtx.Debugf("Expected deletion detected - allowing it to proceed")
		return false, nil
	}

	logCtx.Warnf("Unauthorized deletion detected - recreating")
	// This is an unauthorized deletion (user-initiated), recreate the resource
	resource := outbound.DeepCopyObject().(R)
	resource.SetResourceVersion("")
	resource.SetDeletionTimestamp(nil)
	resource.SetUID(types.UID(sourceUID))
	_, err := mgr.Create(ctx, resource)
	if err != nil {
		return false, err
	} else {
		logCtx.Infof("Recreated resource after unauthorized deletion")
	}

	return true, nil
}

// DeletionTracker tracks expected deletions from the source.
type DeletionTracker struct {
	mu       sync.RWMutex
	expected map[types.UID]bool
}

func NewDeletionTracker() *DeletionTracker {
	return &DeletionTracker{
		expected: make(map[types.UID]bool),
	}
}

func (d *DeletionTracker) MarkExpected(uid types.UID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.expected[uid] = true
}

func (d *DeletionTracker) RemoveExpected(uid types.UID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, exists := d.expected[uid]
	if exists {
		delete(d.expected, uid)
	}
	return exists
}

func (d *DeletionTracker) Unmark(uid types.UID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.expected, uid)
}
