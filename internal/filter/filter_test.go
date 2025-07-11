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

package filter

import (
	"testing"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func Test_Admit(t *testing.T) {
	fc := NewFilterChain[*v1alpha1.Application]()
	app := &v1alpha1.Application{}
	t.Run("Empty FilterChain always admits", func(t *testing.T) {
		assert.True(t, fc.Admit(app))
	})
	t.Run("Some filter admits", func(t *testing.T) {
		fc.AppendAdmitFilter(func(app *v1alpha1.Application) bool {
			return true
		})
		assert.True(t, fc.Admit(app))
	})
	t.Run("Any filter blocks admission", func(t *testing.T) {
		fc.AppendAdmitFilter(func(app *v1alpha1.Application) bool {
			return false
		})
		assert.False(t, fc.Admit(app))
	})
}
func Test_ProcessChange(t *testing.T) {
	fc := NewFilterChain[*v1alpha1.Application]()
	app := &v1alpha1.Application{}
	t.Run("Empty FilterChain always allows change", func(t *testing.T) {
		assert.True(t, fc.ProcessChange(app, app))
	})
	t.Run("Some filter admits", func(t *testing.T) {
		fc.AppendAdmitFilter(func(app *v1alpha1.Application) bool {
			return true
		})
		assert.True(t, fc.Admit(app))
	})
	t.Run("Filter blocks change processing", func(t *testing.T) {
		fc.AppendChangeFilter(func(old, new *v1alpha1.Application) bool {
			return false
		})
		assert.False(t, fc.ProcessChange(app, app))
	})
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
