// Copyright 2025 The argocd-agent Authors
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

package tracing

import (
	"go.opentelemetry.io/otel/attribute"
)

// Common attribute keys for argocd-agent tracing
const (
	// AgentName is the name of the agent
	AttrAgentName = attribute.Key("argocd.agent.name")

	// AgentMode is the mode of the agent (autonomous or managed)
	AttrAgentMode = attribute.Key("argocd.agent.mode")

	// ComponentType indicates whether this is principal or agent
	AttrComponentType = attribute.Key("argocd.component.type")

	// EventID is the unique identifier for an event
	AttrEventID = attribute.Key("argocd.event.id")

	// EventTarget is the target of the event being processed
	AttrEventTarget = attribute.Key("argocd.event.target")

	// EventType is the type of event being processed
	AttrEventType = attribute.Key("argocd.event.type")

	// OperationType is the type of operation (create, update, delete, etc.)
	AttrOperationType = attribute.Key("argocd.operation.type")

	// ResourceKind is the Kubernetes resource kind
	AttrResourceKind = attribute.Key("k8s.resource.kind")

	// ResourceName is the Kubernetes resource name
	AttrResourceName = attribute.Key("k8s.resource.name")

	// Namespace is the Kubernetes resource namespace
	AttrNamespace = attribute.Key("k8s.resource.namespace")

	// ResourceUID is the Kubernetes resource UID
	AttrResourceUID = attribute.Key("k8s.resource.uid")
)
