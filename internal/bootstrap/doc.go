// Package bootstrap wires the application: it reads intent, baseline and
// policy files, resolves device credentials from the environment and
// builds the dialers the workflows use. Vendors and adapters are chosen
// here explicitly; nothing registers itself.
package bootstrap
