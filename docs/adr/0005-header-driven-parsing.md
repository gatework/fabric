# 0005 Header-driven show parsing and evidence status

Status: accepted (2026-11-18)

## Context

The 83 captured switches run NX-OS 7.0(3)I7 through 10.6(1). Column widths differ between releases, `show l2route evpn mac-ip all` has two column orders, newer BGP summaries append a per-route-type table, and `show vpc brief` wraps long cells onto continuation rows.

## Decision

- Show parsers locate tables by their headers and read rows by tokens, or by header column offsets where cells wrap (vPC). Column order comes from the header.
- A line no pattern explains downgrades the dimension to partial with a coded issue; a missing header on non-empty output is partial; "% Invalid command" is unsupported; a dimension the configuration proves absent (no vPC domain, no NVE) is complete without collection.
- Rules declare the dimensions they need and are skipped, never passed, when evidence is incomplete.
- Captures are keyed by the command in each sidecar, never by file number, because optional commands shift numbering.

## Consequences

All 996 dimension observations in the corpus parse complete with no issues. A new release that changes a table shows up as partial evidence, not as wrong data.
