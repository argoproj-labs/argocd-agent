package cluster

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
)

func Test_ClusterMappings(t *testing.T) {
	m := &Manager{
		clusters: make(map[string]*v1alpha1.Cluster),
	}
	t.Run("Map cluster to agent", func(t *testing.T) {
		err := m.MapCluster("agent", &v1alpha1.Cluster{Name: "cluster"})
		require.NoError(t, err)
	})
	t.Run("Cluster is mapped successfully", func(t *testing.T) {
		require.True(t, m.HasMapping("agent"))
		require.Equal(t, "cluster", m.Mapping("agent").Name)
	})
	t.Run("Agent cannot be mapped again", func(t *testing.T) {
		err := m.MapCluster("agent", &v1alpha1.Cluster{})
		require.ErrorIs(t, err, ErrAlreadyMapped)
	})
	t.Run("Mapping can be deleted", func(t *testing.T) {
		err := m.UnmapCluster("agent")
		require.NoError(t, err)
	})
	t.Run("Mapping has been deleted", func(t *testing.T) {
		require.False(t, m.HasMapping("agent"))
		require.Nil(t, m.Mapping("agent"))
	})
	t.Run("Unmap on unmapped agent returns error", func(t *testing.T) {
		require.ErrorIs(t, m.UnmapCluster("agent"), ErrNotMapped)
	})
}
