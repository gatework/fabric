// Package check assesses observed fabrics with go-netops assess rules.
//
// Two catalogs run: switch rules over each [*device.Switch] (configuration
// relations, operational health, security), and fabric rules over each
// [*Fabric] (consensus between VTEPs, reachability, intent and baseline
// drift, regressions against a previous collection). Every rule declares
// the evidence it needs; a rule whose evidence is incomplete is skipped,
// never read as healthy.
//
// Titles and details are operator-facing Chinese; identifiers stay in
// English.
//
// The package performs no I/O.
package check
