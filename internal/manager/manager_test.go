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

package manager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_IsPrincipal(t *testing.T) {
	r := ManagerRoleAgent
	assert.True(t, r.IsAgent())
	assert.False(t, r.IsPrincipal())
}
func Test_IsAgent(t *testing.T) {
	r := ManagerRolePrincipal
	assert.True(t, r.IsPrincipal())
	assert.False(t, r.IsAgent())
}

func Test_IsAutonomous(t *testing.T) {
	m := ManagerModeAutonomous
	assert.True(t, m.IsAutonomous())
	assert.False(t, m.IsManaged())
}

func Test_IsManaged(t *testing.T) {
	m := ManagerModeManaged
	assert.True(t, m.IsManaged())
	assert.False(t, m.IsAutonomous())
}
