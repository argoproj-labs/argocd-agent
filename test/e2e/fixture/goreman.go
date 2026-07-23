package fixture

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// StopProcess stops the named process managed by goreman
func StopProcess(processName string, t *testing.T) error {
	// Goreman is only usable in local case
	SkipIfAgentInClusterEnvVarIsSet(t)
	cmd := exec.Command("goreman", "run", "stop", processName)
	return cmd.Run()
}

// StartProcess starts the named process managed by goreman
func StartProcess(processName string, t *testing.T) error {
	// Goreman is only usable in local case
	SkipIfAgentInClusterEnvVarIsSet(t)
	cmd := exec.Command("goreman", "run", "start", processName)
	return cmd.Run()
}

// IsProcessRunning returns whether the named process managed by goreman is
// running
func IsProcessRunning(processName string, t *testing.T) bool {
	// Goreman is only usable in local case
	SkipIfAgentInClusterEnvVarIsSet(t)
	out := &bytes.Buffer{}
	cmd := exec.Command("goreman", "run", "status")
	cmd.Stdout = out
	err := cmd.Run()
	if err != nil {
		panic(err)
	}
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		l := sc.Text()
		if len(l) < 2 {
			panic("unknown output")
		}
		switch l[0] {
		case '*':
			// process running
			if l[1:] == processName {
				return true
			}
		case ' ':
			// process not running
			if l[1:] == processName {
				return false
			}
		default:
			panic("unknown output")
		}
	}
	// process not found
	return false
}

// VerifyProcessLogs checks if expected messages are present in process logs.
func VerifyProcessLogs(t testing.TB, processName string, requiredMessages []string) {
	t.Helper()
	require.Eventually(t, func() bool {
		content, err := os.ReadFile("/tmp/argocd-agent-e2e-process-output.log")
		if err != nil {
			t.Logf("reading process log for %s: %v", processName, err)
			return false
		}
		for _, msg := range requiredMessages {
			if !strings.Contains(string(content), msg) {
				t.Logf("expected log not found for %s: %q", processName, msg)
				return false
			}
		}
		return true
	}, 120*time.Second, 5*time.Second, "process %s logs should contain required messages", processName)
}
