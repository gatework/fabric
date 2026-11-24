# 0013 Maintenance impact: what breaks while a switch is out of service

Status: accepted (2026-11-24)

## Context

Before a reload, an upgrade or a drain, an operator has to know what will break and whether it is safe to proceed now. fabric collects everything needed to answer: uplink redundancy and BGP EVPN sessions (ADR 0012), the vPC state of both peers, port-channel membership, and the MAC and MAC-IP tables. But the answer had to be assembled by hand from `topology`, `locate` and `switch show`. Commercial fabric managers (Apstra's what-if, NDFC's maintenance mode) answer it directly.

## Decision

- A new domain package, `maintenance`, assesses one switch from the region's topology audit and its observed switches. It performs no I/O.
- **Spine.** It lists:
  - VTEPs whose only healthy uplink runs to this spine (isolated during the window);
  - VTEPs whose only established BGP EVPN session runs to it (their overlay routes would be withdrawn);
  - VTEPs that keep another path but lose redundancy.
  Either of the first two blocks maintenance.
- **Leaf and border.** Locally learned MAC addresses are grouped by host port, with member ports rolled into their port-channel and fabric links and the peer-link excluded. Each port carries its description, VLANs and local IPs. A port on a vPC that is up on the peer is dual-homed; every other port is single-homed and its endpoints are interrupted.
  - The vPC peer must be able to carry the pair alone: its uplinks are fully redundant, the peer adjacency and keepalive are up, and consistency checks pass. Dual-homed endpoints behind a peer that fails these checks block maintenance.
  - External CDP devices on host ports are listed.
- **Verdict.**
  - Blocked when something would be cut off.
  - Unknown when the needed evidence was not collected; unknown evidence is never treated as safe.
  - Risky when endpoints or redundancy are lost.
  - Ready otherwise.

  Each report lists what to do before the window. `fabric impact <switch>` prints it and exits 0 only when the verdict is ready.

## Consequences

On the 2026-11-13 corpus:
- SHA2-SPN01 is blocked, because SHA2-BDR02 already lost its uplink to SPN02.
- SHA2-LEF15 is risky: 85 dual-homed MACs move to SHA2-LEF16, whose uplinks and vPC are healthy.
- SHA2-BDR02 is risky: 21 single-homed endpoints on 16 ports, including NAS and iDRAC ports, with their descriptions and IPs.

## Amendment: border north–south (2026-11-24)

A border's VRF static routes are its exits from the fabric. For each exit, the other borders of the region with the same VRF route are classified by how they leave the fabric without this switch:
- **Own**: a FULL OSPF neighbor outside the underlay process that is not this switch. This is a valid backup.
- **Standby**: an external routed port of their own that is up and runs OSPF, but has no neighbor. This is typically the standby unit of a firewall pair, so it is risky, and the report asks to confirm the failover.
- **Through**: no other way out than this switch. This blocks maintenance.

OSPF adjacencies outside the underlay are listed. On the 2026-11-13 corpus, all four regions use the standby pattern: BDR01 peers with the active firewall, while BDR02's link to the other firewall is up without OSPF. Maintaining BDR01 therefore relies on a firewall failover.

Routes learned by BGP in VRFs (IPv4 unicast) are not collected and stay unassessed.
