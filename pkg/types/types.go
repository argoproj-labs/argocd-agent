package types

const (
	AuthResultOK           string = "ok"
	AuthResultUnauthorized string = "unauthorized"
)

type AgentMode string

const agentModeManaged = "managed"
const agentModeAutonomous = "autonomous"
const agentModeUnknown = "unknown"

const (
	AgentModeUnknown    AgentMode = ""
	AgentModeManaged    AgentMode = "managed"
	AgentModeAutonomous AgentMode = "autonomous"
)

func (m AgentMode) String() string {
	switch m {
	case AgentModeManaged:
		return agentModeManaged
	case AgentModeAutonomous:
		return agentModeAutonomous
	}
	return agentModeUnknown
}

func AgentModeFromString(mode string) AgentMode {
	switch mode {
	case agentModeManaged:
		return AgentModeManaged
	case agentModeAutonomous:
		return AgentModeAutonomous
	default:
		return AgentModeUnknown
	}
}

type EventContextKey string

func (k EventContextKey) String() string {
	return string(k)
}

const ContextAgentIdentifier EventContextKey = "agent_name"
