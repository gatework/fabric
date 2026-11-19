// Package status summarizes fabric health the way an operator reads a
// dashboard: per switch and per region, control-plane indicators (NVE
// peers, EVPN sessions, OSPF adjacencies, VNIs, vPC, bundles) as ratios of
// what is up to what is expected, compliance (intent and baseline drift,
// findings by severity), and a 0-100 score with a level.
//
// An indicator whose evidence was not collected completely is unknown,
// never healthy; a switch without any known indicator is unknown.
//
// The package performs no I/O.
package status
