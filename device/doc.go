// Package device is the observed state of one Nexus switch: its typed
// running configuration ([Config]) and its operational tables ([State]),
// each tagged with how completely it was observed.
//
// The types are vendor-shaped (NX-OS VXLAN EVPN) but syntax-free: parsing
// and rendering live in vendors/nxos. Every map is keyed by the object's
// device identity, so two configurations can be compared object by object.
// Secrets are never stored; keys are kept only as digests so that drift in
// a shared secret is still detectable.
//
// The package performs no I/O.
package device
