# fabric design

## Operator model

```
Intent ─ Region (site) ── one Fabric
           ├─ Settings    ASN · anycast gateway MAC · replication · SVI MTU · ARP suppression
           ├─ Numbering   L2VNI = base + VLAN when a network omits its VNI
           ├─ Switch      name · mgmt · role (spine|leaf|border) · vPC peer
           ├─ Tenant      VRF · L3VNI · core VLAN
           ├─ Network     VLAN · L2VNI · tenant · anycast gateway · inbound ACL · placement
           └─ Attachment  switch · port · access|trunk · networks
Baseline ─ default → role → region → role in region
           features · forbidden features · settings · NTP/syslog servers · exclusive servers
```

The intent is what the fabric should carry; the baseline is how every switch must look regardless of services. Observation (`device`) is what each switch is: a typed running configuration and its operational tables, each dimension tagged complete, partial, failed, unsupported or not requested.

## Packages

| Package | Role | May import |
|---|---|---|
| `fabric` | intent model, value types, validation | — |
| `device` | observed model: Config, State, Segment, Setting | fabric |
| `plan` | Compile, Desired, Op, Diff, Order, Simulate, Converges, Safe, Changeset | fabric, device |
| `baseline` | Profile, Resolve, Check, Desired, Derive | fabric, device, plan |
| `adopt` | draft intent from observation, conflicts | fabric, device |
| `check` | switch and fabric rule catalogs on go-netops assess | fabric, device, plan, baseline |
| `rollout` | waves and health gates | fabric, device, plan |
| `report` | Chinese text for findings and conflicts | domain packages |
| `vendors/nxos` | all NX-OS syntax: parse config and show, render ops, profile | fabric, device, plan |
| `adapter/snapshot` | capture directory read/write | domain, vendors |
| `adapter/netconnector` | the only package that talks to switches | domain, vendors |
| `internal/workflow` | use cases: load, check, import, derive, plan, bundle, deploy, rollback, collect | everything above |
| `internal/bootstrap` | files, credentials, dialers | workflow, adapters |
| `internal/cli`, `cmd/fabric` | cobra CLI | bootstrap, workflow |

`architecture.yaml` enforces these edges and keeps every domain package free of I/O.

## Pipelines

```
collect   switch ──ssh──▶ outputs ──snapshot──▶ <batch>/<mgmt>/NN-<cmd>.{output,json}
observe   outputs ──nxos.Assemble──▶ device.Switch {Config, State, Coverage, Issues}
check     Fabric{switches, intent, baseline, previous} ──assess──▶ findings (skipped ≠ healthy)
import    switches ──adopt──▶ draft Region + conflicts
plan      Compile(region, switch) ⊕ baseline.Desired ─Diff─▶ ops ─Converges/Safe─▶ Changeset
          ─go-netops change.NewPlan─▶ envelope + review script + rollback ─rollout.Schedule─▶ waves
deploy    per wave: health → checkpoint+read → CheckBefore → apply.Run → Verify → health gate
          any non-verified switch stops later waves; rollback is explicit
```

## Rule catalog

Switch rules (23): evidence gaps; incomplete VNI segments; VNI used as L2 and L3; anycast SVI attributes; orphan SVIs; empty VRFs; unused BGP templates; undefined ACLs and route-maps; overlay residue on spines; NVE control plane; vPC domain options; anycast VTEP / PIP / virtual-rmac on vPC pairs; virtual peer-link TCAM; weak SNMP; NVE peers down; VNIs down; configured VNIs not operational; EVPN sessions down; sessions without prefixes; OSPF not full; vPC peer and consistency; port-channel members not bundled; duplicate or frozen MACs.

Fabric rules (11): VLAN→VNI mapping consensus; per-VNI gateway and VRF consensus; anycast gateway MAC; tenant L3VNI; VTEP reachability (each VTEP sees every other VTEP address); route-reflector sessions; duplicate router-ids and VTEP addresses; BGP template naming; intent drift (with candidate operations); baseline drift; regressions against the previous collection.

Every rule can be disabled, re-graded or tuned with a go-netops policy file (`--policy`).

## Day-2 operations (provisioning)

```
fabric network add | remove      distributed anycast gateway on every VTEP (or a subset)
fabric tenant add                tenant VRF with allocated L3VNI and core VLAN
fabric switch add | remove       leaf / border / spine on- and off-boarding, uplinks, vPC pairing
fabric switch day0               full configuration for a factory switch (POAP / console)
fabric port assign | release     port, LACP bundle or vPC bundle; profile or explicit VLANs
fabric port free                 assignable ports, quiet ones first
```

Each command: validate against intent (+ snapshot) → allocate from region pools → write the intent (`.bak` kept) → plan only the change on the affected observed switches (ADR 0006). Underlay management and day-0 are ADR 0007; ports and vPC are ADR 0008.

| Package | Role | May import |
|---|---|---|
| `numbering` | deterministic number and address allocation | — |
| `provision` | typed operator requests, observation checks | fabric, device, numbering |

## Assure and govern

```
fabric status     health per switch and region: control-plane ratios, findings, 0-100 score (ADR 0009)
fabric locate     MAC/IP → attaching switch and port (vPC aware), remote VTEPs, anomalies
fabric topology   intent links vs CDP and both ends' config; leaf × spine matrix
fabric capacity   pools and per-switch scale (VLAN, ports, MAC, NVE peers, bundles)
fabric report deploy   post-implementation review of an execution
```

Plans carry impact (VLANs, tenants, ports, vPCs, underlay, endpoints disturbed), a change record and a risk class; deploy passes the change gate first (ADR 0010). Performance method and numbers: ADR 0011.

| Package | Role | May import |
|---|---|---|
| `status`, `endpoint`, `topology`, `capacity` | views of the observed fabric | fabric, device |
| `governance` | change records, risk classes, gate | fabric, device, plan |
| `internal/parallel` | ordered bounded worker pool | — |
| `internal/synth` | synthetic fabrics for benchmarks and tests | everything |

## Next stage (designed, not yet built)

### M5.1 Endpoint locator — built in phase 3 (`fabric locate`)

Index `show l2route evpn mac-ip all`, `show l2route evpn mac all` and `show mac address-table` from every switch of every region. For a MAC or IP return: region, VLAN/VNI, the VTEP that learned it locally (`Local`/`HMM` producer), the port or vPC, and every remote VTEP that installed it, with sequence numbers. Flag mobility (rising sequence numbers), duplicates (`Dup`, `PF`) and disagreement between the local table and BGP. Data model: `endpoint.Index{byMAC, byIP}` built from `device.State`; pure, in a new `endpoint` package; CLI reads a snapshot.

### M5.2 Cabling and underlay audit — built in phase 3 (`fabric topology`)

From CDP and configuration: every leaf connects to every spine of its region; fabric links are routed point-to-point /30 or /31 with MTU 9216 on both ends; OSPF area and network type agree on both ends; vPC peers are cabled for keepalive (and peer-link when physical); no leaf-to-leaf links outside vPC pairs. Output both findings (as fabric rules) and a per-region adjacency matrix.

### M5.3 Change impact — built in phase 3 (plan impact and risk)

Before deploy, annotate each plan with the endpoints behind every touched VLAN/VNI and port (from M5.1), and the vPC members affected, so the operator reviews business impact rather than commands.

### Later

Netdisco/inventory sync for switch lists; multisite (BGW) intent; scheduled collection with `--previous` regression alerts; rendering for other NX-OS designs (multicast replication groups, IS-IS underlay) once a corpus exists.
