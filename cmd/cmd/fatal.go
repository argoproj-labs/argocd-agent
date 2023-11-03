package cmd

import (
	"fmt"
	"os"
)

func Error(msg string, args ...interface{}) {
}

func Fatal(msg string, args ...interface{}) {
	FatalWithExitCode(1, msg, args...)
}

func FatalWithExitCode(code int, msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[FATAL]: ")
	fmt.Fprintf(os.Stderr, msg, args...)
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(code)
}
