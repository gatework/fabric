# 0003 Typed operations proven by simulation and parse-back

Status: accepted (2026-11-18)

## Context

Rendering commands directly from intent makes it impossible to prove what a script does before it runs, and to know what to undo.

## Decision

- Planning produces typed operations, one per managed object, each holding the object's planned state (Have) and target state (Want).
- An operation can apply itself to the model, invert itself (rollback is the inverted operations in reverse order), check that the device still holds Have, and check that a readback holds Want.
- A plan is accepted only if (1) simulating the operations leaves nothing to change, (2) nothing outside the targeted objects changed, and (3) the simulated configuration introduces no new hazard (overlapping addresses in a VRF, a VNI mapped twice).
- The NX-OS renderer is tested so that its output, read back by the parser, satisfies every operation.
- Persistence uses go-netops `change.Plan` envelopes: loading re-renders every plan and requires identical commands, and the bundle identifier is a digest over all envelopes.

## Consequences

Readbacks are compared exactly: a device that normalizes configuration differently from the model will fail verification rather than be trusted. Logging-server severities are normalized at parse time for this reason (older releases omit the default 5).
