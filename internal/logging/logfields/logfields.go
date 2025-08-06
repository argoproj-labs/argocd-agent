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

// Package logfields defines structured logging field names used across ArgoCD Agent packages.
// This package provides centralized constants for consistent logging field naming.
//
// All logging code MUST use these constants instead of string literals for field names.
// This ensures consistency and prevents typos in field names across the codebase.
package logfields

const (
	// Core component identifiers
	Module    = "module"
	Component = "component"
	Subsystem = "subsys"

	// Request/Response tracking
	Method         = "method"
	UUID           = "uuid"
	RequestID      = "requestId"
	ConnectionUUID = "connectionUUID"
	EventID        = "eventId"
	Event          = "event"

	// Client and agent
	Client = "client"
	Agent  = "agent"

	// Networking
	Direction   = "direction"
	ClientAddr  = "clientAddr"
	ServerAddr  = "serverAddr"
	RemoteAddr  = "remoteAddr"
	LocalAddr   = "localAddr"
	ChannelName = "channelName"

	// ArgoCD specific
	Application   = "application"
	AppProject    = "appProject"
	Cluster       = "cluster"
	ClusterName   = "clusterName"
	ClusterServer = "clusterServer"
	Repository    = "repository"
	Namespace     = "namespace"

	// Kubernetes resources
	Kind               = "kind"
	Name               = "name"
	UID                = "uid"
	ResourceVersion    = "resourceVersion"
	NewResourceVersion = "newResourceVersion"
	OldResourceVersion = "oldResourceVersion"

	// Queue operations
	SendQueueLen  = "sendq_len"
	SendQueueName = "sendq_name"
	QueueSize     = "queueSize"

	// Redis operations
	RedisKey     = "redisKey"
	RedisValue   = "redisValue"
	RedisCommand = "redisCommand"
	RedisChannel = "redisChannel"

	// Time and duration
	Duration  = "duration"
	Timeout   = "timeout"
	Interval  = "interval"
	LastPing  = "lastPing"
	StartTime = "startTime"
	EndTime   = "endTime"

	// Authentication & Authorization
	Username   = "username"
	UserAgent  = "userAgent"
	Token      = "token"
	AuthMethod = "authMethod"

	// gRPC operations
	GRPCMethod  = "grpcMethod"
	GRPCCode    = "grpcCode"
	GRPCMessage = "grpcMessage"

	// File operations
	FilePath = "filePath"
	FileName = "fileName"
	FileSize = "fileSize"

	// Error handling
	Error        = "error"
	ErrorCode    = "errorCode"
	ErrorMessage = "errorMessage"
	ErrorType    = "errorType"

	// HTTP operations
	HTTPMethod  = "httpMethod"
	HTTPStatus  = "httpStatus"
	HTTPPath    = "httpPath"
	HTTPHeaders = "httpHeaders"

	// TLS/Security
	TLSVersion  = "tlsVersion"
	CertSubject = "certSubject"
	CertIssuer  = "certIssuer"
	CertExpiry  = "certExpiry"

	// Performance metrics
	CPU          = "cpu"
	Memory       = "memory"
	RequestCount = "requestCount"
	ResponseTime = "responseTime"

	// Miscellaneous
	Version   = "version"
	BuildInfo = "buildInfo"
	Config    = "config"
	Options   = "options"
	State     = "state"
	Status    = "status"
	Reason    = "reason"
	Message   = "message"
	Count     = "count"
	Size      = "size"
	Index     = "index"
	Offset    = "offset"
	Limit     = "limit"
	Total     = "total"
	Available = "available"
	Used      = "used"
	Remaining = "remaining"
)
