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
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/writer"
)

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
func CreateLogger(logLevel string, logFormat string) *logrus.Logger {
	logger := logrus.New()
	if logLevel == "" {
		logLevel = "info"
	}
	if logLevel != "" {
		lvl, err := StringToLoglevel(logLevel)
		if err != nil {
			Fatal("invalid resource proxy log level: %s. Available levels are %s", logLevel, AvailableLogLevels())
		}
		logger.SetLevel(lvl)
	}
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
