// Code generated by mockery v2.35.4. DO NOT EDIT.

package mocks

import (
	eventstreammock "github.com/argoproj-labs/argocd-agent/principal/apis/eventstream/mock"
	mock "github.com/stretchr/testify/mock"
)

// RecvHook is an autogenerated mock type for the RecvHook type
type RecvHook struct {
	mock.Mock
}

// Execute provides a mock function with given fields: s
func (_m *RecvHook) Execute(s *eventstreammock.MockEventServer) error {
	ret := _m.Called(s)

	var r0 error
	if rf, ok := ret.Get(0).(func(*eventstreammock.MockEventServer) error); ok {
		r0 = rf(s)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewRecvHook creates a new instance of RecvHook. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewRecvHook(t interface {
	mock.TestingT
	Cleanup(func())
}) *RecvHook {
	mock := &RecvHook{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
