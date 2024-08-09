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

package clock

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_StandadClock(t *testing.T) {
	clock := StandardClock()
	var now, cnow time.Time
	// For the unlikely event that now and cnow are being called in another
	// second.
	for {
		now = time.Now()
		cnow = clock.Now()
		if now.Unix() == cnow.Unix() {
			break
		}
	}
}

func Test_SeededClock(t *testing.T) {
	t.Run("Seeded clock", func(t *testing.T) {
		seed, err := time.Parse(time.RFC3339, "2023-12-12T00:01:00Z")
		require.NoError(t, err)
		clock := SeededClock(seed)
		now := clock.Now()
		assert.Equal(t, 2023, now.Year())
		assert.Equal(t, time.Month(12), now.Month())
		assert.Equal(t, 12, now.Day())
	})
}
