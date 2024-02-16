package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_AgentMode_String(t *testing.T) {
	assert.Equal(t, agentModeAutonomous, AgentModeAutonomous.String())
	assert.Equal(t, agentModeManaged, AgentModeManaged.String())
	assert.Equal(t, agentModeUnknown, AgentModeUnknown.String())
}

func Test_AgentMode_FromString(t *testing.T) {
	assert.Equal(t, AgentModeAutonomous, AgentModeFromString(agentModeAutonomous))
	assert.Equal(t, AgentModeManaged, AgentModeFromString(agentModeManaged))
	assert.Equal(t, AgentModeUnknown, AgentModeFromString("whatever"))
}
