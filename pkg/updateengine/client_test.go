package updateengine_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	godbus "github.com/godbus/dbus/v5"
	"github.com/google/go-cmp/cmp"

	"github.com/flatcar/flatcar-linux-update-operator/pkg/dbus"
	"github.com/flatcar/flatcar-linux-update-operator/pkg/updateengine"
)

//nolint:funlen,cyclop // Just many test cases.
func Test_Receiving_status(t *testing.T) {
	t.Parallel()

	t.Run("emits_first_status_immediately_after_start", func(t *testing.T) {
		t.Parallel()

		mockConnection := &dbus.MockConnection{
			ObjectF: func(string, godbus.ObjectPath) godbus.BusObject {
				return &dbus.MockObject{
					CallF: func(method string, flags godbus.Flags, args ...interface{}) *godbus.Call {
						return &godbus.Call{
							Body: statusToSignalBody(updateengine.Status{}),
						}
					},
				}
			},
		}

		client, err := updateengine.New(func() (dbus.Connection, error) { return mockConnection, nil })
		if err != nil {
			t.Fatalf("Got unexpected error while creating client: %v", err)
		}

		stop := make(chan struct{})

		t.Cleanup(func() {
			close(stop)
		})

		statusCh := make(chan updateengine.Status, 1)

		go client.ReceiveStatuses(statusCh, stop)

		timeout := time.NewTimer(time.Second)
		select {
		case <-statusCh:
		case <-timeout.C:
			t.Fatal("Failed getting status within expected timeframe")
		}
	})

	t.Run("parses_received_values_in_order_defined_by_update_engine", func(t *testing.T) {
		t.Parallel()

		expectedStatus := testStatus()

		mockConnection := &dbus.MockConnection{
			ObjectF: func(string, godbus.ObjectPath) godbus.BusObject {
				return &dbus.MockObject{
					CallF: func(method string, flags godbus.Flags, args ...interface{}) *godbus.Call {
						return &godbus.Call{
							Body: statusToSignalBody(expectedStatus),
						}
					},
				}
			},
		}

		client, err := updateengine.New(func() (dbus.Connection, error) { return mockConnection, nil })
		if err != nil {
			t.Fatalf("Got unexpected error while creating client: %v", err)
		}

		stop := make(chan struct{})

		t.Cleanup(func() {
			close(stop)
		})

		statusCh := make(chan updateengine.Status, 1)

		go client.ReceiveStatuses(statusCh, stop)

		timeout := time.NewTimer(time.Second)
		select {
		case status := <-statusCh:
			if diff := cmp.Diff(expectedStatus, status); diff != "" {
				t.Fatalf("Unexpectected status values received:\n%s", diff)
			}
		case <-timeout.C:
			t.Fatal("Failed getting status within expected timeframe")
		}
	})

	t.Run("forwards_status_updates_received_from_update_engine_to_given_receiver_channel", func(t *testing.T) {
		t.Parallel()

		firstExpectedStatus := updateengine.Status{}

		secondExpectedStatus := testStatus()

		mockConnection := &dbus.MockConnection{
			ObjectF: func(string, godbus.ObjectPath) godbus.BusObject {
				return &dbus.MockObject{
					CallF: func(method string, flags godbus.Flags, args ...interface{}) *godbus.Call {
						return &godbus.Call{
							Body: statusToSignalBody(firstExpectedStatus),
						}
					},
				}
			},
			SignalF: func(ch chan<- *godbus.Signal) {
				ch <- &godbus.Signal{
					Body: statusToSignalBody(secondExpectedStatus),
				}
			},
		}

		client, err := updateengine.New(func() (dbus.Connection, error) { return mockConnection, nil })
		if err != nil {
			t.Fatalf("Got unexpected error while creating client: %v", err)
		}

		stop := make(chan struct{})

		t.Cleanup(func() {
			close(stop)
		})

		statusCh := make(chan updateengine.Status, 1)

		go client.ReceiveStatuses(statusCh, stop)

		timeout := time.NewTimer(time.Second)

		select {
		case status := <-statusCh:
			if diff := cmp.Diff(firstExpectedStatus, status); diff != "" {
				t.Fatalf("Unexpectected first status values received (-expected/+got):\n%s", diff)
			}
		case <-timeout.C:
			t.Fatal("Failed getting first status within expected timeframe")
		}

		timeout.Reset(time.Second)

		select {
		case status := <-statusCh:
			if diff := cmp.Diff(secondExpectedStatus, status); diff != "" {
				t.Fatalf("Unexpectected second status values received:\n%s", diff)
			}
		case <-timeout.C:
			t.Fatal("Failed getting second status within expected timeframe")
		}
	})

	t.Run("returns_empty_status_when_getting_initial_status_fails", func(t *testing.T) {
		t.Parallel()

		expectedStatus := updateengine.Status{}

		mockConnection := &dbus.MockConnection{
			ObjectF: func(string, godbus.ObjectPath) godbus.BusObject {
				return &dbus.MockObject{
					CallF: func(method string, flags godbus.Flags, args ...interface{}) *godbus.Call {
						return &godbus.Call{
							Body: statusToSignalBody(testStatus()),
							Err:  fmt.Errorf("some error"),
						}
					},
				}
			},
		}

		client, err := updateengine.New(func() (dbus.Connection, error) { return mockConnection, nil })
		if err != nil {
			t.Fatalf("Got unexpected error while creating client: %v", err)
		}

		stop := make(chan struct{})

		t.Cleanup(func() {
			close(stop)
		})

		statusCh := make(chan updateengine.Status, 1)

		go client.ReceiveStatuses(statusCh, stop)

		timeout := time.NewTimer(time.Second)

		select {
		case status := <-statusCh:
			if diff := cmp.Diff(expectedStatus, status); diff != "" {
				t.Fatalf("Unexpectected status values received:\n%s", diff)
			}
		case <-timeout.C:
			t.Fatal("Failed getting status within expected timeframe")
		}
	})
}

//nolint:funlen,gocognit,cyclop // Just many test cases.
func Test_Creating_client(t *testing.T) {
	t.Parallel()

	t.Run("subscribes_to_status_update_signals", func(t *testing.T) {
		t.Parallel()

		mockConnection := &dbus.MockConnection{
			AddMatchSignalF: func(matchOptions ...godbus.MatchOption) error {
				foundInterface := false
				foundMember := false

				for _, option := range matchOptions {
					optionValue := reflect.ValueOf(&option).Elem()
					key := optionValue.Field(0)
					value := optionValue.Field(1)

					switch key.String() {
					case "interface":
						if value.String() == updateengine.DBusInterface {
							foundInterface = true
						}
					case "member":
						if value.String() == updateengine.DBusSignalNameStatusUpdate {
							foundMember = true
						}
					}
				}

				if !foundInterface {
					t.Fatal("Did not receive match option with interface specified")
				}

				if !foundMember {
					t.Fatal("Did not receive match option with member specified")
				}

				return nil
			},
		}

		if _, err := updateengine.New(func() (dbus.Connection, error) { return mockConnection, nil }); err != nil {
			t.Fatalf("Got unexpected error while creating client: %v", err)
		}
	})

	t.Run("fails_when", func(t *testing.T) {
		t.Parallel()

		t.Run("no_D-Bus_connection_is_given", func(t *testing.T) {
			t.Parallel()

			if _, err := updateengine.New(nil); err == nil {
				t.Fatalf("Creating client should fail when unable to connect to system bus")
			}
		})

		t.Run("creating_D-Bus_client_fails", func(t *testing.T) {
			t.Parallel()

			expectedError := fmt.Errorf("D-Bus connection error")

			client, err := updateengine.New(func() (dbus.Connection, error) { return nil, expectedError })
			if !errors.Is(err, expectedError) {
				t.Fatalf("Got unexpected error while creating client, expected %q, got %q", expectedError, err)
			}

			if client != nil {
				t.Fatalf("Expected client to be nil when creating fails")
			}
		})

		t.Run("adding_D-Bus_filter_fails", func(t *testing.T) {
			t.Parallel()

			expectedError := fmt.Errorf("match signal failed")

			failingAddMatchSignalConnection := &dbus.MockConnection{
				AddMatchSignalF: func(...godbus.MatchOption) error { return expectedError },
			}

			client, err := updateengine.New(func() (dbus.Connection, error) { return failingAddMatchSignalConnection, nil })
			if !errors.Is(err, expectedError) {
				t.Fatalf("Got unexpected error while creating client, expected %q, got %q", expectedError, err)
			}

			if client != nil {
				t.Fatalf("Expected client to be nil when creating fails")
			}
		})
	})
}

func Test_Closing_client(t *testing.T) {
	t.Parallel()

	t.Run("closes_underlying_D-Bus_connection", func(t *testing.T) {
		t.Parallel()

		closeCalled := false

		mockConnection := &dbus.MockConnection{
			CloseF: func() error {
				closeCalled = true

				return nil
			},
		}

		client, err := updateengine.New(func() (dbus.Connection, error) { return mockConnection, nil })
		if err != nil {
			t.Fatalf("Got unexpected error while creating client: %v", err)
		}

		if err := client.Close(); err != nil {
			t.Fatalf("Unexpected error closing client: %v", err)
		}

		if !closeCalled {
			t.Fatalf("Expected client to close D-Bus connection")
		}
	})

	t.Run("returns_error_when_closing_D-Bus_connection_fails", func(t *testing.T) {
		t.Parallel()

		expectedError := fmt.Errorf("closing error")

		mockConnection := &dbus.MockConnection{
			CloseF: func() error {
				return expectedError
			},
		}

		client, err := updateengine.New(func() (dbus.Connection, error) { return mockConnection, nil })
		if err != nil {
			t.Fatalf("Got unexpected error while creating client: %v", err)
		}

		if err := client.Close(); !errors.Is(err, expectedError) {
			t.Fatalf("Expected error closing client %q, got %q", expectedError, err)
		}
	})
}

func testStatus() updateengine.Status {
	return updateengine.Status{
		LastCheckedTime:  10,
		Progress:         20,
		CurrentOperation: updateengine.UpdateStatusVerifying,
		NewVersion:       "1.2.3",
		NewSize:          30,
	}
}

func statusToSignalBody(s updateengine.Status) []interface{} {
	return []interface{}{s.LastCheckedTime, s.Progress, s.CurrentOperation, s.NewVersion, s.NewSize}
}
