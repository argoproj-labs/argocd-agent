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

import "context"

type AuthSubject struct {
	ClientID string `json:"clientID"`
	Mode     string `json:"mode"`
}

// Credentials is a data type for passing arbitrary credentials to auth methods
type Credentials map[string]string

// Method is the interface to be implemented by all auth methods
type Method interface {
	Init() error
	Authenticate(ctx context.Context, credentials Credentials) (string, error)
}
