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
