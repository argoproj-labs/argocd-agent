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

package appproject

import "fmt"

// ClearManaged clears the managed apps
func (m *AppProjectManager) ClearManaged() {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.managedAppProjects = make(map[string]bool)
}

func (m *AppProjectManager) ClearIgnored() {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.observedAppProjects = make(map[string]string)
}

// IsManaged returns whether the app projectName is currently managed by this agent
func (m *AppProjectManager) IsManaged(projectName string) bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	_, ok := m.managedAppProjects[projectName]
	return ok
}

// Manage marks the app projectName as being managed by this agent
func (m *AppProjectManager) Manage(projectName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	_, ok := m.managedAppProjects[projectName]
	if !ok {
		m.managedAppProjects[projectName] = true
		return nil
	} else {
		return fmt.Errorf("app %s is already managed", projectName)
	}
}

// Unmanage marks the app projectName as not being managed by this agent
func (m *AppProjectManager) Unmanage(projectName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	_, ok := m.managedAppProjects[projectName]
	if !ok {
		return fmt.Errorf("app %s is not managed", projectName)
	} else {
		delete(m.managedAppProjects, projectName)
		return nil
	}
}

// IgnoreChange adds a particular version for the application named projectName to
// list of changes to ignore.
func (m *AppProjectManager) IgnoreChange(projectName string, version string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	if cur, ok := m.observedAppProjects[projectName]; ok && cur == version {
		return fmt.Errorf("version %s is already ignored for %s", version, projectName)
	} else {
		m.observedAppProjects[projectName] = version
		return nil
	}
}

// IsChangeIgnored returns true if the version for projectName is already being
// ignored.
func (m *AppProjectManager) IsChangeIgnored(projectName string, version string) bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	last, ok := m.observedAppProjects[projectName]
	if !ok {
		return false
	}
	return last == version
}

func (m *AppProjectManager) UnignoreChange(projectName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	if _, ok := m.observedAppProjects[projectName]; ok {
		delete(m.observedAppProjects, projectName)
		return nil
	} else {
		return fmt.Errorf("no generation recorded for app %s", projectName)
	}
}
