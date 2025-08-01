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

// Package logging provides centralized logging functionality for ArgoCD Agent.
// This package replaces the per-package log() functions with a centralized
// logging system that uses structured field constants.
//
// All components should use this package instead of creating their own
// log() functions to ensure consistency across the codebase.
package logging

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
)

// LogLevel represents the different log levels
type LogLevel string

const (
	LogLevelTrace LogLevel = "trace"
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
	LogLevelFatal LogLevel = "fatal"
	LogLevelPanic LogLevel = "panic"
)

// LogFormat represents the different log output formats
type LogFormat string

const (
	LogFormatText LogFormat = "text"
	LogFormatJSON LogFormat = "json"
)

// defaultLogger is the global logger instance
var defaultLogger *logrus.Logger

func init() {
	// Initialize the default logger with sensible defaults
	defaultLogger = logrus.New()
	defaultLogger.SetLevel(logrus.InfoLevel)
	defaultLogger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	defaultLogger.SetOutput(os.Stdout)
}

// SetupLogging configures the global logger with the provided options
func SetupLogging(level LogLevel, format LogFormat, output io.Writer) error {
	// Set log level
	logrusLevel, err := parseLogLevel(level)
	if err != nil {
		return fmt.Errorf("invalid log level %q: %w", level, err)
	}
	defaultLogger.SetLevel(logrusLevel)

	// Set log format
	switch format {
	case LogFormatJSON:
		defaultLogger.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		})
	case LogFormatText:
		defaultLogger.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		})
	default:
		return fmt.Errorf("unsupported log format: %s", format)
	}

	// Set output
	if output != nil {
		defaultLogger.SetOutput(output)
	}

	return nil
}

// parseLogLevel converts a string log level to logrus.Level
func parseLogLevel(level LogLevel) (logrus.Level, error) {
	switch strings.ToLower(string(level)) {
	case "trace":
		return logrus.TraceLevel, nil
	case "debug":
		return logrus.DebugLevel, nil
	case "info":
		return logrus.InfoLevel, nil
	case "warn", "warning":
		return logrus.WarnLevel, nil
	case "error":
		return logrus.ErrorLevel, nil
	case "fatal":
		return logrus.FatalLevel, nil
	case "panic":
		return logrus.PanicLevel, nil
	default:
		return logrus.InfoLevel, fmt.Errorf("unknown log level: %s", level)
	}
}

// GetDefaultLogger returns the global logger instance
func GetDefaultLogger() *logrus.Logger {
	return defaultLogger
}

// ComponentLogger creates a logger for a specific component with the component field pre-set
func ComponentLogger(component string) *logrus.Entry {
	return defaultLogger.WithField(logfields.Component, component)
}

// ModuleLogger creates a logger for a specific module with the module field pre-set
func ModuleLogger(module string) *logrus.Entry {
	return defaultLogger.WithField(logfields.Module, module)
}

// SubsystemLogger creates a logger for a specific subsystem with the subsystem field pre-set
func SubsystemLogger(subsystem string) *logrus.Entry {
	return defaultLogger.WithField(logfields.Subsystem, subsystem)
}

// MethodLogger creates a logger with method context
func MethodLogger(module, method string) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module: module,
		logfields.Method: method,
	})
}

// RequestLogger creates a logger with request context
func RequestLogger(module, method, requestID string) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:    module,
		logfields.Method:    method,
		logfields.RequestID: requestID,
	})
}

// ConnectionLogger creates a logger with connection context
func ConnectionLogger(module, connectionUUID, clientAddr string) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:         module,
		logfields.ConnectionUUID: connectionUUID,
		logfields.ClientAddr:     clientAddr,
	})
}

// EventLogger creates a logger with event context
func EventLogger(module, event, eventID string) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:  module,
		logfields.Event:   event,
		logfields.EventID: eventID,
	})
}

// KubernetesResourceLogger creates a logger with Kubernetes resource context
func KubernetesResourceLogger(module, kind, namespace, name string) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:    module,
		logfields.Kind:      kind,
		logfields.Namespace: namespace,
		logfields.Name:      name,
	})
}

// ApplicationLogger creates a logger with ArgoCD application context
func ApplicationLogger(module, application string) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:      module,
		logfields.Application: application,
	})
}

// RedisLogger creates a logger with Redis operation context
func RedisLogger(module, command, key string) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:       module,
		logfields.RedisCommand: command,
		logfields.RedisKey:     key,
	})
}

// GRPCLogger creates a logger with gRPC context
func GRPCLogger(module, method string) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:     module,
		logfields.GRPCMethod: method,
	})
}

// HTTPLogger creates a logger with HTTP context
func HTTPLogger(module, method, path string) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:     module,
		logfields.HTTPMethod: method,
		logfields.HTTPPath:   path,
	})
}

// ErrorLogger creates a logger with error context
func ErrorLogger(module, errorType string, err error) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:    module,
		logfields.ErrorType: errorType,
		logfields.Error:     err.Error(),
	})
}

// PerformanceLogger creates a logger with performance metrics context
func PerformanceLogger(module string, duration int64, requestCount int) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:       module,
		logfields.Duration:     duration,
		logfields.RequestCount: requestCount,
	})
}

// QueueLogger creates a logger with queue context
func QueueLogger(module, queueName string, queueSize int) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:        module,
		logfields.SendQueueName: queueName,
		logfields.SendQueueLen:  queueSize,
	})
}

// ClusterLogger creates a logger with cluster context
func ClusterLogger(module, clusterName, clusterServer string) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:        module,
		logfields.ClusterName:   clusterName,
		logfields.ClusterServer: clusterServer,
	})
}

// AuthLogger creates a logger with authentication context
func AuthLogger(module, username, authMethod string) *logrus.Entry {
	return defaultLogger.WithFields(logrus.Fields{
		logfields.Module:     module,
		logfields.Username:   username,
		logfields.AuthMethod: authMethod,
	})
}

// SetLogLevel dynamically changes the log level
func SetLogLevel(level LogLevel) error {
	logrusLevel, err := parseLogLevel(level)
	if err != nil {
		return err
	}
	defaultLogger.SetLevel(logrusLevel)
	return nil
}

// GetLogLevel returns the current log level
func GetLogLevel() logrus.Level {
	return defaultLogger.GetLevel()
}

// WithContext creates a logger entry with common context fields
func WithContext(module string, fields map[string]interface{}) *logrus.Entry {
	logFields := logrus.Fields{logfields.Module: module}
	for k, v := range fields {
		logFields[k] = v
	}
	return defaultLogger.WithFields(logFields)
}

// Trace logs a message at trace level with module context
func Trace(module, message string) {
	ModuleLogger(module).Trace(message)
}

// Debug logs a message at debug level with module context
func Debug(module, message string) {
	ModuleLogger(module).Debug(message)
}

// Info logs a message at info level with module context
func Info(module, message string) {
	ModuleLogger(module).Info(message)
}

// Warn logs a message at warn level with module context
func Warn(module, message string) {
	ModuleLogger(module).Warn(message)
}

// Error logs a message at error level with module context
func Error(module, message string) {
	ModuleLogger(module).Error(message)
}

// Fatal logs a message at fatal level with module context and exits
func Fatal(module, message string) {
	ModuleLogger(module).Fatal(message)
}

// Panic logs a message at panic level with module context and panics
func Panic(module, message string) {
	ModuleLogger(module).Panic(message)
}
