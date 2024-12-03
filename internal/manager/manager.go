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

package manager

type ManagerRole int
type ManagerMode int

const (
	ManagerRoleUnset ManagerRole = iota
	ManagerRolePrincipal
	ManagerRoleAgent
)

const (
	ManagerModeUnset ManagerMode = iota
	ManagerModeAutonomous
	ManagerModeManaged
)

const (
	// SourceUIDAnnotation is an annotation that represents the UID of the source resource.
	// It is added to the resources managed on the target.
	SourceUIDAnnotation = "argocd.argoproj.io/source-uid"
)

type Manager interface {
	SetRole(role ManagerRole)
	SetMode(role ManagerRole)
}

// IsPrincipal returns true if the manager role is principal
func (r ManagerRole) IsPrincipal() bool {
	return r == ManagerRolePrincipal
}

// IsAgent returns true if the manager role is agent
func (r ManagerRole) IsAgent() bool {
	return r == ManagerRoleAgent
}

// IsAutonomous returns true if the manager mode is autonomous
func (m ManagerMode) IsAutonomous() bool {
	return m == ManagerModeAutonomous
}

// IsManaged returns true if the manager mode is managed mode
func (m ManagerMode) IsManaged() bool {
	return m == ManagerModeManaged
}
