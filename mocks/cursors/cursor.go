// Code generated by MockGen. DO NOT EDIT.
// Source: cursors/cursor.go

// Package mock_cursors is a generated GoMock package.
package mock_cursors

import (
	image "image"
	color "image/color"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
)

// MockChangeableImage is a mock of ChangeableImage interface.
type MockChangeableImage struct {
	ctrl     *gomock.Controller
	recorder *MockChangeableImageMockRecorder
}

// MockChangeableImageMockRecorder is the mock recorder for MockChangeableImage.
type MockChangeableImageMockRecorder struct {
	mock *MockChangeableImage
}

// NewMockChangeableImage creates a new mock instance.
func NewMockChangeableImage(ctrl *gomock.Controller) *MockChangeableImage {
	mock := &MockChangeableImage{ctrl: ctrl}
	mock.recorder = &MockChangeableImageMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockChangeableImage) EXPECT() *MockChangeableImageMockRecorder {
	return m.recorder
}

// At mocks base method.
func (m *MockChangeableImage) At(x, y int) color.Color {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "At", x, y)
	ret0, _ := ret[0].(color.Color)
	return ret0
}

// At indicates an expected call of At.
func (mr *MockChangeableImageMockRecorder) At(x, y interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "At", reflect.TypeOf((*MockChangeableImage)(nil).At), x, y)
}

// Bounds mocks base method.
func (m *MockChangeableImage) Bounds() image.Rectangle {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Bounds")
	ret0, _ := ret[0].(image.Rectangle)
	return ret0
}

// Bounds indicates an expected call of Bounds.
func (mr *MockChangeableImageMockRecorder) Bounds() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Bounds", reflect.TypeOf((*MockChangeableImage)(nil).Bounds))
}

// ColorModel mocks base method.
func (m *MockChangeableImage) ColorModel() color.Model {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ColorModel")
	ret0, _ := ret[0].(color.Model)
	return ret0
}

// ColorModel indicates an expected call of ColorModel.
func (mr *MockChangeableImageMockRecorder) ColorModel() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ColorModel", reflect.TypeOf((*MockChangeableImage)(nil).ColorModel))
}

// Set mocks base method.
func (m *MockChangeableImage) Set(x, y int, c color.Color) {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "Set", x, y, c)
}

// Set indicates an expected call of Set.
func (mr *MockChangeableImageMockRecorder) Set(x, y, c interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Set", reflect.TypeOf((*MockChangeableImage)(nil).Set), x, y, c)
}

// MockCursor is a mock of Cursor interface.
type MockCursor struct {
	ctrl     *gomock.Controller
	recorder *MockCursorMockRecorder
}

// MockCursorMockRecorder is the mock recorder for MockCursor.
type MockCursorMockRecorder struct {
	mock *MockCursor
}

// NewMockCursor creates a new mock instance.
func NewMockCursor(ctrl *gomock.Controller) *MockCursor {
	mock := &MockCursor{ctrl: ctrl}
	mock.recorder = &MockCursorMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockCursor) EXPECT() *MockCursorMockRecorder {
	return m.recorder
}

// ReadBit mocks base method.
func (m *MockCursor) ReadBit() (uint8, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ReadBit")
	ret0, _ := ret[0].(uint8)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ReadBit indicates an expected call of ReadBit.
func (mr *MockCursorMockRecorder) ReadBit() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ReadBit", reflect.TypeOf((*MockCursor)(nil).ReadBit))
}

// Seek mocks base method.
func (m *MockCursor) Seek(offset int64, whence int) (int64, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Seek", offset, whence)
	ret0, _ := ret[0].(int64)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Seek indicates an expected call of Seek.
func (mr *MockCursorMockRecorder) Seek(offset, whence interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Seek", reflect.TypeOf((*MockCursor)(nil).Seek), offset, whence)
}

// WriteBit mocks base method.
func (m *MockCursor) WriteBit(bit uint8) (uint, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "WriteBit", bit)
	ret0, _ := ret[0].(uint)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// WriteBit indicates an expected call of WriteBit.
func (mr *MockCursorMockRecorder) WriteBit(bit interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "WriteBit", reflect.TypeOf((*MockCursor)(nil).WriteBit), bit)
}
