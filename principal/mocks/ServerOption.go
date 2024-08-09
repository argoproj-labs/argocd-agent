// Code generated by mockery v2.35.4. DO NOT EDIT.

package mocks

import (
	server "github.com/argoproj-labs/argocd-agent/principal"
	mock "github.com/stretchr/testify/mock"
)

// ServerOption is an autogenerated mock type for the ServerOption type
type ServerOption struct {
	mock.Mock
}

// Execute provides a mock function with given fields: o
func (_m *ServerOption) Execute(o *server.ServerOptions) error {
	ret := _m.Called(o)

	var r0 error
	if rf, ok := ret.Get(0).(func(*server.ServerOptions) error); ok {
		r0 = rf(o)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewServerOption creates a new instance of ServerOption. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewServerOption(t interface {
	mock.TestingT
	Cleanup(func())
}) *ServerOption {
	mock := &ServerOption{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
