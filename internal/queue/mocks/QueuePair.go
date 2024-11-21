// Code generated by mockery v2.43.0. DO NOT EDIT.

package mocks

import (
	event "github.com/cloudevents/sdk-go/v2/event"
	mock "github.com/stretchr/testify/mock"

	workqueue "k8s.io/client-go/util/workqueue"
)

// QueuePair is an autogenerated mock type for the QueuePair type
type QueuePair struct {
	mock.Mock
}

type QueuePair_Expecter struct {
	mock *mock.Mock
}

func (_m *QueuePair) EXPECT() *QueuePair_Expecter {
	return &QueuePair_Expecter{mock: &_m.Mock}
}

// Create provides a mock function with given fields: name
func (_m *QueuePair) Create(name string) error {
	ret := _m.Called(name)

	if len(ret) == 0 {
		panic("no return value specified for Create")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(string) error); ok {
		r0 = rf(name)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// QueuePair_Create_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Create'
type QueuePair_Create_Call struct {
	*mock.Call
}

// Create is a helper method to define mock.On call
//   - name string
func (_e *QueuePair_Expecter) Create(name interface{}) *QueuePair_Create_Call {
	return &QueuePair_Create_Call{Call: _e.mock.On("Create", name)}
}

func (_c *QueuePair_Create_Call) Run(run func(name string)) *QueuePair_Create_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(string))
	})
	return _c
}

func (_c *QueuePair_Create_Call) Return(_a0 error) *QueuePair_Create_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *QueuePair_Create_Call) RunAndReturn(run func(string) error) *QueuePair_Create_Call {
	_c.Call.Return(run)
	return _c
}

// Delete provides a mock function with given fields: name, shutdown
func (_m *QueuePair) Delete(name string, shutdown bool) error {
	ret := _m.Called(name, shutdown)

	if len(ret) == 0 {
		panic("no return value specified for Delete")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(string, bool) error); ok {
		r0 = rf(name, shutdown)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// QueuePair_Delete_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Delete'
type QueuePair_Delete_Call struct {
	*mock.Call
}

// Delete is a helper method to define mock.On call
//   - name string
//   - shutdown bool
func (_e *QueuePair_Expecter) Delete(name interface{}, shutdown interface{}) *QueuePair_Delete_Call {
	return &QueuePair_Delete_Call{Call: _e.mock.On("Delete", name, shutdown)}
}

func (_c *QueuePair_Delete_Call) Run(run func(name string, shutdown bool)) *QueuePair_Delete_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(string), args[1].(bool))
	})
	return _c
}

func (_c *QueuePair_Delete_Call) Return(_a0 error) *QueuePair_Delete_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *QueuePair_Delete_Call) RunAndReturn(run func(string, bool) error) *QueuePair_Delete_Call {
	_c.Call.Return(run)
	return _c
}

// HasQueuePair provides a mock function with given fields: name
func (_m *QueuePair) HasQueuePair(name string) bool {
	ret := _m.Called(name)

	if len(ret) == 0 {
		panic("no return value specified for HasQueuePair")
	}

	var r0 bool
	if rf, ok := ret.Get(0).(func(string) bool); ok {
		r0 = rf(name)
	} else {
		r0 = ret.Get(0).(bool)
	}

	return r0
}

// QueuePair_HasQueuePair_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'HasQueuePair'
type QueuePair_HasQueuePair_Call struct {
	*mock.Call
}

// HasQueuePair is a helper method to define mock.On call
//   - name string
func (_e *QueuePair_Expecter) HasQueuePair(name interface{}) *QueuePair_HasQueuePair_Call {
	return &QueuePair_HasQueuePair_Call{Call: _e.mock.On("HasQueuePair", name)}
}

func (_c *QueuePair_HasQueuePair_Call) Run(run func(name string)) *QueuePair_HasQueuePair_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(string))
	})
	return _c
}

func (_c *QueuePair_HasQueuePair_Call) Return(_a0 bool) *QueuePair_HasQueuePair_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *QueuePair_HasQueuePair_Call) RunAndReturn(run func(string) bool) *QueuePair_HasQueuePair_Call {
	_c.Call.Return(run)
	return _c
}

// Len provides a mock function with given fields:
func (_m *QueuePair) Len() int {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Len")
	}

	var r0 int
	if rf, ok := ret.Get(0).(func() int); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(int)
	}

	return r0
}

// QueuePair_Len_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Len'
type QueuePair_Len_Call struct {
	*mock.Call
}

// Len is a helper method to define mock.On call
func (_e *QueuePair_Expecter) Len() *QueuePair_Len_Call {
	return &QueuePair_Len_Call{Call: _e.mock.On("Len")}
}

func (_c *QueuePair_Len_Call) Run(run func()) *QueuePair_Len_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *QueuePair_Len_Call) Return(_a0 int) *QueuePair_Len_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *QueuePair_Len_Call) RunAndReturn(run func() int) *QueuePair_Len_Call {
	_c.Call.Return(run)
	return _c
}

// Names provides a mock function with given fields:
func (_m *QueuePair) Names() []string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Names")
	}

	var r0 []string
	if rf, ok := ret.Get(0).(func() []string); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]string)
		}
	}

	return r0
}

// QueuePair_Names_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Names'
type QueuePair_Names_Call struct {
	*mock.Call
}

// Names is a helper method to define mock.On call
func (_e *QueuePair_Expecter) Names() *QueuePair_Names_Call {
	return &QueuePair_Names_Call{Call: _e.mock.On("Names")}
}

func (_c *QueuePair_Names_Call) Run(run func()) *QueuePair_Names_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *QueuePair_Names_Call) Return(_a0 []string) *QueuePair_Names_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *QueuePair_Names_Call) RunAndReturn(run func() []string) *QueuePair_Names_Call {
	_c.Call.Return(run)
	return _c
}

// RecvQ provides a mock function with given fields: name
func (_m *QueuePair) RecvQ(name string) workqueue.TypedRateLimitingInterface[*event.Event] {
	ret := _m.Called(name)

	if len(ret) == 0 {
		panic("no return value specified for RecvQ")
	}

	var r0 workqueue.TypedRateLimitingInterface[*event.Event]
	if rf, ok := ret.Get(0).(func(string) workqueue.TypedRateLimitingInterface[*event.Event]); ok {
		r0 = rf(name)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(workqueue.TypedRateLimitingInterface[*event.Event])
		}
	}

	return r0
}

// QueuePair_RecvQ_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'RecvQ'
type QueuePair_RecvQ_Call struct {
	*mock.Call
}

// RecvQ is a helper method to define mock.On call
//   - name string
func (_e *QueuePair_Expecter) RecvQ(name interface{}) *QueuePair_RecvQ_Call {
	return &QueuePair_RecvQ_Call{Call: _e.mock.On("RecvQ", name)}
}

func (_c *QueuePair_RecvQ_Call) Run(run func(name string)) *QueuePair_RecvQ_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(string))
	})
	return _c
}

func (_c *QueuePair_RecvQ_Call) Return(_a0 workqueue.TypedRateLimitingInterface[*event.Event]) *QueuePair_RecvQ_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *QueuePair_RecvQ_Call) RunAndReturn(run func(string) workqueue.TypedRateLimitingInterface[*event.Event]) *QueuePair_RecvQ_Call {
	_c.Call.Return(run)
	return _c
}

// SendQ provides a mock function with given fields: name
func (_m *QueuePair) SendQ(name string) workqueue.TypedRateLimitingInterface[*event.Event] {
	ret := _m.Called(name)

	if len(ret) == 0 {
		panic("no return value specified for SendQ")
	}

	var r0 workqueue.TypedRateLimitingInterface[*event.Event]
	if rf, ok := ret.Get(0).(func(string) workqueue.TypedRateLimitingInterface[*event.Event]); ok {
		r0 = rf(name)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(workqueue.TypedRateLimitingInterface[*event.Event])
		}
	}

	return r0
}

// QueuePair_SendQ_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SendQ'
type QueuePair_SendQ_Call struct {
	*mock.Call
}

// SendQ is a helper method to define mock.On call
//   - name string
func (_e *QueuePair_Expecter) SendQ(name interface{}) *QueuePair_SendQ_Call {
	return &QueuePair_SendQ_Call{Call: _e.mock.On("SendQ", name)}
}

func (_c *QueuePair_SendQ_Call) Run(run func(name string)) *QueuePair_SendQ_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(string))
	})
	return _c
}

func (_c *QueuePair_SendQ_Call) Return(_a0 workqueue.TypedRateLimitingInterface[*event.Event]) *QueuePair_SendQ_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *QueuePair_SendQ_Call) RunAndReturn(run func(string) workqueue.TypedRateLimitingInterface[*event.Event]) *QueuePair_SendQ_Call {
	_c.Call.Return(run)
	return _c
}

// NewQueuePair creates a new instance of QueuePair. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewQueuePair(t interface {
	mock.TestingT
	Cleanup(func())
}) *QueuePair {
	mock := &QueuePair{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
