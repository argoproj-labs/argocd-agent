package fixture

import (
	"bufio"
	"bytes"
	"os/exec"
)

// StopProcess stops the named process managed by goreman
func StopProcess(processName string) error {
	cmd := exec.Command("goreman", "run", "stop", processName)
	return cmd.Run()
}

// StartProcess starts the named process managed by goreman
func StartProcess(processName string) error {
	cmd := exec.Command("goreman", "run", "start", processName)
	return cmd.Run()
}

// IsProcessRunning returns whether the named process managed by goreman is
// running
func IsProcessRunning(processName string) bool {
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
