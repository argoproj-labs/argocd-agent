package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/argoproj/argo-cd/v3/common"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
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

	// Add the required Redis secret for cluster cache functionality
	redisSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-redis",
			Namespace: "argocd",
		},
		Data: map[string][]byte{
			"auth": []byte(uuid.NewString()),
		},
	}
	clt := kube.NewFakeClientsetWithResources(redisSecret)
	m, err := NewManager(context.TODO(), "argocd", "", "", cacheutil.RedisCompressionGZip, clt, nil)
	require.NoError(t, err)
	require.NotNil(t, m)
	err = m.Start()
	require.NoError(t, err)
	err = m.Stop()
	require.NoError(t, err)
}

func Test_onClusterAdd(t *testing.T) {
	// Add the required Redis secret for cluster cache functionality
	redisSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-redis",
			Namespace: "argocd",
		},
		Data: map[string][]byte{
			"auth": []byte(uuid.NewString()),
		},
	}
	clt := kube.NewFakeClientsetWithResources(redisSecret)
	m, err := NewManager(context.TODO(), "argocd", "", "", cacheutil.RedisCompressionGZip, clt, nil)
	require.NoError(t, err)
	require.NotNil(t, m)
	err = m.Start()
	require.NoError(t, err)

	t.Run("Cluster is mapped successfully when secret is added", func(t *testing.T) {
		c, s := newClusterSecret(t, "agent-1")
		_, err := clt.CoreV1().Secrets("argocd").Create(context.TODO(), s, metav1.CreateOptions{})
		require.NoError(t, err)
		var nc *v1alpha1.Cluster
		err = wait.PollUntilContextTimeout(context.Background(), 1*time.Second, 10*time.Second, true, func(ctx context.Context) (done bool, err error) {
			nc = m.Mapping("agent-1")
			return nc != nil, nil
		})
		assert.NoError(t, err)
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
		err = wait.PollUntilContextTimeout(context.Background(), 1*time.Second, 10*time.Second, true, func(ctx context.Context) (done bool, err error) {
			if m.HasMapping("agent-2") {
				return m.Mapping("agent-2").Server == c.Server, nil
			}
			return false, nil
		})
		assert.NoError(t, err)
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
		err = wait.PollUntilContextTimeout(context.Background(), 1*time.Second, 10*time.Second, true, func(ctx context.Context) (done bool, err error) {
			return !m.HasMapping("agent-2") && m.HasMapping("agent-3"), nil
		})
		assert.NoError(t, err)
	})

	t.Run("Cluster mapping is updated when secret is updated", func(t *testing.T) {
		s, err := clt.CoreV1().Secrets("argocd").Get(context.TODO(), secretName, metav1.GetOptions{})
		require.NoError(t, err)
		s.Data["server"] = []byte("127.0.0.1:8081")
		_, err = clt.CoreV1().Secrets("argocd").Update(context.TODO(), s, metav1.UpdateOptions{})
		assert.NoError(t, err)
		err = wait.PollUntilContextTimeout(context.Background(), 1*time.Second, 10*time.Second, true, func(ctx context.Context) (done bool, err error) {
			c := m.Mapping("agent-1")
			if c == nil {
				return false, nil
			}
			return c.Server == "127.0.0.1:8081", nil
		})
		assert.NoError(t, err)
	})

	t.Run("Cluster mapping is deleted when label is removed", func(t *testing.T) {
		s, err := clt.CoreV1().Secrets("argocd").Get(context.TODO(), secretName, metav1.GetOptions{})
		require.NoError(t, err)
		delete(s.Labels, LabelKeyClusterAgentMapping)
		_, err = clt.CoreV1().Secrets("argocd").Update(context.TODO(), s, metav1.UpdateOptions{})
		assert.NoError(t, err)
		// Cluster should be unmapped
		err = wait.PollUntilContextTimeout(context.Background(), 1*time.Second, 10*time.Second, true, func(ctx context.Context) (done bool, err error) {
			return !m.HasMapping("agent-1"), nil
		})
		assert.NoError(t, err)
	})

	t.Run("Cluster mapping is deleted when secret is deleted", func(t *testing.T) {
		_, err := clt.CoreV1().Secrets("argocd").Get(context.TODO(), "test-234", metav1.GetOptions{})
		require.NoError(t, err)
		require.True(t, m.HasMapping("agent-3"))
		err = clt.CoreV1().Secrets("argocd").Delete(context.TODO(), "test-234", metav1.DeleteOptions{})
		require.NoError(t, err)
		err = wait.PollUntilContextTimeout(context.Background(), 1*time.Second, 10*time.Second, true, func(ctx context.Context) (done bool, err error) {
			return !m.HasMapping("agent-3"), nil
		})
		assert.NoError(t, err)
	})

}

func newSelfRegisteredClusterSecret(t *testing.T, name string) *v1.Secret {
	t.Helper()
	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       "argocd",
			UID:             k8stypes.UID("uid-" + name),
			ResourceVersion: "1",
			Labels: map[string]string{
				common.LabelKeySecretType:     common.LabelValueSecretTypeCluster,
				LabelKeySelfRegisteredCluster: "true",
			},
		},
		Data: map[string][]byte{"server": []byte("https://example.com")},
	}
}

func TestGetClusterSecrets(t *testing.T) {
	selfReg := newSelfRegisteredClusterSecret(t, "cluster-agent-a")

	manualCluster := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-manual",
			Namespace: "argocd",
			Labels: map[string]string{
				common.LabelKeySecretType: common.LabelValueSecretTypeCluster,
			},
		},
	}

	clt := kube.NewFakeClientsetWithResources(selfReg, manualCluster)
	m := &Manager{namespace: "argocd", kubeclient: clt}

	t.Run("returns only self-registered cluster secrets", func(t *testing.T) {
		secrets, err := m.GetClusterSecrets(context.Background())
		require.NoError(t, err)
		require.Len(t, secrets, 1)
		assert.Equal(t, "cluster-agent-a", secrets[0].Name)
	})

	t.Run("returns empty slice when no self-registered secrets exist", func(t *testing.T) {
		clt2 := kube.NewFakeClientsetWithResources(manualCluster)
		m2 := &Manager{namespace: "argocd", kubeclient: clt2}
		secrets, err := m2.GetClusterSecrets(context.Background())
		require.NoError(t, err)
		assert.Empty(t, secrets)
	})
}

func TestCreateOrUpdateClusterSecret(t *testing.T) {
	t.Run("creates secret when absent", func(t *testing.T) {
		clt := kube.NewFakeClientsetWithResources()
		m := &Manager{namespace: "argocd", kubeclient: clt}

		secret := newSelfRegisteredClusterSecret(t, "cluster-new")
		err := m.CreateOrUpdateClusterSecret(context.Background(), secret)
		require.NoError(t, err)

		created, err := clt.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-new", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "cluster-new", created.Name)
		assert.Equal(t, "argocd", created.Namespace)
	})

	t.Run("clears UID and ResourceVersion before create", func(t *testing.T) {
		clt := kube.NewFakeClientsetWithResources()
		m := &Manager{namespace: "argocd", kubeclient: clt}

		secret := newSelfRegisteredClusterSecret(t, "cluster-clear")
		secret.UID = "original-uid"
		secret.ResourceVersion = "999"

		err := m.CreateOrUpdateClusterSecret(context.Background(), secret)
		require.NoError(t, err)

		created, err := clt.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-clear", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Empty(t, string(created.UID))
		assert.Empty(t, created.ResourceVersion)
	})

	t.Run("updates existing secret preserving UID and ResourceVersion", func(t *testing.T) {
		existing := newSelfRegisteredClusterSecret(t, "cluster-exist")
		existing.UID = "existing-uid"
		existing.ResourceVersion = "42"
		existing.Data = map[string][]byte{"server": []byte("https://old.example.com")}

		clt := kube.NewFakeClientsetWithResources(existing)
		m := &Manager{namespace: "argocd", kubeclient: clt}

		updated := newSelfRegisteredClusterSecret(t, "cluster-exist")
		updated.Data = map[string][]byte{"server": []byte("https://new.example.com")}

		err := m.CreateOrUpdateClusterSecret(context.Background(), updated)
		require.NoError(t, err)

		got, err := clt.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-exist", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, []byte("https://new.example.com"), got.Data["server"])
	})

	t.Run("sets namespace from manager", func(t *testing.T) {
		clt := kube.NewFakeClientsetWithResources()
		m := &Manager{namespace: "argocd", kubeclient: clt}

		secret := newSelfRegisteredClusterSecret(t, "cluster-ns")
		secret.Namespace = "some-other-ns"

		err := m.CreateOrUpdateClusterSecret(context.Background(), secret)
		require.NoError(t, err)

		_, err = clt.CoreV1().Secrets("argocd").Get(context.Background(), "cluster-ns", metav1.GetOptions{})
		require.NoError(t, err)
	})
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
