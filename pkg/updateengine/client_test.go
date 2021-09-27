package updateengine_test

import (
	"fmt"
	"os"
	"testing"

	dbus "github.com/godbus/dbus/v5"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/updateengine"
)

//nolint:paralleltest // Test uses environment variables, which are global.
func Test_Connecting_to_non_existing_system_bus_fails(t *testing.T) {
	if err := os.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "foo"); err != nil {
		t.Fatalf("Setting systemd bus address: %v", err)
	}

	if _, err := updateengine.New(); err == nil {
		t.Fatalf("Creating client should fail when unable to connect to system bus")
	}
}

//nolint:funlen,tparallel // Test uses environment variables, which are global.
func Test_Emitted_status_parses(t *testing.T) {
	var (
		lastCheckedTime  int64   = 10
		progress         float64 = 20
		currentOperation         = updateengine.UpdateStatusVerifying
		newVersion               = "1.2.3"
		newSize          int64   = 30
	)

	withMockGetStatus(t, func(message dbus.Message) (int64, float64, string, string, int64, *dbus.Error) {
		return lastCheckedTime, progress, currentOperation, newVersion, newSize, nil
	})

	c, err := updateengine.New()
	if err != nil {
		t.Fatalf("Creating client should succeed, got: %v", err)
	}

	stop := make(chan struct{})
	ch := make(chan updateengine.Status, 1)

	go c.ReceiveStatuses(ch, stop)

	status := <-ch

	t.Run("first_value_as_last_checked_time", func(t *testing.T) {
		t.Parallel()

		if status.LastCheckedTime != lastCheckedTime {
			t.Errorf("Expected %v, got %v", lastCheckedTime, status.LastCheckedTime)
		}
	})

	t.Run("second_value_as_progress", func(t *testing.T) {
		t.Parallel()

		if status.Progress != progress {
			t.Errorf("Expected %v, got %v", progress, status.Progress)
		}
	})

	t.Run("third_value_as_current_operation", func(t *testing.T) {
		t.Parallel()

		if status.CurrentOperation != currentOperation {
			t.Errorf("Expected %q, got %q", currentOperation, status.CurrentOperation)
		}
	})

	t.Run("forth_value_as_new_version", func(t *testing.T) {
		t.Parallel()

		if status.NewVersion != newVersion {
			t.Errorf("Expected %q, got %q", newVersion, status.NewVersion)
		}
	})

	t.Run("fifth_value_as_new_size", func(t *testing.T) {
		t.Parallel()

		if status.NewSize != newSize {
			t.Errorf("Expected %v, got %v", newSize, status.NewSize)
		}
	})
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

	if err := os.Setenv("DBUS_SYSTEM_BUS_ADDRESS", fmt.Sprintf("unix:path=%s", socket)); err != nil {
		t.Fatalf("Setting systemd bus address: %v", err)
	}

	conn, err := dbus.SystemBus()
	if err != nil {
		t.Fatalf("Opening private connection to system bus: %v", err)
	}

	return conn
}

func withMockGetStatus(t *testing.T, fn interface{}) {
	t.Helper()

	conn := testSystemConnection(t)

	if _, err := conn.RequestName("com.coreos.update1", 0); err != nil {
		t.Fatalf("Requesting name: %v", err)
	}

	tbl := map[string]interface{}{
		"GetStatus": fn,
	}

	if err := conn.ExportMethodTable(tbl, "/com/coreos/update1", "com.coreos.update1.Manager"); err != nil {
		t.Fatalf("Exporting method table: %v", err)
	}
}
