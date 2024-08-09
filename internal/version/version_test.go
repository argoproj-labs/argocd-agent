// Copyright 2024 The argocd-agent Authors
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

package version

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func Test_Version(t *testing.T) {
	t.Run("Text version", func(t *testing.T) {
		v := New("foo", "bar")
		assert.Equal(t, "foo", v.Name())
		assert.Equal(t, "bar", v.Component())
		assert.Equal(t, version, v.Version())
		assert.Equal(t, gitRevision, v.GitRevision())
		assert.Equal(t, gitStatus, v.GitStatus())
		assert.Greater(t, len(v.QualifiedVersion()), len(v.Name())+len(v.Version()))
	})

	t.Run("YAML version", func(t *testing.T) {
		v := New("foo", "bar")
		y := v.YAML()
		require.NotContains(t, y, "error: ")
		vn := Version{}
		err := yaml.Unmarshal([]byte(y), &vn.v)
		require.NoError(t, err)
		assert.Equal(t, "foo", vn.Name())
		assert.Equal(t, "bar", vn.Component())
		assert.Equal(t, version, vn.Version())
		assert.Equal(t, gitRevision, vn.GitRevision())
		assert.Equal(t, gitStatus, vn.GitStatus())
		assert.Greater(t, len(vn.QualifiedVersion()), len(vn.Name())+len(vn.Version()))
	})

	t.Run("JSON version", func(t *testing.T) {
		v := New("foo", "bar")
		j := v.JSON(false)
		require.NotEqual(t, "{}", j)
		vn := Version{}
		err := json.Unmarshal([]byte(j), &vn.v)
		require.NoError(t, err)
		assert.Equal(t, "foo", vn.Name())
		assert.Equal(t, "bar", vn.Component())
		assert.Equal(t, version, vn.Version())
		assert.Equal(t, gitRevision, vn.GitRevision())
		assert.Equal(t, gitStatus, vn.GitStatus())
		assert.Greater(t, len(vn.QualifiedVersion()), len(vn.Name())+len(vn.Version()))
	})

	t.Run("JSON version with indent", func(t *testing.T) {
		v := New("foo", "bar")
		j := v.JSON(true)
		require.NotEqual(t, "{}", j)
		vn := Version{}
		err := json.Unmarshal([]byte(j), &vn.v)
		require.NoError(t, err)
		assert.Equal(t, "foo", vn.Name())
		assert.Equal(t, "bar", vn.Component())
		assert.Equal(t, version, vn.Version())
		assert.Equal(t, gitRevision, vn.GitRevision())
		assert.Equal(t, gitStatus, vn.GitStatus())
		assert.Greater(t, len(vn.QualifiedVersion()), len(vn.Name())+len(vn.Version()))
	})

}
