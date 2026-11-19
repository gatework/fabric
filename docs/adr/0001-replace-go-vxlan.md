# 0001 fabric replaces go-vxlan

Status: accepted (2026-11-18)

## Context

go-vxlan is a port of a Ruby tool. Its parsers emit `map[string]any` that is round-tripped through JSON into typed structs; it reimplements plan persistence, execution, storage and batching that go-netops now provides; it can apply only three hard-coded increments; and it has no notion of desired state, so it cannot plan a fabric, only patch it.

## Decision

Build a new module, `github.com/gatework/fabric`, from the operator's model of a fabric (regions, tenants, networks, attachments) and freeze go-vxlan as a behaviour reference.

- Parsers produce `device` types directly; there is no untyped intermediate.
- go-netops provides evidence, assessment, drift, plan envelopes, execution and storage.
- Every go-vxlan detection has a counterpart rule (segment relations, VNI layer conflicts, NVE/BGP/OSPF/vPC health, port-channel members, consensus on mappings, gateways, anycast MAC and route targets, baseline drift, regressions).

## Consequences

- go-vxlan retires once fabric has run alongside it on production collections. Until then both read the same snapshot layout.
- Ruby byte compatibility (fingerprints, golden files) is dropped; configuration fingerprints are SHA-256 of the normalized running configuration.
- Deliberate differences: fabric checks against declared intent and a layered baseline, not only against consensus; it plans arbitrary intent changes, not three increment kinds.
