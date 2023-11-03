/*
Package clock provides abstraction for the system clock. It is mainly used to
ease testing time-based functions throughout the code.

It only provides the most basic primitives Now(), Until() and Since().
*/
package clock

import "time"

type Clock interface {
	Now() time.Time
	Until(t time.Time) time.Duration
	Since(t time.Time) time.Duration
}

var _ Clock = &standardClock{}
var _ Clock = &seededClock{}

type standardClock struct{}

type seededClock struct {
	now time.Time
}

func StandardClock() standardClock {
	return standardClock{}
}

func (c standardClock) Now() time.Time {
	return time.Now()
}

func (c standardClock) Until(t time.Time) time.Duration {
	return time.Until(t)
}

func (c standardClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}

func SeededClock(now time.Time) seededClock {
	return seededClock{now: now}
}

func (c seededClock) Now() time.Time {
	return c.now
}

func (c seededClock) Until(t time.Time) time.Duration {
	return t.Sub(c.now)
}

func (c seededClock) Since(t time.Time) time.Duration {
	return c.now.Sub(t)
}
