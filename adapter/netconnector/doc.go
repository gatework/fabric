// Package netconnector connects to Nexus switches through net-connector.
// It is the only package in the module that talks to devices.
//
// A [Client] opens one net-connector session per operation: it runs show
// commands, reads and parses the running configuration, lends a session to
// netops apply, takes or restores NX-OS checkpoints and saves. Nothing is
// retried; the caller decides what happens after a failure.
package netconnector
