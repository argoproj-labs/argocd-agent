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

package agent

import (
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/filter"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/util/glob"
)

// DefaultAppFilterChain returns a FilterChain for Application resources.
// This chain contains a set of default filters that the agent will
// evaluate for every change.
func (a *Agent) DefaultAppFilterChain() *filter.Chain[*v1alpha1.Application] {
	fc := &filter.Chain[*v1alpha1.Application]{}

	// Admit based on namespace of the application
	fc.AppendAdmitFilter(func(app *v1alpha1.Application) bool {
		nsList := append([]string{a.namespace}, a.allowedNamespaces...)
		if a.destinationBasedMapping {
			nsList = append(nsList, app.Namespace)
		}
		if !glob.MatchStringInList(nsList, app.Namespace, glob.REGEXP) {
			log().Warnf("namespace not allowed: %s", app.QualifiedName())
			return false
		}
		// if a.managedApps.IsManaged(app.QualifiedName()) {
		// 	log().Warnf("App is not managed: %s", app.QualifiedName())
		// 	return false
		// }
		return true
	})

	// Ignore applications that have the skip sync label
	fc.AppendAdmitFilter(func(app *v1alpha1.Application) bool {
		if v, ok := app.Labels[config.SkipSyncLabel]; ok && v == "true" {
			return false
		}
		return true
	})

	return fc
}

func (a *Agent) WithLabelFilter() {
}
