# Glossary

| Term | Meaning in fabric |
|---|---|
| **Region** | One site. Runs exactly one fabric; the unit of numbering, baseline overrides and rollout grouping. |
| **Fabric** | The VXLAN EVPN overlay of a region: one BGP AS, spines as route reflectors, one anycast gateway MAC. |
| **Switch** | A managed Nexus switch identified by its management address; has a **role**: spine, leaf or border. |
| **VTEP** | A leaf or border switch: terminates VXLAN tunnels through its NVE interface. |
| **vPC pair** | Two VTEPs of the same role sharing an anycast VTEP address; never changed in the same wave. |
| **Tenant** | A routed VRF with an **L3VNI** and the core VLAN that carries it. |
| **Network** | A VLAN mapped to an **L2VNI**, optionally routed by an anycast gateway in a tenant. |
| **Attachment** | A host-facing port (Ethernet or port-channel) and the networks it carries. |
| **Intent** | The declared desired state (YAML): regions, switches, tenants, networks, attachments. Root package `fabric`. |
| **Observation** | What a switch is: its typed configuration (`device.Config`) and operational tables (`device.State`), each with an evidence status. |
| **Dimension** | One kind of observed fact, collected by one command (configuration, nve_peers, bgp_evpn, …). |
| **Segment** | Everything one switch configures for one VNI: VLAN, VRF, NVE member, EVPN instance, SVI. |
| **Baseline** | The golden configuration independent of services: features, management settings, servers, vPC/NVE options. |
| **Setting** | One scalar baseline value (timezone, `vpc.peer_gateway`, …) with a uniform get/set on the model. |
| **Desired** | The target state of one switch's managed objects, compiled from intent and baseline. |
| **Op** | One typed change to one managed object, carrying its planned **Have** and target **Want** state. |
| **Changeset** | A switch's ordered ops bound to a renderer; implements netops `change.Change`. |
| **Managed scope** | Only objects the desired state mentions are changed; others are reported, never removed, unless pruning. |
| **Bundle** | A reviewed multi-switch plan: per-switch netops plan envelopes, review scripts and waves; identified by a digest. |
| **Wave** | Switches changed concurrently. Canary first, leaves (vPC peers apart), then borders and spines one at a time. |
| **Gate** | The post-change comparison of NVE peers, EVPN sessions, OSPF adjacencies and vPC peer health. |
| **Checkpoint** | An NX-OS configuration checkpoint taken before writing; the target of `fabric rollback`. |
| **Underlay** | A region's routed fabric: OSPF between loopbacks over point-to-point links, iBGP EVPN with spines as route reflectors, and the pools new switches draw from. Managed only when declared. |
| **Link** | One routed point-to-point fabric link between a VTEP and a spine, with both ends' ports and addresses. |
| **Port profile** | A reusable host-port definition (mode, networks, MTU, edge), like an SSID profile on a wireless controller. |
| **Request** | One typed operator task (add a gateway, add a switch, assign a port) applied to a copy of the intent. |
| **Allocation** | A value a request chose from a pool: VLAN, VNI, router-id, VTEP, link subnet, vPC domain, port-channel. |
| **Delta** | The operations a request's intent change needs, without drift that predates it. |
| **Day-0** | The complete configuration of a factory switch, rendered from its desired state. |
| **Health score** | 0–100 per switch: 60 for control-plane ratios with known evidence, 40 for compliance minus weighted findings. |
| **Impact** | What a plan touches: VLANs, tenants, ports, vPCs, underlay, and endpoints on objects whose state changes. |
| **Change class** | Standard, normal or major change, derived from the impact; major needs acknowledgement. |
| **Change record** | Ticket, requester, reason, maintenance window and expiry bound to a plan. |
| **Gate** | The deploy-time check of the change record and class; a refused gate contacts no device. |
| **PIR** | Post-implementation review: results, timings and health before/after per switch. |
| **Conflict** | A disagreement between switches found while adopting; the majority becomes the draft. |
