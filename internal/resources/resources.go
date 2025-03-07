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

package resources

import (
	"crypto/md5"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
)

const (
	applicationKind = "Application"
	appProjectKind  = "AppProject"
)

// ResourceKey uniquely identifies a resource in the cluster
type ResourceKey struct {
	Name      string
	Namespace string
	Kind      string

	// Source UID of the resource. It should be same for both agent and principal.
	// Source: .ObjectMeta.UID of the resource in the cluster
	// Peer: UID from the source-uid annotation
	UID string
}

func (r *ResourceKey) String() string {
	return fmt.Sprintf("%s/%s/%s/%s", r.Kind, r.Namespace, r.Name, r.UID)
}

func NewResourceKeyFromApp(app *v1alpha1.Application) ResourceKey {
	// sourceUID annotation indicates that the app was created from a source.
	// So, consider the source UID instead of the resource UID.
	sourceUID, ok := app.Annotations[manager.SourceUIDAnnotation]
	if ok {
		return newResourceKey(applicationKind, app.Name, app.Namespace, sourceUID)
	}

	return newResourceKey(applicationKind, app.Name, app.Namespace, string(app.UID))
}

func NewResourceKeyFromAppProject(appProject *v1alpha1.AppProject) ResourceKey {
	// sourceUID annotation indicates that the app was created from a source.
	// So, consider the source UID instead of the resource UID.
	sourceUID, ok := appProject.Annotations[manager.SourceUIDAnnotation]
	if ok {
		return newResourceKey(appProjectKind, appProject.Name, appProject.Namespace, sourceUID)
	}

	return newResourceKey(appProjectKind, appProject.Name, appProject.Namespace, string(appProject.UID))
}

func newResourceKey(kind, name, namespace, uid string) ResourceKey {
	return ResourceKey{
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
		UID:       uid,
	}
}

// AgentResources keeps track of all the resources that are associated with a specific agent
type AgentResources struct {
	// key: agent name
	resources map[string]*Resources

	mu sync.RWMutex
}

func NewAgentResources() *AgentResources {
	return &AgentResources{
		resources: make(map[string]*Resources),
	}
}

func (r *AgentResources) Add(agent string, key ResourceKey) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.resources[agent] == nil {
		r.resources[agent] = NewResources()
	}

	r.resources[agent].Add(key)
}

func (r *AgentResources) Remove(agent string, key ResourceKey) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.resources[agent].Remove(key)

	if len(r.resources[agent].resources) == 0 {
		delete(r.resources, agent)
	}
}

func (r *AgentResources) GetAllResources(agent string) []ResourceKey {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res, ok := r.resources[agent]
	if ok {
		return res.GetAll()
	}

	return []ResourceKey{}
}

func (r *AgentResources) Get(agent string) *Resources {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.resources[agent]
}

func (r *AgentResources) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.resources)
}

func (r *AgentResources) Checksum(agent string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	agentResources, ok := r.resources[agent]
	if !ok {
		return []byte{}
	}

	return agentResources.Checksum()
}

// Resources is a map of all the resources(apps,appProjects,etc) that are managed by the agent/principal.
// It is dynamically populated by the agent/principal while handling informer events and is used
// to quickly generate the checksum and send resync messages without having to query the API server.
type Resources struct {
	// key: ResourceKey of the resource
	resources map[ResourceKey]bool

	mu sync.RWMutex
}

func NewResources() *Resources {
	return &Resources{
		resources: make(map[ResourceKey]bool),
	}
}

func (r *Resources) Add(key ResourceKey) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.resources[key] = true
}

func (r *Resources) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.resources)
}

func (r *Resources) Remove(key ResourceKey) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.resources, key)
}

func (r *Resources) GetAll() []ResourceKey {
	if r == nil {
		return []ResourceKey{}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]ResourceKey, 0, len(r.resources))
	for item := range r.resources {
		res = append(res, item)
	}

	return res
}

// Checksum calculates the checksum of all the resources keys
func (r *Resources) Checksum() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	resources := make([]string, 0, len(r.resources))
	for res := range r.resources {
		// we are omitting the namespace while calculating checksum because the
		// resource namespace might differ on the agent and principal
		resources = append(resources, fmt.Sprintf("%s/%s/%s", res.Kind, res.Name, res.UID))
	}

	if len(resources) != 0 {
		return calculateChecksum(resources)
	}

	return nil
}

func calculateChecksum(item []string) []byte {
	// sort the items to ensure a consistent hash
	sort.Strings(item)

	joinedItems := strings.Join(item, "")
	hash := md5.Sum([]byte(joinedItems))

	return hash[:]
}
