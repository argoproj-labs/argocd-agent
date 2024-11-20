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

func StandardClock() *standardClock {
	return &standardClock{}
}

func (c *standardClock) Now() time.Time {
	return time.Now()
}

func (c *standardClock) Until(t time.Time) time.Duration {
	return time.Until(t)
}

func (c *standardClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}

func SeededClock(now time.Time) *seededClock {
	return &seededClock{now: now}
}

func (c *seededClock) Now() time.Time {
	return c.now
}

func (c *seededClock) Until(t time.Time) time.Duration {
	return t.Sub(c.now)
}

func (c *seededClock) Since(t time.Time) time.Duration {
	return c.now.Sub(t)
}

func (c *seededClock) At(seed time.Time) {
	c.now = seed
}
