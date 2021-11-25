package updateengine_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/updateengine"
	dbus "github.com/godbus/dbus/v5"
)

//nolint:paralleltest,funlen,tparallel // Test uses environment variables, which are global.
func Test_Creating_client_fails_when(t *testing.T) {
	t.Run("connecting_to_system_bus_fails", func(t *testing.T) {
		t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "foo")

		if _, err := updateengine.New(updateengine.DBusSystemPrivateConnector); err == nil {
			t.Fatalf("Creating client should fail when unable to connect to system bus")
		}
	})

	t.Run("D-Bus_authentication_fails", func(t *testing.T) {
		t.Parallel()

		expectedError := fmt.Errorf("auth failed")

		closeCalled := false

		connector := newMockDBusConnection()
		connector.authF = func([]dbus.Auth) error { return expectedError }
		connector.closeF = func() error {
			closeCalled = true

			return fmt.Errorf("closing error")
		}

		client, err := updateengine.New(func() (updateengine.DBusConnection, error) { return connector, nil })
		if !errors.Is(err, expectedError) {
			t.Fatalf("Got unexpected error while creating client, expected %q, got %q", expectedError, err)
		}

		if client != nil {
			t.Fatalf("Expected client to be nil when creating fails")
		}

		t.Run("and_tries_to_close_the_client_while_ignoring_closing_error", func(t *testing.T) {
			if !closeCalled {
				t.Fatalf("Expected close function to be called")
			}
		})
	})

	t.Run("D-Bus_hello_fails", func(t *testing.T) {
		t.Parallel()

		expectedError := fmt.Errorf("hello failed")

		closeCalled := false

		connector := newMockDBusConnection()
		connector.helloF = func() error { return expectedError }
		connector.closeF = func() error {
			closeCalled = true

			return fmt.Errorf("closing error")
		}

		client, err := updateengine.New(func() (updateengine.DBusConnection, error) { return connector, nil })
		if !errors.Is(err, expectedError) {
			t.Fatalf("Got unexpected error while creating client, expected %q, got %q", expectedError, err)
		}

		if client != nil {
			t.Fatalf("Expected client to be nil when creating fails")
		}

		t.Run("and_tries_to_close_the_client_while_ignoring_closing_error", func(t *testing.T) {
			if !closeCalled {
				t.Fatalf("Expected close function to be called")
			}
		})
	})

	t.Run("adding_D-Bus_filter_fails", func(t *testing.T) {
		t.Parallel()

		expectedError := fmt.Errorf("match signal failed")

		connector := newMockDBusConnection()
		connector.addMatchSignalF = func(...dbus.MatchOption) error { return expectedError }

		client, err := updateengine.New(func() (updateengine.DBusConnection, error) { return connector, nil })
		if !errors.Is(err, expectedError) {
			t.Fatalf("Got unexpected error while creating client, expected %q, got %q", expectedError, err)
		}

		if client != nil {
			t.Fatalf("Expected client to be nil when creating fails")
		}
	})
}

func Test_Closing_client_returns_error_when_closing_DBus_client_fails(t *testing.T) {
	t.Parallel()

	expectedError := fmt.Errorf("closing failed")

	connector := newMockDBusConnection()
	connector.closeF = func() error { return expectedError }

	client, err := updateengine.New(func() (updateengine.DBusConnection, error) { return connector, nil })
	if err != nil {
		t.Fatalf("Unexpected error creating client: %v", err)
	}

	if err := client.Close(); !errors.Is(err, expectedError) {
		t.Fatalf("Got unexpected error closing the client, expected %q, got %q", expectedError, err)
	}
}

type mockDBusConnection struct {
	authF           func([]dbus.Auth) error
	helloF          func() error
	closeF          func() error
	addMatchSignalF func(...dbus.MatchOption) error
	signalF         func(chan<- *dbus.Signal)
	objectF         func(string, dbus.ObjectPath) dbus.BusObject
}

func (m *mockDBusConnection) Auth(methods []dbus.Auth) error {
	return m.authF(methods)
}

func (m *mockDBusConnection) Hello() error {
	return m.helloF()
}

func (m *mockDBusConnection) Close() error {
	return m.closeF()
}

func (m *mockDBusConnection) AddMatchSignal(options ...dbus.MatchOption) error {
	return m.addMatchSignalF(options...)
}

func (m *mockDBusConnection) Signal(ch chan<- *dbus.Signal) {
	m.signalF(ch)
}

func (m *mockDBusConnection) Object(dest string, path dbus.ObjectPath) dbus.BusObject {
	return m.objectF(dest, path)
}

func newMockDBusConnection() *mockDBusConnection {
	return &mockDBusConnection{
		authF:           func([]dbus.Auth) error { return nil },
		helloF:          func() error { return nil },
		closeF:          func() error { return nil },
		addMatchSignalF: func(...dbus.MatchOption) error { return nil },
		signalF:         func(chan<- *dbus.Signal) {},
		objectF:         func(string, dbus.ObjectPath) dbus.BusObject { return &dbus.Object{} },
	}
}
