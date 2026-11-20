// Package workflow implements the operator use cases: load evidence,
// check, adopt, derive baselines, plan, deploy and roll back. It wires the
// domain packages together and reaches devices and files only through the
// ports it declares ([Switch], [Dialer]) and the netops store.
package workflow
