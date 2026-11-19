// Package numbering allocates identifiers and addresses deterministically:
// the lowest free number of a range, the lowest free host address of a
// prefix, or the lowest free aligned subnet. Allocation never wraps,
// guesses or reuses: what is used is passed in explicitly, and the same
// inputs always yield the same result.
//
// The package performs no I/O.
package numbering
