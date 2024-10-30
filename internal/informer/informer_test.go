package informer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

func Test_Informer(t *testing.T) {
	listFunc := func(options v1.ListOptions, namespace string) (runtime.Object, error) {
		return &v1alpha1.ApplicationList{Items: []v1alpha1.Application{}}, nil
	}
	watcher := watch.NewFake()
	nopWatchFunc := func(options v1.ListOptions, namespace string) (watch.Interface, error) {
		return watcher, nil
	}
	t.Run("Error when no list function given", func(t *testing.T) {
		i, err := NewGenericInformer(&v1alpha1.Application{})
		assert.Nil(t, i)
		assert.ErrorContains(t, err, "without list function")
	})
	t.Run("Error when no watch function given", func(t *testing.T) {
		i, err := NewGenericInformer(&v1alpha1.Application{},
			WithListCallback(listFunc))
		assert.Nil(t, i)
		assert.ErrorContains(t, err, "without watch function")
	})

	t.Run("Error on option", func(t *testing.T) {
		opt := func(i *GenericInformer) error {
			return errors.New("some error")
		}
		i, err := NewGenericInformer(&v1alpha1.Application{}, opt)
		assert.Nil(t, i)
		assert.ErrorContains(t, err, "some error")
	})

	t.Run("Instantiate generic informer without processing", func(t *testing.T) {
		i, err := NewGenericInformer(&v1alpha1.Application{},
			WithListCallback(listFunc),
			WithWatchCallback(nopWatchFunc),
		)
		require.NotNil(t, i)
		require.NoError(t, err)
		err = i.Start(context.Background())
		assert.NoError(t, err)
		assert.True(t, i.IsRunning())
		err = i.Stop()
		assert.NoError(t, err)
		assert.False(t, i.IsRunning())
	})

	t.Run("Cannot start informer twice", func(t *testing.T) {
		ctl := fakeGenericInformer(t)
		err := ctl.i.Start(context.TODO())
		assert.NoError(t, err)
		err = ctl.i.Start(context.TODO())
		assert.Error(t, err)
	})

	t.Run("Can stop informer only when running", func(t *testing.T) {
		ctl := fakeGenericInformer(t)
		// Not running yet
		err := ctl.i.Stop()
		assert.Error(t, err)
		err = ctl.i.Start(context.TODO())
		assert.NoError(t, err)
		// Running
		err = ctl.i.Stop()
		assert.NoError(t, err)
		// Not running
		err = ctl.i.Stop()
		assert.Error(t, err)
	})

	t.Run("Run callbacks", func(t *testing.T) {
		ctl := fakeGenericInformer(t)
		err := ctl.i.Start(context.TODO())
		assert.NoError(t, err)
		app := &v1alpha1.Application{}
		ctl.watcher.Add(app)
		ctl.watcher.Modify(app)
		ctl.watcher.Delete(app)
		added, updated, deleted := requireCallbacks(t, ctl)
		assert.True(t, added)
		assert.True(t, updated)
		assert.True(t, deleted)
		err = ctl.i.Stop()
		assert.NoError(t, err)
	})
}

type informerCtl struct {
	add     chan (bool)
	upd     chan (bool)
	del     chan (bool)
	i       *GenericInformer
	watcher *watch.FakeWatcher
}

// fakeGenericInformer returns a generic informer suitable for unit testing.
func fakeGenericInformer(t *testing.T, opts ...InformerOption) *informerCtl {
	var err error
	ctl := &informerCtl{
		add: make(chan bool),
		upd: make(chan bool),
		del: make(chan bool),
	}
	t.Helper()
	listFunc := func(options v1.ListOptions, namespace string) (runtime.Object, error) {
		return &v1alpha1.ApplicationList{Items: []v1alpha1.Application{}}, nil
	}
	ctl.watcher = watch.NewFake()
	nopWatchFunc := func(options v1.ListOptions, namespace string) (watch.Interface, error) {
		return ctl.watcher, nil
	}

	ctl.i, err = NewGenericInformer(&v1alpha1.Application{},
		append([]InformerOption{
			WithListCallback(listFunc),
			WithWatchCallback(nopWatchFunc),
			WithAddCallback(func(obj interface{}) {
				ctl.add <- true
			}),
			WithUpdateCallback(func(newObj, oldObj interface{}) {
				ctl.upd <- true
			}),
			WithDeleteCallback(func(obj interface{}) {
				ctl.del <- true
			}),
		}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return ctl
}

func requireCallbacks(t *testing.T, ctl *informerCtl) (added, updated, deleted bool) {
	t.Helper()
	run := true
	tick := time.NewTicker(1 * time.Second)
	added = false
	for run {
		select {
		case added = <-ctl.add:
		case updated = <-ctl.upd:
		case deleted = <-ctl.del:
		case <-tick.C:
			run = false
		}
	}
	return
}

func Test_NamespaceRestrictions(t *testing.T) {
	app := &v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test",
			Namespace: "argocd",
		},
	}
	t.Run("Namespace is allowed", func(t *testing.T) {
		ctl := fakeGenericInformer(t)
		assert.True(t, ctl.i.isNamespaceAllowed(app))
		ctl = fakeGenericInformer(t, WithNamespaces("argocd"))
		assert.True(t, ctl.i.isNamespaceAllowed(app))
	})
	t.Run("Namespace is not allowed", func(t *testing.T) {
		ctl := fakeGenericInformer(t, WithNamespaces("argocd"))
		app := app.DeepCopy()
		app.Namespace = "foobar"
		assert.False(t, ctl.i.isNamespaceAllowed(app))
	})
	t.Run("Adding a namespace works", func(t *testing.T) {
		ctl := fakeGenericInformer(t, WithNamespaces("foobar"))
		assert.False(t, ctl.i.isNamespaceAllowed(app))
		err := ctl.i.AddNamespace("argocd")
		require.NoError(t, err)
		assert.True(t, ctl.i.isNamespaceAllowed(app))
		// May not be added a second time
		err = ctl.i.AddNamespace("argocd")
		require.Error(t, err)
	})
	t.Run("Removing a namespace works", func(t *testing.T) {
		ctl := fakeGenericInformer(t, WithNamespaces("argocd", "foobar"))
		assert.True(t, ctl.i.isNamespaceAllowed(app))
		err := ctl.i.RemoveNamespace("argocd")
		require.NoError(t, err)
		assert.False(t, ctl.i.isNamespaceAllowed(app))
		// May not be removed if not existing
		err = ctl.i.RemoveNamespace("argocd")
		require.Error(t, err)
	})
	t.Run("Prevent resources in non-allowed namespaces to execute add handlers", func(t *testing.T) {
		ctl := fakeGenericInformer(t, WithNamespaces("argocd"))
		err := ctl.i.Start(context.TODO())
		require.NoError(t, err)
		ctl.watcher.Add(&v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "foobar",
			},
		})
		added, _, _ := requireCallbacks(t, ctl)
		assert.False(t, added)
	})
}

func Test_watchAndListNamespaces(t *testing.T) {
	t.Run("Watch namespace is empty when no namespace is given", func(t *testing.T) {
		ctl := fakeGenericInformer(t)
		assert.Empty(t, ctl.i.watchAndListNamespace())
	})
	t.Run("Watch namespace is same as single namespace constraint", func(t *testing.T) {
		ctl := fakeGenericInformer(t, WithNamespaces("argocd"))
		assert.Equal(t, "argocd", ctl.i.watchAndListNamespace())
	})

	t.Run("Watch namespace is empty when multiple namespaces are given", func(t *testing.T) {
		ctl := fakeGenericInformer(t, WithNamespaces("argocd", "foobar"))
		assert.Empty(t, ctl.i.watchAndListNamespace())
	})
}
