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
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/filter"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

func Test_Options(t *testing.T) {
	t.Run("WithListerFunc", func(t *testing.T) {
		i := &Informer[runtime.Object]{}
		o := WithListHandler[runtime.Object](func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error) {
			return &v1alpha1.Application{}, nil
		})
		err := o(i)
		assert.NoError(t, err)
		assert.NotNil(t, i.listFunc)
	})
	t.Run("WithListerFunc", func(t *testing.T) {
		i := &Informer[runtime.Object]{}
		o := WithWatchHandler[runtime.Object](func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
			return watch.NewEmptyWatch(), nil
		})
		err := o(i)
		assert.NoError(t, err)
		assert.NotNil(t, i.watchFunc)
	})
	t.Run("WithAddHandler", func(t *testing.T) {
		i := &Informer[runtime.Object]{}
		o := WithAddHandler[runtime.Object](func(obj runtime.Object) {
		})
		err := o(i)
		assert.NoError(t, err)
		assert.NotNil(t, i.onAdd)
	})
	t.Run("WithUpdateHandler", func(t *testing.T) {
		i := &Informer[runtime.Object]{}
		o := WithUpdateHandler[runtime.Object](func(obj1 runtime.Object, obj2 runtime.Object) {
		})
		err := o(i)
		assert.NoError(t, err)
		assert.NotNil(t, i.onUpdate)
	})
	t.Run("WithDeleteHandler", func(t *testing.T) {
		i := &Informer[runtime.Object]{}
		o := WithDeleteHandler[runtime.Object](func(obj runtime.Object) {
		})
		err := o(i)
		assert.NoError(t, err)
		assert.NotNil(t, i.onDelete)
	})
	t.Run("WithNamespaceScope", func(t *testing.T) {
		i := &Informer[runtime.Object]{}
		o := WithNamespaceScope[runtime.Object]("hello")
		err := o(i)
		assert.NoError(t, err)
		assert.Equal(t, "hello", i.namespace)
	})

	t.Run("WithFilters", func(t *testing.T) {
		i := &Informer[runtime.Object]{}
		o := WithFilters[runtime.Object](filter.NewFilterChain[runtime.Object]())
		err := o(i)
		assert.NoError(t, err)
		assert.NotNil(t, i.filters)
	})

	t.Run("WithResyncPeriod", func(t *testing.T) {
		i := &Informer[runtime.Object]{}
		o := WithResyncPeriod[runtime.Object](360)
		err := o(i)
		assert.NoError(t, err)
		assert.Equal(t, time.Duration(360), i.resyncPeriod)
	})
}
