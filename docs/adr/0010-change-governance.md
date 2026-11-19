# 0010 Change records, risk classes and the deploy gate

Status: accepted (2026-11-19)

## Decision

- Every bundle carries a change record (ticket, requester, reason, maintenance window with time zone, created and expires — 24 h by default) and its risk; both enter the bundle identifier, so editing either invalidates the plan.
- Risk follows ITIL classes, derived from what the plan touches:
  - standard — new networks, tenants, ports (nothing existing changes);
  - normal — existing networks or ports change, objects without endpoints are deleted, baseline settings change;
  - major — underlay or control-plane objects, any spine, deleting objects with endpoints, more than one region, or more than 50 disturbed endpoints (tunable).
  Major changes are scheduled one switch per wave behind a canary.
- Endpoints are counted from the snapshot's MAC tables on the VLANs and ports whose existing state changes; creations disturb nobody.
- `deploy --apply` passes the gate first and contacts no device when refused: expired plan; outside the window (unless `--emergency` with a reason, recorded as a bypass); missing ticket when `--require-ticket`; major change without `--ack-risk`.
- The execution record keeps the gate decision, timings, and every switch's health before and after; `fabric report deploy` renders the post-implementation review (text or Markdown) with rollback commands. `fabric rollback` re-checks health around the rollback and reports what came back.
