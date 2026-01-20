package cmdutil

import (
	"bytes"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func Test_parseLogLevels(t *testing.T) {
	tests := []struct {
		name                  string
		logLevels             []string
		resourceProxyExpected logrus.Level
		redisProxyExpected    logrus.Level
		grpcEventExpected     logrus.Level
		defaultExpected       logrus.Level
		expectedMessage       string
	}{
		{
			name:                  "just resource-proxy ",
			logLevels:             []string{"resource-proxy=warning"},
			resourceProxyExpected: logrus.WarnLevel,
			redisProxyExpected:    logrus.InfoLevel,
			grpcEventExpected:     logrus.InfoLevel,
			defaultExpected:       logrus.InfoLevel,
			expectedMessage:       "",
		},
		{
			name:                  "just redis-proxy ",
			logLevels:             []string{"redis-proxy=debug"},
			resourceProxyExpected: logrus.InfoLevel,
			redisProxyExpected:    logrus.DebugLevel,
			grpcEventExpected:     logrus.InfoLevel,
			defaultExpected:       logrus.InfoLevel,
			expectedMessage:       "",
		},
		{
			name:                  "just grpc-event ",
			logLevels:             []string{"grpc-event=trace"},
			resourceProxyExpected: logrus.InfoLevel,
			redisProxyExpected:    logrus.InfoLevel,
			grpcEventExpected:     logrus.TraceLevel,
			defaultExpected:       logrus.InfoLevel,
			expectedMessage:       "",
		},
		{
			name:                  "multiple ",
			logLevels:             []string{"redis-proxy=debug", "grpc-event=fatal"},
			resourceProxyExpected: logrus.InfoLevel,
			redisProxyExpected:    logrus.DebugLevel,
			grpcEventExpected:     logrus.FatalLevel,
			defaultExpected:       logrus.InfoLevel,
			expectedMessage:       "",
		},
		{
			name:                  "combination of set and general",
			logLevels:             []string{"warning", "redis-proxy=debug"},
			resourceProxyExpected: logrus.WarnLevel,
			redisProxyExpected:    logrus.DebugLevel,
			grpcEventExpected:     logrus.WarnLevel,
			defaultExpected:       logrus.WarnLevel,
			expectedMessage:       "",
		},
		{
			name:                  "general is not first argument ",
			logLevels:             []string{"grpc-event=trace", "fatal", "resource-proxy=info"},
			resourceProxyExpected: logrus.InfoLevel,
			redisProxyExpected:    logrus.FatalLevel,
			grpcEventExpected:     logrus.TraceLevel,
			defaultExpected:       logrus.FatalLevel,
			expectedMessage:       "",
		},
		{
			name:                  "general is last argument",
			logLevels:             []string{"resource-proxy=trace", "redis-proxy=debug", "grpc-event=warning", "fatal"},
			resourceProxyExpected: logrus.TraceLevel,
			redisProxyExpected:    logrus.DebugLevel,
			grpcEventExpected:     logrus.WarnLevel,
			defaultExpected:       logrus.FatalLevel,
			expectedMessage:       "",
		},
		{
			name:                  "nothing is there",
			logLevels:             []string{""},
			resourceProxyExpected: logrus.InfoLevel,
			redisProxyExpected:    logrus.InfoLevel,
			grpcEventExpected:     logrus.InfoLevel,
			defaultExpected:       logrus.InfoLevel,
			expectedMessage:       "",
		},
		{
			name:                  "too many =",
			logLevels:             []string{"grpc-event=trace=debug"},
			resourceProxyExpected: logrus.InfoLevel,
			redisProxyExpected:    logrus.InfoLevel,
			grpcEventExpected:     logrus.InfoLevel,
			defaultExpected:       logrus.InfoLevel,
			expectedMessage:       "invalid please use the format subsystem",
		},
		{
			name:                  "too many = and a valid after",
			logLevels:             []string{"grpc-event=trace=debug", "redis-proxy=warning"},
			resourceProxyExpected: logrus.InfoLevel,
			redisProxyExpected:    logrus.WarnLevel,
			grpcEventExpected:     logrus.InfoLevel,
			defaultExpected:       logrus.InfoLevel,
			expectedMessage:       "invalid please use the format subsystem",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset logrus state for subsequent tests
			logrus.SetLevel(logrus.InfoLevel)
			logrus.SetOutput(os.Stdout)

			ss := SubSystemLoggers{
				ResourceProxyLogger: logrus.New(),
				RedisProxyLogger:    logrus.New(),
				GrpcEventLogger:     logrus.New(),
			}

			var buf bytes.Buffer
			logrus.SetOutput(&buf)

			ParseLogLevels(tt.logLevels, &ss)

			assert.Equal(t, tt.resourceProxyExpected, ss.ResourceProxyLogger.GetLevel())
			assert.Equal(t, tt.redisProxyExpected, ss.RedisProxyLogger.GetLevel())
			assert.Equal(t, tt.grpcEventExpected, ss.GrpcEventLogger.GetLevel())
			assert.Equal(t, tt.defaultExpected, logrus.GetLevel())

			if tt.expectedMessage != "" {
				output := buf.String()
				assert.Contains(t, output, tt.expectedMessage)
			}
		})
	}
}
