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

package agent

import (
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/pkg/client"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"
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
			a.mode = types.AgentModeUnknown
			return fmt.Errorf("unknown agent mode: %s. Must be one of: managed,autonomous", mode)
		}
		return nil
	}
}

func WithMetricsPort(port int) AgentOption {
	return func(o *Agent) error {
		if port > 0 && port < 32768 {
			o.options.metricsPort = port
			return nil
		} else {
			return fmt.Errorf("invalid port: %d", port)
		}
	}
}

func WithHealthzPort(port int) AgentOption {
	return func(o *Agent) error {
		if port > 0 && port < 32768 {
			o.options.healthzPort = port
			return nil
		} else {
			return fmt.Errorf("invalid port: %d", port)
		}
	}
}

func WithRedisHost(host string) AgentOption {
	return func(o *Agent) error {
		o.redisProxyMsgHandler.redisAddress = host
		return nil
	}
}

func WithRedisUsername(username string) AgentOption {
	return func(o *Agent) error {
		o.redisProxyMsgHandler.redisUsername = username
		return nil
	}
}

func WithRedisPassword(password string) AgentOption {
	return func(o *Agent) error {
		o.redisProxyMsgHandler.redisPassword = password
		return nil
	}
}

func WithEnableResourceProxy(enable bool) AgentOption {
	return func(o *Agent) error {
		o.enableResourceProxy = enable
		return nil
	}
}

func WithCacheRefreshInterval(interval time.Duration) AgentOption {
	return func(o *Agent) error {
		o.cacheRefreshInterval = interval
		return nil
	}
}

func WithHeartbeatInterval(interval time.Duration) AgentOption {
	return func(o *Agent) error {
		o.options.heartbeatInterval = interval
		return nil
	}
}

func WithSubsystemLoggers(resourceProxy, redisProxy, grpcEvent *logrus.Logger) AgentOption {
	return func(o *Agent) error {
		if resourceProxy != nil {
			o.resourceProxyLogger = logging.New(resourceProxy)
		}

		if redisProxy != nil {
			o.redisProxyLogger = logging.New(redisProxy)
		}

		if grpcEvent != nil {
			o.grpcEventLogger = logging.New(grpcEvent)
		}
		return nil
	}
}

// WithCreateNamespace configures the agent to automatically create
// namespaces if they don't exist when syncing applications. This is used in
// combination with destination-based mapping to ensure that target namespaces
// exist before creating applications.
func WithCreateNamespace(enabled bool) AgentOption {
	return func(o *Agent) error {
		o.createNamespace = enabled
		return nil
	}
}

// WithDestinationBasedMapping enables destination-based mapping mode on the agent.
// When enabled, the agent will:
// - Preserve the source namespace of applications (sync to the same namespace as on the principal)
// - Watch for applications in all namespaces (not just the agent's namespace)
// This should be enabled when the principal also has destination-based mapping enabled.
func WithDestinationBasedMapping(enabled bool) AgentOption {
	return func(o *Agent) error {
		o.destinationBasedMapping = enabled
		return nil
	}
}
