package agent

import (
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/util/glob"
	"github.com/argoproj-labs/argocd-agent/internal/filter"
)

// DefaultFilterChain returns a FilterChain with a set of default filters that
// the agent will evaluate for every change.
func (a *Agent) DefaultFilterChain() *filter.Chain {
	fc := &filter.Chain{}

	// Admit based on namespace of the application
	fc.AppendAdmitFilter(func(app *v1alpha1.Application) bool {
		if !glob.MatchStringInList(append([]string{a.namespace}, a.options.namespaces...), app.Namespace, false) {
			log().Warnf("namespace not allowed: %s", app.QualifiedName())
			return false
		}
		// if a.managedApps.IsManaged(app.QualifiedName()) {
		// 	log().Warnf("App is not managed: %s", app.QualifiedName())
		// 	return false
		// }
		return true
	})

	return fc
}

func (a *Agent) WithLabelFilter() {
}
