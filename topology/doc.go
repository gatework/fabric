// Package topology audits cabling: it compares the fabric links the intent
// declares with the CDP adjacencies the switches report, checks that both
// ends of every link are configured alike (MTU, OSPF process, area and
// network type, subnet), and builds the leaf × spine matrix an operator
// reads to see a missing or miswired uplink at a glance.
//
// A link is unknown, not healthy, when neither end's CDP was collected.
//
// The package performs no I/O.
package topology
