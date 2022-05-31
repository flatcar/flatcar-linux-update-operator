// Package dbus provides a helper function for creating new D-Bus client.
package dbus

import (
	"fmt"
	"os"
	"strconv"

	godbus "github.com/godbus/dbus/v5"
)

// Client is an interface describing capabilities of internal D-Bus client.
type Client interface {
	AddMatchSignal(matchOptions ...godbus.MatchOption) error
	Signal(ch chan<- *godbus.Signal)
	Object(dest string, path godbus.ObjectPath) godbus.BusObject
	Close() error
}

// Connection is an interface describing how much functionality we need from object providing D-Bus connection.
type Connection interface {
	Auth(authMethods []godbus.Auth) error
	Hello() error

	Client
}

// Connector is a constructor function providing D-Bus connection.
type Connector func() (Connection, error)

// SystemPrivateConnector is a standard connector using system bus.
func SystemPrivateConnector() (Connection, error) {
	return godbus.SystemBusPrivate()
}

// New creates new D-Bus client using given connector.
func New(connector Connector) (Client, error) {
	if connector == nil {
		return nil, fmt.Errorf("no connection creator given")
	}

	conn, err := connector()
	if err != nil {
		return nil, fmt.Errorf("connecting to D-Bus: %w", err)
	}

	methods := []godbus.Auth{godbus.AuthExternal(strconv.Itoa(os.Getuid()))}

	if err := conn.Auth(methods); err != nil {
		// Best effort closing the connection.
		//
		//nolint:errcheck // TODO: We will add logger as a dependencty to client to fix it.
		_ = conn.Close()

		return nil, fmt.Errorf("authenticating to D-Bus: %w", err)
	}

	if err := conn.Hello(); err != nil {
		// Best effort closing the connection.
		//
		//nolint:errcheck // TODO: We will add logger as a dependencty to client to fix it.
		_ = conn.Close()

		return nil, fmt.Errorf("sending hello to D-Bus: %w", err)
	}

	return conn, nil
}
