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

package cache

import (
	"context"
	"testing"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func Test_ApplicationSpecCache(t *testing.T) {
	t.Run("Test ApplicationSpec Cache", func(t *testing.T) {
		assert.Empty(t, appSpecCache.appSpec)

		log := logrus.New().WithContext(context.Background())

		expectedAppSpec := v1alpha1.ApplicationSpec{Project: "test-project"}
		SetApplicationSpec("test", expectedAppSpec, log)
		assert.Equal(t, 1, len(appSpecCache.appSpec))

		actualAppSpec, ok := GetApplicationSpec("test", log)
		assert.True(t, ok)
		assert.Equal(t, expectedAppSpec, actualAppSpec)

		DeleteApplicationSpec("test", log)
		assert.Empty(t, appSpecCache.appSpec)
	})
}
