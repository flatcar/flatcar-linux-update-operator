package updateengine_test

import (
	"testing"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/updateengine"
)

//nolint:paralleltest // Test uses environment variables, which are global.
func Test_Creating_client_fails_when(t *testing.T) {
	t.Run("connecting_to_system_bus_fails", func(t *testing.T) {
		t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "foo")

		if _, err := updateengine.New(updateengine.DBusSystemPrivateConnector); err == nil {
			t.Fatalf("Creating client should fail when unable to connect to system bus")
		}
	})
}
