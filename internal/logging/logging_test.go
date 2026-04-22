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

package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
)

func TestSetupLogging(t *testing.T) {
	tests := []struct {
		name        string
		level       LogLevel
		format      LogFormat
		expectError bool
	}{
		{
			name:        "valid text format with info level",
			level:       LogLevelInfo,
			format:      LogFormatText,
			expectError: false,
		},
		{
			name:        "valid json format with debug level",
			level:       LogLevelDebug,
			format:      LogFormatJSON,
			expectError: false,
		},
		{
			name:        "invalid log level",
			level:       LogLevel("invalid"),
			format:      LogFormatText,
			expectError: true,
		},
		{
			name:        "invalid format",
			level:       LogLevelInfo,
			format:      LogFormat("invalid"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := defaultLogger.SetupLogging(tt.level, tt.format, &buf)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    LogLevel
		expected logrus.Level
		hasError bool
	}{
		{LogLevelTrace, logrus.TraceLevel, false},
		{LogLevelDebug, logrus.DebugLevel, false},
		{LogLevelInfo, logrus.InfoLevel, false},
		{LogLevelWarn, logrus.WarnLevel, false},
		{LogLevelError, logrus.ErrorLevel, false},
		{LogLevelFatal, logrus.FatalLevel, false},
		{LogLevelPanic, logrus.PanicLevel, false},
		{LogLevel("warning"), logrus.WarnLevel, false}, // alias
		{LogLevel("invalid"), logrus.InfoLevel, true},  // defaults to info but returns error
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			level, err := parseLogLevel(tt.input)
			if tt.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expected, level)
		})
	}
}

func TestComponentLogger(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := defaultLogger.ComponentLogger("TestComponent")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestComponent", logEntry[logfields.Component])
	assert.Equal(t, "test message", logEntry["msg"])
	assert.Equal(t, "info", logEntry["level"])
}

func TestModuleLogger(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := defaultLogger.ModuleLogger("TestModule")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "test message", logEntry["msg"])
}

func TestMethodLogger(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := defaultLogger.MethodLogger("TestModule", "TestMethod")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "TestMethod", logEntry[logfields.Method])
}

func TestRequestLogger(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := defaultLogger.RequestLogger("TestModule", "TestMethod", "test-request-id")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "TestMethod", logEntry[logfields.Method])
	assert.Equal(t, "test-request-id", logEntry[logfields.RequestID])
}

func TestApplicationLogger(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := defaultLogger.ApplicationLogger("TestModule", "test-app")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "test-app", logEntry[logfields.Application])
}

func TestKubernetesResourceLogger(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := defaultLogger.KubernetesResourceLogger("TestModule", "Pod", "default", "test-pod")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "Pod", logEntry[logfields.Kind])
	assert.Equal(t, "default", logEntry[logfields.Namespace])
	assert.Equal(t, "test-pod", logEntry[logfields.Name])
}

func TestRedisLogger(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := defaultLogger.RedisLogger("TestModule", "GET", "test-key")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "GET", logEntry[logfields.RedisCommand])
	assert.Equal(t, "test-key", logEntry[logfields.RedisKey])
}

func TestGRPCLogger(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := defaultLogger.GRPCLogger("TestModule", "/test.Service/Method")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "/test.Service/Method", logEntry[logfields.GRPCMethod])
}

func TestHTTPLogger(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := defaultLogger.HTTPLogger("TestModule", "GET", "/api/v1/test")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "GET", logEntry[logfields.HTTPMethod])
	assert.Equal(t, "/api/v1/test", logEntry[logfields.HTTPPath])
}

func TestTextFormat(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelInfo, LogFormatText, &buf)
	require.NoError(t, err)

	logger := defaultLogger.ComponentLogger("TestComponent")
	logger.Info("test message")

	logOutput := buf.String()
	assert.Contains(t, logOutput, "component=TestComponent")
	assert.Contains(t, logOutput, "test message")
	assert.Contains(t, logOutput, "level=info")
}

func TestJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelDebug, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := defaultLogger.ComponentLogger("TestComponent")
	logger.Debug("debug message")

	// Should contain the debug message since we set level to debug
	assert.NotEmpty(t, buf.String())

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestComponent", logEntry[logfields.Component])
	assert.Equal(t, "debug message", logEntry["msg"])
	assert.Equal(t, "debug", logEntry["level"])
}

func TestLogLevels(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelWarn, LogFormatText, &buf)
	require.NoError(t, err)

	logger := defaultLogger.ComponentLogger("TestComponent")

	// Debug and Info should not appear (below warn level)
	logger.Debug("debug message")
	logger.Info("info message")

	// Warn and Error should appear
	logger.Warn("warn message")
	logger.Error("error message")

	logOutput := buf.String()
	assert.NotContains(t, logOutput, "debug message")
	assert.NotContains(t, logOutput, "info message")
	assert.Contains(t, logOutput, "warn message")
	assert.Contains(t, logOutput, "error message")
}

func TestGetDefaultLogger(t *testing.T) {
	logger := GetDefaultLogger()
	assert.NotNil(t, logger)
	assert.IsType(t, &CentralizedLogger{}, logger)
}

func TestSetLogLevel(t *testing.T) {
	// Save original level
	originalLevel := defaultLogger.GetLogLevel()
	defer func() {
		defaultLogger.logger.SetLevel(originalLevel)
	}()

	err := defaultLogger.SetLogLevel(LogLevelDebug)
	assert.NoError(t, err)
	assert.Equal(t, logrus.DebugLevel, defaultLogger.GetLogLevel())

	err = defaultLogger.SetLogLevel(LogLevelError)
	assert.NoError(t, err)
	assert.Equal(t, logrus.ErrorLevel, defaultLogger.GetLogLevel())

	// Test invalid level
	err = defaultLogger.SetLogLevel(LogLevel("invalid"))
	assert.Error(t, err)
}

func TestGetLogLevel(t *testing.T) {
	// Save original level
	originalLevel := defaultLogger.GetLogLevel()
	defer func() {
		defaultLogger.logger.SetLevel(originalLevel)
	}()

	defaultLogger.logger.SetLevel(logrus.WarnLevel)
	assert.Equal(t, logrus.WarnLevel, defaultLogger.GetLogLevel())
}

func TestWithContext(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	fields := map[string]interface{}{
		logfields.Username:  "testuser",
		logfields.RequestID: "req-123",
	}

	logger := defaultLogger.WithContext("TestModule", fields)
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "testuser", logEntry[logfields.Username])
	assert.Equal(t, "req-123", logEntry[logfields.RequestID])
}

func TestDirectLoggingFunctions(t *testing.T) {
	var buf bytes.Buffer
	err := defaultLogger.SetupLogging(LogLevelTrace, LogFormatJSON, &buf)
	require.NoError(t, err)

	// Test different log levels
	defaultLogger.Trace("TestModule", "trace message")
	defaultLogger.Debug("TestModule", "debug message")
	defaultLogger.Info("TestModule", "info message")
	defaultLogger.Warn("TestModule", "warn message")
	defaultLogger.Error("TestModule", "error message")

	logOutput := buf.String()
	assert.Contains(t, logOutput, "trace message")
	assert.Contains(t, logOutput, "debug message")
	assert.Contains(t, logOutput, "info message")
	assert.Contains(t, logOutput, "warn message")
	assert.Contains(t, logOutput, "error message")
}

func TestDefaultLoggerNotAltered(t *testing.T) {
	var defaultBuf bytes.Buffer
	var newBuf bytes.Buffer

	require.NoError(t, defaultLogger.SetupLogging(LogLevelInfo, LogFormatJSON, &defaultBuf))

	newLogger := New(nil)
	require.NoError(t, newLogger.SetupLogging(LogLevelDebug, LogFormatJSON, &newBuf))

	defaultLogger.Info("TestModule", "default info message")
	defaultLogger.Debug("TestModule", "default debug message")
	newLogger.Debug("TestModule", "new debug message")

	defaultOutput := defaultBuf.String()
	newOutput := newBuf.String()

	assert.Contains(t, defaultOutput, "default info message")
	assert.NotContains(t, defaultOutput, "default debug message")
	assert.NotContains(t, defaultOutput, "new debug message")

	assert.NotContains(t, newOutput, "default info message")
	assert.NotContains(t, newOutput, "default debug message")
	assert.Contains(t, newOutput, "new debug message")
}

func TestSeparateLoggerLevels(t *testing.T) {
	var debugBuf bytes.Buffer
	var warnBuf bytes.Buffer

	debugLogger := New(nil)
	err := debugLogger.SetupLogging(LogLevelDebug, LogFormatJSON, &debugBuf)
	require.NoError(t, err)

	warnLogger := New(nil)
	err = warnLogger.SetupLogging(LogLevelWarn, LogFormatJSON, &warnBuf)
	require.NoError(t, err)

	debugLogger.Trace("TestModule", "trace message")
	debugLogger.Debug("TestModule", "debug message")
	debugLogger.Info("TestModule", "info message")
	debugLogger.Warn("TestModule", "warn message")
	debugLogger.Error("TestModule", "error message")

	debugOutput := debugBuf.String()
	assert.NotContains(t, debugOutput, "trace message")
	assert.Contains(t, debugOutput, "debug message")
	assert.Contains(t, debugOutput, "info message")
	assert.Contains(t, debugOutput, "warn message")
	assert.Contains(t, debugOutput, "error message")

	warnLogger.Trace("TestModule", "trace message")
	warnLogger.Debug("TestModule", "debug message")
	warnLogger.Info("TestModule", "info message")
	warnLogger.Warn("TestModule", "warn message")
	warnLogger.Error("TestModule", "error message")

	warnOutput := warnBuf.String()
	assert.NotContains(t, warnOutput, "trace message")
	assert.NotContains(t, warnOutput, "debug message")
	assert.NotContains(t, warnOutput, "info message")
	assert.Contains(t, warnOutput, "warn message")
	assert.Contains(t, warnOutput, "error message")
}

func newTestEntry(buf *bytes.Buffer) *logrus.Entry {
	logger := logrus.New()
	logger.SetOutput(buf)
	logger.SetFormatter(&logrus.JSONFormatter{})
	logger.SetLevel(logrus.TraceLevel)
	return logrus.NewEntry(logger)
}

func parseLogLine(t *testing.T, buf *bytes.Buffer) map[string]interface{} {
	t.Helper()
	var entry map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	return entry
}

func newConfigMap(name, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
}

func newSecret(name, namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string][]byte{"password": []byte("s3cret")},
	}
}

func newCloudEvent(evType, target, subject string, data []byte) *cloudevents.Event {
	ev := cloudevents.NewEvent()
	ev.SetType(evType)
	ev.SetDataSchema(target)
	ev.SetSubject(subject)
	if data != nil {
		ev.DataEncoded = data
	}
	return &ev
}

func TestMarshalResource(t *testing.T) {
	t.Run("configmap produces JSON", func(t *testing.T) {
		cm := newConfigMap("test-cm", "default")
		result := marshalResource(cm)
		assert.Contains(t, result, "test-cm")
	})

	t.Run("secret is redacted", func(t *testing.T) {
		s := newSecret("my-secret", "default")
		result := marshalResource(s)
		assert.Equal(t, "<redacted: Secret>", result)
		assert.NotContains(t, result, "s3cret")
	})
}

func TestResourceMeta(t *testing.T) {
	t.Run("valid k8s object", func(t *testing.T) {
		cm := newConfigMap("my-cm", "my-ns")
		name, ns := resourceMeta(cm)
		assert.Equal(t, "my-cm", name)
		assert.Equal(t, "my-ns", ns)
	})

	t.Run("non-k8s object returns unknown", func(t *testing.T) {
		name, ns := resourceMeta("not-a-k8s-object")
		assert.Equal(t, "<unknown>", name)
		assert.Equal(t, "<unknown>", ns)
	})
}

func TestHasEventData(t *testing.T) {
	assert.False(t, hasEventData(nil))
	assert.False(t, hasEventData([]byte("")))
	assert.False(t, hasEventData([]byte("{}")))
	assert.False(t, hasEventData([]byte("null")))
	assert.False(t, hasEventData([]byte("  ")))
	assert.True(t, hasEventData([]byte(`{"key":"value"}`)))
}

func TestShortType(t *testing.T) {
	assert.Equal(t, "request-update", shortType("io.argoproj.argocd-agent.event.request-update"))
	assert.Equal(t, "custom-type", shortType("custom-type"))
}

func TestIsMetaEvent(t *testing.T) {
	assert.True(t, isMetaEvent(newCloudEvent("", "eventProcessed", "", nil)))
	assert.True(t, isMetaEvent(newCloudEvent("", "heartbeat", "", nil)))
	assert.True(t, isMetaEvent(newCloudEvent("", "clusterCacheInfoUpdate", "", nil)))
	assert.False(t, isMetaEvent(newCloudEvent("", "application", "", nil)))
}

func TestParseEventSubject(t *testing.T) {
	var buf bytes.Buffer
	logCtx := newTestEntry(&buf)

	t.Run("parses namespace/name from subject", func(t *testing.T) {
		ev := newCloudEvent("", "", "my-ns/my-app", nil)
		enriched := parseEventSubject(logCtx, ev)
		enriched.Info("test")
		entry := parseLogLine(t, &buf)
		assert.Equal(t, "my-app", entry[logfields.Name])
		assert.Equal(t, "my-ns", entry[logfields.Namespace])
	})

	t.Run("empty subject leaves fields absent", func(t *testing.T) {
		buf.Reset()
		ev := newCloudEvent("", "", "", nil)
		enriched := parseEventSubject(logCtx, ev)
		enriched.Info("test")
		entry := parseLogLine(t, &buf)
		assert.Nil(t, entry[logfields.Name])
		assert.Nil(t, entry[logfields.Namespace])
	})
}

func TestLogActionCreate(t *testing.T) {
	defer SetFullDetailConfig(FullDetailConfig{})

	t.Run("basic fields", func(t *testing.T) {
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		cm := newConfigMap("my-cm", "default")

		LogActionCreate(logCtx, "configmap", cm)

		entry := parseLogLine(t, &buf)
		assert.Equal(t, "actions", entry[logfields.LogCategory])
		assert.Equal(t, "create", entry[logfields.Action])
		assert.Equal(t, "configmap", entry[logfields.ResourceType])
		assert.Equal(t, "my-cm", entry[logfields.Name])
		assert.Equal(t, "default", entry[logfields.Namespace])
		assert.Nil(t, entry[logfields.Detail])
	})

	t.Run("with full detail", func(t *testing.T) {
		SetFullDetailConfig(FullDetailConfig{Actions: true})
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		cm := newConfigMap("my-cm", "default")

		LogActionCreate(logCtx, "configmap", cm)

		entry := parseLogLine(t, &buf)
		assert.NotNil(t, entry[logfields.Detail])
		assert.Contains(t, entry[logfields.Detail], "my-cm")
	})

	t.Run("secret is redacted with full detail", func(t *testing.T) {
		SetFullDetailConfig(FullDetailConfig{Actions: true})
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		s := newSecret("my-secret", "default")

		LogActionCreate(logCtx, "secret", s)

		entry := parseLogLine(t, &buf)
		detail := entry[logfields.Detail].(string)
		assert.Equal(t, "<redacted: Secret>", detail)
	})
}

func TestLogActionUpdate(t *testing.T) {
	defer SetFullDetailConfig(FullDetailConfig{})

	t.Run("basic fields without detail", func(t *testing.T) {
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		oldCM := newConfigMap("my-cm", "default")
		newCM := newConfigMap("my-cm", "default")
		newCM.Data = map[string]string{"key": "value"}

		LogActionUpdate(logCtx, "configmap", oldCM, newCM)

		entry := parseLogLine(t, &buf)
		assert.Equal(t, "actions", entry[logfields.LogCategory])
		assert.Equal(t, "update", entry[logfields.Action])
		assert.Nil(t, entry[logfields.Detail])
	})

	t.Run("with full detail includes diff", func(t *testing.T) {
		SetFullDetailConfig(FullDetailConfig{Actions: true})
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		oldCM := newConfigMap("my-cm", "default")
		newCM := newConfigMap("my-cm", "default")
		newCM.Data = map[string]string{"key": "value"}

		LogActionUpdate(logCtx, "configmap", oldCM, newCM)

		entry := parseLogLine(t, &buf)
		assert.NotNil(t, entry[logfields.Detail])
	})

	t.Run("secret update skips diff", func(t *testing.T) {
		SetFullDetailConfig(FullDetailConfig{Actions: true})
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		oldSec := newSecret("my-secret", "default")
		newSec := newSecret("my-secret", "default")
		newSec.Data = map[string][]byte{"password": []byte("new")}

		LogActionUpdate(logCtx, "secret", oldSec, newSec)

		entry := parseLogLine(t, &buf)
		assert.Nil(t, entry[logfields.Detail])
	})
}

func TestLogActionDelete(t *testing.T) {
	var buf bytes.Buffer
	logCtx := newTestEntry(&buf)

	LogActionDelete(logCtx, "configmap", "default", "my-cm")

	entry := parseLogLine(t, &buf)
	assert.Equal(t, "actions", entry[logfields.LogCategory])
	assert.Equal(t, "delete", entry[logfields.Action])
	assert.Equal(t, "configmap", entry[logfields.ResourceType])
	assert.Equal(t, "my-cm", entry[logfields.Name])
	assert.Equal(t, "default", entry[logfields.Namespace])
}

func TestLogActionError(t *testing.T) {
	defer SetFullDetailConfig(FullDetailConfig{})

	t.Run("basic error fields", func(t *testing.T) {
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		cm := newConfigMap("my-cm", "default")

		LogActionError(logCtx, "configmap", "create", cm, errors.New("test error"))

		entry := parseLogLine(t, &buf)
		assert.Equal(t, "actions", entry[logfields.LogCategory])
		assert.Equal(t, "create", entry[logfields.Action])
		assert.Equal(t, "my-cm", entry[logfields.Name])
		assert.Equal(t, "error", entry["level"])
		assert.Contains(t, entry["error"], "test error")
		assert.Nil(t, entry[logfields.Detail])
	})

	t.Run("with full detail", func(t *testing.T) {
		SetFullDetailConfig(FullDetailConfig{Actions: true})
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		cm := newConfigMap("my-cm", "default")

		LogActionError(logCtx, "configmap", "create", cm, errors.New("fail"))

		entry := parseLogLine(t, &buf)
		assert.NotNil(t, entry[logfields.Detail])
		assert.Contains(t, entry[logfields.Detail], "my-cm")
	})
}

func TestLogEventSent(t *testing.T) {
	defer SetFullDetailConfig(FullDetailConfig{})

	t.Run("skips meta events", func(t *testing.T) {
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		ev := newCloudEvent("io.argoproj.argocd-agent.event.processed", "eventProcessed", "", nil)

		LogEventSent(logCtx, ev)

		assert.Empty(t, buf.String())
	})

	t.Run("logs non-meta event with fields", func(t *testing.T) {
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		ev := newCloudEvent(
			"io.argoproj.argocd-agent.event.request-update",
			"application",
			"default/my-app",
			[]byte(`{"spec":{}}`),
		)

		LogEventSent(logCtx, ev)

		entry := parseLogLine(t, &buf)
		assert.Equal(t, "events", entry[logfields.LogCategory])
		assert.Equal(t, "send", entry[logfields.Direction])
		assert.Equal(t, "application", entry[logfields.EventTarget])
		assert.Equal(t, "my-app", entry[logfields.Name])
		assert.Equal(t, "default", entry[logfields.Namespace])
		assert.Nil(t, entry[logfields.Detail])
	})

	t.Run("with full detail includes data", func(t *testing.T) {
		SetFullDetailConfig(FullDetailConfig{Events: true})
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		ev := newCloudEvent(
			"io.argoproj.argocd-agent.event.request-update",
			"application",
			"default/my-app",
			[]byte(`{"spec":{}}`),
		)

		LogEventSent(logCtx, ev)

		entry := parseLogLine(t, &buf)
		assert.NotNil(t, entry[logfields.Detail])
	})

	t.Run("with full detail skips empty data", func(t *testing.T) {
		SetFullDetailConfig(FullDetailConfig{Events: true})
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		ev := newCloudEvent(
			"io.argoproj.argocd-agent.event.request-update",
			"application",
			"default/my-app",
			[]byte("{}"),
		)

		LogEventSent(logCtx, ev)

		entry := parseLogLine(t, &buf)
		assert.Nil(t, entry[logfields.Detail])
	})
}

func TestLogEventReceived(t *testing.T) {
	defer SetFullDetailConfig(FullDetailConfig{})

	t.Run("skips meta events", func(t *testing.T) {
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		ev := newCloudEvent("", "heartbeat", "", nil)

		LogEventReceived(logCtx, ev)

		assert.Empty(t, buf.String())
	})

	t.Run("logs non-meta event", func(t *testing.T) {
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		ev := newCloudEvent(
			"io.argoproj.argocd-agent.event.request-update",
			"application",
			"ns/app",
			nil,
		)

		LogEventReceived(logCtx, ev)

		entry := parseLogLine(t, &buf)
		assert.Equal(t, "events", entry[logfields.LogCategory])
		assert.Equal(t, "recv", entry[logfields.Direction])
	})
}

func TestLogEventError(t *testing.T) {
	defer SetFullDetailConfig(FullDetailConfig{})

	t.Run("basic error fields", func(t *testing.T) {
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		ev := newCloudEvent(
			"io.argoproj.argocd-agent.event.request-update",
			"application",
			"ns/app",
			[]byte(`{"spec":{}}`),
		)

		LogEventError(logCtx, ev, errors.New("event failed"))

		entry := parseLogLine(t, &buf)
		assert.Equal(t, "events", entry[logfields.LogCategory])
		assert.Equal(t, "error", entry["level"])
		assert.Contains(t, entry["error"], "event failed")
		assert.Equal(t, "app", entry[logfields.Name])
		assert.Nil(t, entry[logfields.Detail])
	})

	t.Run("with full detail includes data", func(t *testing.T) {
		SetFullDetailConfig(FullDetailConfig{Events: true})
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		ev := newCloudEvent(
			"io.argoproj.argocd-agent.event.request-update",
			"application",
			"ns/app",
			[]byte(`{"spec":{}}`),
		)

		LogEventError(logCtx, ev, errors.New("event failed"))

		entry := parseLogLine(t, &buf)
		assert.NotNil(t, entry[logfields.Detail])
	})
}

func TestLogInformerAdd(t *testing.T) {
	defer SetFullDetailConfig(FullDetailConfig{})

	t.Run("basic fields", func(t *testing.T) {
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		cm := newConfigMap("my-cm", "default")

		LogInformerAdd(logCtx, cm)

		entry := parseLogLine(t, &buf)
		assert.Equal(t, "informers", entry[logfields.LogCategory])
		assert.Equal(t, "create", entry[logfields.Action])
		assert.Equal(t, "configmap", entry[logfields.ResourceType])
		assert.Equal(t, "my-cm", entry[logfields.Name])
		assert.Equal(t, "default", entry[logfields.Namespace])
		assert.Nil(t, entry[logfields.Detail])
	})

	t.Run("with full detail", func(t *testing.T) {
		SetFullDetailConfig(FullDetailConfig{Informers: true})
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		cm := newConfigMap("my-cm", "default")

		LogInformerAdd(logCtx, cm)

		entry := parseLogLine(t, &buf)
		assert.NotNil(t, entry[logfields.Detail])
	})
}

func TestLogInformerUpdate(t *testing.T) {
	defer SetFullDetailConfig(FullDetailConfig{})

	t.Run("basic fields", func(t *testing.T) {
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		old := newConfigMap("my-cm", "default")
		new := newConfigMap("my-cm", "default")

		LogInformerUpdate(logCtx, old, new)

		entry := parseLogLine(t, &buf)
		assert.Equal(t, "informers", entry[logfields.LogCategory])
		assert.Equal(t, "update", entry[logfields.Action])
		assert.Equal(t, "configmap", entry[logfields.ResourceType])
	})

	t.Run("with full detail includes diff", func(t *testing.T) {
		SetFullDetailConfig(FullDetailConfig{Informers: true})
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		old := newConfigMap("my-cm", "default")
		upd := newConfigMap("my-cm", "default")
		upd.Data = map[string]string{"key": "val"}

		LogInformerUpdate(logCtx, old, upd)

		entry := parseLogLine(t, &buf)
		assert.NotNil(t, entry[logfields.Detail])
	})

	t.Run("secret update skips diff", func(t *testing.T) {
		SetFullDetailConfig(FullDetailConfig{Informers: true})
		var buf bytes.Buffer
		logCtx := newTestEntry(&buf)
		old := newSecret("s", "ns")
		upd := newSecret("s", "ns")
		upd.Data = map[string][]byte{"password": []byte("new")}

		LogInformerUpdate(logCtx, old, upd)

		entry := parseLogLine(t, &buf)
		assert.Nil(t, entry[logfields.Detail])
	})
}

func TestLogInformerDelete(t *testing.T) {
	var buf bytes.Buffer
	logCtx := newTestEntry(&buf)
	cm := newConfigMap("my-cm", "default")

	LogInformerDelete(logCtx, cm)

	entry := parseLogLine(t, &buf)
	assert.Equal(t, "informers", entry[logfields.LogCategory])
	assert.Equal(t, "delete", entry[logfields.Action])
	assert.Equal(t, "configmap", entry[logfields.ResourceType])
	assert.Equal(t, "my-cm", entry[logfields.Name])
	assert.Equal(t, "default", entry[logfields.Namespace])
}
