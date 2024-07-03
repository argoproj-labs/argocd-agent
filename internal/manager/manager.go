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
