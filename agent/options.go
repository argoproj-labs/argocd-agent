package agent

import (
	"fmt"

	"github.com/jannfis/argocd-agent/pkg/client"
	"github.com/jannfis/argocd-agent/pkg/types"
)

func WithAllowedNamespaces(namespaces ...string) AgentOption {
	return func(a *Agent) error {
		a.allowedNamespaces = namespaces
		return nil
	}
}

func WithRemote(remote *client.Remote) AgentOption {
	return func(a *Agent) error {
		a.remote = remote
		return nil
	}
}

func WithMode(mode string) AgentOption {
	return func(a *Agent) error {
		switch mode {
		case "autonomous":
			a.mode = types.AgentModeAutonomous
		case "managed":
			a.mode = types.AgentModeManaged
		default:
			a.mode = types.AgentModeNone
			return fmt.Errorf("unknown agent mode: %s. Must be one of: managed,autonomous", mode)
		}
		return nil
	}
}
