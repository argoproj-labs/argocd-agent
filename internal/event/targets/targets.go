// Copyright 2026 The argocd-agent Authors
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

// Package targets defines CloudEvent target constants used across ArgoCD Agent packages.
// This package is intentionally dependency-free to avoid import cycles between
// internal/event and internal/logging.
package targets

type EventTarget string

func (t EventTarget) String() string {
	return string(t)
}

const TypePrefix = "io.argoproj.argocd-agent.event"

const (
	Unknown                EventTarget = "unknown"
	Application            EventTarget = "application"
	AppProject             EventTarget = "appproject"
	EventAck               EventTarget = "eventProcessed"
	Resource               EventTarget = "resource"
	Redis                  EventTarget = "redis"
	ResourceResync         EventTarget = "resourceResync"
	ClusterCacheInfoUpdate EventTarget = "clusterCacheInfoUpdate"
	Repository             EventTarget = "repository"
	GPGKey                 EventTarget = "gpgkey"
	ContainerLog           EventTarget = "containerlog"
	Heartbeat              EventTarget = "heartbeat"
	Terminal               EventTarget = "terminal"
	ApplicationSet         EventTarget = "applicationset"
)
