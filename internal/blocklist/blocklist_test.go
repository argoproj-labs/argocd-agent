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

package blocklist

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFingerprints(t *testing.T) {
	t.Run("empty string returns empty slice", func(t *testing.T) {
		fps, err := ParseFingerprints("")
		require.NoError(t, err)
		assert.Empty(t, fps)
	})

	t.Run("valid JSON array", func(t *testing.T) {
		fps, err := ParseFingerprints(`["AA:BB","CC:DD"]`)
		require.NoError(t, err)
		assert.Equal(t, []string{"AA:BB", "CC:DD"}, fps)
	})

	t.Run("empty JSON array", func(t *testing.T) {
		fps, err := ParseFingerprints(`[]`)
		require.NoError(t, err)
		assert.Empty(t, fps)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		_, err := ParseFingerprints(`not-json`)
		assert.Error(t, err)
	})
}

func TestBlocklist(t *testing.T) {
	t.Run("new blocklist is empty", func(t *testing.T) {
		bl := New()
		assert.Equal(t, 0, bl.Len())
		assert.False(t, bl.Contains("AA:BB"))
	})

	t.Run("add and contains", func(t *testing.T) {
		bl := New()
		bl.Add("AA:BB")
		assert.True(t, bl.Contains("AA:BB"))
		assert.False(t, bl.Contains("CC:DD"))
		assert.Equal(t, 1, bl.Len())
	})

	t.Run("remove", func(t *testing.T) {
		bl := New()
		bl.Add("AA:BB")
		bl.Remove("AA:BB")
		assert.False(t, bl.Contains("AA:BB"))
		assert.Equal(t, 0, bl.Len())
	})

	t.Run("remove non-existent is no-op", func(t *testing.T) {
		bl := New()
		bl.Remove("AA:BB")
		assert.Equal(t, 0, bl.Len())
	})

	t.Run("list returns all entries", func(t *testing.T) {
		bl := New()
		bl.Add("AA:BB")
		bl.Add("CC:DD")
		listed := bl.List()
		assert.Len(t, listed, 2)
		assert.ElementsMatch(t, []string{"AA:BB", "CC:DD"}, listed)
	})

	t.Run("replace overwrites all entries", func(t *testing.T) {
		bl := New()
		bl.Add("AA:BB")
		bl.Add("CC:DD")
		bl.Replace([]string{"EE:FF"})
		assert.Equal(t, 1, bl.Len())
		assert.True(t, bl.Contains("EE:FF"))
		assert.False(t, bl.Contains("AA:BB"))
		assert.False(t, bl.Contains("CC:DD"))
	})

	t.Run("replace with empty clears all entries", func(t *testing.T) {
		bl := New()
		bl.Add("AA:BB")
		bl.Replace([]string{})
		assert.Equal(t, 0, bl.Len())
	})

	t.Run("add duplicate is idempotent", func(t *testing.T) {
		bl := New()
		bl.Add("AA:BB")
		bl.Add("AA:BB")
		assert.Equal(t, 1, bl.Len())
	})
}
