package cluster

import (
	"context"
	"testing"

	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/argoproj/argo-cd/v3/common"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_onClusterAdded(t *testing.T) {
	t.Run("Successfully add a cluster", func(t *testing.T) {
		m, err := NewManager(context.TODO(), "argocd", "", "", "", kube.NewFakeKubeClient("argocd"))
		require.NoError(t, err)
		s := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					LabelKeyClusterAgentMapping: "agent",
					common.LabelKeySecretType:   common.LabelValueSecretTypeCluster,
				},
			},
		}
		m.onClusterAdded(s)
		assert.Len(t, m.clusters, 1)
	})
	t.Run("Secret is missing one or more labels", func(t *testing.T) {
		m, err := NewManager(context.TODO(), "argocd", "", "", "", kube.NewFakeKubeClient("argocd"))
		require.NoError(t, err)
		s := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					common.LabelKeySecretType: common.LabelValueSecretTypeCluster,
				},
			},
		}
		m.onClusterAdded(s)
		assert.Len(t, m.clusters, 0)
		delete(s.Labels, common.LabelKeySecretType)
		s.Labels[LabelKeyClusterAgentMapping] = "agent"
		m.onClusterAdded(s)
		assert.Len(t, m.clusters, 0)
	})
	t.Run("Target agent already has a mapping", func(t *testing.T) {
		m, err := NewManager(context.TODO(), "argocd", "", "", "", kube.NewFakeKubeClient("argocd"))
		require.NoError(t, err)
		s := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					LabelKeyClusterAgentMapping: "agent",
					common.LabelKeySecretType:   common.LabelValueSecretTypeCluster,
				},
			},
		}
		m.mapCluster("agent", &v1alpha1.Cluster{})
		assert.Len(t, m.clusters, 1)
		m.onClusterAdded(s)
		assert.Len(t, m.clusters, 1)
	})
}

func Test_onClusterUpdated(t *testing.T) {
	t.Run("Successfully remap a cluster to another agent", func(t *testing.T) {
		old := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					LabelKeyClusterAgentMapping: "agent1",
					common.LabelKeySecretType:   common.LabelValueSecretTypeCluster,
				},
				Name: "cluster",
			},
		}
		new := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					LabelKeyClusterAgentMapping: "agent2",
					common.LabelKeySecretType:   common.LabelValueSecretTypeCluster,
				},
				Name: "cluster",
			},
		}
		m, err := NewManager(context.TODO(), "argocd", "", "", "", kube.NewFakeKubeClient("argocd"))
		require.NoError(t, err)
		m.mapCluster("agent1", &v1alpha1.Cluster{})
		assert.NotNil(t, m.mapping("agent1"))
		m.onClusterUpdated(old, new)
		assert.Nil(t, m.mapping("agent1"))
		assert.NotNil(t, m.mapping("agent2"))
	})

	t.Run("Can't remap to an agent that has a mapping already", func(t *testing.T) {
		old := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					LabelKeyClusterAgentMapping: "agent1",
					common.LabelKeySecretType:   common.LabelValueSecretTypeCluster,
				},
				Name: "cluster1",
			},
		}
		new := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					LabelKeyClusterAgentMapping: "agent2",
					common.LabelKeySecretType:   common.LabelValueSecretTypeCluster,
				},
				Name: "cluster2",
			},
		}
		m, err := NewManager(context.TODO(), "argocd", "", "", "", kube.NewFakeKubeClient("argocd"))
		require.NoError(t, err)
		m.mapCluster("agent1", &v1alpha1.Cluster{Name: "cluster1"})
		assert.NotNil(t, m.mapping("agent1"))
		m.mapCluster("agent2", &v1alpha1.Cluster{Name: "cluster3"})
		assert.NotNil(t, m.mapping("agent2"))
		m.onClusterUpdated(old, new)
		assert.NotNil(t, m.mapping("agent1"))
		assert.NotNil(t, m.mapping("agent2"))

	})
}
