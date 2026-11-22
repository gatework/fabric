# 0012 Operator-first CLI: layered link diagnosis, uplink redundancy, switch detail

Status: accepted (2026-11-22)

## Context

The views answered "what" but not "why" or "what next". `topology` printed a leaf × spine matrix of CDP adjacency: a missing link showed as ✗ with "两端 CDP 均未看到对端", while the port state, the interface addresses, the layer (routed or switchport) and the OSPF adjacency on the link were collected but never shown. The same fault surfaced as `OSPF 1/2` in `status` and as `link_missing` in `check`, with nothing tying them together or saying what it means for the switch. There was no view of one switch, and no command completed switch or region names.

Commercial fabric managers (Cisco NDFC, Juniper Apstra, Forward/IP Fabric) lead with anomalies and their impact, verify a link layer by layer, report redundancy per node, and give each device a detail page.

## Decision

- **Layered link verification.** Every declared fabric link is verified bottom up: physical (both ends' `show interface brief` state and reason), neighbor (CDP), configuration (routed port, no shutdown, the intent's address, MTU/OSPF/subnet alike on both ends) and routing (OSPF FULL on the link from every end whose OSPF was collected; a one-sided adjacency fails). A link's fault is its lowest failing layer, since every layer above fails as a consequence. The diagnosis names a cause and a next step (cable/optic, err-disabled, admin shutdown, CDP disabled, miscabling, config mismatch, OSPF parameters).
- **Ends.** Each link reports both ends: interface, L3/L2, configured and intended address, status and reason, speed, MTU.
- **Evidence, not absence, decides.** A port absent from `show interface brief` is unknown for that end, unless the complete configuration also lacks it (then the port does not exist). Unknown layers never make a link healthy or failed.
- **Uplink redundancy.** Per VTEP: declared and healthy uplinks, spines reached and lost, level full / degraded / single path / isolated / unknown, and the distinct speeds of healthy uplinks (unequal speeds are flagged: ECMP splits flows per link, so the slower link congests first). Per spine: healthy downlinks. Intent switches missing from the snapshot are listed.
- **Problems first.** `topology` prints a summary, then numbered faults with ends, cause, impact, hint and the per-layer verdicts, then redundancy risks; `--matrix`, `--switch` and `--all` add the matrix and the per-link table. `status` gains the top finding per switch. `check` filters by `--switch` and `--severity`.
- **Switch detail.** `fabric switch show <name>` combines health, uplinks or downlinks with the link table, OSPF and BGP EVPN sessions named by peer, NVE peers and VNIs, vPC, port-channels and the switch's findings grouped by title. Tables longer than six rows list only abnormal rows unless `-v`.
- **Interaction.** Every view ends with the next commands to run. `--switch`, `--region`, `--severity` and `switch show` complete from the intent named by `--intent` or `fabric.yml`.

## Consequences

The two missing sha2 links of the 2026-11-13 corpus read as "physical: Link not connected on both ends → SHA2-BDR02 / SHA2-LEF15 single path", and four leaves surface with 40G/100G uplinks that the matrix could not show. `topology` exits with findings for redundancy risks, speed asymmetry and unobserved switches, not only for miscabling. JSON output gains `ends`, `layers`, `diagnosis`, `uplinks`, `spine_loads` and `unobserved`; existing fields are unchanged.
