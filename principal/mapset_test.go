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

package principal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_newMapToSet(t *testing.T) {
	ms := NewMapToSet()
	require.NotNil(t, ms)
	require.NotNil(t, ms.m)
	assert.Equal(t, 0, len(ms.m))
}

func Test_mapToSet_Add(t *testing.T) {
	t.Run("Add single value", func(t *testing.T) {
		ms := NewMapToSet()
		ms.Add("repo1", "agent1")

		values := ms.Get("repo1")
		require.NotNil(t, values)
		assert.True(t, values["agent1"])
		assert.Equal(t, 1, len(values))
	})

	t.Run("Add multiple values to same key", func(t *testing.T) {
		ms := NewMapToSet()
		ms.Add("repo1", "agent2")
		ms.Add("repo1", "agent3")

		values := ms.Get("repo1")
		require.NotNil(t, values)
		assert.True(t, values["agent2"])
		assert.True(t, values["agent3"])
		assert.Equal(t, 2, len(values))
	})

	t.Run("Add duplicate value", func(t *testing.T) {
		ms := NewMapToSet()
		ms.Add("repo1", "agent1")
		ms.Add("repo1", "agent2")
		ms.Add("repo1", "agent1") // duplicate

		values := ms.Get("repo1")
		require.NotNil(t, values)
		assert.True(t, values["agent1"])
		assert.True(t, values["agent2"])
		assert.Equal(t, 2, len(values))
	})

	t.Run("Add to different keys", func(t *testing.T) {
		ms := NewMapToSet()
		ms.Add("repo1", "agent1")
		ms.Add("repo1", "agent2")
		ms.Add("repo1", "agent3")
		ms.Add("repo2", "agent4")
		ms.Add("repo3", "agent5")

		values1 := ms.Get("repo1")
		values2 := ms.Get("repo2")
		values3 := ms.Get("repo3")

		assert.Equal(t, 3, len(values1))
		assert.Equal(t, 1, len(values2))
		assert.Equal(t, 1, len(values3))
		assert.True(t, values2["agent4"])
		assert.True(t, values3["agent5"])
	})
}

func Test_mapToSet_Get(t *testing.T) {
	t.Run("Get non-existent key returns nil", func(t *testing.T) {
		ms := NewMapToSet()
		values := ms.Get("nonexistent")
		assert.Nil(t, values)
	})

	t.Run("Get existing key returns correct values", func(t *testing.T) {
		ms := NewMapToSet()
		ms.Add("project1", "agent1")
		ms.Add("project1", "agent2")

		values := ms.Get("project1")
		require.NotNil(t, values)
		assert.True(t, values["agent1"])
		assert.True(t, values["agent2"])
		assert.False(t, values["agent3"]) // should be false for non-existent value
		assert.Equal(t, 2, len(values))
	})

	t.Run("Get after delete returns updated values", func(t *testing.T) {
		ms := NewMapToSet()
		ms.Add("project1", "agent1")
		ms.Add("project1", "agent2")
		ms.Delete("project1", "agent1")

		values := ms.Get("project1")
		require.NotNil(t, values)
		assert.False(t, values["agent1"])
		assert.True(t, values["agent2"])
		assert.Equal(t, 1, len(values))
	})
}

func Test_mapToSet_Delete(t *testing.T) {
	t.Run("Delete from non-existent key does not panic", func(t *testing.T) {
		ms := NewMapToSet()
		assert.NotPanics(t, func() {
			ms.Delete("nonexistent", "value")
		})

		// Should still be empty
		values := ms.Get("nonexistent")
		assert.Nil(t, values)
	})

	t.Run("Delete non-existent value from existing key", func(t *testing.T) {
		ms := NewMapToSet()
		ms.Add("project1", "agent1")

		assert.NotPanics(t, func() {
			ms.Delete("project1", "nonexistent")
		})

		values := ms.Get("project1")
		require.NotNil(t, values)
		assert.True(t, values["agent1"])
		assert.Equal(t, 1, len(values))
	})

	t.Run("Delete existing value", func(t *testing.T) {
		ms := NewMapToSet()
		ms.Add("project1", "agent1")
		ms.Add("project1", "agent2")

		ms.Delete("project1", "agent1")

		values := ms.Get("project1")
		require.NotNil(t, values)
		assert.False(t, values["agent1"])
		assert.True(t, values["agent2"])
		assert.Equal(t, 1, len(values))
	})

	t.Run("Delete last value removes key entirely", func(t *testing.T) {
		ms := NewMapToSet()
		ms.Add("project1", "agent1")
		ms.Delete("project1", "agent1")

		values := ms.Get("project1")
		assert.Nil(t, values)

		values = ms.Get("project1") // should be nil
		assert.Nil(t, values)
	})

	t.Run("Delete same value multiple times does not panic", func(t *testing.T) {
		ms := NewMapToSet()
		ms.Add("project1", "agent1")

		assert.NotPanics(t, func() {
			ms.Delete("project1", "agent1")
			ms.Delete("project1", "agent1") // second delete
			ms.Delete("project1", "agent1") // third delete
		})

		values := ms.Get("project1")
		assert.Nil(t, values) // key should be removed after first delete
	})
}

func Test_mapToSet_PanicPrevention(t *testing.T) {
	t.Run("Multiple operations on empty map do not panic", func(t *testing.T) {
		ms := NewMapToSet()
		assert.NotPanics(t, func() {
			ms.Delete("key1", "value1")
			ms.Delete("key2", "value2")
			values := ms.Get("key1")
			assert.Nil(t, values)
			values = ms.Get("key1") // should be nil
			assert.Nil(t, values)
		})
	})

	t.Run("Delete operations after adding and removing do not panic", func(t *testing.T) {
		ms := NewMapToSet()
		assert.NotPanics(t, func() {
			// Add some data
			ms.Add("project1", "agent1")
			ms.Add("project1", "agent2")
			ms.Add("project2", "agent3")

			// Delete everything
			ms.Delete("project1", "agent1")
			ms.Delete("project1", "agent2")
			ms.Delete("project2", "agent3")

			// Try to delete more (should not panic)
			ms.Delete("project1", "agent1")
			ms.Delete("project2", "agent3")
			ms.Delete("nonexistent", "value")
		})
	})

	t.Run("Rapid add/delete cycles do not panic", func(t *testing.T) {
		ms := NewMapToSet()
		assert.NotPanics(t, func() {
			for i := 0; i < 100; i++ {
				ms.Add("key", "value")
				ms.Delete("key", "value")
			}
		})
	})
}
