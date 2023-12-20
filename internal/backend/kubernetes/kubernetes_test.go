package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	fakeappclient "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	"github.com/jannfis/argocd-agent/internal/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wI2L/jsondiff"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func Test_NewKubernetes(t *testing.T) {
	t.Run("New with patch capability", func(t *testing.T) {
		k := NewKubernetesBackend(nil, nil, true)
		assert.NotNil(t, k)
		assert.True(t, k.SupportsPatch())
	})
	t.Run("New without patch capability", func(t *testing.T) {
		k := NewKubernetesBackend(nil, nil, false)
		assert.NotNil(t, k)
		assert.False(t, k.SupportsPatch())
	})

}

func mkApps() []runtime.Object {
	apps := make([]runtime.Object, 10)
	for i := 0; i < 10; i += 1 {
		apps[i] = runtime.Object(&v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "app",
				Namespace: fmt.Sprintf("ns%d", i),
			},
		})
	}
	return apps
}

func Test_List(t *testing.T) {
	apps := mkApps()
	t.Run("No apps", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset()
		k := NewKubernetesBackend(fakeAppC, nil, true)
		apps, err := k.List(context.TODO(), backend.ApplicationSelector{})
		require.NoError(t, err)
		assert.Len(t, apps, 0)
	})
	t.Run("Few apps", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		apps, err := k.List(context.TODO(), backend.ApplicationSelector{})
		require.NoError(t, err)
		assert.Len(t, apps, 10)
	})
	t.Run("Apps with matching selector", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		apps, err := k.List(context.TODO(), backend.ApplicationSelector{Namespaces: []string{"ns1", "ns2"}})
		require.NoError(t, err)
		assert.Len(t, apps, 2)
	})

	t.Run("Apps with non-matching selector", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		apps, err := k.List(context.TODO(), backend.ApplicationSelector{Namespaces: []string{"ns11", "ns12"}})
		require.NoError(t, err)
		assert.Len(t, apps, 0)
	})
}

func Test_Create(t *testing.T) {
	apps := mkApps()
	t.Run("Create app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		app, err := k.Create(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "foo", Namespace: "bar"}})
		assert.NoError(t, err)
		assert.NotNil(t, app)
	})
	t.Run("Create existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		app, err := k.Create(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app", Namespace: "ns1"}})
		assert.ErrorContains(t, err, "exists")
		assert.Nil(t, app)
	})
}

func Test_Get(t *testing.T) {
	apps := mkApps()
	t.Run("Get existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		app, err := k.Get(context.TODO(), "app", "ns1")
		assert.NoError(t, err)
		assert.NotNil(t, app)
	})
	t.Run("Get non-existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		app, err := k.Get(context.TODO(), "foo", "ns1")
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, app)
	})
}

func Test_Delete(t *testing.T) {
	apps := mkApps()
	t.Run("Delete existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		err := k.Delete(context.TODO(), "app", "ns1")
		assert.NoError(t, err)
	})
	t.Run("Delete non-existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		err := k.Delete(context.TODO(), "app", "ns10")
		assert.ErrorContains(t, err, "not found")
	})
}

func Test_Update(t *testing.T) {
	apps := mkApps()
	t.Run("Update existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		app, err := k.Update(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app", Namespace: "ns1"}})
		assert.NoError(t, err)
		assert.NotNil(t, app)
	})
	t.Run("Update non-existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		app, err := k.Update(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app", Namespace: "ns10"}})
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, app)
	})
}

func Test_Patch(t *testing.T) {
	apps := mkApps()
	t.Run("Patch existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		p := jsondiff.Patch{jsondiff.Operation{Type: "add", Path: "/foo", Value: "bar"}}
		jsonpatch, err := json.Marshal(p)
		require.NoError(t, err)
		app, err := k.Patch(context.TODO(), "app", "ns1", jsonpatch)
		assert.NoError(t, err)
		assert.NotNil(t, app)
	})
	t.Run("Update non-existing app", func(t *testing.T) {
		fakeAppC := fakeappclient.NewSimpleClientset(apps...)
		k := NewKubernetesBackend(fakeAppC, nil, true)
		app, err := k.Update(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app", Namespace: "ns10"}})
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, app)
	})
}
