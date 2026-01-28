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

package cache

import (
	"sync"

	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/types"
)

type SourceCache struct {
	Application *ResourceCache[v1alpha1.ApplicationSpec]
	AppProject  *ResourceCache[v1alpha1.AppProjectSpec]
	Repository  *ResourceCache[map[string][]byte]
}

func NewSourceCache() *SourceCache {
	return &SourceCache{
		Application: newResourceCache[v1alpha1.ApplicationSpec]("ApplicationSpec"),
		AppProject:  newResourceCache[v1alpha1.AppProjectSpec]("AppProjectSpec"),
		Repository:  newResourceCache[map[string][]byte]("RepositorySpec"),
	}
}

// ResourceCache is used to cache the source resource spec. It is used to keep the resources on the peer in sync with the source.
type ResourceCache[T any] struct {
	lock  sync.RWMutex
	items map[types.UID]T
	name  string

	log *logrus.Entry
}

// NewResourceCache creates a new resource cache
func newResourceCache[T any](name string) *ResourceCache[T] {
	return &ResourceCache[T]{
		items: make(map[types.UID]T),
		name:  name,
		log:   logging.GetDefaultLogger().ModuleLogger("SourceCache").WithField("key", name),
	}
}

// Set inserts/updates a resource in the cache
func (c *ResourceCache[T]) Set(sourceUID types.UID, item T) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.log.Tracef("Setting value in source cache: '%s'", sourceUID)

	c.items[sourceUID] = item
}

// Get retrieves a resource from the cache
func (c *ResourceCache[T]) Get(sourceUID types.UID) (T, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	item, ok := c.items[sourceUID]

	c.log.Tracef("Retrieved value from cache: '%s' (found: %v)", sourceUID, ok)

	return item, ok
}

// Contains checks if a resource is in the cache
func (c *ResourceCache[T]) Contains(sourceUID types.UID) bool {
	c.lock.RLock()
	defer c.lock.RUnlock()

	_, ok := c.items[sourceUID]
	return ok
}

// Delete removes a resource from the cache
func (c *ResourceCache[T]) Delete(sourceUID types.UID) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.log.Tracef("Deleting value from cache: '%s'", sourceUID)

	delete(c.items, sourceUID)
}

// Clear removes all items from the cache
func (c *ResourceCache[T]) Clear() {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.log.Tracef("Clearing all values from cache")

	c.items = make(map[types.UID]T)
}

// Len returns the number of items in the cache
func (c *ResourceCache[T]) Len() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.items)
}
