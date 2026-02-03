package cluster

import (
	"github.com/argoproj/argo-cd/v3/common"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/util/db"
	v1 "k8s.io/api/core/v1"
)

/*
The code in this file does not implement an actual informer, but the handlers
that are used for the Manager's informer on add/update/delete events.
*/

// onClusterAdded is called by the informer whenever the manager's informer is
// notified about a new cluster secret. This usually happens on two occasions:
// A new, correctly labeled secret is created on the cluster, or an existing
// unlabeled secret is being labeled correctly.
func (m *Manager) onClusterAdded(res *v1.Secret) {
	log().Tracef("Executing cluster add handler for secret %s/%s", res.Namespace, res.Name)
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// The informer filter should have already ensured that the following
	// labels are set. But we check again just to be sure.
	agent, ok := res.Labels[LabelKeyClusterAgentMapping]
	if !ok || agent == "" {
		log().Warnf("Processing invalid cluster secret (missing labels): %s", res.Name)
		return
	}
	if v, ok := res.Labels[common.LabelKeySecretType]; !ok || v != common.LabelValueSecretTypeCluster {
		log().Warnf("Processing invalid cluster secret (missing labels): %s", res.Name)
		return
	}

	// Check if we already have a mapping for the requested agent
	existing := m.mapping(agent)
	if existing != nil {
		log().Errorf("Agent %s is already mapped to cluster %s", agent, existing.Name)
		return
	}

	// Unmarshal the cluster from the secret
	cluster, err := db.SecretToCluster(res)
	if err != nil {
		log().WithError(err).Error("Not a cluster secret or malformed data")
		return
	}

	// TODO(jannfis): Do we want to validate cluster configuration here?

	// Map the cluster to the agent
	err = m.mapCluster(agent, cluster)
	if err != nil {
		log().WithError(err).Infof("Error mapping cluster %s to agent %s", cluster.Name, agent)
		return
	}

	log().Infof("Mapped cluster %s to agent %s", cluster.Name, agent)
}

// onClusterUpdated is called by the informer whenever there is a change to a
// secret that is watched by the informer. However, when the change involves
// the resource to be added or removed from the informer's cache (e.g. a label
// was added or removed), onClusterAdded or onClusterDeleted will be called
// respectively, instead.
func (m *Manager) onClusterUpdated(old *v1.Secret, new *v1.Secret) {
	log().Tracef("Executing cluster update callback for secret %s/%s", old.Namespace, old.Name)
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// This should never happen, but we check anyway
	oldAgent, oldOk := old.Labels[LabelKeyClusterAgentMapping]
	newAgent, newOk := new.Labels[LabelKeyClusterAgentMapping]
	if !oldOk && !newOk {
		log().Errorf("Could not update cluster mapping: Either secret is not labeled properly")
		return
	}

	c, err := db.SecretToCluster(new)
	if err != nil {
		log().WithError(err).Errorf("Not a cluster secret or malformed data")
		return
	}

	// The mapping label has changed value. Check existing mappings.
	if oldAgent != newAgent {
		if !m.hasMapping(oldAgent) || m.hasMapping(newAgent) {
			log().Errorf("Remapping secret from %s to %s not possible", oldAgent, newAgent)
			return
		}
	}

	// Unmap cluster from old agent. Cluster could not be mapped yet due to
	// potential errors when adding the cluster to the manager (i.e. not being
	// able to unmarshal the cluster from the secret). In this case, we don't
	// want to return an error but add the cluster to the manager anyway.
	log().Tracef("Unmapping cluster %s from agent %s", c.Name, oldAgent)
	if err = m.unmapCluster(oldAgent); err != nil && err != ErrNotMapped {
		log().WithError(err).Errorf("Could not unmap cluster %s from agent %s", c.Name, oldAgent)
		return
	}

	// Map cluster to new agent
	log().Tracef("Mapping cluster %s to agent %s", c.Name, newAgent)
	if err = m.mapCluster(newAgent, c); err != nil {
		log().WithError(err).Errorf("Could not map cluster %s to agent %s", c.Name, newAgent)
		return
	}

	log().Infof("Updated cluster mapping for agent %s", newAgent)
}

// onClusterDeleted will be called by the informer on certain occasions when a
// cluster secret is removed from the informer. Usually, this happens in the
// following cases: The cluster secret is deleted from the cluster, or one of
// the labels that are required are removed from a cluster secret.
func (m *Manager) onClusterDeleted(res *v1.Secret) {
	log().Trace("Executing cluster delete callback")
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// This situation should not happen, but we test anyway.
	agent, ok := res.Labels[LabelKeyClusterAgentMapping]
	if !ok {
		log().Errorf("Secret %s should have agent mapping label, but does not", res.Name)
		return
	}

	// Log out if there is no mapping for the requested agent. This indicates
	// an informer cache out of sync or some other problem that needs to be
	// investigated.
	var c *v1alpha1.Cluster
	if c = m.mapping(agent); c == nil {
		log().Errorf("There should be a cluster mapped to agent %s, but there is not.", agent)
		return
	}

	// Finally, unmap the cluster
	if err := m.unmapCluster(agent); err != nil {
		log().WithError(err).Errorf("Could not unmap cluster %s from agent %s", c.Name, agent)
		return
	}

	log().Infof("Unmapped cluster from agent %s", agent)
}
