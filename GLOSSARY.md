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
| **Changeset** | A switch's ordered ops bound to a renderer; implements go-netops `change.Change`. |
| **Managed scope** | Only objects the desired state mentions are changed; others are reported, never removed, unless pruning. |
| **Bundle** | A reviewed multi-switch plan: per-switch go-netops plan envelopes, review scripts and waves; identified by a digest. |
| **Wave** | Switches changed concurrently. Canary first, leaves (vPC peers apart), then borders and spines one at a time. |
| **Gate** | The post-change comparison of NVE peers, EVPN sessions, OSPF adjacencies and vPC peer health. |
| **Checkpoint** | An NX-OS configuration checkpoint taken before writing; the target of `fabric rollback`. |
| **Conflict** | A disagreement between switches found while adopting; the majority becomes the draft. |
