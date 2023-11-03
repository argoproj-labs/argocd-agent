package cmd

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
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
