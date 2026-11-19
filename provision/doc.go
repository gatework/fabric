// Package provision turns operator requests into intent changes, the way
// a controller turns "add an SSID" into configuration for every access
// point: add or retire a distributed gateway, add a tenant, bring a leaf,
// border or spine into the fabric (or retire it), and assign or release
// host ports, including LACP bundles across a vPC pair.
//
// Each [Request] validates against the intent and, when given, the
// observed switches; allocates what it needs (VLANs, VNIs, loopback and
// link addresses, vPC domains, port-channel numbers) from the region's
// pools; and records what changed in a [Result] for the operator. [Apply]
// works on a copy and returns it only if the whole intent stays valid.
//
// The package performs no I/O and contains no device syntax.
package provision
