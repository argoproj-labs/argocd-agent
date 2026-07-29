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
	"sync"

	corev1 "k8s.io/api/core/v1"

	"github.com/argoproj-labs/argocd-agent/internal/informer"
)

// Blocklist is a thread-safe set of blocked certificate fingerprints.
// It also tracks which agent is using which fingerprint, for active
// disconnects and holds the ConfigMap informer that watches for changes.
type Blocklist struct {
	entries  map[string]bool
	mu       sync.RWMutex
	agents   map[string]string
	agentsMu sync.RWMutex
	// Informer watches the blocklist ConfigMap for changes.
	Informer *informer.Informer[*corev1.ConfigMap]
}

func New() *Blocklist {
	return &Blocklist{
		entries: make(map[string]bool),
		agents:  make(map[string]string),
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

// TrackAgent records that the given agent is using the given fingerprint.
func (b *Blocklist) TrackAgent(fingerprint, agentName string) {
	b.agentsMu.Lock()
	defer b.agentsMu.Unlock()
	b.agents[fingerprint] = agentName
}

// AgentForFingerprint returns the agent name associated with the given
// fingerprint, or empty string if not tracked.
func (b *Blocklist) AgentForFingerprint(fingerprint string) string {
	b.agentsMu.RLock()
	defer b.agentsMu.RUnlock()
	return b.agents[fingerprint]
}
