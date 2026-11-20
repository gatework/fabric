# fabric development notes

Follow [the gatework Go engineering standard](https://github.com/gatework/go-standards/blob/main/STANDARD.md) (go-standards/STANDARD.md); this file adds only what is specific to fabric. Run `make check` before every commit.

Before any Go coding, review, debugging or setup task, load the `samber/cc-skills-golang@golang-how-to` skill first.

## Maturity

Not in production: no compatibility promise (GEN-1). fabric replaces go-vxlan ([ADR 0001](docs/adr/0001-replace-go-vxlan.md)); go-vxlan is a behaviour reference only.

## Boundaries

- The root package `fabric` is the intent model; `device` is the observed model. Neither performs I/O. `architecture.yaml` enforces the direction; register every new package there first.
- `vendors/nxos` owns all NX-OS syntax: parsing configuration and show output into `device`, and rendering `plan` operations into commands. Nothing outside it contains CLI text.
- `adapter/netconnector` is the only package that holds device sessions, and `internal/bootstrap` the only one that assembles the net-connector dialer (SSH transport, built-in registry, credentials); `adapter/snapshot` is the only package that reads capture directories.
- Vendors and adapters are wired explicitly in `internal/bootstrap`; no `init` registration.

## Semantics that must not regress

- Unknown evidence never authorizes a change: rules whose dimensions are incomplete are skipped, and planning refuses a switch whose configuration was not observed completely.
- The intent manages only what it declares. Objects the intent does not mention are reported, never removed, unless the operator asks for pruning.
- A plan is accepted only if simulating its operations on the observed configuration yields the desired state, and only if the rendered commands parse back to the same objects.
- Deploy is dry-run unless `--apply` is given, refuses a switch whose configuration fingerprint differs from the plan's, takes an NX-OS checkpoint before writing, never retries or rolls back automatically, and stops every later wave after the first failed or uncertain switch.
- A vPC pair is never changed in the same wave; spines change one at a time, last.

## Data

Fixtures in `testdata/` are synthetic. Real captures stay outside the repository: point `FABRIC_CORPUS` at a snapshot directory and run `make corpus`; tests print only aggregate counts.
