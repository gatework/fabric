# 0007 Managed underlay and day-0 configuration

Status: accepted (2026-11-19)

## Decision

- A region may declare an `underlay`: OSPF process and area, link MTU and prefix (/31 for new regions, inferred /30 for existing ones), BGP peering design (dynamic prefix neighbors on spines by default, or explicit per-VTEP neighbors; template names) and address pools. Switches carry their router-id (loopback0) and VTEP (NVE source; loopback1, or loopback0 where an existing site sources NVE from it). Links and vPC pairs are first-class objects.
- With an underlay, planning compiles per role: hostname, features, loopbacks, routed links, OSPF, the BGP process with templates and neighbors (VTEPs to every spine; spines to the router-id pool or every VTEP, as route reflectors), the NVE interface, and for vPC members the domain (keepalive over mgmt, virtual peer-link between router-ids with DSCP 56, or a physical peer-link bundle) and the options an anycast VTEP pair needs (peer-switch, peer-gateway, layer3 peer-router, auto-recovery, ip arp synchronize, advertise virtual-rmac, advertise-pip).
- New typed operations (interface, OSPF, BGP process, peer template/neighbor, NVE, vPC domain, port reset) follow the Have/Want contract of ADR 0003; settings are planned after the objects they live on, and eligibility is checked progressively in execution order.
- `nxos.Day0` renders the full configuration of a factory switch from its desired state (ports seeded as switchports so routed links say `no switchport`); it parses back to the desired state. A TCAM carve for virtual peer-link is a reload-bound prerequisite and is listed as a comment, never pushed.
- Adoption infers the underlay and refuses (leaves it unmanaged, with a conflict) when the design is ambiguous. Only ports in the underlay's own OSPF process are fabric links, so a border's external OSPF link is never taken over.

## Consequences

On the 83-switch corpus all four regions adopt their underlay (150 links) and planning changes no underlay object; the only control-plane changes are the two real defects (missing virtual-rmac/advertise-pip on one vPC pair, a duplicated border router-id).
