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

// Package examples demonstrates how to migrate existing logging code
// to use the centralized logging package.
package examples

import (
	"errors"

	"github.com/sirupsen/logrus"

	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
)

// Example 1: Replace package-level log() function
// BEFORE:
//
//	func log() *logrus.Entry {
//	    return logrus.WithField("module", "Connector")
//	}
//
// AFTER:
func log() *logrus.Entry {
	return logging.ModuleLogger("Connector")
}

// Example 2: Replace manual field creation with constants
// BEFORE:
//
//	logCtx := log().WithFields(logrus.Fields{
//	    "method":         "processIncomingRedisRequest",
//	    "uuid":           rreq.UUID,
//	    "connectionUUID": rreq.ConnectionUUID,
//	})
//
// AFTER:
func ExampleStructuredLogging(uuid, connectionUUID string) *logrus.Entry {
	return log().WithFields(logrus.Fields{
		logfields.Method:         "processIncomingRedisRequest",
		logfields.UUID:           uuid,
		logfields.ConnectionUUID: connectionUUID,
	})
}

// Example 3: Using specialized logger functions
func ExampleSpecializedLoggers() {
	// For request handling
	requestLogger := logging.RequestLogger("AgentModule", "HandleRequest", "req-123")
	requestLogger.Info("Processing request")

	// For Kubernetes resources
	k8sLogger := logging.KubernetesResourceLogger("AgentModule", "Pod", "default", "my-pod")
	k8sLogger.Info("Processing Kubernetes resource")

	// For Redis operations
	redisLogger := logging.RedisLogger("AgentModule", "GET", "app:config")
	redisLogger.Debug("Redis operation completed")

	// For gRPC calls
	grpcLogger := logging.GRPCLogger("AgentModule", "/argocd.agent.v1.AgentService/Subscribe")
	grpcLogger.Info("gRPC call started")

	// For HTTP operations
	httpLogger := logging.HTTPLogger("AgentModule", "POST", "/api/v1/applications")
	httpLogger.Info("HTTP request received")

	// For error handling
	err := errors.New("connection failed")
	errorLogger := logging.ErrorLogger("AgentModule", "ConnectionError", err)
	errorLogger.Error("Connection error occurred")

	// For authentication
	authLogger := logging.AuthLogger("AgentModule", "admin", "bearer-token")
	authLogger.Info("User authenticated")

	// For cluster operations
	clusterLogger := logging.ClusterLogger("AgentModule", "prod-cluster", "https://k8s.example.com")
	clusterLogger.Info("Cluster operation completed")

	// For queue operations
	queueLogger := logging.QueueLogger("AgentModule", "event-queue", 42)
	queueLogger.Debug("Queue operation")
}

// Example 4: Migrating complex logging scenarios
func ExampleComplexLoggingScenario(appName, namespace, eventType string) {
	// BEFORE: Multiple WithField calls with string literals
	// logCtx := logrus.WithField("module", "ApplicationManager").
	//     WithField("application", appName).
	//     WithField("namespace", namespace).
	//     WithField("event", eventType)

	// AFTER: Using structured fields and specialized logger
	logCtx := logging.ApplicationLogger("ApplicationManager", appName).
		WithFields(logrus.Fields{
			logfields.Namespace: namespace,
			logfields.Event:     eventType,
		})

	logCtx.Info("Application event processed")
}

// Example 5: Setup logging in main function
func ExampleSetupLogging() {
	// Configure logging at application startup
	err := logging.SetupLogging(
		logging.LogLevelInfo,  // Set log level
		logging.LogFormatJSON, // Use JSON format for production
		nil,                   // Use default output (stdout)
	)
	if err != nil {
		// Handle error
		panic(err)
	}

	// Get logger for the main component
	mainLogger := logging.ComponentLogger("Main")
	mainLogger.Info("Application started")
}

// Example 6: Performance logging
func ExamplePerformanceLogging() {
	// Log performance metrics
	performanceLogger := logging.PerformanceLogger("APIHandler", 150, 100)
	performanceLogger.WithFields(logrus.Fields{
		logfields.ResponseTime: "150ms",
		logfields.Status:       "success",
	}).Info("API request completed")
}

// Example 7: Using field constants for consistency
func ExampleFieldConstants() {
	logger := logging.ModuleLogger("ExampleModule")

	// BEFORE: String literals (inconsistent, error-prone)
	// logger.WithField("user_name", "john").Info("User action")
	// logger.WithField("username", "jane").Info("Another user action")  // Inconsistent field name!

	// AFTER: Using constants (consistent, safe)
	logger.WithField(logfields.Username, "john").Info("User action")
	logger.WithField(logfields.Username, "jane").Info("Another user action")
}
