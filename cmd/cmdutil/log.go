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

	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/writer"
)

const AvailableSubSystems = "resource-proxy, redis-proxy, grpc-event, informer-event-buffer"

type SubSystemLoggers struct {
	ResourceProxyLogger       *logrus.Logger
	RedisProxyLogger          *logrus.Logger
	GrpcEventLogger           *logrus.Logger
	InformerEventBufferLogger *logrus.Logger
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

// logLevelPlan is the validated result of parsing the log level
type logLevelPlan struct {
	// global is the level for the standard logger and for any subsystem that is
	// not explicitly assigned a level. It is nil when the input contains no bare
	// (non "subsystem=level") entry, in which case the global level is left
	// untouched.
	global *logrus.Level
	// subsystems maps a known subsystem name to its explicitly requested level.
	subsystems map[string]logrus.Level
}

// parseLogLevelPlan parses and validates the log level slice without applying
// it. It has no side effects: any invalid entry rejects the whole input with an
// error, so the caller can decide whether to abort (startup) or skip the change
// (runtime) without the loggers having been left half-updated.
func parseLogLevelPlan(input []string) (*logLevelPlan, error) {
	plan := &logLevelPlan{
		subsystems: make(map[string]logrus.Level),
	}

	for _, e := range input {
		// A well-formed entry is either "level" or "subsystem=level".
		split := strings.Split(e, "=")
		if len(split) > 2 {
			return nil, fmt.Errorf("%q is invalid, please use the format [subsystem=]loglevel", e)
		}

		// Global log level
		if len(split) == 1 {
			levelStr := strings.TrimSpace(split[0])
			if levelStr == "" {
				levelStr = "info"
			}
			level, err := StringToLoglevel(levelStr)
			if err != nil {
				return nil, fmt.Errorf("invalid log level %q: available levels are %s", levelStr, AvailableLogLevels())
			}
			plan.global = &level
			continue
		}

		subsystem := strings.TrimSpace(split[0])
		switch subsystem {
		case "resource-proxy", "redis-proxy", "grpc-event", "informer-event-buffer":
		default:
			return nil, fmt.Errorf("invalid subsystem %q: available subsystems are %s", subsystem, AvailableSubSystems)
		}

		levelStr := strings.TrimSpace(split[1])
		if levelStr == "" {
			levelStr = "info"
		}
		level, err := StringToLoglevel(levelStr)
		if err != nil {
			return nil, fmt.Errorf("invalid log level %q for subsystem %q: available levels are %s", levelStr, subsystem, AvailableLogLevels())
		}
		plan.subsystems[subsystem] = level
	}

	return plan, nil
}

// applyLogLevelPlan applies a validated plan to the standard logger and the subsystem loggers.
func applyLogLevelPlan(plan *logLevelPlan, ss *SubSystemLoggers) {
	loggers := map[string]*logrus.Logger{}
	if ss != nil {
		loggers["resource-proxy"] = ss.ResourceProxyLogger
		loggers["redis-proxy"] = ss.RedisProxyLogger
		loggers["grpc-event"] = ss.GrpcEventLogger
		loggers["informer-event-buffer"] = ss.InformerEventBufferLogger
	}

	if plan.global != nil {
		logrus.SetLevel(*plan.global)
		for name, logger := range loggers {
			if _, ok := plan.subsystems[name]; !ok {
				if logger != nil {
					logger.SetLevel(*plan.global)
				}
			}
		}
	}

	for name, level := range plan.subsystems {
		if logger := loggers[name]; logger != nil {
			logger.SetLevel(level)
		}
	}
}

// ParseAndApplyLogLevels parses the slice produced by the log level flag and sets log
// levels for subsystems and the default logger accordingly.
func ParseAndApplyLogLevels(input []string, ss *SubSystemLoggers) error {
	plan, err := parseLogLevelPlan(input)
	if err != nil {
		return err
	}

	applyLogLevelPlan(plan, ss)
	return nil
}

const AvailableFullDetailCategories = "all, actions, events, informers"

// ParseFullDetail parses a comma-separated list of category names and sets
// the global full detail logging configuration. The special value "all"
// enables every category.
func ParseFullDetail(input []string) {
	cfg := logging.FullDetailConfig{}
	for _, e := range input {
		category := strings.TrimSpace(strings.ToLower(e))
		if category == "" {
			continue
		}
		switch category {
		case "all":
			cfg.Actions = true
			cfg.Events = true
			cfg.Informers = true
		case "actions":
			cfg.Actions = true
		case "events":
			cfg.Events = true
		case "informers":
			cfg.Informers = true
		default:
			logrus.Warnf("an invalid full-detail category %q was specified. Available categories are: %s, skipping", category, AvailableFullDetailCategories)
		}
	}
	logging.SetFullDetailConfig(cfg)
}
