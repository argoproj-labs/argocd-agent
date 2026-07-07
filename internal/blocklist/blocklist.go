// Copyright 2026 The argocd-agent Authors
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

package blocklist

import (
	"encoding/json"
	"sync"
)

// ParseFingerprints parses a JSON-encoded list of fingerprint strings.
// Returns an empty slice for empty input.
func ParseFingerprints(data string) ([]string, error) {
	if data == "" {
		return []string{}, nil
	}
	var fingerprints []string
	if err := json.Unmarshal([]byte(data), &fingerprints); err != nil {
		return nil, err
	}
	return fingerprints, nil
}

// Blocklist is a thread-safe set of blocked certificate fingerprints.
type Blocklist struct {
	entries map[string]bool
	mu      sync.RWMutex
}

func New() *Blocklist {
	return &Blocklist{
		entries: make(map[string]bool),
	}
}

// Add adds a fingerprint to the blocklist.
func (b *Blocklist) Add(fingerprint string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[fingerprint] = true
}

// Remove removes a fingerprint from the blocklist.
func (b *Blocklist) Remove(fingerprint string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, fingerprint)
}

// Contains returns true if the given fingerprint is in the blocklist.
func (b *Blocklist) Contains(fingerprint string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.entries[fingerprint]
}

// List returns all fingerprints in the blocklist.
func (b *Blocklist) List() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	fps := make([]string, 0, len(b.entries))
	for fp := range b.entries {
		fps = append(fps, fp)
	}
	return fps
}

// Replace atomically replaces all fingerprints in the blocklist.
func (b *Blocklist) Replace(fingerprints []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = make(map[string]bool, len(fingerprints))
	for _, fp := range fingerprints {
		b.entries[fp] = true
	}
}

// Len returns the number of entries in the blocklist.
func (b *Blocklist) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.entries)
}
