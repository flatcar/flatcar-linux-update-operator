package updateengine_test

import (
	"fmt"
	"os"
	"testing"

	dbus "github.com/godbus/dbus/v5"
	"github.com/google/go-cmp/cmp"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/updateengine"
)

//nolint:paralleltest // Test uses environment variables and D-Bus which are global.
func Test_Emitted_status_parses(t *testing.T) {
	expectedStatus := updateengine.Status{
		LastCheckedTime:  10,
		Progress:         20,
		CurrentOperation: updateengine.UpdateStatusVerifying,
		NewVersion:       "1.2.3",
		NewSize:          30,
	}

	// Order and types of returned values here are what we are really testing.
	getStatusTestResponse := func(message dbus.Message) (int64, float64, string, string, int64, *dbus.Error) {
		s := expectedStatus

		return s.LastCheckedTime, s.Progress, s.CurrentOperation, s.NewVersion, s.NewSize, nil
	}

	withMockGetStatus(t, getStatusTestResponse)

	client, err := updateengine.New()
	if err != nil {
		t.Fatalf("Creating client should succeed, got: %v", err)
	}

	stop := make(chan struct{})

	t.Cleanup(func() {
		close(stop)
		if err := client.Close(); err != nil {
			t.Fatalf("Failed closing client: %v", err)
		}
	})

	ch := make(chan updateengine.Status, 1)

	go client.ReceiveStatuses(ch, stop)

	status := <-ch

	if diff := cmp.Diff(expectedStatus, status); diff != "" {
		t.Fatalf("Unexpectected status values received:\n%s", diff)
	}
}

//nolint:paralleltest // Test uses environment variables, which are global.
func Test_Connecting_to_non_existing_system_bus_fails(t *testing.T) {
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "foo")

	if _, err := updateengine.New(); err == nil {
		t.Fatalf("Creating client should fail when unable to connect to system bus")
	}
}

const (
	testDbusSocketEnv = "FLUO_TEST_DBUS_SOCKET"
)

func testSystemConnection(t *testing.T) *dbus.Conn {
	t.Helper()

	socket := os.Getenv(testDbusSocketEnv)
	if socket == "" {
		t.Skipf("%q environment variable empty", testDbusSocketEnv)
	}

	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", fmt.Sprintf("unix:path=%s", socket))

	conn, err := dbus.SystemBus()
	if err != nil {
		t.Fatalf("Opening private connection to system bus: %v", err)
	}

	return conn
}

func withMockGetStatus(t *testing.T, getStatusF interface{}) {
	t.Helper()

	conn := testSystemConnection(t)

	if _, err := conn.RequestName(updateengine.DBusDestination, 0); err != nil {
		t.Fatalf("Requesting name: %v", err)
	}

	tbl := map[string]interface{}{
		updateengine.DBusMethodNameGetStatus: getStatusF,
	}

	if err := conn.ExportMethodTable(tbl, updateengine.DBusPath, updateengine.DBusInterface); err != nil {
		t.Fatalf("Exporting method table: %v", err)
	}
}
