package updateengine_test

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	dbus "github.com/godbus/dbus/v5"
	"github.com/google/go-cmp/cmp"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/updateengine"
)

//nolint:paralleltest,funlen,cyclop // Test uses environment variables and D-Bus which are global.
//                                     Just many subtests.
func Test_Receiving_status(t *testing.T) {
	t.Run("emits_first_status_immediately_after_start", func(t *testing.T) {
		ch, _ := testGetStatusReceiver(t, updateengine.Status{})

		timeout := time.NewTimer(time.Second)
		select {
		case <-ch:
		case <-timeout.C:
			t.Fatal("Failed getting status within expected timeframe")
		}
	})

	t.Run("parses_received_values_in_order_defined_by_update_engine", func(t *testing.T) {
		expectedStatus := testStatus()

		ch, _ := testGetStatusReceiver(t, expectedStatus)

		timeout := time.NewTimer(time.Second)
		select {
		case status := <-ch:
			if diff := cmp.Diff(expectedStatus, status); diff != "" {
				t.Fatalf("Unexpectected status values received:\n%s", diff)
			}
		case <-timeout.C:
			t.Fatal("Failed getting status within expected timeframe")
		}
	})

	t.Run("forwards_status_updates_received_from_update_engine_to_given_receiver_channel", func(t *testing.T) {
		firstExpectedStatus := updateengine.Status{}

		statusCh, conn := testGetStatusReceiver(t, firstExpectedStatus)

		secondExpectedStatus := testStatus()

		lastCheckedTime, progress, currentOperation, newVersion, newSize, _ := statusToRawValues(secondExpectedStatus, nil)

		withMockStatusUpdate(t, conn, lastCheckedTime, progress, currentOperation, newVersion, newSize)

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
		expectedStatus := updateengine.Status{}

		statusCh, conn := testGetStatusReceiver(t, expectedStatus)

		// Reset method table so GetStatus updates immediately returns error.
		// Once https://github.com/flatcar-linux/flatcar-linux-update-operator/issues/100 is solved,
		// we can make method call to actually return an error.
		tbl := map[string]interface{}{}
		if err := conn.ExportMethodTable(tbl, updateengine.DBusPath, updateengine.DBusInterface); err != nil {
			t.Fatalf("Failed resetting method table: %v", err)
		}

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

//nolint:paralleltest // Test uses environment variables, which are global.
func Test_Creating_client_fails_when(t *testing.T) {
	t.Run("connecting_to_system_bus_fails", func(t *testing.T) {
		t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "foo")

		if _, err := updateengine.New(updateengine.DBusSystemPrivateConnector); err == nil {
			t.Fatalf("Creating client should fail when unable to connect to system bus")
		}
	})
}

func testGetStatusReceiver(t *testing.T, status updateengine.Status) (chan updateengine.Status, *dbus.Conn) {
	t.Helper()

	getStatusTestResponse := func(message dbus.Message) (int64, float64, string, string, int64, *dbus.Error) {
		return statusToRawValues(status, nil)
	}

	conn := withMockGetStatus(t, getStatusTestResponse)

	client, err := updateengine.New(updateengine.DBusSystemPrivateConnector)
	if err != nil {
		t.Fatalf("Creating client should succeed, got: %v", err)
	}

	stop := make(chan struct{})

	t.Cleanup(func() {
		// Stopping receiver routine must be done before closing the client. See
		// https://github.com/flatcar-linux/flatcar-linux-update-operator/issues/101 for more details.
		close(stop)
		if err := client.Close(); err != nil {
			t.Fatalf("Failed closing client: %v", err)
		}
	})

	ch := make(chan updateengine.Status, 1)

	go client.ReceiveStatuses(ch, stop)

	return ch, conn
}

const (
	testDbusSocketEnv = "FLUO_TEST_DBUS_SOCKET"
)

func testStatus() updateengine.Status {
	return updateengine.Status{
		LastCheckedTime:  10,
		Progress:         20,
		CurrentOperation: updateengine.UpdateStatusVerifying,
		NewVersion:       "1.2.3",
		NewSize:          30,
	}
}

func statusToRawValues(s updateengine.Status, err *dbus.Error) (int64, float64, string, string, int64, *dbus.Error) {
	return s.LastCheckedTime, s.Progress, s.CurrentOperation, s.NewVersion, s.NewSize, err
}

func testSystemConnection(t *testing.T) *dbus.Conn {
	t.Helper()

	socket := os.Getenv(testDbusSocketEnv)
	if socket == "" {
		t.Skipf("%q environment variable empty", testDbusSocketEnv)
	}

	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", fmt.Sprintf("unix:path=%s", socket))

	conn, err := dbus.SystemBusPrivate()
	if err != nil {
		t.Fatalf("Opening private connection to system bus: %v", err)
	}

	return conn
}

func withMockGetStatus(t *testing.T, getStatusF interface{}) *dbus.Conn {
	t.Helper()

	conn := testSystemConnection(t)

	methods := []dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Getuid()))}

	if err := conn.Auth(methods); err != nil {
		t.Fatalf("Failed authenticating to system bus: %v", err)
	}

	if err := conn.Hello(); err != nil {
		t.Fatalf("Failed sending hello to system bus: %v", err)
	}

	if _, err := conn.RequestName(updateengine.DBusDestination, 0); err != nil {
		t.Fatalf("Requesting name: %v", err)
	}

	t.Cleanup(func() {
		if _, err := conn.ReleaseName(updateengine.DBusDestination); err != nil {
			t.Fatalf("Failed releasing name: %v", err)
		}
	})

	tbl := map[string]interface{}{
		updateengine.DBusMethodNameGetStatus: getStatusF,
	}

	if err := conn.ExportMethodTable(tbl, updateengine.DBusPath, updateengine.DBusInterface); err != nil {
		t.Fatalf("Exporting method table: %v", err)
	}

	t.Cleanup(func() {
		tbl := map[string]interface{}{}
		if err := conn.ExportMethodTable(tbl, updateengine.DBusPath, updateengine.DBusInterface); err != nil {
			t.Fatalf("Failed resetting method table: %v", err)
		}
	})

	return conn
}

func withMockStatusUpdate(t *testing.T, conn *dbus.Conn, values ...interface{}) {
	t.Helper()

	emitName := fmt.Sprintf("%s.%s", updateengine.DBusInterface, updateengine.DBusSignalNameStatusUpdate)

	ticker := time.NewTicker(100 * time.Millisecond)
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})

	t.Cleanup(func() {
		close(stopCh)
		<-doneCh
	})

	go func() {
		for {
			select {
			case <-ticker.C:
				if err := conn.Emit(updateengine.DBusPath, emitName, values...); err != nil {
					t.Logf("Failed emitting mock status: %v", err)
					t.Fail()
				}
			case <-stopCh:
				close(doneCh)

				return
			}
		}
	}()
}
