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
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/filter"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

type InformerOption[T runtime.Object] func(i *Informer[T]) error
type ListFunc func(ctx context.Context, opts v1.ListOptions) (runtime.Object, error)
type WatchFunc func(ctx context.Context, opts v1.ListOptions) (watch.Interface, error)

// WithListHandler sets the list function for the watcher
func WithListHandler[T runtime.Object](f ListFunc) InformerOption[T] {
	return func(i *Informer[T]) error {
		i.listFunc = f
		return nil
	}
}

// WithWatchHandler sets the watch function for the watcher
func WithWatchHandler[T runtime.Object](f WatchFunc) InformerOption[T] {
	return func(i *Informer[T]) error {
		i.watchFunc = f
		return nil
	}
}

// WithAddHandler sets the callback to be executed when a resource is added
func WithAddHandler[T runtime.Object](f AddHandler[T]) InformerOption[T] {
	return func(i *Informer[T]) error {
		i.onAdd = f
		return nil
	}
}

// WithUpdateHandler sets the callback to be executed when a resource is updated
func WithUpdateHandler[T runtime.Object](f UpdateHandler[T]) InformerOption[T] {
	return func(i *Informer[T]) error {
		i.onUpdate = f
		return nil
	}
}

// WithDeleteHandler sets the callback to be executed when a resource is deleted
func WithDeleteHandler[T runtime.Object](f DeleteHandler[T]) InformerOption[T] {
	return func(i *Informer[T]) error {
		i.onDelete = f
		return nil
	}
}

// WithNamespaceScope sets the scope of the informer to namespace. If namespace
// is the empty string, the informer will be cluster scoped (Which is also the
// default)
func WithNamespaceScope[T runtime.Object](namespace string) InformerOption[T] {
	return func(i *Informer[T]) error {
		i.namespace = namespace
		return nil
	}
}

// WithMetrics sets the informer metrics to be used by this informer
func WithMetrics[T runtime.Object](registry *prometheus.Registry, metrics *metrics.InformerMetrics) InformerOption[T] {
	return func(i *Informer[T]) error {
		i.metrics = metrics
		return nil
	}
}

// WithFilters sets the filter chain used by the informer to decide whether to
// admit specific resources. The filter chain must be for the same resource
// type T as the informer.
func WithFilters[T runtime.Object](fc *filter.Chain[T]) InformerOption[T] {
	return func(i *Informer[T]) error {
		i.filters = fc
		return nil
	}
}

// WithResyncPeriod sets the resync period for the informer to d
func WithResyncPeriod[T runtime.Object](d time.Duration) InformerOption[T] {
	return func(i *Informer[T]) error {
		i.resyncPeriod = d
		return nil
	}
}

// WithGroupResource sets the group and resource for the informer's lister.
func WithGroupResource[T runtime.Object](group, resource string) InformerOption[T] {
	return func(i *Informer[T]) error {
		i.groupResource = schema.GroupResource{
			Group:    group,
			Resource: resource,
		}
		return nil
	}
}
