// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/coder/coder/tailnet/tailnettest (interfaces: MultiAgentConn)

// Package tailnettest is a generated GoMock package.
package tailnettest

import (
	context "context"
	reflect "reflect"

	tailnet "github.com/coder/coder/tailnet"
	gomock "github.com/golang/mock/gomock"
	uuid "github.com/google/uuid"
)

// MockMultiAgentConn is a mock of MultiAgentConn interface.
type MockMultiAgentConn struct {
	ctrl     *gomock.Controller
	recorder *MockMultiAgentConnMockRecorder
}

// MockMultiAgentConnMockRecorder is the mock recorder for MockMultiAgentConn.
type MockMultiAgentConnMockRecorder struct {
	mock *MockMultiAgentConn
}

// NewMockMultiAgentConn creates a new mock instance.
func NewMockMultiAgentConn(ctrl *gomock.Controller) *MockMultiAgentConn {
	mock := &MockMultiAgentConn{ctrl: ctrl}
	mock.recorder = &MockMultiAgentConnMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockMultiAgentConn) EXPECT() *MockMultiAgentConnMockRecorder {
	return m.recorder
}

// AgentIsLegacy mocks base method.
func (m *MockMultiAgentConn) AgentIsLegacy(arg0 uuid.UUID) bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AgentIsLegacy", arg0)
	ret0, _ := ret[0].(bool)
	return ret0
}

// AgentIsLegacy indicates an expected call of AgentIsLegacy.
func (mr *MockMultiAgentConnMockRecorder) AgentIsLegacy(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AgentIsLegacy", reflect.TypeOf((*MockMultiAgentConn)(nil).AgentIsLegacy), arg0)
}

// Close mocks base method.
func (m *MockMultiAgentConn) Close() error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Close")
	ret0, _ := ret[0].(error)
	return ret0
}

// Close indicates an expected call of Close.
func (mr *MockMultiAgentConnMockRecorder) Close() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Close", reflect.TypeOf((*MockMultiAgentConn)(nil).Close))
}

// Enqueue mocks base method.
func (m *MockMultiAgentConn) Enqueue(arg0 []*tailnet.Node) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Enqueue", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// Enqueue indicates an expected call of Enqueue.
func (mr *MockMultiAgentConnMockRecorder) Enqueue(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Enqueue", reflect.TypeOf((*MockMultiAgentConn)(nil).Enqueue), arg0)
}

// IsClosed mocks base method.
func (m *MockMultiAgentConn) IsClosed() bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "IsClosed")
	ret0, _ := ret[0].(bool)
	return ret0
}

// IsClosed indicates an expected call of IsClosed.
func (mr *MockMultiAgentConnMockRecorder) IsClosed() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "IsClosed", reflect.TypeOf((*MockMultiAgentConn)(nil).IsClosed))
}

// NextUpdate mocks base method.
func (m *MockMultiAgentConn) NextUpdate(arg0 context.Context) ([]*tailnet.Node, bool) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "NextUpdate", arg0)
	ret0, _ := ret[0].([]*tailnet.Node)
	ret1, _ := ret[1].(bool)
	return ret0, ret1
}

// NextUpdate indicates an expected call of NextUpdate.
func (mr *MockMultiAgentConnMockRecorder) NextUpdate(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "NextUpdate", reflect.TypeOf((*MockMultiAgentConn)(nil).NextUpdate), arg0)
}

// SubscribeAgent mocks base method.
func (m *MockMultiAgentConn) SubscribeAgent(arg0 uuid.UUID) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "SubscribeAgent", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// SubscribeAgent indicates an expected call of SubscribeAgent.
func (mr *MockMultiAgentConnMockRecorder) SubscribeAgent(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SubscribeAgent", reflect.TypeOf((*MockMultiAgentConn)(nil).SubscribeAgent), arg0)
}

// UnsubscribeAgent mocks base method.
func (m *MockMultiAgentConn) UnsubscribeAgent(arg0 uuid.UUID) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UnsubscribeAgent", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// UnsubscribeAgent indicates an expected call of UnsubscribeAgent.
func (mr *MockMultiAgentConnMockRecorder) UnsubscribeAgent(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UnsubscribeAgent", reflect.TypeOf((*MockMultiAgentConn)(nil).UnsubscribeAgent), arg0)
}

// UpdateSelf mocks base method.
func (m *MockMultiAgentConn) UpdateSelf(arg0 *tailnet.Node) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateSelf", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// UpdateSelf indicates an expected call of UpdateSelf.
func (mr *MockMultiAgentConnMockRecorder) UpdateSelf(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateSelf", reflect.TypeOf((*MockMultiAgentConn)(nil).UpdateSelf), arg0)
}
