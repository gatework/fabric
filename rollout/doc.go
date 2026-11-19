// Package rollout decides the order in which switches change and whether
// it is safe to continue.
//
// [Schedule] groups switches into waves: one canary leaf first, then the
// remaining leaves of each region with vPC peers always in different
// waves, then border leaves one at a time, then spines one at a time.
// [Gate] compares the operational state of a switch before and after its
// change; any lost NVE peer, EVPN session, OSPF adjacency or vPC peer
// stops the rollout.
//
// The package performs no I/O.
package rollout
