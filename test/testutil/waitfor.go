package testutil

import (
	"testing"
	"time"
)

// WaitForChange is a test helper that waits for some kind of change to
// happen. The condition is given as callback f, which must return either
// true or false. The callback f is executed repeatedly until it returns true
// or the duration d been reached. In the latter case, the helper will fail.
//
// This helper is intended to be used for testing asynchronous events.
func WaitForChange(t *testing.T, d time.Duration, f func() bool) {
	t.Helper()
	tmr := time.NewTicker(d)
	for {
		select {
		case <-tmr.C:
			t.Fatalf("Timeout waiting for condition to be true after %.02fs", d.Seconds())
		default:
			ok := f()
			if ok {
				tmr.Stop()
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}
