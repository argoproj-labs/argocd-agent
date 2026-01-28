// Copyright 2024 The argocd-agent Authors
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

package cmdutil

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/writer"
)

const AvailableSubSystems = "resource-proxy, redis-proxy, grpc-event"

type SubSystemLoggers struct {
	ResourceProxyLogger *logrus.Logger
	RedisProxyLogger    *logrus.Logger
	GrpcEventLogger     *logrus.Logger
}

func StringToLoglevel(l string) (logrus.Level, error) {
	switch strings.ToLower(l) {
	case strings.ToLower(logrus.FatalLevel.String()):
		return logrus.FatalLevel, nil
	case strings.ToLower(logrus.ErrorLevel.String()):
		return logrus.ErrorLevel, nil
	case strings.ToLower(logrus.WarnLevel.String()):
		return logrus.WarnLevel, nil
	case strings.ToLower(logrus.InfoLevel.String()):
		return logrus.InfoLevel, nil
	case strings.ToLower(logrus.DebugLevel.String()):
		return logrus.DebugLevel, nil
	case strings.ToLower(logrus.TraceLevel.String()):
		return logrus.TraceLevel, nil
	default:
		return 0, fmt.Errorf("unknown log level: %s", l)
	}
}

func AvailableLogLevels() string {
	levels := make([]string, len(logrus.AllLevels))
	for i, l := range logrus.AllLevels {
		levels[i] = l.String()
	}
	return strings.Join(levels, ", ")
}

// InitLogging will initialize logrus with the setting and hooks we want it to
// use by default.
func InitLogging() {
	logrus.SetOutput(io.Discard) // Send all logs to nowhere by default
	logrus.SetLevel(logrus.DebugLevel)
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.AddHook(&writer.Hook{ // Send logs with level higher than warning to stderr
		Writer: os.Stderr,
		LogLevels: []logrus.Level{
			logrus.PanicLevel,
			logrus.FatalLevel,
			logrus.ErrorLevel,
			logrus.WarnLevel,
		},
	})
	logrus.AddHook(&writer.Hook{ // Send info and debug logs to stdout
		Writer: os.Stdout,
		LogLevels: []logrus.Level{
			logrus.InfoLevel,
			logrus.DebugLevel,
			logrus.TraceLevel,
		},
	})
}

func LogFormatter(format string) (logrus.Formatter, error) {
	switch strings.ToLower(format) {
	case "text":
		return &logrus.TextFormatter{}, nil
	case "json":
		return &logrus.JSONFormatter{}, nil
	default:
		return nil, fmt.Errorf("invalid format '%s', must be one of text, json", format)
	}
}

// CreateLogger creates a new logrus instance with the log level specified
// validation is done on the log level to ensure it is valid, log formatter
// is also set
func CreateLogger(logFormat string) *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	if formatter, err := LogFormatter(logFormat); err != nil {
		Fatal("%s", err.Error())
	} else {
		logger.SetFormatter(formatter)
	}

	logger.AddHook(&writer.Hook{
		Writer: os.Stderr,
		LogLevels: []logrus.Level{
			logrus.PanicLevel,
			logrus.FatalLevel,
			logrus.ErrorLevel,
			logrus.WarnLevel,
		},
	})
	logger.AddHook(&writer.Hook{
		Writer: os.Stdout,
		LogLevels: []logrus.Level{
			logrus.InfoLevel,
			logrus.DebugLevel,
			logrus.TraceLevel,
		},
	})

	return logger
}

// ParseLogLevels parses the slice produced by the log level flag and sets log levels
// for subsystems and the default logger accordingly
func ParseLogLevels(input []string, ss *SubSystemLoggers) {
	seen := []string{}

	for _, e := range input {
		split := strings.Split(e, "=")
		if len(split) > 2 || len(split) == 0 {
			logrus.Warnf("%s is invalid please use the format subsystem=loglevel, skipping", e)
			continue
		}

		split[0] = strings.TrimSpace(split[0])
		if len(split) > 1 {
			split[1] = strings.TrimSpace(split[1])
		}

		if len(split) == 1 {
			if split[0] == "" {
				split[0] = "info"
			}

			level, err := StringToLoglevel(split[0])
			if err != nil {
				Fatal("an invalid log level was entered: %s. Available levels are %s", split[0], AvailableLogLevels())
			}
			logrus.SetLevel(level)

			if !slices.Contains(seen, "resource-proxy") {
				ss.ResourceProxyLogger.SetLevel(level)
			}
			if !slices.Contains(seen, "redis-proxy") {
				ss.RedisProxyLogger.SetLevel(level)
			}
			if !slices.Contains(seen, "grpc-event") {
				ss.GrpcEventLogger.SetLevel(level)
			}
			continue
		}

		if split[1] == "" {
			split[1] = "info"
		}

		level, err := StringToLoglevel(split[1])
		if err != nil {
			Fatal("an invalid log level was entered: %s for %s. Available levels are %s", split[1], split[0], AvailableLogLevels())
		}
		switch split[0] {
		case "resource-proxy":
			ss.ResourceProxyLogger.SetLevel(level)
			seen = append(seen, "resource-proxy")
		case "redis-proxy":
			ss.RedisProxyLogger.SetLevel(level)
			seen = append(seen, "redis-proxy")
		case "grpc-event":
			ss.GrpcEventLogger.SetLevel(level)
			seen = append(seen, "grpc-event")
		default:
			logrus.Warnf("an invalid subsystem %s was specified. subsystems are %s, skipping", split[0], AvailableSubSystems)
		}
	}
}
