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
	"fmt"
	"strconv"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func Test_NewResourceKey(t *testing.T) {
	app := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			UID:       "12345",
		},
	}

	appProject := &v1alpha1.AppProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-appproject",
			Namespace: "default",
			UID:       "67890",
		},
	}

	expected := ResourceKey{
		Kind:      applicationKind,
		Name:      "test-app",
		Namespace: "default",
		UID:       "12345",
	}

	t.Run("resource key for an app without sourceUID annotation", func(t *testing.T) {
		got := NewResourceKeyFromApp(app)
		assert.Equal(t, expected, got)
	})

	t.Run("resource key for an app resource with sourceUID annotation", func(t *testing.T) {
		app.Annotations = map[string]string{
			manager.SourceUIDAnnotation: "source-uid-123",
		}

		expected.UID = app.Annotations[manager.SourceUIDAnnotation]

		got := NewResourceKeyFromApp(app)
		assert.Equal(t, expected, got)
	})

	t.Run("resource key for an appProject without sourceUID annotation", func(t *testing.T) {
		expected := ResourceKey{
			Kind:      appProjectKind,
			Name:      "test-appproject",
			Namespace: "default",
			UID:       "67890",
		}

		got := NewResourceKeyFromAppProject(appProject)
		assert.Equal(t, expected, got)
	})

	t.Run("resource key for an appProject resource with sourceUID annotation", func(t *testing.T) {
		appProject.Annotations = map[string]string{
			manager.SourceUIDAnnotation: "source-uid-456",
		}

		expected := ResourceKey{
			Kind:      appProjectKind,
			Name:      "test-appproject",
			Namespace: "default",
			UID:       "source-uid-456",
		}

		got := NewResourceKeyFromAppProject(appProject)
		assert.Equal(t, expected, got)
	})
}

func Test_AgentResources(t *testing.T) {
	res := NewAgentResources()

	resKeys := make([]ResourceKey, 10)
	for i := 0; i < 10; i++ {
		app := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("test-%d", i),
				Namespace: "default",
				UID:       types.UID(strconv.Itoa(i)),
			},
		}

		resKeys[i] = NewResourceKeyFromApp(app)
	}

	// Add the resources
	for i := 0; i < len(resKeys)/2; i++ {
		res.Add("first", resKeys[i])
	}

	for i := len(resKeys) / 2; i < len(resKeys); i++ {
		res.Add("second", resKeys[i])
	}

	isEqualSlice := func(a, b []ResourceKey) bool {
		if len(a) != len(b) {
			return false
		}

		freq := map[ResourceKey]int{}
		for i := 0; i < len(a); i++ {
			freq[a[i]]++
		}

		for i := 0; i < len(b); i++ {
			if freq[b[i]] == 0 {
				return false
			}
			freq[b[i]]--
		}
		return true
	}

	assert.Equal(t, 2, res.Len())
	assert.Equal(t, 2, res.Len())

	// Verify if they are added to the correct resource map
	first := res.GetAllResources("first")
	assert.Len(t, first, 5)
	assert.True(t, isEqualSlice(first, resKeys[:5]))

	second := res.GetAllResources("second")
	assert.Len(t, second, 5)
	assert.True(t, isEqualSlice(second, resKeys[5:]))

	assert.NotNil(t, res.Checksum("first"))
	assert.NotNil(t, res.Checksum("second"))

	// Remove all the resources
	for i := 0; i < len(resKeys)/2; i++ {
		res.Remove("first", resKeys[i])
	}

	for i := len(resKeys) / 2; i < len(resKeys); i++ {
		res.Remove("second", resKeys[i])
	}

	assert.Empty(t, res.Checksum("first"))
	assert.Empty(t, res.Checksum("second"))
}
