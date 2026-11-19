# 0009 Health score and the evidence precondition

Status: accepted (2026-11-19)

## Decision

- `status` reports per switch the control-plane indicators as up/expected ratios: NVE peers (expected: every other VTEP address of the region), EVPN sessions (VTEP → every spine; spine → every VTEP router-id), underlay OSPF adjacencies (declared links when the underlay is managed; only the underlay process counts), configured VNIs operational, vPC peer/keepalive/consistency, bundle members of up port-channels.
- An indicator without complete evidence is unknown and excluded; a switch with no known indicator and missing evidence is unknown, never healthy. Spines expect no overlay (NVE peers and VNIs are not applicable).
- Score = 60 × mean of known indicators + max(0, 40 − weighted findings) with weights critical 20, high 8, medium 3, low 1. Level: failed below 60 or with any critical finding, degraded below 90, healthy otherwise. A region reports its worst and mean scores; its level follows the worst switch.

## Consequences

Scores are explainable from the table and comparable across collections. Findings dominate a switch with a healthy control plane but poor hygiene (weak SNMP, drift), which is intended: they are operational risk.
