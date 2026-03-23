package principal

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEnsurePrincipalUID_CreatesNewConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctx := context.Background()

	uid, err := EnsurePrincipalUID(ctx, client, "argocd")
	require.NoError(t, err)
	require.NotEmpty(t, uid)

	_, err = uuid.Parse(uid)
	assert.NoError(t, err, "returned uid should be a valid UUID")

	cm, err := client.CoreV1().ConfigMaps("argocd").Get(ctx, IdentityConfigMapName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, uid, cm.Data[identityKey])
}

func TestEnsurePrincipalUID_ReturnsExistingUID(t *testing.T) {
	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      IdentityConfigMapName,
			Namespace: "argocd",
		},
		Data: map[string]string{
			identityKey: "pre-existing-uid",
		},
	}
	client := fake.NewSimpleClientset(existing)

	uid, err := EnsurePrincipalUID(context.Background(), client, "argocd")
	require.NoError(t, err)
	assert.Equal(t, "pre-existing-uid", uid)
}

func TestEnsurePrincipalUID_StableAcrossCalls(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctx := context.Background()

	uid1, err := EnsurePrincipalUID(ctx, client, "argocd")
	require.NoError(t, err)

	uid2, err := EnsurePrincipalUID(ctx, client, "argocd")
	require.NoError(t, err)

	assert.Equal(t, uid1, uid2)
}

func TestEnsurePrincipalUID_ErrorOnEmptyKey(t *testing.T) {
	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      IdentityConfigMapName,
			Namespace: "argocd",
		},
		Data: map[string]string{
			identityKey: "",
		},
	}
	client := fake.NewSimpleClientset(existing)

	_, err := EnsurePrincipalUID(context.Background(), client, "argocd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}
