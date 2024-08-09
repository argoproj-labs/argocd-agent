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

package namedlock

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_Lock(t *testing.T) {
	nl := NewNamedLock()
	assert.True(t, nl.TryLock("foo"))
	assert.False(t, nl.TryLock("foo"))
	assert.True(t, nl.TryLock("bar"))
	testch := make(chan struct{})
	go func() {
		// We have no assertions here, other than being able to acquire locks
		nl.Unlock("foo")
		nl.Unlock("bar")
		nl.RLock("foo")
		nl.RLock("foo")
		assert.False(t, nl.TryLock("foo"))
		nl.RUnlock("foo")
		nl.RUnlock("foo")
		nl.Lock("foo")
		assert.False(t, nl.TryRLock("foo"))
		nl.Unlock("foo")
		assert.True(t, nl.TryRLock("foo"))
		nl.RUnlock("foo")
		close(testch)
	}()
	timeout := time.NewTimer(1 * time.Second)
	for {
		select {
		case <-testch:
			return
		case <-timeout.C:
			t.Fatal("timeout reached")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
