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
