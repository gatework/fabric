// Package snapshot reads and writes per-switch capture directories: one
// directory per switch named after its management address, holding for
// every command a raw "NN-<command>.output" receipt and a ".json" sidecar
// with the command, its SHA-256 and the prompts around it.
//
// File numbers are not stable between switches (optional commands shift
// them), so captures are keyed by the sidecar's command, never by number.
package snapshot
