// Package fabric is the intent model of a multi-region Cisco Nexus VXLAN
// EVPN fabric, written from a network operator's point of view.
//
// An [Intent] declares regions (one fabric each), their switches and vPC
// pairs, and the services the fabric carries: tenants (VRFs with an L3VNI),
// networks (VLAN-backed L2 segments with an optional anycast gateway) and
// attachments (access or trunk ports carrying networks). The model is the
// desired state; the observed state of a switch lives in package device.
//
// Value types such as [VLAN], [VNI] and [ASN] are parsed, never validated
// after the fact: constructing one proves it is in range. [Intent.Validate]
// checks cross-object invariants and reports every problem at once.
//
// The package performs no I/O.
package fabric
