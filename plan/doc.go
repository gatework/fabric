// Package plan turns desired state into verified, ordered configuration
// operations for one switch.
//
// [Compile] derives a switch's [Desired] state from the intent; baselines
// contribute further desired settings. [Diff] compares desired with the
// observed configuration and returns typed [Op] values, each carrying the
// object's current (Have) and target (Want) state so that it can be
// rendered, inverted for rollback and verified on readback. [Simulate]
// applies operations to a copy of the configuration; a plan is accepted
// only when the simulated configuration satisfies the desired state.
//
// A [Changeset] binds a switch's operations to a vendor [Renderer] and
// implements the go-netops change.Change contract, so plans are persisted,
// re-rendered on load and checked before and after execution by go-netops.
//
// The package performs no I/O and contains no device syntax.
package plan
