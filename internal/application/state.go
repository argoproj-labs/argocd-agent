package application

import "fmt"

// ClearManaged clears the managed apps
func (m *Manager) ClearManaged() {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.managedApps = make(map[string]bool)
}

func (m *Manager) ClearIgnored() {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.observedApp = make(map[string]string)
}

// IsManaged returns whether the app appName is currently managed by this agent
func (m *Manager) IsManaged(appName string) bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	_, ok := m.managedApps[appName]
	return ok
}

// Manage marks the app appName as being managed by this agent
func (m *Manager) Manage(appName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	_, ok := m.managedApps[appName]
	if !ok {
		m.managedApps[appName] = true
		return nil
	} else {
		return fmt.Errorf("app %s is already managed", appName)
	}
}

// Unmanage marks the app appName as not being managed by this agent
func (m *Manager) Unmanage(appName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	_, ok := m.managedApps[appName]
	if !ok {
		return fmt.Errorf("app %s is not managed", appName)
	} else {
		delete(m.managedApps, appName)
		return nil
	}
}

// IgnoreChange adds a particular version for the application named appName to
// list of changes to ignore.
func (m *Manager) IgnoreChange(appName string, version string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	if cur, ok := m.observedApp[appName]; ok && cur == version {
		return fmt.Errorf("version %s is already ignored for %s", version, appName)
	} else {
		m.observedApp[appName] = version
		return nil
	}
}

// IsChangeIgnored returns true if the version for appName is already being
// ignored.
func (m *Manager) IsChangeIgnored(appName string, version string) bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	last, ok := m.observedApp[appName]
	if !ok {
		return false
	}
	return last == version
}

func (m *Manager) UnignoreChange(appName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	if _, ok := m.observedApp[appName]; ok {
		delete(m.observedApp, appName)
		return nil
	} else {
		return fmt.Errorf("no generation recorded for app %s", appName)
	}
}
