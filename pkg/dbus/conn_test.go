package dbus_test

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"

	godbus "github.com/godbus/dbus/v5"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/dbus"
)

func Test_Creating_client_authenticates_using_user_id(t *testing.T) {
	t.Parallel()

	authCheckingConnection := &mockConnection{
		authF: func(authMethods []godbus.Auth) error {
			uidAuthFound := false

			for i, method := range authMethods {
				_, data, _ := method.FirstData()

				decodedData, err := hex.DecodeString(string(data))
				if err != nil {
					t.Fatalf("Received auth method %d has bad hex data %v: %v", i, data, err)
				}

				potentialUID, err := strconv.Atoi(string(decodedData))
				if err != nil {
					t.Logf("Data %q couldn't be converted to UID: %v", string(decodedData), err)
				}

				if potentialUID == os.Getuid() {
					uidAuthFound = true
				}
			}

			if !uidAuthFound {
				t.Fatalf("Expected auth method with user id")
			}

			return nil
		},
	}

	client, err := dbus.New(func() (dbus.Connection, error) { return authCheckingConnection, nil })
	if err != nil {
		t.Fatalf("Unexpected error creating client: %v", err)
	}

	if client == nil {
		t.Fatalf("When new succeeds, returned client should not be nil")
	}
}

//nolint:funlen // Just many subtests.
func Test_Creating_client_returns_error_when(t *testing.T) {
	t.Parallel()

	t.Run("no_connector_is_given", func(t *testing.T) {
		t.Parallel()

		testNewError(t, nil, nil)
	})

	t.Run("connecting_to_D-Bus_socket_fails", func(t *testing.T) {
		t.Parallel()

		expectedErr := fmt.Errorf("connection error")

		failingConnectionConnector := func() (dbus.Connection, error) { return nil, expectedErr }

		testNewError(t, failingConnectionConnector, expectedErr)
	})

	t.Run("authenticating_to_D-Bus_fails", func(t *testing.T) {
		t.Parallel()

		expectedErr := fmt.Errorf("auth error")

		closeCalled := false

		failingAuthConnection := &mockConnection{
			authF: func([]godbus.Auth) error {
				return expectedErr
			},
			closeF: func() error {
				closeCalled = true

				return fmt.Errorf("closing error")
			},
		}

		testNewError(t, func() (dbus.Connection, error) { return failingAuthConnection, nil }, expectedErr)

		t.Run("and_tries_to_close_the_client_while_ignoring_closing_error", func(t *testing.T) {
			if !closeCalled {
				t.Fatalf("Expected close function to be called")
			}
		})
	})

	t.Run("sending_hello_to_D-Bus_fails", func(t *testing.T) {
		t.Parallel()

		expectedErr := fmt.Errorf("hello error")

		closeCalled := false

		failingHelloConnection := &mockConnection{
			helloF: func() error {
				return expectedErr
			},
			closeF: func() error {
				closeCalled = true

				return fmt.Errorf("closing error")
			},
		}

		testNewError(t, func() (dbus.Connection, error) { return failingHelloConnection, nil }, expectedErr)

		t.Run("and_tries_to_close_the_client_while_ignoring_closing_error", func(t *testing.T) {
			if !closeCalled {
				t.Fatalf("Expected close function to be called")
			}
		})
	})
}

func testNewError(t *testing.T, connector dbus.Connector, expectedErr error) {
	t.Helper()

	client, err := dbus.New(connector)
	if err == nil {
		t.Fatalf("Expected error creating client")
	}

	if client != nil {
		t.Fatalf("Client should not be returned when creation error occurs")
	}

	if expectedErr != nil && !errors.Is(err, expectedErr) {
		t.Fatalf("Unexpected error occurred, expected %q, got %q", expectedErr, err)
	}
}

type mockConnection struct {
	authF  func(authMethods []godbus.Auth) error
	helloF func() error
	closeF func() error
}

func (m *mockConnection) Auth(authMethods []godbus.Auth) error {
	if m.authF == nil {
		return nil
	}

	return m.authF(authMethods)
}

func (m *mockConnection) Hello() error {
	if m.helloF == nil {
		return nil
	}

	return m.helloF()
}

func (m *mockConnection) AddMatchSignal(matchOptions ...godbus.MatchOption) error {
	return nil
}

func (m *mockConnection) Signal(ch chan<- *godbus.Signal) {}

func (m *mockConnection) Object(dest string, path godbus.ObjectPath) godbus.BusObject {
	return nil
}

func (m *mockConnection) Close() error {
	if m.closeF == nil {
		return nil
	}

	return m.closeF()
}
