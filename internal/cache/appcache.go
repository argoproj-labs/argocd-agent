// Copyright 2025 The argocd-agent Authors
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

package cache

import (
	"sync"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/types"
)

// ApplicationSpecCache type is for caching Argo CD application spec to keep
// application in sync with last known state of principal application
type ApplicationSpecCache struct {
	lock sync.RWMutex

	// key: source UID of app
	// value: application spec
	// - acquire 'lock' before accessing
	appSpec map[types.UID]v1alpha1.ApplicationSpec
}

// Initialize instance of ApplicationSpecCache.
var appSpecCache = &ApplicationSpecCache{
	appSpec: make(map[types.UID]v1alpha1.ApplicationSpec),
}

// SetApplicationSpec inserts/updates app spec in cache
func SetApplicationSpec(sourceUID types.UID, app v1alpha1.ApplicationSpec, log *logrus.Entry) {
	appSpecCache.lock.Lock()
	defer appSpecCache.lock.Unlock()

	log.Tracef("Setting value in ApplicationSpec cache: '%s' %v", sourceUID, app)

	appSpecCache.appSpec[sourceUID] = app
}

// GetApplicationSpec returns cached app spec
func GetApplicationSpec(sourceUID types.UID, log *logrus.Entry) (v1alpha1.ApplicationSpec, bool) {

	appSpecCache.lock.RLock()
	defer appSpecCache.lock.RUnlock()

	appSpec, ok := appSpecCache.appSpec[sourceUID]

	log.Tracef("Retrieved value from ApplicationSpec cache: '%s' %v", sourceUID, appSpec)
	return appSpec, ok
}

// DeleteApplicationSpec removes app spec from cache
func DeleteApplicationSpec(sourceUID types.UID, log *logrus.Entry) {
	appSpecCache.lock.Lock()
	defer appSpecCache.lock.Unlock()

	log.Tracef("Deleting value from ApplicationSpec cache: '%s'", sourceUID)

	delete(appSpecCache.appSpec, sourceUID)
}
