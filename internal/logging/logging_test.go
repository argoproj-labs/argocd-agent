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
			err := SetupLogging(tt.level, tt.format, &buf)

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
	err := SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := ComponentLogger("TestComponent")
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
	err := SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := ModuleLogger("TestModule")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "test message", logEntry["msg"])
}

func TestMethodLogger(t *testing.T) {
	var buf bytes.Buffer
	err := SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := MethodLogger("TestModule", "TestMethod")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "TestMethod", logEntry[logfields.Method])
}

func TestRequestLogger(t *testing.T) {
	var buf bytes.Buffer
	err := SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := RequestLogger("TestModule", "TestMethod", "test-request-id")
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
	err := SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := ApplicationLogger("TestModule", "test-app")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "test-app", logEntry[logfields.Application])
}

func TestKubernetesResourceLogger(t *testing.T) {
	var buf bytes.Buffer
	err := SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := KubernetesResourceLogger("TestModule", "Pod", "default", "test-pod")
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
	err := SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := RedisLogger("TestModule", "GET", "test-key")
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
	err := SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := GRPCLogger("TestModule", "/test.Service/Method")
	logger.Info("test message")

	var logEntry map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "TestModule", logEntry[logfields.Module])
	assert.Equal(t, "/test.Service/Method", logEntry[logfields.GRPCMethod])
}

func TestHTTPLogger(t *testing.T) {
	var buf bytes.Buffer
	err := SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := HTTPLogger("TestModule", "GET", "/api/v1/test")
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
	err := SetupLogging(LogLevelInfo, LogFormatText, &buf)
	require.NoError(t, err)

	logger := ComponentLogger("TestComponent")
	logger.Info("test message")

	logOutput := buf.String()
	assert.Contains(t, logOutput, "component=TestComponent")
	assert.Contains(t, logOutput, "test message")
	assert.Contains(t, logOutput, "level=info")
}

func TestJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	err := SetupLogging(LogLevelDebug, LogFormatJSON, &buf)
	require.NoError(t, err)

	logger := ComponentLogger("TestComponent")
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
	err := SetupLogging(LogLevelWarn, LogFormatText, &buf)
	require.NoError(t, err)

	logger := ComponentLogger("TestComponent")

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
	assert.IsType(t, &logrus.Logger{}, logger)
}

func TestSetLogLevel(t *testing.T) {
	// Save original level
	originalLevel := GetLogLevel()
	defer func() {
		defaultLogger.SetLevel(originalLevel)
	}()

	err := SetLogLevel(LogLevelDebug)
	assert.NoError(t, err)
	assert.Equal(t, logrus.DebugLevel, GetLogLevel())

	err = SetLogLevel(LogLevelError)
	assert.NoError(t, err)
	assert.Equal(t, logrus.ErrorLevel, GetLogLevel())

	// Test invalid level
	err = SetLogLevel(LogLevel("invalid"))
	assert.Error(t, err)
}

func TestGetLogLevel(t *testing.T) {
	// Save original level
	originalLevel := GetLogLevel()
	defer func() {
		defaultLogger.SetLevel(originalLevel)
	}()

	defaultLogger.SetLevel(logrus.WarnLevel)
	assert.Equal(t, logrus.WarnLevel, GetLogLevel())
}

func TestWithContext(t *testing.T) {
	var buf bytes.Buffer
	err := SetupLogging(LogLevelInfo, LogFormatJSON, &buf)
	require.NoError(t, err)

	fields := map[string]interface{}{
		logfields.Username:  "testuser",
		logfields.RequestID: "req-123",
	}

	logger := WithContext("TestModule", fields)
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
	err := SetupLogging(LogLevelTrace, LogFormatJSON, &buf)
	require.NoError(t, err)

	// Test different log levels
	Trace("TestModule", "trace message")
	Debug("TestModule", "debug message")
	Info("TestModule", "info message")
	Warn("TestModule", "warn message")
	Error("TestModule", "error message")

	logOutput := buf.String()
	assert.Contains(t, logOutput, "trace message")
	assert.Contains(t, logOutput, "debug message")
	assert.Contains(t, logOutput, "info message")
	assert.Contains(t, logOutput, "warn message")
	assert.Contains(t, logOutput, "error message")
}
