//go:build integration
// +build integration

package dbus_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/flatcar/flatcar-linux-update-operator/pkg/dbus"
)

const (
	testDbusSocketEnv = "FLUO_TEST_DBUS_SOCKET"
)

//nolint:paralleltest // This test use environment variables.
func Test_System_private_connector_successfully_connects_to_running_system_bus(t *testing.T) {
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", fmt.Sprintf("unix:path=%s", os.Getenv(testDbusSocketEnv)))

	client, err := dbus.New(dbus.SystemPrivateConnector)
	if err != nil {
		t.Fatalf("Failed creating client: %v", err)
	}

	if client == nil {
		t.Fatalf("Expected not nil client when new succeeds")
	}
}
