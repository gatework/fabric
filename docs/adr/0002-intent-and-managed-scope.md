# 0002 Declarative intent with managed scope

Status: accepted (2026-11-18)

## Context

Four production fabrics already exist and differ in numbering (L2VNI = 10000/100000/900000 + VLAN), naming and options. A controller that owns everything would either rewrite them or refuse to start.

## Decision

- The source of truth is a versioned YAML intent. `fabric import` drafts it from the running fabric; disagreements become conflicts and the majority becomes the draft.
- Only declared objects are managed. Objects the intent does not mention are reported by checks and never removed, unless the operator plans with `--prune`, which removes only fabric objects (VNI-mapped VLANs, L3VNI VRFs without routes or leaks, NVE members, EVPN instances, anycast SVIs, their BGP VRFs without neighbors).
- Zero-valued attributes in desired state are unmanaged: an existing value is kept and never compared. Names, MTUs and ACLs are therefore managed only where the intent states them.
- Settings the intent owns (anycast gateway MAC) are never derived into a baseline.

## Consequences

Adoption of an existing region yields a plan that contains only real gaps (for example an L2VNI missing from `evpn`, an inbound ACL bound on most VTEPs but not on one vPC pair). The corpus test proves this on 83 switches.
