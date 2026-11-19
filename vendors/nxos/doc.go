// Package nxos owns every piece of Cisco NX-OS syntax in the module: it
// parses running configurations and show-command output into package
// device types, and renders plan operations into configuration commands.
//
// Parsers never fail on content they do not understand. Unknown
// configuration is counted per keyword in [device.Config.Unparsed]; unknown
// show-output lines downgrade the observation to partial with a coded
// issue. Only malformed input (oversized, invalid UTF-8) is an error.
// No parser output or error ever carries a secret: keys and communities are
// reduced to digests.
package nxos
