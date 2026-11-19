// Package endpoint indexes where hosts are: from every switch's EVPN
// MAC-IP and MAC routes and its MAC table, it answers "where is this MAC
// or IP" across regions — the switch and port that learned it locally and
// the VTEPs that installed it remotely — and finds anomalies: one IP
// behind several MACs, one MAC learned locally on switches that are not
// vPC peers, and EVPN duplicate or frozen entries.
//
// The package performs no I/O.
package endpoint
