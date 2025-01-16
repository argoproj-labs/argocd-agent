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

package metrics

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fetchMetricsOutput(t *testing.T) string {
	t.Helper()
	r, err := http.Get("http://127.0.0.1:31337/metrics")
	require.NoError(t, err)
	require.Equal(t, 200, r.StatusCode)
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	require.NoError(t, err)
	return string(body)
}

func Test_MetricsServer(t *testing.T) {
	t.Run("Start metrics server", func(t *testing.T) {
		errCh := StartMetricsServer(WithListener("127.0.0.1", 31337))
		NewPrincipalMetrics()
		ticker := time.NewTicker(time.Second)
		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-ticker.C:
			fetchMetricsOutput(t)
		}
	})
}
