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

func (m AgentMode) IsAutonomous() bool {
	return m == AgentModeAutonomous
}

func (m AgentMode) IsManaged() bool {
	return m == AgentModeManaged
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

const (
	ContextAgentIdentifier EventContextKey = "agent_name"
	ContextAgentMode       EventContextKey = "agent_mode"
)
