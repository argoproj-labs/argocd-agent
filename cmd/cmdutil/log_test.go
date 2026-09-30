package cmdutil

import (
	"bytes"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/argoproj-labs/argocd-agent/internal/logging"
)

func Test_parseLogLevels(t *testing.T) {
	tests := []struct {
		name                        string
		logLevels                   []string
		resourceProxyExpected       logrus.Level
		redisProxyExpected          logrus.Level
		grpcEventExpected           logrus.Level
		informerEventBufferExpected logrus.Level
		defaultExpected             logrus.Level
		expectedError               string
	}{
		{
			name:                        "set everything to warning",
			logLevels:                   []string{"warning"},
			resourceProxyExpected:       logrus.WarnLevel,
			redisProxyExpected:          logrus.WarnLevel,
			grpcEventExpected:           logrus.WarnLevel,
			informerEventBufferExpected: logrus.WarnLevel,
			defaultExpected:             logrus.WarnLevel,
		},
		{
			name:                        "just resource-proxy ",
			logLevels:                   []string{"resource-proxy=warning"},
			resourceProxyExpected:       logrus.WarnLevel,
			redisProxyExpected:          logrus.InfoLevel,
			grpcEventExpected:           logrus.InfoLevel,
			informerEventBufferExpected: logrus.InfoLevel,
			defaultExpected:             logrus.InfoLevel,
		},
		{
			name:                        "just redis-proxy ",
			logLevels:                   []string{"redis-proxy=debug"},
			resourceProxyExpected:       logrus.InfoLevel,
			redisProxyExpected:          logrus.DebugLevel,
			grpcEventExpected:           logrus.InfoLevel,
			informerEventBufferExpected: logrus.InfoLevel,
			defaultExpected:             logrus.InfoLevel,
		},
		{
			name:                        "just grpc-event ",
			logLevels:                   []string{"grpc-event=trace"},
			resourceProxyExpected:       logrus.InfoLevel,
			redisProxyExpected:          logrus.InfoLevel,
			grpcEventExpected:           logrus.TraceLevel,
			informerEventBufferExpected: logrus.InfoLevel,
			defaultExpected:             logrus.InfoLevel,
		},
		{
			name:                        "just informer-event-buffer",
			logLevels:                   []string{"informer-event-buffer=debug"},
			resourceProxyExpected:       logrus.InfoLevel,
			redisProxyExpected:          logrus.InfoLevel,
			grpcEventExpected:           logrus.InfoLevel,
			informerEventBufferExpected: logrus.DebugLevel,
			defaultExpected:             logrus.InfoLevel,
		},
		{
			name:                        "multiple ",
			logLevels:                   []string{"redis-proxy=debug", "grpc-event=fatal"},
			resourceProxyExpected:       logrus.InfoLevel,
			redisProxyExpected:          logrus.DebugLevel,
			grpcEventExpected:           logrus.FatalLevel,
			informerEventBufferExpected: logrus.InfoLevel,
			defaultExpected:             logrus.InfoLevel,
		},
		{
			name:                        "combination of set and general",
			logLevels:                   []string{"warning", "redis-proxy=debug"},
			resourceProxyExpected:       logrus.WarnLevel,
			redisProxyExpected:          logrus.DebugLevel,
			grpcEventExpected:           logrus.WarnLevel,
			informerEventBufferExpected: logrus.WarnLevel,
			defaultExpected:             logrus.WarnLevel,
		},
		{
			name:                        "general is not first argument ",
			logLevels:                   []string{"grpc-event=trace", "fatal", "resource-proxy=info"},
			resourceProxyExpected:       logrus.InfoLevel,
			redisProxyExpected:          logrus.FatalLevel,
			grpcEventExpected:           logrus.TraceLevel,
			informerEventBufferExpected: logrus.FatalLevel,
			defaultExpected:             logrus.FatalLevel,
		},
		{
			name:                        "general is last argument",
			logLevels:                   []string{"resource-proxy=trace", "redis-proxy=debug", "grpc-event=warning", "fatal"},
			resourceProxyExpected:       logrus.TraceLevel,
			redisProxyExpected:          logrus.DebugLevel,
			grpcEventExpected:           logrus.WarnLevel,
			informerEventBufferExpected: logrus.FatalLevel,
			defaultExpected:             logrus.FatalLevel,
		},
		{
			name:                        "nothing is there",
			logLevels:                   []string{""},
			resourceProxyExpected:       logrus.InfoLevel,
			redisProxyExpected:          logrus.InfoLevel,
			grpcEventExpected:           logrus.InfoLevel,
			informerEventBufferExpected: logrus.InfoLevel,
			defaultExpected:             logrus.InfoLevel,
		},
		{
			name:                        "too many =",
			logLevels:                   []string{"grpc-event=trace=debug"},
			resourceProxyExpected:       logrus.InfoLevel,
			redisProxyExpected:          logrus.InfoLevel,
			grpcEventExpected:           logrus.InfoLevel,
			informerEventBufferExpected: logrus.InfoLevel,
			defaultExpected:             logrus.InfoLevel,
			expectedError:               `"grpc-event=trace=debug" is invalid, please use the format [subsystem=]loglevel`,
		},
		{
			// A single invalid entry rejects the whole input: no level from any
			// entry, valid or not, is applied.
			name:                        "too many = and a valid after",
			logLevels:                   []string{"grpc-event=trace=debug", "redis-proxy=warning"},
			resourceProxyExpected:       logrus.InfoLevel,
			redisProxyExpected:          logrus.InfoLevel,
			grpcEventExpected:           logrus.InfoLevel,
			informerEventBufferExpected: logrus.InfoLevel,
			defaultExpected:             logrus.InfoLevel,
			expectedError:               `"grpc-event=trace=debug" is invalid, please use the format [subsystem=]loglevel`,
		},
		{
			name:                        "a valid entry before an invalid one is not applied",
			logLevels:                   []string{"debug", "grpc-event=bogus"},
			resourceProxyExpected:       logrus.InfoLevel,
			redisProxyExpected:          logrus.InfoLevel,
			grpcEventExpected:           logrus.InfoLevel,
			informerEventBufferExpected: logrus.InfoLevel,
			defaultExpected:             logrus.InfoLevel,
			expectedError:               `invalid log level "bogus" for subsystem "grpc-event"`,
		},
		{
			name:                        "unknown subsystem",
			logLevels:                   []string{"grpc-evnt=trace"},
			resourceProxyExpected:       logrus.InfoLevel,
			redisProxyExpected:          logrus.InfoLevel,
			grpcEventExpected:           logrus.InfoLevel,
			informerEventBufferExpected: logrus.InfoLevel,
			defaultExpected:             logrus.InfoLevel,
			expectedError:               `invalid subsystem "grpc-evnt"`,
		},
		{
			name:                        "invalid global log level",
			logLevels:                   []string{"invalid"},
			resourceProxyExpected:       logrus.InfoLevel,
			redisProxyExpected:          logrus.InfoLevel,
			grpcEventExpected:           logrus.InfoLevel,
			informerEventBufferExpected: logrus.InfoLevel,
			defaultExpected:             logrus.InfoLevel,
			expectedError:               `invalid log level "invalid"`,
		},
		{
			name:                        "invalid log level",
			logLevels:                   []string{"grpc-event=invalid"},
			resourceProxyExpected:       logrus.InfoLevel,
			redisProxyExpected:          logrus.InfoLevel,
			grpcEventExpected:           logrus.InfoLevel,
			informerEventBufferExpected: logrus.InfoLevel,
			defaultExpected:             logrus.InfoLevel,
			expectedError:               `invalid log level "invalid" for subsystem "grpc-event"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset logrus state for subsequent tests
			logrus.SetLevel(logrus.InfoLevel)
			logrus.SetOutput(os.Stdout)

			ss := SubSystemLoggers{
				ResourceProxyLogger:       logrus.New(),
				RedisProxyLogger:          logrus.New(),
				GrpcEventLogger:           logrus.New(),
				InformerEventBufferLogger: logrus.New(),
			}

			var buf bytes.Buffer
			logrus.SetOutput(&buf)

			err := ParseAndApplyLogLevels(tt.logLevels, &ss)
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.resourceProxyExpected.String(), ss.ResourceProxyLogger.GetLevel().String())
			assert.Equal(t, tt.redisProxyExpected, ss.RedisProxyLogger.GetLevel())
			assert.Equal(t, tt.grpcEventExpected, ss.GrpcEventLogger.GetLevel())
			assert.Equal(t, tt.informerEventBufferExpected, ss.InformerEventBufferLogger.GetLevel())
			assert.Equal(t, tt.defaultExpected, logrus.GetLevel())
		})
	}
}

func Test_ParseFullDetail(t *testing.T) {
	tests := []struct {
		name      string
		input     []string
		expected  logging.FullDetailConfig
		warnAbout string
	}{
		{
			name:     "empty input",
			input:    []string{},
			expected: logging.FullDetailConfig{},
		},
		{
			name:     "all enables everything",
			input:    []string{"all"},
			expected: logging.FullDetailConfig{Actions: true, Events: true, Informers: true},
		},
		{
			name:     "actions only",
			input:    []string{"actions"},
			expected: logging.FullDetailConfig{Actions: true},
		},
		{
			name:     "events only",
			input:    []string{"events"},
			expected: logging.FullDetailConfig{Events: true},
		},
		{
			name:     "informers only",
			input:    []string{"informers"},
			expected: logging.FullDetailConfig{Informers: true},
		},
		{
			name:     "multiple categories",
			input:    []string{"actions", "events"},
			expected: logging.FullDetailConfig{Actions: true, Events: true},
		},
		{
			name:     "case insensitive",
			input:    []string{"ACTIONS", "Events"},
			expected: logging.FullDetailConfig{Actions: true, Events: true},
		},
		{
			name:     "whitespace trimmed",
			input:    []string{" actions ", " informers "},
			expected: logging.FullDetailConfig{Actions: true, Informers: true},
		},
		{
			name:      "invalid category warns",
			input:     []string{"invalid"},
			expected:  logging.FullDetailConfig{},
			warnAbout: "invalid full-detail category",
		},
		{
			name:     "empty strings are ignored",
			input:    []string{"", "actions", ""},
			expected: logging.FullDetailConfig{Actions: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer logging.SetFullDetailConfig(logging.FullDetailConfig{})

			var buf bytes.Buffer
			logrus.SetOutput(&buf)
			defer logrus.SetOutput(os.Stdout)

			ParseFullDetail(tt.input)

			got := logging.GetFullDetailConfig()
			assert.Equal(t, tt.expected, got)

			if tt.warnAbout != "" {
				assert.Contains(t, buf.String(), tt.warnAbout)
			}
		})
	}
}
