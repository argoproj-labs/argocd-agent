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
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	defaultLogger.SetupLogging(LogLevelInfo, LogFormatJSON, &defaultBuf)

	newLogger := New()
	newLogger.SetupLogging(LogLevelDebug, LogFormatJSON, &newBuf)

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

func TestSeperateLoggerLevels(t *testing.T) {
	var debugBuf bytes.Buffer
	var warnBuf bytes.Buffer

	debugLogger := New()
	err := debugLogger.SetupLogging(LogLevelDebug, LogFormatJSON, &debugBuf)
	require.NoError(t, err)

	warnLogger := New()
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
