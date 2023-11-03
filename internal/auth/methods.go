package auth

import (
	"fmt"
	"sync"
)

// Methods provide a thread-safe way to register and look up available auth
// methods.
type Methods struct {
	lock    sync.RWMutex
	methods map[string]Method
}

func NewMethods() *Methods {
	return &Methods{methods: make(map[string]Method)}
}

// RegisterMethod registers the auth method with the given name. If another
// method is already registered with the same name, returns error.
func (m *Methods) RegisterMethod(name string, method Method) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	_, ok := m.methods[name]
	if ok {
		return fmt.Errorf("auth method %s already registered", name)
	}
	m.methods[name] = method
	return nil
}

// Method gets the authentication method identified by name. If no such
// method exists, returns nil.
func (m *Methods) Method(name string) Method {
	m.lock.RLock()
	defer m.lock.RUnlock()
	method, ok := m.methods[name]
	if !ok {
		return nil
	}
	return method
}
