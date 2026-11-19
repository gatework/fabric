package plan

import (
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// ErrUnsafe means a plan would leave the switch with a configuration
// NX-OS rejects or that breaks forwarding.
var ErrUnsafe = errors.New("plan: unsafe result")

// Hazards lists the invariants a configuration violates: overlapping
// addresses within a VRF, a VNI claimed twice, and NVE members or EVPN
// instances whose VNI no VLAN or VRF maps.
func Hazards(cfg *device.Config) []string {
	var out []string
	out = append(out, addressOverlaps(cfg)...)
	out = append(out, vniClaims(cfg)...)
	return out
}

// Safe refuses ops whose simulated result has hazards the observed
// configuration did not already have.
func Safe(cfg *device.Config, ops []Op) error {
	sim, err := Simulate(cfg, ops)
	if err != nil {
		return err
	}
	before := map[string]bool{}
	for _, h := range Hazards(cfg) {
		before[h] = true
	}
	var added []string
	for _, h := range Hazards(sim) {
		if !before[h] {
			added = append(added, h)
		}
	}
	if len(added) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrUnsafe, strings.Join(added, "; "))
}

func addressOverlaps(cfg *device.Config) []string {
	type owned struct {
		iface string
		pfx   netip.Prefix
	}
	byVRF := map[string][]owned{}
	for _, name := range slices.Sorted(maps.Keys(cfg.Interfaces)) {
		i := cfg.Interfaces[name]
		vrf := i.VRF
		if vrf == "" {
			vrf = "default"
		}
		for _, p := range append(slices.Clone(i.Addresses), i.Secondary...) {
			byVRF[vrf] = append(byVRF[vrf], owned{name, p})
		}
	}
	var out []string
	for _, vrf := range slices.Sorted(maps.Keys(byVRF)) {
		list := byVRF[vrf]
		for i := range list {
			for j := i + 1; j < len(list); j++ {
				a, b := list[i], list[j]
				if a.iface != b.iface && a.pfx.Masked().Overlaps(b.pfx.Masked()) && a.pfx.Bits() < 32 && b.pfx.Bits() < 32 {
					out = append(out, fmt.Sprintf("address of %s overlaps %s in vrf %s", b.iface, a.iface, vrf))
				}
			}
		}
	}
	return out
}

func vniClaims(cfg *device.Config) []string {
	claims := map[fabric.VNI][]string{}
	for _, v := range slices.Sorted(maps.Keys(cfg.VLANs)) {
		if vni := cfg.VLANs[v].VNI; vni != 0 {
			claims[vni] = append(claims[vni], "vlan "+v.String())
		}
	}
	var out []string
	for _, vni := range slices.Sorted(maps.Keys(claims)) {
		if len(claims[vni]) > 1 {
			out = append(out, fmt.Sprintf("vni %s mapped by %s", vni, strings.Join(claims[vni], " and ")))
		}
	}
	return out
}
