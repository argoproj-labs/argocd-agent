package cmd

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
		},
	})
}
