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
	"testing"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func Test_SourceResourceCache(t *testing.T) {

	sourceCache := NewSourceCache()
	t.Run("Test ApplicationSpec Cache", func(t *testing.T) {

		appSpecCache := sourceCache.Application
		assert.Empty(t, appSpecCache.items)

		expectedAppSpec := v1alpha1.ApplicationSpec{Project: "test-project"}
		appSpecCache.Set("test", expectedAppSpec)
		assert.Equal(t, 1, len(appSpecCache.items))

		actualAppSpec, ok := appSpecCache.Get("test")
		assert.True(t, ok)
		assert.Equal(t, expectedAppSpec, actualAppSpec)

		expectedAppSpec.Project = "test-project-updated"
		appSpecCache.Set("test", expectedAppSpec)
		assert.Equal(t, 1, len(appSpecCache.items))

		actualAppSpec, ok = appSpecCache.Get("test")
		assert.True(t, ok)
		assert.Equal(t, expectedAppSpec, actualAppSpec)

		appSpecCache.Delete("test")
		assert.Empty(t, appSpecCache.items)
	})

	t.Run("Test AppProjectSpec Cache", func(t *testing.T) {

		appProjectSpecCache := sourceCache.AppProject
		assert.Empty(t, appProjectSpecCache.items)

		expectedAppSpec := v1alpha1.AppProjectSpec{Description: "test-project"}
		appProjectSpecCache.Set("test", expectedAppSpec)
		assert.Equal(t, 1, len(appProjectSpecCache.items))

		actualAppSpec, ok := appProjectSpecCache.Get("test")
		assert.True(t, ok)
		assert.Equal(t, expectedAppSpec, actualAppSpec)

		expectedAppSpec.Description = "test-project-updated"
		appProjectSpecCache.Set("test", expectedAppSpec)
		assert.Equal(t, 1, len(appProjectSpecCache.items))

		actualAppSpec, ok = appProjectSpecCache.Get("test")
		assert.True(t, ok)
		assert.Equal(t, expectedAppSpec, actualAppSpec)

		appProjectSpecCache.Delete("test")
		assert.Empty(t, appProjectSpecCache.items)
	})

	t.Run("Test RepositorySpec Cache", func(t *testing.T) {

		repositorySpecCache := sourceCache.Repository
		assert.Empty(t, repositorySpecCache.items)

		expectedRepositorySpec := map[string][]byte{
			"test": {0x01, 0x02, 0x03},
		}
		repositorySpecCache.Set("test", expectedRepositorySpec)
		assert.Equal(t, 1, len(repositorySpecCache.items))

		actualRepositorySpec, ok := repositorySpecCache.Get("test")
		assert.True(t, ok)
		assert.Equal(t, expectedRepositorySpec, actualRepositorySpec)

		expectedRepositorySpec = map[string][]byte{
			"test": {0x04, 0x05, 0x06},
		}
		repositorySpecCache.Set("test", expectedRepositorySpec)
		assert.Equal(t, 1, len(repositorySpecCache.items))

		actualRepositorySpec, ok = repositorySpecCache.Get("test")
		assert.True(t, ok)
		assert.Equal(t, expectedRepositorySpec, actualRepositorySpec)

		repositorySpecCache.Delete("test")
		assert.Empty(t, repositorySpecCache.items)
	})
}
