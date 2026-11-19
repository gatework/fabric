// Package governance applies change management to fabric plans: a change
// record (ticket, requester, reason, maintenance window, expiry) bound to
// every bundle, an automatic risk class derived from what the plan
// touches (standard, normal or major change), and the gate a deployment
// must pass: not expired, inside the window unless declared an emergency,
// a ticket when required, and an explicit acknowledgement for major risk.
//
// The package performs no I/O.
package governance
