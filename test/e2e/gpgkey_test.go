// Copyright 2026 The argocd-agent Authors
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

package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	"github.com/argoproj/argo-cd/v3/common"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	gpgKeyIDPrimary   = "4AEE18F83AFDEB23"
	gpgKeyIDSecondary = "9B2F7A3C1D4E5F60"
	gpgKeyIDTampered  = "5B2F7A3C1D4E5F60"
)

type GPGKeyTestSuite struct {
	fixture.BaseSuite
}

// This test verifies:
// - The GPG key is pushed to the managed agent when created on the principal's cluster
// - The GPG key is updated on the managed agent when updated on the principal's cluster
// - The GPG key is deleted from the managed agent when deleted from the principal's cluster
// - The GPG key is not pushed to the autonomous agent
func (suite *GPGKeyTestSuite) Test_GPGKey_Managed() {
	requires := suite.Require()

	// Create the GPG keys ConfigMap on the principal's cluster
	sourceGPGKeys := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.ArgoCDGPGKeysConfigMapName,
			Namespace: "argocd",
			Labels: map[string]string{
				"app.kubernetes.io/name":    common.ArgoCDGPGKeysConfigMapName,
				"app.kubernetes.io/part-of": "argocd",
			},
		},
		Data: map[string]string{
			gpgKeyIDPrimary: "-----BEGIN PGP PUBLIC KEY BLOCK-----\ntest-key-data-1\n-----END PGP PUBLIC KEY BLOCK-----\n",
		},
	}

	err := suite.PrincipalClient.Create(suite.Ctx, &sourceGPGKeys, metav1.CreateOptions{})
	requires.NoError(err)

	key := types.NamespacedName{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: "argocd"}

	// Ensure the GPG keys ConfigMap has been pushed to the managed agent with correct source UID and data
	var gpgKeysCM corev1.ConfigMap
	requires.Eventually(func() bool {
		tempCM := corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &tempCM, metav1.GetOptions{})
		if err != nil {
			return false
		}
		if tempCM.Annotations[manager.SourceUIDAnnotation] != string(sourceGPGKeys.UID) {
			return false
		}
		gpgKeysCM = tempCM
		return true
	}, 60*time.Second, 1*time.Second, "GPG keys ConfigMap should be pushed to managed-agent with correct source UID")

	requires.Equal(sourceGPGKeys.Data, gpgKeysCM.Data)

	// Ensure the GPG keys ConfigMap is not pushed to the autonomous agent
	requires.Never(func() bool {
		cm := corev1.ConfigMap{}
		err := suite.AutonomousAgentClient.Get(suite.Ctx, key, &cm, metav1.GetOptions{})
		return err == nil
	}, 15*time.Second, 1*time.Second, "GPG keys ConfigMap should not be pushed to autonomous agent")

	// Update the GPG keys ConfigMap and verify changes propagate to the managed agent
	updatedKeyData := "-----BEGIN PGP PUBLIC KEY BLOCK-----\ntest-key-data-updated\n-----END PGP PUBLIC KEY BLOCK-----\n"
	err = fixture.EnsureUpdate(suite.Ctx, suite.PrincipalClient, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: "argocd"}}, func(obj fixture.KubeObject) {
		obj.(*corev1.ConfigMap).Data[gpgKeyIDPrimary] = updatedKeyData
	})
	requires.NoError(err)

	// Ensure the GPG keys ConfigMap is updated on the managed agent
	requires.Eventually(func() bool {
		cm := corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &cm, metav1.GetOptions{})
		if err != nil {
			fmt.Println("error getting GPG keys ConfigMap", err)
			return false
		}
		if cm.Data[gpgKeyIDPrimary] != updatedKeyData {
			fmt.Println("GPG key data does not match", cm.Data[gpgKeyIDPrimary], updatedKeyData)
			return false
		}
		return true
	}, 60*time.Second, 1*time.Second, "GPG keys ConfigMap should be updated on managed-agent")

	// Delete the GPG keys ConfigMap on the principal
	err = suite.PrincipalClient.Delete(suite.Ctx, &sourceGPGKeys, metav1.DeleteOptions{})
	requires.NoError(err)

	// Ensure the GPG keys ConfigMap has been deleted from the managed agent
	requires.Eventually(func() bool {
		cm := corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &cm, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 60*time.Second, 1*time.Second, "GPG keys ConfigMap should be deleted from managed-agent")
}

// This test verifies:
// - The first GPG key is pushed to the managed agent when it is added on the principal's cluster
// - The second GPG key is added to the managed agent when it is added on the principal's cluster
// - One of the GPG keys is removed from the managed agent when it is removed on the principal's cluster
func (suite *GPGKeyTestSuite) Test_GPGKey_AddMultipleKeys() {
	requires := suite.Require()

	// Create the GPG keys ConfigMap with one key on the principal
	sourceGPGKeys := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.ArgoCDGPGKeysConfigMapName,
			Namespace: "argocd",
			Labels: map[string]string{
				"app.kubernetes.io/name":    common.ArgoCDGPGKeysConfigMapName,
				"app.kubernetes.io/part-of": "argocd",
			},
		},
		Data: map[string]string{
			gpgKeyIDPrimary: "-----BEGIN PGP PUBLIC KEY BLOCK-----\nkey-one\n-----END PGP PUBLIC KEY BLOCK-----\n",
		},
	}

	err := suite.PrincipalClient.Create(suite.Ctx, &sourceGPGKeys, metav1.CreateOptions{})
	requires.NoError(err)

	key := types.NamespacedName{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: "argocd"}

	// Ensure the GPG keys ConfigMap has been pushed to the managed agent
	requires.Eventually(func() bool {
		cm := corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &cm, metav1.GetOptions{})
		return err == nil && len(cm.Data) == 1
	}, 60*time.Second, 1*time.Second, "GPG keys ConfigMap should be pushed to managed-agent")

	// Add a second key to the ConfigMap on the principal
	err = fixture.EnsureUpdate(suite.Ctx, suite.PrincipalClient, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: "argocd"}}, func(obj fixture.KubeObject) {
		obj.(*corev1.ConfigMap).Data[gpgKeyIDSecondary] = "-----BEGIN PGP PUBLIC KEY BLOCK-----\nkey-two\n-----END PGP PUBLIC KEY BLOCK-----\n"
	})
	requires.NoError(err)

	// Ensure the second key appears on the managed agent
	requires.Eventually(func() bool {
		cm := corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &cm, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return len(cm.Data) == 2 && cm.Data[gpgKeyIDSecondary] != ""
	}, 60*time.Second, 1*time.Second, "GPG keys ConfigMap should be updated on managed-agent with two keys")

	// Delete one of the keys on the principal
	err = fixture.EnsureUpdate(suite.Ctx, suite.PrincipalClient, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: "argocd"}}, func(obj fixture.KubeObject) {
		delete(obj.(*corev1.ConfigMap).Data, gpgKeyIDPrimary)
	})
	requires.NoError(err)

	// Ensure the key is deleted from the managed agent
	requires.Eventually(func() bool {
		cm := corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &cm, metav1.GetOptions{})
		return err == nil && len(cm.Data) == 1 && cm.Data[gpgKeyIDSecondary] != ""
	}, 60*time.Second, 1*time.Second, "GPG keys ConfigMap should be updated on managed-agent with one key")
}

// This test verifies:
// - The GPG keys ConfigMap is reverted on the managed agent when local change is made
func (suite *GPGKeyTestSuite) Test_GPGKey_RevertLocalChange() {
	requires := suite.Require()

	// Create the GPG keys ConfigMap on the principal
	originalKeyData := "-----BEGIN PGP PUBLIC KEY BLOCK-----\noriginal-key\n-----END PGP PUBLIC KEY BLOCK-----\n"
	sourceGPGKeys := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.ArgoCDGPGKeysConfigMapName,
			Namespace: "argocd",
			Labels: map[string]string{
				"app.kubernetes.io/name":    common.ArgoCDGPGKeysConfigMapName,
				"app.kubernetes.io/part-of": "argocd",
			},
		},
		Data: map[string]string{
			gpgKeyIDPrimary: originalKeyData,
		},
	}

	err := suite.PrincipalClient.Create(suite.Ctx, &sourceGPGKeys, metav1.CreateOptions{})
	requires.NoError(err)

	key := types.NamespacedName{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: "argocd"}

	// Ensure the GPG keys ConfigMap has been pushed to the managed agent
	requires.Eventually(func() bool {
		cm := corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &cm, metav1.GetOptions{})
		return err == nil && cm.Data[gpgKeyIDPrimary] == originalKeyData
	}, 60*time.Second, 1*time.Second, "GPG keys ConfigMap should be pushed to managed-agent")

	// Tamper with the GPG keys ConfigMap on the managed agent
	err = fixture.EnsureUpdate(suite.Ctx, suite.ManagedAgentClient, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: "argocd"}}, func(obj fixture.KubeObject) {
		obj.(*corev1.ConfigMap).Data[gpgKeyIDTampered] = "-----BEGIN PGP PUBLIC KEY BLOCK-----\ninvalid-public-key\n-----END PGP PUBLIC KEY BLOCK-----\n"
	})
	requires.NoError(err)

	// Ensure the local change is reverted
	requires.Eventually(func() bool {
		cm := corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &cm, metav1.GetOptions{})
		if err != nil {
			return false
		}
		_, ok := cm.Data[gpgKeyIDTampered]
		return !ok && cm.Data[gpgKeyIDPrimary] == originalKeyData && len(cm.Data) == 1
	}, 60*time.Second, 1*time.Second, "GPG keys ConfigMap should be reverted on managed-agent")
}

// This test verifies:
// - The GPG keys ConfigMap is recreated on the managed agent when local deletion is made
func (suite *GPGKeyTestSuite) Test_GPGKey_RevertLocalDeletion() {
	requires := suite.Require()

	// Create the GPG keys ConfigMap on the principal
	originalKeyData := "-----BEGIN PGP PUBLIC KEY BLOCK-----\noriginal-key\n-----END PGP PUBLIC KEY BLOCK-----\n"
	sourceGPGKeys := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.ArgoCDGPGKeysConfigMapName,
			Namespace: "argocd",
			Labels: map[string]string{
				"app.kubernetes.io/name":    common.ArgoCDGPGKeysConfigMapName,
				"app.kubernetes.io/part-of": "argocd",
			},
		},
		Data: map[string]string{
			gpgKeyIDPrimary: originalKeyData,
		},
	}

	err := suite.PrincipalClient.Create(suite.Ctx, &sourceGPGKeys, metav1.CreateOptions{})
	requires.NoError(err)

	key := types.NamespacedName{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: "argocd"}

	// Ensure the GPG keys ConfigMap has been pushed to the managed agent
	requires.Eventually(func() bool {
		cm := corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &cm, metav1.GetOptions{})
		return err == nil && cm.Data[gpgKeyIDPrimary] == originalKeyData
	}, 60*time.Second, 1*time.Second, "GPG keys ConfigMap should be pushed to managed-agent")

	// Delete the GPG keys ConfigMap on the managed agent
	managedCM := corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: "argocd"}}
	err = suite.ManagedAgentClient.Delete(suite.Ctx, &managedCM, metav1.DeleteOptions{})
	requires.NoError(err)

	// Ensure the ConfigMap is recreated on the managed agent
	requires.Eventually(func() bool {
		cm := corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &cm, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return cm.Data[gpgKeyIDPrimary] == originalKeyData
	}, 60*time.Second, 1*time.Second, "GPG keys ConfigMap should be recreated on managed-agent after local deletion")
}

// This test verifies:
// - An orphan argocd-gpg-keys-cm on the managed agent, without source-uid annotation is used and updated.
func (suite *GPGKeyTestSuite) Test_GPGKey_UseAndUpdatePreExistingConfigMap() {
	requires := suite.Require()

	key := types.NamespacedName{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: "argocd"}

	// Wait until the GPG keys ConfigMap is deleted from the managed agent by the previous cleanup process
	requires.Eventually(func() bool {
		cm := corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &cm, metav1.GetOptions{})
		return err != nil && errors.IsNotFound(err)
	}, 30*time.Second, 1*time.Second, "GPG keys ConfigMap should not exist on managed-agent before creating orphan")

	// Create an orphan GPG keys ConfigMap on the managed agent which has no source-uid annotation
	orphanCM := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.ArgoCDGPGKeysConfigMapName,
			Namespace: "argocd",
			Labels: map[string]string{
				"app.kubernetes.io/name":    common.ArgoCDGPGKeysConfigMapName,
				"app.kubernetes.io/part-of": "argocd",
			},
		},
		Data: map[string]string{
			"old-key": "old-value",
		},
	}

	err := suite.ManagedAgentClient.Create(suite.Ctx, &orphanCM, metav1.CreateOptions{})
	requires.NoError(err)

	// Delete ConfigMap manually created on the managed agent
	defer func() {
		cm := &corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx,
			types.NamespacedName{Name: common.ArgoCDGPGKeysConfigMapName, Namespace: "argocd"}, cm, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return
			}
			requires.NoError(err)
			return
		}

		// Only delete if CM is still an orphan (no source-uid)
		// once adopted, TearDownTest handles it via principal deletion.
		if _, adopted := cm.Annotations[manager.SourceUIDAnnotation]; !adopted {
			requires.NoError(suite.ManagedAgentClient.Delete(suite.Ctx, cm, metav1.DeleteOptions{}))
		}
	}()

	// Verify the orphan ConfigMap has no source-uid annotation
	agentCM := corev1.ConfigMap{}
	err = suite.ManagedAgentClient.Get(suite.Ctx, key, &agentCM, metav1.GetOptions{})
	requires.NoError(err)
	requires.Empty(agentCM.Annotations[manager.SourceUIDAnnotation])

	// Create the GPG keys ConfigMap on the principal with actual key data
	principalKeyData := "-----BEGIN PGP PUBLIC KEY BLOCK-----\nprincipal-key\n-----END PGP PUBLIC KEY BLOCK-----\n"
	sourceGPGKeys := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.ArgoCDGPGKeysConfigMapName,
			Namespace: "argocd",
			Labels: map[string]string{
				"app.kubernetes.io/name":    common.ArgoCDGPGKeysConfigMapName,
				"app.kubernetes.io/part-of": "argocd",
			},
		},
		Data: map[string]string{
			gpgKeyIDPrimary: principalKeyData,
		},
	}

	err = suite.PrincipalClient.Create(suite.Ctx, &sourceGPGKeys, metav1.CreateOptions{})
	requires.NoError(err)

	// Verify the agent's ConfigMap is adopted: data replaced with the principal's version
	// and source-uid annotation is set
	requires.Eventually(func() bool {
		cm := corev1.ConfigMap{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, key, &cm, metav1.GetOptions{})
		if err != nil {
			return false
		}
		hasSourceUID := cm.Annotations[manager.SourceUIDAnnotation] != ""
		hasCorrectData := cm.Data[gpgKeyIDPrimary] == principalKeyData
		_, hasOldKey := cm.Data["old-key"]
		return hasSourceUID && hasCorrectData && !hasOldKey
	}, 60*time.Second, 1*time.Second, "Orphan GPG keys ConfigMap should be used and updated with principal's data and source-uid annotation")
}

func TestGPGKeyTestSuite(t *testing.T) {
	suite.Run(t, new(GPGKeyTestSuite))
}
