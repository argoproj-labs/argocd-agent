package checkpoint

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/clock"
)

// Step represents a single step within a checkpoint.
type Step struct {
	name  string
	start time.Time
	end   time.Time
}

// Checkpoint is a very simple type for measuring time. Each checkpoint has
// one or more steps, each measured individually. Use NewCheckpoint to get
// a properly initialized instance.
type Checkpoint struct {
	name  string
	steps []Step
	clock clock.Clock
	mutex sync.RWMutex
}

// NewCheckpoint returns a new initialized checkpoint with the given name
// If clockimpl has at least one parameter, the first of the list will be
// used as the system clock.
func NewCheckpoint(name string, clockimpl ...clock.Clock) *Checkpoint {
	cp := &Checkpoint{
		name: name,
	}
	if len(clockimpl) > 0 {
		cp.clock = clockimpl[0]
	} else {
		cp.clock = clock.StandardClock()
	}
	return cp
}

// Start starts measuring time for the checkpoint cp
func (cp *Checkpoint) Start(name string) {
	cp.mutex.Lock()
	defer cp.mutex.Unlock()
	// If there's a last step, and it hasn't been stopped, stop it now.
	cp.finishLastStep()
	cp.steps = append(cp.steps, Step{name: name, start: cp.clock.Now()})
}

// End stops measuring time for checkpoint cp
func (cp *Checkpoint) End() {
	cp.mutex.Lock()
	defer cp.mutex.Unlock()
	cp.finishLastStep()
}

// Duration returns the duration for the whole checkpoint, i.e. from the start
// of the first step to the end of the last step.
func (cp *Checkpoint) Duration() time.Duration {
	cp.mutex.RLock()
	defer cp.mutex.RUnlock()
	if len(cp.steps) == 0 {
		return 0
	}
	cp.finishLastStep()
	return cp.steps[len(cp.steps)-1].end.Sub(cp.steps[0].start)
}

// NumSteps returns the number of steps checkpoint cp has measured
func (cp *Checkpoint) NumSteps() int {
	return len(cp.steps)
}

// Steps returns the steps within the checkpoint cp
func (cp *Checkpoint) Steps() []Step {
	return cp.steps
}

// String returns a string represantation of the checkpoint's timing data.
// Steps that are still running will be ignored.
func (cp *Checkpoint) String() string {
	cp.mutex.RLock()
	defer cp.mutex.RUnlock()
	steps := []string{}
	for _, s := range cp.steps {
		if !s.end.IsZero() {
			steps = append(steps, fmt.Sprintf("%s=%.3fs", s.name, s.Duration().Seconds()))
		}
	}
	return fmt.Sprintf("checkpoint %s duration=%.3fs (steps: %s)", cp.name, cp.Duration().Seconds(), strings.Join(steps, ", "))
}

// Name returns the checkpoint's given name
func (cp *Checkpoint) Name() string {
	return cp.name
}

// Duration returns the duration a step took to complete
func (st Step) Duration() time.Duration {
	if st.end.IsZero() {
		return 0
	}
	return st.end.Sub(st.start)
}

// finishLastStep finishes the last step if it's open.
//
// This function is NOT thread-safe.
func (cp *Checkpoint) finishLastStep() {
	if len(cp.steps) > 0 && cp.steps[len(cp.steps)-1].end.IsZero() {
		cp.steps[len(cp.steps)-1].end = cp.clock.Now()
	}
}
