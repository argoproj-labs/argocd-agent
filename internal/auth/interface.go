package auth

type AuthSubject struct {
	ClientID string `json:"clientID"`
	Mode     string `json:"mode"`
}

// Credentials is a data type for passing arbitrary credentials to auth methods
type Credentials map[string]string

// Method is the interface to be implemented by all auth methods
type Method interface {
	Init() error
	Authenticate(credentials Credentials) (string, error)
}
