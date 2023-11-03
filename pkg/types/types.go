package types

const (
	AuthResultOK           string = "ok"
	AuthResultUnauthorized string = "unauthorized"
)

type AgentMode int

const (
	AgentModeNone AgentMode = iota
	AgentModeManaged
	AgentModeAutonomous
)

func (m AgentMode) String() string {
	switch m {
	case AgentModeNone:
		return "none"
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
		return AgentModeNone
	}
}

type EventContextKey string

func (k EventContextKey) String() string {
	return string(k)
}

const ContextAgentIdentifier EventContextKey = "agent_name"
