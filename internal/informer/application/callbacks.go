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

import "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"

// ListAppsCallback is executed when the informer builds its cache. It receives
// all apps matching the configured label selector and must returned a list of
// apps to keep in the cache.
//
// Callbacks are executed after the AppInformer validated that the application
// is good for processing.
type ListAppsCallback func(apps []v1alpha1.Application) []v1alpha1.Application

// NewAppCallback is executed when a new app is determined by the underlying
// watcher.
//
// Callbacks are executed after the AppInformer validated that the application
// is allowed for processing.
type NewAppCallback func(app *v1alpha1.Application)

// UpdateAppCallback is executed when a change event for an app is determined
// by the underlying watcher.
//
// Callbacks are executed after the AppInformer validated that the application
// is allowed for processing.
type UpdateAppCallback func(old *v1alpha1.Application, new *v1alpha1.Application)

// DeleteAppCallback is executed when an app delete event is determined by the
// underlying watcher.
//
// Callbacks are executed after the AppInformer validated that the application
// is allowed for processing.
type DeleteAppCallback func(app *v1alpha1.Application)

// ErrorCallback is executed when the watcher events encounter an error
type ErrorCallback func(err error, fmt string, args ...string)

// NewAppCallback returns the new application callback of the AppInformer
func (i *AppInformer) NewAppCallback() NewAppCallback {
	i.lock.RLock()
	defer i.lock.RUnlock()
	return i.options.newCb
}

// SetNewAppCallback sets the new application callback for the AppInformer
func (i *AppInformer) SetNewAppCallback(cb NewAppCallback) {
	i.lock.Lock()
	defer i.lock.Unlock()
	i.options.newCb = cb
}

// UpdateAppCallback returns the update application callback of the AppInformer
func (i *AppInformer) UpdateAppCallback() UpdateAppCallback {
	i.lock.RLock()
	defer i.lock.RUnlock()
	return i.options.updateCb
}

// SetUpdateAppCallback sets the update application callback for the AppInformer
func (i *AppInformer) SetUpdateAppCallback(cb UpdateAppCallback) {
	i.lock.Lock()
	defer i.lock.Unlock()
	i.options.updateCb = cb
}

// DeleteAppCallback returns the delete application callback of the AppInformer
func (i *AppInformer) DeleteAppCallback() DeleteAppCallback {
	i.lock.RLock()
	defer i.lock.RUnlock()
	return i.options.deleteCb
}

// SetDeleteAppCallback sets the delete application callback for the AppInformer
func (i *AppInformer) SetDeleteAppCallback(cb DeleteAppCallback) {
	i.lock.Lock()
	defer i.lock.Unlock()
	i.options.deleteCb = cb
}
