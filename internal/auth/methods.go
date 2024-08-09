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

func (m *Methods) Names() []string {
	m.lock.RLock()
	defer m.lock.RUnlock()
	ret := []string{}
	for n := range m.methods {
		ret = append(ret, n)
	}
	return ret
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
