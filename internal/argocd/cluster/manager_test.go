package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/argoproj-labs/argocd-agent/test/testutil"
	"github.com/argoproj/argo-cd/v2/common"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const secretName = "cluster-123"

func newClusterSecret(t *testing.T, agentName string) (*v1alpha1.Cluster, *v1.Secret) {
	t.Helper()
	labels := map[string]string{
		common.LabelKeySecretType: common.LabelValueSecretTypeCluster,
	}
	if agentName != "" {
		labels[LabelKeyClusterAgentMapping] = agentName
	}
	c := &v1alpha1.Cluster{
		Labels: labels,
		Name:   "my-cluster",
		Server: "127.0.0.1:8080",
	}
	s := &v1.Secret{}
	err := ClusterToSecret(c, s)
	require.NoError(t, err)
	s.Namespace = "argocd"
	s.Name = secretName
	return c, s
}

func Test_StartStop(t *testing.T) {
	clt := kube.NewFakeClientsetWithResources()
	m, err := NewManager(context.TODO(), "argocd", clt)
	require.NoError(t, err)
	require.NotNil(t, m)
	err = m.Start()
	require.NoError(t, err)
	err = m.Stop()
	require.NoError(t, err)
}

func Test_onClusterAdd(t *testing.T) {
	clt := kube.NewFakeClientsetWithResources()
	m, err := NewManager(context.TODO(), "argocd", clt)
	require.NoError(t, err)
	require.NotNil(t, m)
	err = m.Start()
	require.NoError(t, err)

	t.Run("Cluster is mapped successfully when secret is added", func(t *testing.T) {
		c, s := newClusterSecret(t, "agent-1")
		_, err := clt.CoreV1().Secrets("argocd").Create(context.TODO(), s, metav1.CreateOptions{})
		require.NoError(t, err)
		var nc *v1alpha1.Cluster
		testutil.WaitForChange(t, 1*time.Second, func() bool {
			nc = m.Mapping("agent-1")
			return nc != nil
		})
		assert.NotNil(t, nc)
		assert.Equal(t, c.Name, nc.Name)
		assert.Equal(t, c.Server, nc.Server)
	})

	t.Run("Cluster is mapped when existing secret is labeled", func(t *testing.T) {
		require.False(t, m.HasMapping("agent-2"))
		c, s := newClusterSecret(t, "")
		s.Name = "test-234"
		_, err := clt.CoreV1().Secrets("argocd").Create(context.TODO(), s, metav1.CreateOptions{})
		require.NoError(t, err)
		// Let the informer catch and ignore the event
		time.Sleep(500 * time.Millisecond)
		s.Labels[LabelKeyClusterAgentMapping] = "agent-2"
		_, err = clt.CoreV1().Secrets("argocd").Update(context.TODO(), s, metav1.UpdateOptions{})
		require.NoError(t, err)
		testutil.WaitForChange(t, 1*time.Second, func() bool {
			if m.HasMapping("agent-2") {
				return m.Mapping("agent-2").Server == c.Server
			}
			return false
		})
	})

	t.Run("Cluster is remapped when agent label changes", func(t *testing.T) {
		s, err := clt.CoreV1().Secrets("argocd").Get(context.TODO(), "test-234", metav1.GetOptions{})
		require.NoError(t, err)
		require.NotEmpty(t, s.Labels)
		require.Equal(t, "agent-2", s.Labels[LabelKeyClusterAgentMapping])
		require.True(t, m.HasMapping("agent-2"))
		s.Labels[LabelKeyClusterAgentMapping] = "agent-3"
		_, err = clt.CoreV1().Secrets("argocd").Update(context.TODO(), s, metav1.UpdateOptions{})
		require.NoError(t, err)
		testutil.WaitForChange(t, 1*time.Second, func() bool {
			return !m.HasMapping("agent-2") && m.HasMapping("agent-3")
		})
	})

	t.Run("Cluster mapping is updated when secret is updated", func(t *testing.T) {
		s, err := clt.CoreV1().Secrets("argocd").Get(context.TODO(), secretName, metav1.GetOptions{})
		require.NoError(t, err)
		s.Data["server"] = []byte("127.0.0.1:8081")
		_, err = clt.CoreV1().Secrets("argocd").Update(context.TODO(), s, metav1.UpdateOptions{})
		assert.NoError(t, err)
		testutil.WaitForChange(t, 1*time.Second, func() bool {
			c := m.Mapping("agent-1")
			if c == nil {
				return false
			}
			return c.Server == "127.0.0.1:8081"
		})
	})

	t.Run("Cluster mapping is deleted when label is removed", func(t *testing.T) {
		s, err := clt.CoreV1().Secrets("argocd").Get(context.TODO(), secretName, metav1.GetOptions{})
		require.NoError(t, err)
		delete(s.Labels, LabelKeyClusterAgentMapping)
		_, err = clt.CoreV1().Secrets("argocd").Update(context.TODO(), s, metav1.UpdateOptions{})
		assert.NoError(t, err)
		// Cluster should be unmapped
		testutil.WaitForChange(t, 1*time.Second, func() bool {
			return !m.HasMapping("agent-1")
		})
	})

	t.Run("Cluster mapping is deleted when secret is deleted", func(t *testing.T) {
		_, err := clt.CoreV1().Secrets("argocd").Get(context.TODO(), "test-234", metav1.GetOptions{})
		require.NoError(t, err)
		require.True(t, m.HasMapping("agent-3"))
		err = clt.CoreV1().Secrets("argocd").Delete(context.TODO(), "test-234", metav1.DeleteOptions{})
		require.NoError(t, err)
		testutil.WaitForChange(t, 1*time.Second, func() bool {
			return !m.HasMapping("agent-3")
		})
	})

}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
