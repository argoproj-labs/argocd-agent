package filter

import (
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func Test_Admit(t *testing.T) {
	fc := NewFilterChain()
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
	fc := NewFilterChain()
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
