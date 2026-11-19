// Package adopt reverse-engineers a draft intent from switches that are
// already running, so an existing fabric can be brought under management.
//
// Adoption never guesses. Where switches disagree (a VLAN mapped to two
// VNIs, a gateway with two addresses) the majority value becomes the draft
// and every dissenting switch is reported as a [Conflict] for the operator
// to resolve; planning the draft then shows exactly what would change.
// Values without a majority are left unmanaged.
//
// The package performs no I/O.
package adopt
