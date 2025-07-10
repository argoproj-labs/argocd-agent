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

package informer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/filter"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/pkg/client/clientset/versioned/fake"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

var apps []*v1alpha1.Application = []*v1alpha1.Application{
	{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test1",
			Namespace: "argocd",
		},
	},
	{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test2",
			Namespace: "other",
		},
	},
}

func newInformer(t *testing.T, namespace string, objs ...runtime.Object) *Informer[*v1alpha1.Application] {
	t.Helper()
	client := fake.NewSimpleClientset(objs...)
	i, err := NewInformer[*v1alpha1.Application](context.TODO(),
		WithListHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
			return client.ArgoprojV1alpha1().Applications(namespace).List(ctx, opts)
		}),
		WithWatchHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
			return client.ArgoprojV1alpha1().Applications(namespace).Watch(ctx, opts)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func Test_NewInformer(t *testing.T) {
	t.Run("Failed option returns error", func(t *testing.T) {
		rerr := errors.New("stop")
		i, err := NewInformer[*v1alpha1.Application](context.TODO(),
			func(i *Informer[*v1alpha1.Application]) error {
				return rerr
			},
		)
		assert.Nil(t, i)
		assert.ErrorIs(t, err, rerr)
	})
	t.Run("List func has to be set", func(t *testing.T) {
		i, err := NewInformer[*v1alpha1.Application](context.TODO())
		assert.Nil(t, i)
		assert.ErrorIs(t, err, ErrNoListFunc)
	})
	t.Run("Watch func has to be set", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		i, err := NewInformer[*v1alpha1.Application](context.TODO(),
			WithListHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
				return client.ArgoprojV1alpha1().Applications("").List(ctx, opts)
			}))
		assert.Nil(t, i)
		assert.ErrorIs(t, err, ErrNoWatchFunc)
	})
	t.Run("It starts and stops", func(t *testing.T) {
		i := newInformer(t, "", apps[0])
		go i.Start(context.TODO())
		i.WaitForSync(context.TODO())
		err := i.Stop()
		require.NoError(t, err)
	})

	t.Run("It doesn't start twice", func(t *testing.T) {
		i := newInformer(t, "")
		go i.Start(context.TODO())
		i.WaitForSync(context.TODO())
		err := i.Start(context.TODO())
		assert.ErrorIs(t, err, ErrRunning)
	})

	t.Run("It doesn't stop when not started", func(t *testing.T) {
		i := newInformer(t, "")
		err := i.Stop()
		assert.ErrorIs(t, err, ErrNotRunning)
	})

	t.Run("It doesn't wait endless for the sync", func(t *testing.T) {
		client := fake.NewSimpleClientset(apps[0])
		i, err := NewInformer[*v1alpha1.Application](context.TODO(),
			WithListHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
				return client.ArgoprojV1alpha1().Applications("").List(ctx, opts)
			}),
			WithWatchHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
				return client.ArgoprojV1alpha1().Applications("").Watch(ctx, opts)
			}),
		)
		require.NotNil(t, i)
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		err = i.WaitForSync(ctx)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func Test_InformerScope(t *testing.T) {
	t.Run("It adheres to namespace scope", func(t *testing.T) {
		client := fake.NewSimpleClientset(apps[0], apps[1])
		added := 0
		listed := 0
		i, err := NewInformer[*v1alpha1.Application](context.TODO(),
			WithListHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
				objs, err := client.ArgoprojV1alpha1().Applications("argocd").List(ctx, opts)
				listed = len(objs.Items)
				return objs, err
			}),
			WithWatchHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
				return client.ArgoprojV1alpha1().Applications("argocd").Watch(ctx, opts)
			}),
			WithAddHandler[*v1alpha1.Application](func(obj *v1alpha1.Application) {
				added += 1
			}),
		)
		require.NotNil(t, i)
		require.NoError(t, err)
		go i.Start(context.TODO())
		i.WaitForSync(context.TODO())
		assert.Equal(t, 1, listed)
		assert.Equal(t, 1, added)
	})
	t.Run("It adheres to cluster scope", func(t *testing.T) {
		listed := 0
		added := 0
		client := fake.NewSimpleClientset(apps[0], apps[1])
		i, err := NewInformer[*v1alpha1.Application](context.TODO(),
			WithListHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
				objs, err := client.ArgoprojV1alpha1().Applications("").List(ctx, opts)
				listed = len(objs.Items)
				return objs, err
			}),
			WithWatchHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
				return client.ArgoprojV1alpha1().Applications("").Watch(ctx, opts)
			}),
			WithAddHandler[*v1alpha1.Application](func(obj *v1alpha1.Application) {
				added += 1
			}),
		)
		require.NotNil(t, i)
		require.NoError(t, err)
		go i.Start(context.TODO())
		i.WaitForSync(context.TODO())
		assert.Equal(t, 2, listed)
		assert.Equal(t, 2, added)
	})

	t.Run("It adheres to filters on all callbacks", func(t *testing.T) {
		listed := 0
		addch := make(chan int, 2)
		updch := make(chan int, 2)
		delch := make(chan int, 2)
		client := fake.NewSimpleClientset(apps[0], apps[1])
		filters := filter.NewFilterChain[*v1alpha1.Application]()
		filters.AppendAdmitFilter(func(res *v1alpha1.Application) bool {
			return res.Name == "test1"
		})
		i, err := NewInformer[*v1alpha1.Application](context.TODO(),
			WithListHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
				// The lister is cluster scoped, and lists all available resources
				objs, err := client.ArgoprojV1alpha1().Applications("").List(ctx, opts)
				listed = len(objs.Items)
				return objs, err
			}),
			WithWatchHandler[*v1alpha1.Application](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
				return client.ArgoprojV1alpha1().Applications("").Watch(ctx, opts)
			}),
			WithAddHandler[*v1alpha1.Application](func(obj *v1alpha1.Application) {
				addch <- 1
			}),
			WithUpdateHandler[*v1alpha1.Application](func(old, new *v1alpha1.Application) {
				updch <- 1
			}),
			WithDeleteHandler[*v1alpha1.Application](func(obj *v1alpha1.Application) {
				delch <- 1
			}),
			WithFilters[*v1alpha1.Application](filters),
		)
		require.NotNil(t, i)
		require.NoError(t, err)
		go i.Start(context.TODO())
		i.WaitForSync(context.TODO())
		assert.Equal(t, 2, listed)
		added := <-addch
		assert.Equal(t, 1, added)
		napp1 := apps[0].DeepCopy()
		napp2 := apps[1].DeepCopy()
		_, err = client.ArgoprojV1alpha1().Applications("argocd").Update(context.TODO(), napp1, v1.UpdateOptions{})
		require.NoError(t, err)
		_, err = client.ArgoprojV1alpha1().Applications("other").Update(context.TODO(), napp2, v1.UpdateOptions{})
		require.NoError(t, err)
		updated := <-updch
		assert.Equal(t, 1, updated)
		err = client.ArgoprojV1alpha1().Applications("argocd").Delete(context.TODO(), "test1", v1.DeleteOptions{})
		require.NoError(t, err)
		deleted := <-delch
		assert.Equal(t, 1, deleted)
	})

}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
