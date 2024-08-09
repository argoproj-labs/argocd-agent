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
Package namedlock implements primitives for accessing NamedLocks. A NamedLock
is a sync.RWMutex, bound to a specific name. A NamedLock's method names are
similar
*/
package namedlock

import "sync"

// NamedLock implements a named lock. It must not be copied after first use.
type NamedLock struct {
	l          sync.RWMutex
	namedLocks map[string]*sync.RWMutex
}

// NewNamedLock returns a new instance of NamedLock
func NewNamedLock() *NamedLock {
	return &NamedLock{
		namedLocks: make(map[string]*sync.RWMutex),
	}
}

// RLock acquires the named read-only lock for name
//
// See sync.RLock for more information on the semantics.
func (nl *NamedLock) RLock(name string) {
	nl.lock(name).RLock()
}

// RLock releases the named read-only lock for name
//
// See sync.RUnlock for more information on the semantics.
func (nl *NamedLock) RUnlock(name string) {
	nl.lock(name).RUnlock()
}

// Lock acquires the named read-write lock for name
//
// See sync.Lock for more information on the semantics.
func (nl *NamedLock) Lock(name string) {
	nl.lock(name).Lock()
}

// Unlock releases the named read-write lock for name
//
// See sync.Unlock for more information on the semantics.
func (nl *NamedLock) Unlock(name string) {
	nl.lock(name).Unlock()
}

// TryLock tries to acquire the named read-write lock for name. If the lock
// could not be acquired because it is already being held, TryLock returns
// false. Otherwise, when the lock could be acquired, TryLock returns true.
//
// See sync.TryLock for more information on the semantics.
func (nl *NamedLock) TryLock(name string) bool {
	return nl.lock(name).TryLock()
}

// TryRLock tries to acquire the named read-only lock for name. If the lock
// could not be acquired because it is already being held, TryRLock returns
// false. Otherwise, when the lock could be acquired, TryRLock returns true.
//
// See sync.TryRLock for more information on the semantics.
func (nl *NamedLock) TryRLock(name string) bool {
	return nl.lock(name).TryRLock()
}

// lock returns a pointer to the mutex for name. If the mutex for name does
// not yet exist, it will be initialized.
//
// The lock function will hold a read-write lock on NamedLock nl until the
// function returns.
func (nl *NamedLock) lock(name string) *sync.RWMutex {
	nl.l.Lock()
	defer nl.l.Unlock()
	if l, ok := nl.namedLocks[name]; ok {
		return l
	} else {
		l = new(sync.RWMutex)
		nl.namedLocks[name] = l
		return l
	}
}
