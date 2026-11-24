# 0015 What changed between two collections

Status: accepted (2026-11-24)

## Context

After an incident or a change window, the first question is "what changed since the last collection?". `check --previous` reports a few regressions as findings: lost NVE peers, EVPN sessions and OSPF neighbors. It does not say who changed which configuration, which ports went down, or which endpoints moved. Forward/IP Fabric snapshot diffs and NCM change audits answer exactly that.

## Decision

- `fabric diff --from <old> [--snapshot <new>]` compares two snapshot directories, switch by switch, keyed by management address.
- **Configuration.**
  - Each running configuration is parsed with the netops Cisco dialect and compared block by block with `diff.Tree`.
  - ACL entries are order-sensitive.
  - Every line is masked before it leaves the workflow. A removal and an addition that read the same once masked are merged into one change, reported as "secret changed".
  - Changes are grouped by top-level block and summarized by kind (`interface ×11`, `aaa ×6`).
- **State.** State is compared only when both sides observed it:
  - Ethernet and port-channel ports going down or coming up;
  - OSPF FULL neighbors, established BGP EVPN sessions and up NVE peers lost or gained;
  - locally learned endpoints that moved to another port, or appeared or vanished.

  A dimension that the configuration proves absent does not count as collected state. A side without state is reported as not compared, never as unchanged.
- **Output.** Switches with losses come first, then those with the most changed lines. `--switch` or `-v` prints the details. The command exits with findings when anything changed.
- The domain package `delta` performs no I/O. `workflow.Diff` reads the snapshots, parses and masks.

## Consequences

Between the 2026-10-14 backup and the 2026-11-13 capture, 17 of 83 switches changed:
- SHA2-LEF18 and SHA2-LEF19 moved 11 server ports from VLAN 99 to VLAN 233 and described them as K8S hosts.
- CKG3-LEF25 changed 27 interfaces and 3 VLANs.
- SZX9-LEF11 gained TACACS+ AAA, and its TACACS keys changed.
- Several switches gained NTP servers.

The backup holds configuration only, so no state is compared for it.
