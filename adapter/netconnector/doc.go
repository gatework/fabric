// Package netconnector connects to Nexus switches through net-connector.
// It is the only package in the module that talks to devices.
//
// A [Client] lends one [Session] per operation. Sessions run show commands,
// read and parse the running configuration, write single configuration
// lines for go-netops apply, and take or restore NX-OS checkpoints.
// Nothing is retried: a failed session is retired by net-connector and the
// caller decides what happens next.
package netconnector
