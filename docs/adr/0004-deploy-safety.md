# 0004 Deployment safety: checkpoint, stop, manual rollback

Status: accepted (2026-11-18)

## Decision

- Deploy is a preview unless `--apply` is given, and executing requires naming the plan identifier (`--confirm` or typed on a terminal).
- Per switch: record health (NVE peers, EVPN sessions, OSPF adjacencies, vPC peer); read the configuration and take an NX-OS checkpoint in one session; refuse if the fingerprint or any planned object changed; execute with go-netops `apply.Run` (journaled, one command at a time, never retried); verify the readback (satisfied and nothing else changed); compare health again.
- Waves: one canary leaf (outside any vPC pair when possible), then leaves per region with vPC peers in different waves, then borders and spines one at a time.
- Any switch that is drifted, failed, uncertain or degraded stops every later wave. Nothing is rolled back automatically; the report names the checkpoint and the exact `fabric rollback` command (`rollback running-config checkpoint <name> atomic`).
- `copy running-config startup-config` runs only with `--save`, after verification.

## Consequences

A partial write leaves the switch uncertain; the operator decides between rollback and completion with full evidence, as go-netops requires.
