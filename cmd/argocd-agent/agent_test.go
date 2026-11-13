// Copyright 2026 The argocd-agent Authors
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

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_redisCreds(t *testing.T) {
	const expPwd = "secret_pwd"
	const expUser = "secret_user"

	onlyPwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(onlyPwd, "auth"), []byte(expPwd), 0600))

	fullCreds := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(fullCreds, "auth"), []byte(expPwd), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(fullCreds, "auth_username"), []byte(expUser), 0600))

	// Dir where file is expected
	usernameUnreadable := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(usernameUnreadable, "auth_username"), 0600))

	emptyFileCreds := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(emptyFileCreds, "auth"), []byte{}, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(emptyFileCreds, "auth_username"), []byte{}, 0600))

	tests := []struct {
		name         string
		credsDirPath string
		username     string
		password     string
		expUsername  string
		expPassword  string
		expError     func(string)
	}{
		{
			name:         "blanks are ok",
			credsDirPath: "",
			username:     "",
			password:     "",
			expUsername:  "",
			expPassword:  "",
		}, {
			name:         "explicit values when no dir",
			credsDirPath: "",
			username:     "uuuu",
			password:     "ppp",
			expUsername:  "uuuu",
			expPassword:  "ppp",
		}, {
			name:         "dir and username clash",
			credsDirPath: fullCreds,
			username:     "that",
			password:     "",
			expError: func(e string) {
				assert.Equal(t, "dir path cannot be combined with username / password", e)
			},
		}, {
			name:         "dir and password clash",
			credsDirPath: fullCreds,
			username:     "",
			password:     "that",
			expError: func(e string) {
				assert.Equal(t, "dir path cannot be combined with username / password", e)
			},
		}, {
			name:         "error on not existing dir",
			credsDirPath: "/not/found",
			expError: func(e string) {
				assert.Contains(t, e, "failed reading password from '/not/found/auth'")
				assert.Contains(t, e, "no such file or directory")
			},
		}, {
			name:         "error on unreadable username file",
			credsDirPath: usernameUnreadable,
			expError: func(e string) {
				assert.Contains(t, e, "failed reading username from '"+usernameUnreadable+"/auth_username'")
				assert.Contains(t, e, "is a directory")
			},
		}, {
			name:         "read password from dir",
			credsDirPath: onlyPwd,
			expUsername:  "",
			expPassword:  expPwd,
		},
		{
			name:         "read username and password from dir",
			credsDirPath: fullCreds,
			expUsername:  expUser,
			expPassword:  expPwd,
		}, {
			name:         "read creds from dir even when empty",
			credsDirPath: emptyFileCreds,
			expUsername:  "",
			expPassword:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualUsername, actualPassword, actualErr := redisCreds(tt.credsDirPath, tt.username, tt.password)
			if tt.expError != nil {
				require.NotNil(t, actualErr)
				tt.expError(actualErr.Error())
			} else {
				require.Nil(t, actualErr)
				assert.Equal(t, tt.expUsername, actualUsername)
				assert.Equal(t, tt.expPassword, actualPassword)
			}
		})
	}
}

func Test_resolveDestinationBasedMapping(t *testing.T) {
	const key = "ARGOCD_AGENT_DESTINATION_BASED_MAPPING"
	if v, ok := os.LookupEnv(key); ok {
		t.Cleanup(func() { _ = os.Setenv(key, v) })
		require.NoError(t, os.Unsetenv(key))
	}
	t.Run("default follows the agent mode", func(t *testing.T) {
		assert.True(t, resolveDestinationBasedMapping(false, "managed", false))
		assert.False(t, resolveDestinationBasedMapping(false, "autonomous", false))
		assert.False(t, resolveDestinationBasedMapping(false, "unknown", false))
	})
	t.Run("unparsable environment falls back to the mode default", func(t *testing.T) {
		t.Setenv(key, "yes")
		assert.True(t, resolveDestinationBasedMapping(false, "managed", false))
		assert.False(t, resolveDestinationBasedMapping(false, "autonomous", false))
	})
	t.Run("flag wins", func(t *testing.T) {
		assert.False(t, resolveDestinationBasedMapping(true, "managed", false))
		assert.True(t, resolveDestinationBasedMapping(true, "autonomous", true))
	})
	t.Run("flag wins over a conflicting environment", func(t *testing.T) {
		t.Setenv(key, "true")
		assert.False(t, resolveDestinationBasedMapping(true, "managed", false))
		t.Setenv(key, "false")
		assert.True(t, resolveDestinationBasedMapping(true, "autonomous", true))
	})
	t.Run("environment wins", func(t *testing.T) {
		t.Setenv(key, "false")
		assert.False(t, resolveDestinationBasedMapping(false, "managed", false))
		t.Setenv(key, "true")
		assert.True(t, resolveDestinationBasedMapping(false, "autonomous", true))
	})
}
