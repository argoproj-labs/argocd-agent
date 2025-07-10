package cluster

import (
	"errors"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
)

// ErrAlreadyMapped indicates that an agent has a cluster mapped to it already
var ErrAlreadyMapped = errors.New("agent already mapped")

// ErrNotMapped indicates that an agent has no cluster mapped to it
var ErrNotMapped = errors.New("agent not mapped")

// Mapping returns the cluster mapping for an agent with the given name. If the
// agent has no cluster mapped to it, Mapping returns nil.
func (m *Manager) Mapping(agent string) *v1alpha1.Cluster {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return m.mapping(agent)
}

// mapping returns the cluster mapping for an agent with the given name. If the
// agent has no cluster mapped to it, mapping returns nil.
//
// This function is not thread safe, unless the caller holds the manager's
// mutex in read mode.
func (m *Manager) mapping(agent string) *v1alpha1.Cluster {
	c, ok := m.clusters[agent]
	if !ok {
		return nil
	}
	return c
}

// HasMapping returns true when the manager has a cluster mapping for an agent
// with the given name.
func (m *Manager) HasMapping(agent string) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return m.hasMapping(agent)
}

// hasMapping returns true when the manager has a cluster mapping for an agent
// with the given name.
//
// This function is not thread safe, unless the caller holds the manager's
// mutex in read mode.
func (m *Manager) hasMapping(agent string) bool {
	_, ok := m.clusters[agent]
	return ok
}

// MapCluster maps the given cluster to the agent with the given name. It may
// return an error if the agent has a cluster mapped to it already.
func (m *Manager) MapCluster(agent string, cluster *v1alpha1.Cluster) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.mapCluster(agent, cluster)
}

// mapCluster maps the given cluster to the agent with the given name. It may
// return an error if the agent has a cluster mapped to it already.
//
// This function is not thread safe, unless the caller holds the manager's
// mutex in write mode.
func (m *Manager) mapCluster(agent string, cluster *v1alpha1.Cluster) error {
	if m.hasMapping(agent) {
		return ErrAlreadyMapped
	}
	m.clusters[agent] = cluster
	return nil
}

// UnmapCluster removes the mapping for an agent with the given name. If there
// is no mapping for the agent, UnmapCluster will return an error.
func (m *Manager) UnmapCluster(agent string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.unmapCluster(agent)
}

// unmapCluster removes the mapping for an agent with the given name. If there
// is no mapping for the agent, unmapCluster will return an error.
//
// This function is not thread safe, unless the caller holds the manager's
// mutex in write mode.
func (m *Manager) unmapCluster(agent string) error {
	if !m.hasMapping(agent) {
		return ErrNotMapped
	}
	delete(m.clusters, agent)
	return nil
}
