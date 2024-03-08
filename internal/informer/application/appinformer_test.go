package application

import (
	"context"
	"testing"
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	fakeappclient "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func Test_AppInformer(t *testing.T) {
	app1 := &v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test1",
			Namespace: "test",
		},
		Spec: v1alpha1.ApplicationSpec{
			Source: &v1alpha1.ApplicationSource{
				RepoURL: "foo",
			},
		},
	}
	app2 := &v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test2",
			Namespace: "test",
		},
		Spec: v1alpha1.ApplicationSpec{
			Source: &v1alpha1.ApplicationSource{
				RepoURL: "bar",
			},
		},
	}
	t.Run("Simple list callback", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset(app1, app2)
		eventCh := make(chan interface{})
		inf := NewAppInformer(context.TODO(), fac, "test", WithListAppCallback(func(apps []v1alpha1.Application) []v1alpha1.Application {
			eventCh <- true
			return apps
		}))
		stopCh := make(chan struct{})
		go func() {
			inf.AppInformer.Run(stopCh)
		}()
		ticker := time.NewTicker(1 * time.Second)
		running := true
		for running {
			select {
			case <-ticker.C:
				t.Fatal("callback timeout reached")
			case <-eventCh:
				time.Sleep(500 * time.Millisecond)
				running = false
			default:
				time.Sleep(100 * time.Millisecond)
			}
		}
		apps, err := inf.AppLister.Applications(inf.options.namespace).List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 2)
	})

	t.Run("List callback with filter", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset(app1, app2)
		eventCh := make(chan interface{})
		inf := NewAppInformer(context.TODO(), fac, "test", WithListAppCallback(func(apps []v1alpha1.Application) []v1alpha1.Application {
			eventCh <- true
			return []v1alpha1.Application{*app1}
		}))
		stopCh := make(chan struct{})
		go func() {
			inf.AppInformer.Run(stopCh)
		}()
		ticker := time.NewTicker(1 * time.Second)
		running := true
		for running {
			select {
			case <-ticker.C:
				t.Fatal("callback timeout reached")
			case <-eventCh:
				time.Sleep(500 * time.Millisecond)
				running = false
			default:
				time.Sleep(100 * time.Millisecond)
			}
		}
		apps, err := inf.AppLister.Applications(inf.options.namespace).List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 1)
		app, err := inf.AppLister.Applications(inf.options.namespace).Get("test1")
		assert.NoError(t, err)
		assert.NotNil(t, app)
		app, err = inf.AppLister.Applications(inf.options.namespace).Get("test2")
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, app)
	})

	t.Run("Add callback", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset()
		eventCh := make(chan interface{})
		inf := NewAppInformer(context.TODO(), fac, "test", WithNewAppCallback(func(app *v1alpha1.Application) {
			eventCh <- true
		}))
		stopCh := make(chan struct{})
		go func() {
			inf.AppInformer.Run(stopCh)
		}()
		ticker := time.NewTicker(1 * time.Second)
		running := true
		for running {
			select {
			case <-ticker.C:
				t.Fatal("callback timeout reached")
			case <-eventCh:
				time.Sleep(500 * time.Millisecond)
				running = false
			default:
				_, _ = fac.ArgoprojV1alpha1().Applications(inf.options.namespace).Create(context.TODO(), app1, v1.CreateOptions{})
				time.Sleep(100 * time.Millisecond)
			}
		}
		apps, err := inf.AppLister.Applications(inf.options.namespace).List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 1)
		app, err := inf.AppLister.Applications(inf.options.namespace).Get("test1")
		assert.NoError(t, err)
		assert.NotNil(t, app)
		app, err = inf.AppLister.Applications(inf.options.namespace).Get("test2")
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, app)
	})

	t.Run("Update callback", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset(app1)
		eventCh := make(chan interface{})
		inf := NewAppInformer(context.TODO(), fac, "test", WithUpdateAppCallback(func(old *v1alpha1.Application, new *v1alpha1.Application) {
			eventCh <- true
		}))
		stopCh := make(chan struct{})
		go func() {
			inf.AppInformer.Run(stopCh)
		}()
		ticker := time.NewTicker(2 * time.Second)
		running := true
		for running {
			select {
			case <-ticker.C:
				t.Fatal("callback timeout reached")
			case <-eventCh:
				time.Sleep(500 * time.Millisecond)
				running = false
			default:
				time.Sleep(100 * time.Millisecond)
				appc := app1.DeepCopy()
				appc.Spec.Project = "hello"
				_, _ = fac.ArgoprojV1alpha1().Applications(inf.options.namespace).Update(context.TODO(), appc, v1.UpdateOptions{})
			}
		}
		apps, err := inf.AppLister.Applications(inf.options.namespace).List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 1)
		napp, err := inf.AppLister.Applications(inf.options.namespace).Get("test1")
		assert.NoError(t, err)
		assert.NotNil(t, napp)
		assert.Equal(t, "hello", napp.Spec.Project)
	})

	t.Run("Delete callback", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset(app1)
		eventCh := make(chan interface{})
		inf := NewAppInformer(context.TODO(), fac, "test", WithDeleteAppCallback(func(app *v1alpha1.Application) {
			eventCh <- true
		}))
		stopCh := make(chan struct{})
		go func() {
			inf.AppInformer.Run(stopCh)
		}()
		ticker := time.NewTicker(2 * time.Second)
		running := true
		for running {
			select {
			case <-ticker.C:
				t.Fatal("callback timeout reached")
			case <-eventCh:
				time.Sleep(500 * time.Millisecond)
				running = false
			default:
				time.Sleep(100 * time.Millisecond)
				_ = fac.ArgoprojV1alpha1().Applications(inf.options.namespace).Delete(context.TODO(), "test1", v1.DeleteOptions{})
			}
		}
		apps, err := inf.AppLister.Applications(inf.options.namespace).List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 0)
		napp, err := inf.AppLister.Applications(inf.options.namespace).Get("test1")
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, napp)
	})

	t.Run("Test admission in forbidden namespace", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset(app1)
		eventCh := make(chan interface{})
		inf := NewAppInformer(context.TODO(), fac, "default", WithListAppCallback(func(apps []v1alpha1.Application) []v1alpha1.Application {
			eventCh <- true
			return apps
		}), WithNamespaces("kube-system"))
		stopCh := make(chan struct{})
		go func() {
			inf.AppInformer.Run(stopCh)
		}()
		ticker := time.NewTicker(2 * time.Second)
		running := true
		for running {
			select {
			case <-ticker.C:
				t.Fatal("callback timeout reached")
			case <-eventCh:
				time.Sleep(500 * time.Millisecond)
				running = false
			default:
				time.Sleep(100 * time.Millisecond)
			}
		}
		apps, err := inf.AppLister.Applications("").List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 0)
		napp, err := inf.AppLister.Applications("").Get("test1")
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, napp)
	})

	t.Run("Test admission in allowed namespace", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset(app1)
		eventCh := make(chan interface{})
		inf := NewAppInformer(context.TODO(), fac, "default", WithListAppCallback(func(apps []v1alpha1.Application) []v1alpha1.Application {
			eventCh <- true
			return apps
		}), WithNamespaces("test"))
		stopCh := make(chan struct{})
		go func() {
			inf.AppInformer.Run(stopCh)
		}()
		ticker := time.NewTicker(2 * time.Second)
		running := true
		for running {
			select {
			case <-ticker.C:
				t.Fatal("callback timeout reached")
			case <-eventCh:
				time.Sleep(500 * time.Millisecond)
				running = false
			default:
				time.Sleep(100 * time.Millisecond)
			}
		}
		apps, err := inf.AppLister.Applications("").List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 1)
		napp, err := inf.AppLister.Applications("test").Get("test1")
		assert.NoError(t, err)
		assert.NotNil(t, napp)
	})
}

func Test_SetAppCallbacks(t *testing.T) {
	t.Run("Add callback", func(t *testing.T) {
		called := false
		addFn := func(app *v1alpha1.Application) {
			called = true
		}
		i := &AppInformer{options: &AppInformerOptions{}}
		i.SetNewAppCallback(addFn)
		i.NewAppCallback()(nil)
		assert.True(t, called)
	})

	t.Run("Update callback", func(t *testing.T) {
		called := false
		updFn := func(old *v1alpha1.Application, new *v1alpha1.Application) {
			called = true
		}
		i := &AppInformer{options: &AppInformerOptions{}}
		i.SetUpdateAppCallback(updFn)
		i.UpdateAppCallback()(nil, nil)
		assert.True(t, called)
	})

	t.Run("Delete callback", func(t *testing.T) {
		called := false
		delFn := func(app *v1alpha1.Application) {
			called = true
		}
		i := &AppInformer{options: &AppInformerOptions{}}
		i.SetDeleteAppCallback(delFn)
		i.DeleteAppCallback()(nil)
		assert.True(t, called)
	})

}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
