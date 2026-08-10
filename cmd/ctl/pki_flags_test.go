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

package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func Test_rejectUnusedKeyGenFlags(t *testing.T) {
	t.Run("allows defaults when not generating", func(t *testing.T) {
		cmd := &cobra.Command{Use: "test"}
		var algorithm string
		var size int
		addKeyGenFlags(cmd, &algorithm, &size)

		assert.NotPanics(t, func() {
			rejectUnusedKeyGenFlags(cmd, false, "are only used when generating certificates from the PKI")
		})
	})

	t.Run("allows explicit flags when generating", func(t *testing.T) {
		cmd := &cobra.Command{Use: "test"}
		var algorithm string
		var size int
		addKeyGenFlags(cmd, &algorithm, &size)
		assert.NoError(t, cmd.Flags().Set("key-algorithm", "ed25519"))

		assert.NotPanics(t, func() {
			rejectUnusedKeyGenFlags(cmd, true, "are only used when generating certificates from the PKI")
		})
	})
}
