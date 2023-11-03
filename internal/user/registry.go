package user

import "sync"

type User struct {
	username string
	password string
}

type Registry interface {
	User(username string) *User
	Authenticate(user *User, password string) bool
}

var _ Registry = &SimpleUserRegistry{}

type SimpleUserRegistry struct {
	lock  sync.RWMutex
	users map[string]*User
}

func NewSimpleUserRegistry(users []User) *SimpleUserRegistry {
	r := &SimpleUserRegistry{}
	for _, u := range users {
		r.users[u.username] = &u
	}
	return r
}

func (r *SimpleUserRegistry) User(username string) *User {
	r.lock.RLock()
	defer r.lock.RUnlock()
	return r.users[username]
}

func (r *SimpleUserRegistry) Authenticate(user *User, password string) bool {
	return user.password == password
}
