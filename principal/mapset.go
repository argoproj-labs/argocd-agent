// Copyright 2025 The argocd-agent Authors
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

package principal

import "sync"

// MapToSet is a map of keys to a set of unique values.
type MapToSet struct {
	mu sync.RWMutex
	m  map[string]set
}

type set map[string]bool

func NewMapToSet() *MapToSet {
	return &MapToSet{m: make(map[string]set)}
}

func (m *MapToSet) Get(key string) map[string]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.m[key]
}

func (m *MapToSet) Add(key string, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.m[key]; !ok {
		m.m[key] = make(set)
	}
	m.m[key][value] = true
}

func (m *MapToSet) Delete(key string, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if the key exists to avoid panic
	if s, ok := m.m[key]; ok {
		delete(s, value)

		if len(s) == 0 {
			delete(m.m, key)
		}
	}
}
