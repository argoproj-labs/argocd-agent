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
	"testing"

	"github.com/stretchr/testify/require"
)

type mockAuth struct{}

func (a *mockAuth) Authenticate(creds Credentials) (string, error) {
	return "some", nil
}

func (a *mockAuth) Init() error {
	return nil
}

func Test_AuthMethods(t *testing.T) {
	t.Run("Register an auth method and verify", func(t *testing.T) {
		m := NewMethods()
		authmethod := &mockAuth{}
		err := m.RegisterMethod("userpass", authmethod)
		require.NoError(t, err)
		require.NotNil(t, m.Method("userpass"))
	})

	t.Run("Register two auth methods under same name", func(t *testing.T) {
		m := NewMethods()
		authmethod := &mockAuth{}
		err := m.RegisterMethod("userpass", authmethod)
		require.NoError(t, err)
		err = m.RegisterMethod("userpass", authmethod)
		require.Error(t, err)
	})

	t.Run("Look up non-existing auth method", func(t *testing.T) {
		m := NewMethods()
		authmethod := &mockAuth{}
		err := m.RegisterMethod("userpass", authmethod)
		require.NoError(t, err)
		require.NotNil(t, m.Method("userpass"))
		require.Nil(t, m.Method("username"))
	})

}
