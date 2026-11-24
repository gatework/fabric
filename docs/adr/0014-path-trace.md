# 0014 Path trace between two endpoints

Status: accepted (2026-11-24)

## Context

"Why can't A reach B?" is the question operators ask most. `locate` places one endpoint; nothing verified the path between two. Forward Enterprise and IP Fabric answer it with a path search over collected state.

## Decision

- A new domain package, `reach`, traces two endpoints given as MAC or IP addresses, using the endpoint index, the observed switches and the topology audits. It performs no I/O.
- **Mode.**
  - Local: same switch and VLAN.
  - Bridged: same L2VNI.
  - Routed: same VRF, through the anycast gateway SVI, and over the VRF's L3VNI when the two VTEPs differ.
  - Separated: different VRFs, which do not meet inside the fabric.
- **Hops, outbound and back.**
  1. The access port is up and carries the VLAN.
  2. The L2VNI is up. When routed, the gateway SVI has an address and is not shut, and the L3VNI is up.
  3. The ingress VTEP holds an EVPN route to the destination (MAC, or IP when routed) whose next hop is the destination's VTEP or its vPC pair's anycast VTEP.
  4. The NVE peer to that next hop is up.
  5. Both VTEPs keep healthy uplinks; the spines they both reach form the ECMP set, and a VTEP that lost redundancy is flagged.
  6. The destination's segment and port are checked in the same way.

  The reverse route is checked separately, because one-way failures are common.
- The first failing hop is the break and comes with a hint. Unknown evidence is never taken as a pass.
- `fabric path <src> <dst>` draws the path on one line, then lists the verified hops and the conclusion. It exits 0 only for a complete path.

## Consequences

On the 2026-11-13 corpus:
- SHA2-LEF01 → SHA2-LEF10 traces as routed over L3VNI 20000 with ECMP over both spines.
- SHA2-LEF01 → SHA2-BDR02 traces as bridged with a single ECMP path, because BDR02 lost an uplink.

Path MTU is judged from configuration only. VXLAN adds 50 bytes:
- An underlay whose smallest healthy link MTU cannot carry a 1500-byte frame breaks the path.
- An access port or gateway SVI configured above what the underlay carries is a warning, not a break. With 9216 on both the SVI and the underlay, the common design, frames above 9166 bytes are dropped.

Layer-2 MTU set by network-qos is not collected, so an unconfigured access port is not judged. ACLs and firewall policy are not evaluated; firemon covers firewall policy.
