# 0008 Host ports, bundles and vPC attachments

Status: accepted (2026-11-19)

## Decision

- An attachment is a port, an LACP port-channel on one switch, or an LACP bundle across both members of a vPC pair (the vPC number equals the port-channel number). Port profiles (mode, networks, MTU, edge) make assignments uniform; explicit fields override the profile.
- Compilation configures the port-channel and every member identically (switchport mode and VLANs, MTU, spanning-tree type, description) and adds `channel-group N mode active` to members, so vPC type-1 parameters match on both peers by construction; the two peers render identical bundle configuration.
- A port is free only if no fabric link, peer-link or attachment uses it and, when observed, it has no address, bundle membership or vPC role. Port-channel numbers are the lowest free on every switch involved.
- Assigning a port extends any network limited to other switches to the port's switches. Releasing any port of a bundle releases the bundle: members are reset to factory and shut, the port-channel removed. Restoring a reset port (rollback) restores what the model describes; the NX-OS checkpoint remains the complete rollback.
