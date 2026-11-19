# 0006 Operator requests edit the intent, then plan

Status: accepted (2026-11-19)

## Context

Day-2 work (a new gateway, a new leaf, a server port) should not mean editing YAML by hand, but the intent must stay the single source of truth.

## Decision

- Package `provision` models each operator task as a typed request (`AddNetwork`, `RemoveNetwork`, `AddTenant`, `AddSwitch`, `PairVPC`, `RemoveSwitch`, `AssignPort`, `ReleasePort`). A request validates against the intent and, when a snapshot is given, the observed switches; allocates what is missing; edits a copy of the intent; and reports changes, allocations and affected switches. `provision.Apply` returns the copy only if the whole intent validates.
- Allocation (`numbering`) is deterministic and explicit about what is used: the lowest free VLAN, VNI, port-channel or vPC domain of the region's pool, the lowest free host address, the lowest free aligned link subnet. Used values include the intent and every address or number configured on the region's switches.
- The CLI writes the intent back (keeping `.bak`) and plans the affected observed switches at once. Switches without evidence (a new switch) are listed with the day-0 command.
- A provisioning plan is scoped to the change (`plan.Delta`): operations the previous intent would also plan (drift that predates the request) are left out, so "add this gateway" changes only that gateway. An object that both drifted and is changed keeps its full operation.
- Retirement is targeted: a removed network deletes only its VLAN, SVI, NVE member and EVPN instance; a removed switch frees its link ports on the far end (reset to factory and shut) and, with explicit spine neighbors, deletes its BGP neighbor. No global prune is needed.

## Consequences

Every change leaves an auditable intent diff. Removing a network refuses while ports or profiles carry it or endpoints are learned on it (unless forced).
