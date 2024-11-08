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

package application

import (
	"context"
	"testing"
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	fakeappclient "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		expected := 2
		received := 0
		eventCh := make(chan interface{}, expected)
		ai, err := NewAppInformer(context.TODO(), fac, "test", WithNamespaces("test"), WithListAppCallback(func(app *v1alpha1.Application) bool {
			eventCh <- true
			return true
		}))
		require.NoError(t, err)
		go func() {
			ai.Start(context.TODO())
		}()
		ticker := time.NewTicker(1 * time.Second)
		running := true
		for running {
			select {
			case <-ticker.C:
				t.Fatal("callback timeout reached")
			case <-eventCh:
				received += 1
			default:
				if received >= expected {
					running = false
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		apps, err := ai.appLister.Applications("test").List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 2)
	})

	t.Run("List callback with filter", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset(app1, app2)
		expected := 2
		received := 0
		eventCh := make(chan interface{}, expected)
		ai, err := NewAppInformer(context.TODO(), fac, "test", WithNamespaces("test"), WithListAppCallback(func(app *v1alpha1.Application) bool {
			eventCh <- true
			if app.Name == "test1" {
				return true
			} else {
				return false
			}
		}))
		require.NoError(t, err)
		go func() {
			ai.appInformer.Start(context.TODO())
		}()
		ticker := time.NewTicker(1 * time.Second)
		running := true
		for running {
			select {
			case <-ticker.C:
				t.Fatal("callback timeout reached")
			case <-eventCh:
				received += 1
			default:
				if received >= expected {
					running = false
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		apps, err := ai.appLister.Applications("test").List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 1)
		app, err := ai.appLister.Applications("test").Get("test1")
		assert.NoError(t, err)
		assert.NotNil(t, app)
		app, err = ai.appLister.Applications("test").Get("test2")
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, app)
	})

	t.Run("Add callback", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset()
		eventCh := make(chan interface{})
		inf, err := NewAppInformer(context.TODO(), fac, "test", WithNewAppCallback(func(app *v1alpha1.Application) {
			eventCh <- true
		}))
		require.NoError(t, err)
		go func() {
			inf.appInformer.Start(context.TODO())
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
				_, _ = fac.ArgoprojV1alpha1().Applications("test").Create(context.TODO(), app1, v1.CreateOptions{})
				time.Sleep(100 * time.Millisecond)
			}
		}
		apps, err := inf.appLister.Applications("test").List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 1)
		app, err := inf.appLister.Applications("test").Get("test1")
		assert.NoError(t, err)
		assert.NotNil(t, app)
		app, err = inf.appLister.Applications("test").Get("test2")
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, app)
	})

	t.Run("Update callback", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset(app1)
		eventCh := make(chan interface{})
		inf, err := NewAppInformer(context.TODO(), fac, "test", WithUpdateAppCallback(func(old *v1alpha1.Application, new *v1alpha1.Application) {
			eventCh <- true
		}))
		require.NoError(t, err)
		go func() {
			inf.appInformer.Start(context.TODO())
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
				_, _ = fac.ArgoprojV1alpha1().Applications("test").Update(context.TODO(), appc, v1.UpdateOptions{})
			}
		}
		apps, err := inf.appLister.Applications("test").List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 1)
		napp, err := inf.appLister.Applications("test").Get("test1")
		assert.NoError(t, err)
		assert.NotNil(t, napp)
		assert.Equal(t, "hello", napp.Spec.Project)
	})

	t.Run("Delete callback", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset(app1)
		eventCh := make(chan interface{})
		inf, err := NewAppInformer(context.TODO(), fac, "test", WithDeleteAppCallback(func(app *v1alpha1.Application) {
			eventCh <- true
		}))
		require.NoError(t, err)
		go func() {
			inf.appInformer.Start(context.TODO())
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
				_ = fac.ArgoprojV1alpha1().Applications("test").Delete(context.TODO(), "test1", v1.DeleteOptions{})
			}
		}
		apps, err := inf.appLister.Applications("test").List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 0)
		napp, err := inf.appLister.Applications("test").Get("test1")
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, napp)
	})

	t.Run("Test admission in forbidden namespace", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset(app1)
		expected := 2
		received := 0
		eventCh := make(chan interface{}, expected)
		inf, err := NewAppInformer(context.TODO(), fac, "default", WithListAppCallback(func(app *v1alpha1.Application) bool {
			eventCh <- true
			return true
		}), WithNamespaces("kube-system"))
		require.NoError(t, err)
		go func() {
			inf.appInformer.Start(context.TODO())
		}()
		ticker := time.NewTicker(2 * time.Second)
		running := true
		for running {
			select {
			case <-ticker.C:
				// We do not expect the list callback to execute
				running = false
			case <-eventCh:
				time.Sleep(500 * time.Millisecond)
			default:
				if received >= expected {
					running = false
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		apps, err := inf.appLister.Applications("").List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 0)
		napp, err := inf.appLister.Applications("").Get("test1")
		assert.ErrorContains(t, err, "not found")
		assert.Nil(t, napp)
	})

	t.Run("Test admission in allowed namespace", func(t *testing.T) {
		fac := fakeappclient.NewSimpleClientset(app1)
		eventCh := make(chan interface{})
		inf, err := NewAppInformer(context.TODO(), fac, "default", WithListAppCallback(func(app *v1alpha1.Application) bool {
			eventCh <- true
			return true
		}), WithNamespaces("test"))
		require.NoError(t, err)
		go func() {
			inf.appInformer.Start(context.TODO())
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
		apps, err := inf.appLister.Applications("").List(labels.Everything())
		assert.NoError(t, err)
		assert.Len(t, apps, 1)
		napp, err := inf.appLister.Applications("test").Get("test1")
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
		i := &AppInformer{}
		i.SetNewAppCallback(addFn)
		i.NewAppCallback()(nil)
		assert.True(t, called)
	})

	t.Run("Update callback", func(t *testing.T) {
		called := false
		updFn := func(old *v1alpha1.Application, new *v1alpha1.Application) {
			called = true
		}
		i := &AppInformer{}
		i.SetUpdateAppCallback(updFn)
		i.UpdateAppCallback()(nil, nil)
		assert.True(t, called)
	})

	t.Run("Delete callback", func(t *testing.T) {
		called := false
		delFn := func(app *v1alpha1.Application) {
			called = true
		}
		i := &AppInformer{}
		i.SetDeleteAppCallback(delFn)
		i.DeleteAppCallback()(nil)
		assert.True(t, called)
	})

}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
