package types

const (
	AuthResultOK           string = "ok"
	AuthResultUnauthorized string = "unauthorized"
)

type AgentMode string

const (
	AgentModeUnknown    AgentMode = ""
	AgentModeManaged    AgentMode = "managed"
	AgentModeAutonomous AgentMode = "autonomous"
)

func (m AgentMode) String() string {
	switch m {
	case AgentModeManaged:
		return "managed"
	case AgentModeAutonomous:
		return "autonomous"
	}
	return "unknown"
}

func AgentModeFromString(mode string) AgentMode {
	switch mode {
	case "managed":
		return AgentModeManaged
	case "autonomous":
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
