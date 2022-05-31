// Copyright 2015 CoreOS, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package updateengine

import (
	"fmt"

	godbus "github.com/godbus/dbus/v5"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/dbus"
)

const (
	// DBusPath is an object path used by update_engine.
	DBusPath = "/com/coreos/update1"
	// DBusDestination is a bus name of update_engine service.
	DBusDestination = "com.coreos.update1"
	// DBusInterface is a update_engine interface name.
	DBusInterface = DBusDestination + ".Manager"
	// DBusSignalNameStatusUpdate is a name of StatusUpdate signal from update_engine interface.
	DBusSignalNameStatusUpdate = "StatusUpdate"
	// DBusMethodNameGetStatus is a name of the method to get current update_engine status.
	DBusMethodNameGetStatus = "GetStatus"

	signalBuffer = 32 // TODO(bp): What is a reasonable value here?
)

// Client allows reading update_engine status using D-Bus.
type Client interface {
	// ReceiveStatuses listens for D-Bus signals coming from update_engine and converts them to Statuses
	// emitted into a given channel. It returns when stop channel gets closed or when the value is sent to it.
	ReceiveStatuses(rcvr chan<- Status, stop <-chan struct{})

	// Close closes underlying connection to the DBus broker. It is up to the user to close the connection
	// and avoid leaking it.
	//
	// Receive statuses call must be stopped before closing the connection.
	Close() error
}

// DBusConnection is set of methods which client expects D-Bus connection to implement.
type DBusConnection interface {
	Close() error
	AddMatchSignal(...godbus.MatchOption) error
	Signal(chan<- *godbus.Signal)
	Object(string, godbus.ObjectPath) godbus.BusObject
}

type caller interface {
	Call(method string, flags godbus.Flags, args ...interface{}) *godbus.Call
}

type client struct {
	conn   DBusConnection
	object caller
	ch     chan *godbus.Signal
}

// New creates new instance of Client and initializes it.
func New(connector dbus.Connector) (Client, error) {
	conn, err := dbus.New(connector)
	if err != nil {
		return nil, fmt.Errorf("creating D-Bus client: %w", err)
	}

	matchOptions := []godbus.MatchOption{
		godbus.WithMatchInterface(DBusInterface),
		godbus.WithMatchMember(DBusSignalNameStatusUpdate),
	}

	if err := conn.AddMatchSignal(matchOptions...); err != nil {
		return nil, fmt.Errorf("adding filter: %w", err)
	}

	ch := make(chan *godbus.Signal, signalBuffer)
	conn.Signal(ch)

	return &client{
		ch:     ch,
		conn:   conn,
		object: conn.Object(DBusDestination, godbus.ObjectPath(DBusPath)),
	}, nil
}

// ReceiveStatuses receives signal messages from dbus and sends them as Statues
// on the rcvr channel, until the stop channel is closed. An attempt is made to
// get the initial status and send it on the rcvr channel before receiving
// starts.
func (c *client) ReceiveStatuses(rcvr chan<- Status, stop <-chan struct{}) {
	// If there is an error getting the current status, ignore it and just
	// move onto the main loop.
	//
	//nolint:errcheck // This will be fixed once we introduce error handling to receiving statuses.
	st, _ := c.getStatus()
	rcvr <- st

	for {
		select {
		case <-stop:
			return
		case signal := <-c.ch:
			rcvr <- NewStatus(signal.Body)
		}
	}
}

// Close closes internal D-Bus connection.
func (c *client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}

	return nil
}

// getStatus gets the current status from update_engine.
func (c *client) getStatus() (Status, error) {
	call := c.object.Call(DBusInterface+"."+DBusMethodNameGetStatus, 0)
	if call.Err != nil {
		return Status{}, call.Err
	}

	return NewStatus(call.Body), nil
}
