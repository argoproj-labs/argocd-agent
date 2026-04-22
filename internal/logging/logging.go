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
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
)

// CentralizedLogger encapsulates a logrus logger and provides defaults to create logs with
type CentralizedLogger struct {
	logger *logrus.Logger
}

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

// defaultLogger is a CentralizedLogger for the global logger instance
var defaultLogger *CentralizedLogger

// FullDetailConfig controls whether verbose resource content (.spec/.status/diffs)
// is included in log messages, independently of log level.
type FullDetailConfig struct {
	Actions   bool
	Events    bool
	Informers bool
}

var fullDetailConfig FullDetailConfig

// SetFullDetailConfig sets the global full detail logging configuration.
func SetFullDetailConfig(cfg FullDetailConfig) {
	fullDetailConfig = cfg
}

// GetFullDetailConfig returns the current global full detail logging configuration.
func GetFullDetailConfig() FullDetailConfig {
	return fullDetailConfig
}

func init() {
	// Use the process-wide standard logger so that CLI flags and InitLogging() apply here too
	defaultLogger = &CentralizedLogger{
		logger: logrus.StandardLogger(),
	}
}

func New(logger *logrus.Logger) *CentralizedLogger {
	if logger == nil {
		logger = logrus.New()
	}

	return &CentralizedLogger{
		logger: logger,
	}
}

// SetupLogging configures the global logger with the provided options
func (l *CentralizedLogger) SetupLogging(level LogLevel, format LogFormat, output io.Writer) error {
	// Set log level
	logrusLevel, err := parseLogLevel(level)
	if err != nil {
		return fmt.Errorf("invalid log level %q: %w", level, err)
	}
	l.logger.SetLevel(logrusLevel)

	// Set log format
	switch format {
	case LogFormatJSON:
		l.logger.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		})
	case LogFormatText:
		l.logger.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		})
	default:
		return fmt.Errorf("unsupported log format: %s", format)
	}

	// Set output
	if output != nil {
		l.logger.SetOutput(output)
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
func GetDefaultLogger() *CentralizedLogger {
	return defaultLogger
}

// ComponentLogger creates a logger for a specific component with the component field pre-set
func (l *CentralizedLogger) ComponentLogger(component string) *logrus.Entry {
	return l.logger.WithField(logfields.Component, component)
}

// ModuleLogger creates a logger for a specific module with the module field pre-set
func (l *CentralizedLogger) ModuleLogger(module string) *logrus.Entry {
	return l.logger.WithField(logfields.Module, module)
}

// SubsystemLogger creates a logger for a specific subsystem with the subsystem field pre-set
func (l *CentralizedLogger) SubsystemLogger(subsystem string) *logrus.Entry {
	return l.logger.WithField(logfields.Subsystem, subsystem)
}

// MethodLogger creates a logger with method context
func (l *CentralizedLogger) MethodLogger(module, method string) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module: module,
		logfields.Method: method,
	})
}

// RequestLogger creates a logger with request context
func (l *CentralizedLogger) RequestLogger(module, method, requestID string) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module:    module,
		logfields.Method:    method,
		logfields.RequestID: requestID,
	})
}

// ConnectionLogger creates a logger with connection context
func (l *CentralizedLogger) ConnectionLogger(module, connectionUUID, clientAddr string) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module:         module,
		logfields.ConnectionUUID: connectionUUID,
		logfields.ClientAddr:     clientAddr,
	})
}

// EventLogger creates a logger with event context
func (l *CentralizedLogger) EventLogger(module, event, eventID string) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module:  module,
		logfields.Event:   event,
		logfields.EventID: eventID,
	})
}

// KubernetesResourceLogger creates a logger with Kubernetes resource context
func (l *CentralizedLogger) KubernetesResourceLogger(module, kind, namespace, name string) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module:    module,
		logfields.Kind:      kind,
		logfields.Namespace: namespace,
		logfields.Name:      name,
	})
}

// ApplicationLogger creates a logger with ArgoCD application context
func (l *CentralizedLogger) ApplicationLogger(module, application string) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module:      module,
		logfields.Application: application,
	})
}

// RedisLogger creates a logger with Redis operation context
func (l *CentralizedLogger) RedisLogger(module, command, key string) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module:       module,
		logfields.RedisCommand: command,
		logfields.RedisKey:     key,
	})
}

// GRPCLogger creates a logger with gRPC context
func (l *CentralizedLogger) GRPCLogger(module, method string) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module:     module,
		logfields.GRPCMethod: method,
	})
}

// HTTPLogger creates a logger with HTTP context
func (l *CentralizedLogger) HTTPLogger(module, method, path string) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module:     module,
		logfields.HTTPMethod: method,
		logfields.HTTPPath:   path,
	})
}

// ErrorLogger creates a logger with error context
func (l *CentralizedLogger) ErrorLogger(module, errorType string, err error) *logrus.Entry {
	fields := logrus.Fields{
		logfields.Module:    module,
		logfields.ErrorType: errorType,
	}
	if err != nil {
		fields[logfields.Error] = err.Error()
	}
	return l.logger.WithFields(fields)
}

// PerformanceLogger creates a logger with performance metrics context
func (l *CentralizedLogger) PerformanceLogger(module string, duration int64, requestCount int) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module:       module,
		logfields.Duration:     duration,
		logfields.RequestCount: requestCount,
	})
}

// QueueLogger creates a logger with queue context
func (l *CentralizedLogger) QueueLogger(module, queueName string, queueSize int) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module:        module,
		logfields.SendQueueName: queueName,
		logfields.SendQueueLen:  queueSize,
	})
}

// ClusterLogger creates a logger with cluster context
func (l *CentralizedLogger) ClusterLogger(module, clusterName, clusterServer string) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module:        module,
		logfields.ClusterName:   clusterName,
		logfields.ClusterServer: clusterServer,
	})
}

// AuthLogger creates a logger with authentication context
func (l *CentralizedLogger) AuthLogger(module, username, authMethod string) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields{
		logfields.Module:     module,
		logfields.Username:   username,
		logfields.AuthMethod: authMethod,
	})
}

// SetLogLevel dynamically changes the log level
func (l *CentralizedLogger) SetLogLevel(level LogLevel) error {
	logrusLevel, err := parseLogLevel(level)
	if err != nil {
		return err
	}
	l.logger.SetLevel(logrusLevel)
	return nil
}

// GetLogLevel returns the current log level
func (l *CentralizedLogger) GetLogLevel() logrus.Level {
	return l.logger.GetLevel()
}

// WithContext creates a logger entry with common context fields
func (l *CentralizedLogger) WithContext(module string, fields map[string]interface{}) *logrus.Entry {
	logFields := logrus.Fields{logfields.Module: module}
	for k, v := range fields {
		logFields[k] = v
	}
	return l.logger.WithFields(logFields)
}

// Trace logs a message at trace level with module context
func (l *CentralizedLogger) Trace(module, message string) {
	l.ModuleLogger(module).Trace(message)
}

// Debug logs a message at debug level with module context
func (l *CentralizedLogger) Debug(module, message string) {
	l.ModuleLogger(module).Debug(message)
}

// Info logs a message at info level with module context
func (l *CentralizedLogger) Info(module, message string) {
	l.ModuleLogger(module).Info(message)
}

// Warn logs a message at warn level with module context
func (l *CentralizedLogger) Warn(module, message string) {
	l.ModuleLogger(module).Warn(message)
}

// Error logs a message at error level with module context
func (l *CentralizedLogger) Error(module, message string) {
	l.ModuleLogger(module).Error(message)
}

// Fatal logs a message at fatal level with module context and exits
func (l *CentralizedLogger) Fatal(module, message string) {
	l.ModuleLogger(module).Fatal(message)
}

// Panic logs a message at panic level with module context and panics
func (l *CentralizedLogger) Panic(module, message string) {
	l.ModuleLogger(module).Panic(message)
}

// SelectLogger checks to see if a centralized logger is nil and if it is
// returns the default logger
func SelectLogger(l *CentralizedLogger) *CentralizedLogger {
	if l == nil {
		return GetDefaultLogger()
	}
	return l
}

// LogActionCreate logs a resource creation
func LogActionCreate(logCtx *logrus.Entry, resourceType string, obj any) {
	name, namespace := resourceMeta(obj)

	logCtx = logCtx.WithFields(logrus.Fields{
		logfields.LogCategory:  "actions",
		logfields.Action:       "create",
		logfields.ResourceType: resourceType,
		logfields.Name:         name,
		logfields.Namespace:    namespace,
	})

	// Include the full resource in the log if full detail is enabled
	if fullDetailConfig.Actions {
		logCtx = logCtx.WithField(logfields.Detail, marshalResource(obj))
	}

	logCtx.Infof("Created %s %s/%s", resourceType, namespace, name)
}

// LogActionUpdate logs a resource update
func LogActionUpdate(logCtx *logrus.Entry, resourceType string, oldObj, newObj any) {
	name, namespace := resourceMeta(newObj)

	logCtx = logCtx.WithFields(logrus.Fields{
		logfields.LogCategory:  "actions",
		logfields.Action:       "update",
		logfields.ResourceType: resourceType,
		logfields.Name:         name,
		logfields.Namespace:    namespace,
	})

	// Include the diff in the log if full detail is enabled
	if fullDetailConfig.Actions && oldObj != nil && newObj != nil && !isSecret(newObj) {
		// Only include the diff if it is non-empty
		if diff := cmp.Diff(oldObj, newObj); diff != "" {
			logCtx = logCtx.WithField(logfields.Detail, diff)
		}
	}

	logCtx.Infof("Updated %s %s/%s", resourceType, namespace, name)
}

// LogActionDelete logs a resource deletion.
func LogActionDelete(logCtx *logrus.Entry, resourceType, namespace, name string) {
	logCtx = logCtx.WithFields(logrus.Fields{
		logfields.LogCategory:  "actions",
		logfields.Action:       "delete",
		logfields.ResourceType: resourceType,
		logfields.Name:         name,
		logfields.Namespace:    namespace,
	})

	logCtx.Infof("Deleted %s %s/%s", resourceType, namespace, name)
}

// LogActionError logs resource action error. When full detail is enabled,
// the incoming resource payload is included in logs.
func LogActionError(logCtx *logrus.Entry, resourceType, action string, obj any, err error) {
	name, namespace := resourceMeta(obj)

	logCtx = logCtx.WithFields(logrus.Fields{
		logfields.LogCategory:  "actions",
		logfields.Action:       action,
		logfields.ResourceType: resourceType,
		logfields.Name:         name,
		logfields.Namespace:    namespace,
	})

	// Include the full resource in the log if full detail is enabled
	if fullDetailConfig.Actions {
		logCtx = logCtx.WithField(logfields.Detail, marshalResource(obj))
	}
	logCtx.WithError(err).Errorf("Error performing action %s on %s %s/%s", action, resourceType, namespace, name)
}

// LogEventSent logs an outbound event
func LogEventSent(logCtx *logrus.Entry, ev *cloudevents.Event) {

	// Skip meta events (ACKs, heartbeats, periodic cache updates), they do not need verbose logging
	// and are handled by the agent and principal respectively
	if isMetaEvent(ev) {
		return
	}

	target := ev.DataSchema()
	action := shortType(ev.Type())

	logCtx = logCtx.WithFields(logrus.Fields{
		logfields.LogCategory: "events",
		logfields.Direction:   "send",
		logfields.EventTarget: target,
		logfields.EventType:   ev.Type(),
	})

	logCtx = parseEventSubject(logCtx, ev)

	// Include the event data in the log if full detail is enabled and event data is not empty
	if fullDetailConfig.Events && hasEventData(ev.Data()) {
		logCtx = logCtx.WithField(logfields.Detail, string(ev.Data()))
	}
	logCtx.Infof("Event sent: %s %s", target, action)
}

// LogEventReceived logs an inbound event
func LogEventReceived(logCtx *logrus.Entry, ev *cloudevents.Event) {
	// Skip meta events
	if isMetaEvent(ev) {
		return
	}

	target := ev.DataSchema()
	action := shortType(ev.Type())

	logCtx = logCtx.WithFields(logrus.Fields{
		logfields.LogCategory: "events",
		logfields.Direction:   "recv",
		logfields.EventTarget: target,
		logfields.EventType:   ev.Type(),
	})

	logCtx = parseEventSubject(logCtx, ev)

	// Include the event data in the log if full detail is enabled and event data is not empty
	if fullDetailConfig.Events && hasEventData(ev.Data()) {
		logCtx = logCtx.WithField(logfields.Detail, string(ev.Data()))
	}
	logCtx.Infof("Event received: %s %s", target, action)
}

// LogEventError logs event error
func LogEventError(logCtx *logrus.Entry, ev *cloudevents.Event, err error) {
	target := ev.DataSchema()
	action := shortType(ev.Type())

	logCtx = logCtx.WithFields(logrus.Fields{
		logfields.LogCategory: "events",
		logfields.EventTarget: target,
		logfields.EventType:   ev.Type(),
	})
	logCtx = parseEventSubject(logCtx, ev)

	// Include the event data in the log if full detail is enabled and event data is not empty
	if fullDetailConfig.Events && hasEventData(ev.Data()) {
		logCtx = logCtx.WithField(logfields.Detail, string(ev.Data()))
	}
	logCtx.WithError(err).Errorf("Error processing event: %s %s", target, action)
}

// LogInformerAdd logs a K8s informer add event
func LogInformerAdd(logCtx *logrus.Entry, obj any) {
	name, namespace := resourceMeta(obj)
	resType := resourceType(obj)

	logCtx = logCtx.WithFields(logrus.Fields{
		logfields.LogCategory:  "informers",
		logfields.Action:       "create",
		logfields.ResourceType: resType,
		logfields.Name:         name,
		logfields.Namespace:    namespace,
	})

	if fullDetailConfig.Informers {
		logCtx = logCtx.WithField(logfields.Detail, marshalResource(obj))
	}
	logCtx.Infof("Informer add: %s %s/%s", resType, namespace, name)
}

// LogInformerUpdate logs a K8s informer update event
func LogInformerUpdate(logCtx *logrus.Entry, oldObj, newObj any) {
	name, namespace := resourceMeta(newObj)
	resType := resourceType(newObj)

	logCtx = logCtx.WithFields(logrus.Fields{
		logfields.LogCategory:  "informers",
		logfields.Action:       "update",
		logfields.ResourceType: resType,
		logfields.Name:         name,
		logfields.Namespace:    namespace,
	})

	// Include the diff in the log if full detail is enabled
	// but not for secret as they are confidential
	if fullDetailConfig.Informers && !isSecret(newObj) {
		if diff := cmp.Diff(oldObj, newObj); diff != "" {
			logCtx = logCtx.WithField(logfields.Detail, diff)
		}
	}
	logCtx.Infof("Informer update: %s %s/%s", resType, namespace, name)
}

// LogInformerDelete logs a K8s informer delete event
func LogInformerDelete(logCtx *logrus.Entry, obj any) {
	name, namespace := resourceMeta(obj)
	resType := resourceType(obj)

	logCtx = logCtx.WithFields(logrus.Fields{
		logfields.LogCategory:  "informers",
		logfields.Action:       "delete",
		logfields.ResourceType: resType,
		logfields.Name:         name,
		logfields.Namespace:    namespace,
	})

	logCtx.Infof("Informer delete: %s %s/%s", resType, namespace, name)
}

// parseEventSubject is to parse and add name and namespace fields from the CloudEvent Subject
func parseEventSubject(logCtx *logrus.Entry, ev *cloudevents.Event) *logrus.Entry {
	if ns, name, ok := strings.Cut(ev.Subject(), "/"); ok && name != "" {
		return logCtx.WithFields(logrus.Fields{
			logfields.Name:      name,
			logfields.Namespace: ns,
		})
	}
	return logCtx
}

// shortType strips the CloudEvent type prefix, returning just the action part
func shortType(evType string) string {
	const prefix = "io.argoproj.argocd-agent.event."
	if strings.HasPrefix(evType, prefix) {
		return evType[len(prefix):]
	}
	return evType
}

// isMetaEvent checks if the event is a meta event
func isMetaEvent(ev *cloudevents.Event) bool {
	target := ev.DataSchema()
	return target == "eventProcessed" ||
		target == "heartbeat" ||
		target == "clusterCacheInfoUpdate"
}

// hasEventData checks if the event data is not empty
func hasEventData(data []byte) bool {
	s := strings.TrimSpace(string(data))
	return len(s) > 0 && s != "{}" && s != "null"
}

// resourceMeta extracts name and namespace from a K8s runtime object.
func resourceMeta(obj any) (name, namespace string) {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return "<unknown>", "<unknown>"
	}
	return accessor.GetName(), accessor.GetNamespace()
}

// resourceType returns the lowercase type name of a K8s runtime object (e.g. "configmap", "application").
func resourceType(obj any) string {
	return strings.ToLower(reflect.TypeOf(obj).Elem().Name())
}

func isSecret(obj any) bool {
	_, ok := obj.(*corev1.Secret)
	return ok
}

// marshalResource marshals a resource to JSON string
func marshalResource(obj any) string {
	// Secrets are confidentials and should not log data
	if isSecret(obj) {
		return "<redacted: Secret>"
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return fmt.Sprintf("<error marshalling resource: %v>", err)
	}
	return string(data)
}
