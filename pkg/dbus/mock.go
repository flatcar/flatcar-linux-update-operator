package dbus

import (
	"context"

	godbus "github.com/godbus/dbus/v5"
)

// MockConnection is a test helper for mocking godbus.Conn.
type MockConnection struct {
	AuthF           func(authMethods []godbus.Auth) error
	HelloF          func() error
	CloseF          func() error
	AddMatchSignalF func(...godbus.MatchOption) error
	SignalF         func(chan<- *godbus.Signal)
	ObjectF         func(string, godbus.ObjectPath) godbus.BusObject
}

// Auth ...
func (m *MockConnection) Auth(authMethods []godbus.Auth) error {
	if m.AuthF == nil {
		return nil
	}

	return m.AuthF(authMethods)
}

// Hello ...
func (m *MockConnection) Hello() error {
	if m.HelloF == nil {
		return nil
	}

	return m.HelloF()
}

// AddMatchSignal ...
func (m *MockConnection) AddMatchSignal(matchOptions ...godbus.MatchOption) error {
	if m.AddMatchSignalF == nil {
		return nil
	}

	return m.AddMatchSignalF(matchOptions...)
}

// Signal ...
func (m *MockConnection) Signal(ch chan<- *godbus.Signal) {
	if m.SignalF == nil {
		return
	}

	m.SignalF(ch)
}

// Object ...
func (m *MockConnection) Object(dest string, path godbus.ObjectPath) godbus.BusObject {
	if m.ObjectF == nil {
		return nil
	}

	return m.ObjectF(dest, path)
}

// Close ...
func (m *MockConnection) Close() error {
	if m.CloseF == nil {
		return nil
	}

	return m.CloseF()
}

// MockObject is a mock of godbus.BusObject.
type MockObject struct {
	CallWithContextF func(context.Context, string, godbus.Flags, ...interface{}) *godbus.Call
	CallF            func(string, godbus.Flags, ...interface{}) *godbus.Call
}

// MockObject must implement godbus.BusObject to be usable for other packages in tests, though not
// all methods must actually be mockable. See https://github.com/godbus/dbus/issues/252 for details.
var _ godbus.BusObject = &MockObject{}

// CallWithContext ...
func (m *MockObject) CallWithContext(
	ctx context.Context, method string, flags godbus.Flags, args ...interface{},
) *godbus.Call {
	if m.CallWithContextF == nil {
		return &godbus.Call{}
	}

	return m.CallWithContextF(ctx, method, flags, args...)
}

// Call ...
func (m *MockObject) Call(method string, flags godbus.Flags, args ...interface{}) *godbus.Call {
	if m.CallF == nil {
		return &godbus.Call{}
	}

	return m.CallF(method, flags, args...)
}

// Go ...
func (m *MockObject) Go(method string, flags godbus.Flags, ch chan *godbus.Call, args ...interface{}) *godbus.Call {
	return &godbus.Call{}
}

// GoWithContext ...
func (m *MockObject) GoWithContext(
	ctx context.Context, method string, flags godbus.Flags, ch chan *godbus.Call, args ...interface{},
) *godbus.Call {
	return &godbus.Call{}
}

// AddMatchSignal ...
func (m *MockObject) AddMatchSignal(iface, member string, options ...godbus.MatchOption) *godbus.Call {
	return &godbus.Call{}
}

// RemoveMatchSignal ...
func (m *MockObject) RemoveMatchSignal(iface, member string, options ...godbus.MatchOption) *godbus.Call {
	return &godbus.Call{}
}

// GetProperty ...
func (m *MockObject) GetProperty(p string) (godbus.Variant, error) { return godbus.Variant{}, nil }

// StoreProperty ...
func (m *MockObject) StoreProperty(p string, value interface{}) error { return nil }

// SetProperty ...
func (m *MockObject) SetProperty(p string, v interface{}) error { return nil }

// Destination ...
func (m *MockObject) Destination() string { return "" }

// Path ...
func (m *MockObject) Path() godbus.ObjectPath { return "" }
