// Package capacity measures how full a fabric is: per region, the number
// pools new services and switches draw from (VLANs, L3VNIs, router-ids,
// VTEP and anycast addresses, link subnets, vPC domains, port-channel
// numbers); per switch, configured VLANs, used Ethernet ports, MAC table
// entries, NVE peers and port-channels against platform limits.
//
// Limits are parameters: the defaults are conservative Nexus 9000 figures
// and policy overrides them per deployment.
//
// The package performs no I/O.
package capacity
