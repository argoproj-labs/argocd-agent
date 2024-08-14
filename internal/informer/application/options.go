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
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/filter"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
)

// AppInformerOptions is a set of options for the AppInformer.
//
// Options should not be modified concurrently, they are not implemented in a
// thread-safe way.
type AppInformerOptions struct {
	// namespace will be set to "", if len(namespaces) > 0
	namespace  string
	namespaces []string
	appMetrics *metrics.ApplicationWatcherMetrics
	filters    *filter.Chain
	resync     time.Duration
	listCb     ListAppsCallback
	newCb      NewAppCallback
	updateCb   UpdateAppCallback
	deleteCb   DeleteAppCallback
	errorCb    ErrorCallback
}

type AppInformerOption func(o *AppInformerOptions)

// WithMetrics sets the ApplicationMetrics instance to be used by the AppInformer
func WithMetrics(m *metrics.ApplicationWatcherMetrics) AppInformerOption {
	return func(o *AppInformerOptions) {
		o.appMetrics = m
	}
}

// WithNamespaces sets additional namespaces to be watched by the AppInformer
func WithNamespaces(namespaces ...string) AppInformerOption {
	return func(o *AppInformerOptions) {
		o.namespaces = namespaces
	}
}

// WithFilterChain sets the FilterChain to be used by the AppInformer
func WithFilterChain(fc *filter.Chain) AppInformerOption {
	return func(o *AppInformerOptions) {
		o.filters = fc
	}
}

// WithListAppCallback sets the ListAppsCallback to be called by the AppInformer
func WithListAppCallback(cb ListAppsCallback) AppInformerOption {
	return func(o *AppInformerOptions) {
		o.listCb = cb
	}
}

// WithNewAppCallback sets the NewAppCallback to be executed by the AppInformer
func WithNewAppCallback(cb NewAppCallback) AppInformerOption {
	return func(o *AppInformerOptions) {
		o.newCb = cb
	}
}

// WithUpdateAppCallback sets the UpdateAppCallback to be executed by the AppInformer
func WithUpdateAppCallback(cb UpdateAppCallback) AppInformerOption {
	return func(o *AppInformerOptions) {
		o.updateCb = cb
	}
}

// WithDeleteAppCallback sets the DeleteAppCallback to be executed by the AppInformer
func WithDeleteAppCallback(cb DeleteAppCallback) AppInformerOption {
	return func(o *AppInformerOptions) {
		o.deleteCb = cb
	}
}

// WithErrorCallback sets the ErrorCallback to be executed by the AppInformer
func WithErrorCallback(cb ErrorCallback) AppInformerOption {
	return func(o *AppInformerOptions) {
		o.errorCb = cb
	}
}

// WithResyncDuration sets the resync duration to be used by the AppInformer's lister
func WithResyncDuration(d time.Duration) AppInformerOption {
	return func(o *AppInformerOptions) {
		o.resync = d
	}
}
