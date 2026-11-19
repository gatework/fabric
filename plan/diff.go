package plan

import (
	"cmp"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Options tune Diff.
type Options struct {
	// Prune deletes fabric objects the desired state does not mention:
	// VNI-mapped VLANs, L3VNI VRFs, NVE members, EVPN instances, anycast
	// SVIs and their BGP VRFs. Without it they are left alone.
	Prune bool
}

// Diff returns the ordered operations that move cfg to the desired state.
//
// Settings are compared against the configuration as the structural
// operations leave it, because many settings live on objects the plan
// creates (the BGP process, the vPC domain, the NVE interface).
func Diff(d *Desired, cfg *device.Config, opt Options) []Op {
	var ops []Op
	ops = append(ops, diffFeatures(d, cfg)...)
	ops = append(ops, diffServers(d, cfg)...)
	ops = append(ops, diffUnderlay(d, cfg)...)
	ops = append(ops, diffVRFs(d, cfg, opt)...)
	ops = append(ops, diffVLANs(d, cfg, opt)...)
	ops = append(ops, diffEVPN(d, cfg, opt)...)
	ops = append(ops, diffMembers(d, cfg, opt)...)
	ops = append(ops, diffSVIs(d, cfg, opt)...)
	ops = append(ops, diffBGPVRFs(d, cfg, opt)...)
	ops = append(ops, diffResets(d, cfg)...)
	ops = append(ops, diffPorts(d, cfg)...)
	ops = Order(ops)
	after := cfg
	if sim, err := Simulate(cfg, ops); err == nil {
		after = sim
	}
	return Order(append(ops, diffSettings(d, after)...))
}

// Order sorts operations so that deletions run first, most dependent
// object first, then creations and updates, least dependent first.
// Ties keep key order, which makes plans deterministic.
func Order(ops []Op) []Op {
	out := slices.Clone(ops)
	slices.SortStableFunc(out, func(a, b Op) int {
		da, db := a.Action() == ActionDelete, b.Action() == ActionDelete
		switch {
		case da && !db:
			return -1
		case !da && db:
			return 1
		case da:
			if c := cmp.Compare(b.Ref().Kind.rank(), a.Ref().Kind.rank()); c != 0 {
				return c
			}
		default:
			if c := cmp.Compare(a.Ref().Kind.rank(), b.Ref().Kind.rank()); c != 0 {
				return c
			}
		}
		return cmp.Compare(a.Ref().Key, b.Ref().Key)
	})
	return out
}

func sortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	return slices.Sorted(maps.Keys(m))
}

func diffFeatures(d *Desired, cfg *device.Config) []Op {
	var ops []Op
	for _, name := range sortedKeys(d.Features) {
		if want := d.Features[name]; cfg.Features[name] != want {
			ops = append(ops, FeatureOp{Name: name, Enable: want})
		}
	}
	return ops
}

func diffSettings(d *Desired, cfg *device.Config) []Op {
	var ops []Op
	for _, s := range sortedKeys(d.Settings) {
		want := d.Settings[s]
		if !cfg.Applicable(s) {
			continue
		}
		if have := cfg.Get(s); have != want {
			ops = append(ops, SettingOp{Setting: s, Scope: settingScope(cfg, s), Have: have, Want: want})
		}
	}
	return ops
}

// settingScope names the object a setting renders under.
func settingScope(cfg *device.Config, s device.Setting) string {
	_, vpc := s.VPCOption()
	switch {
	case vpc && cfg.VPC != nil:
		return strconv.Itoa(cfg.VPC.ID)
	case strings.HasPrefix(string(s), "nve.") && cfg.NVE != nil:
		return cfg.NVE.Name
	case strings.HasPrefix(string(s), "bgp.") && cfg.BGP != nil:
		return cfg.BGP.ASN.String()
	default:
		return ""
	}
}

func diffServers(d *Desired, cfg *device.Config) []Op {
	var ops []Op
	for _, addr := range sortedKeys(d.NTPServers) {
		want := d.NTPServers[addr]
		have, ok := cfg.Management.NTP.Server(addr)
		switch {
		case !ok:
			ops = append(ops, NTPServerOp{Address: addr, Want: &want})
		case have != want:
			ops = append(ops, NTPServerOp{Address: addr, Have: &have, Want: &want})
		}
	}
	for _, addr := range sortedKeys(d.LogServers) {
		want := d.LogServers[addr]
		have, ok := cfg.Management.Logging.Server(addr)
		switch {
		case !ok:
			ops = append(ops, LogServerOp{Address: addr, Want: &want})
		case have != want:
			ops = append(ops, LogServerOp{Address: addr, Have: &have, Want: &want})
		}
	}
	if !d.ExclusiveServers {
		return ops
	}
	for _, s := range cfg.Management.NTP.Servers {
		if _, ok := d.NTPServers[s.Address]; !ok {
			ops = append(ops, NTPServerOp{Address: s.Address, Have: &s})
		}
	}
	for _, s := range cfg.Management.Logging.Servers {
		if _, ok := d.LogServers[s.Address]; !ok {
			ops = append(ops, LogServerOp{Address: s.Address, Have: &s})
		}
	}
	return ops
}

func diffVRFs(d *Desired, cfg *device.Config, opt Options) []Op {
	var ops []Op
	for _, name := range sortedKeys(d.VRFs) {
		have, ok := cfg.VRFs[name]
		want := mergeVRF(have, d.VRFs[name])
		switch {
		case !ok:
			ops = append(ops, VRFOp{Name: name, Want: &want})
		case !reflect.DeepEqual(have, want):
			ops = append(ops, VRFOp{Name: name, Have: &have, Want: &want})
		}
	}
	if opt.Prune {
		for _, name := range sortedKeys(cfg.VRFs) {
			// VRFs with routes or leaks are border policy, not overlay
			// state; they are never pruned.
			if have := cfg.VRFs[name]; have.VNI != 0 && len(have.Routes) == 0 && len(have.Exports) == 0 {
				if _, ok := d.VRFs[name]; !ok {
					ops = append(ops, VRFOp{Name: name, Have: &have})
				}
			}
		}
	}
	return ops
}

func diffVLANs(d *Desired, cfg *device.Config, opt Options) []Op {
	var ops []Op
	for _, v := range sortedKeys(d.VLANs) {
		have, ok := cfg.VLANs[v]
		want := mergeVLAN(have, d.VLANs[v])
		switch {
		case !ok:
			ops = append(ops, VLANOp{VLAN: v, Want: &want})
		case have != want:
			ops = append(ops, VLANOp{VLAN: v, Have: &have, Want: &want})
		}
	}
	if opt.Prune {
		for _, v := range sortedKeys(cfg.VLANs) {
			if have := cfg.VLANs[v]; have.VNI != 0 {
				if _, ok := d.VLANs[v]; !ok {
					ops = append(ops, VLANOp{VLAN: v, Have: &have})
				}
			}
		}
	}
	return ops
}

func diffEVPN(d *Desired, cfg *device.Config, opt Options) []Op {
	var ops []Op
	for _, v := range sortedKeys(d.EVPN) {
		have, ok := cfg.EVPN[v]
		want := mergeEVPN(have, d.EVPN[v])
		switch {
		case !ok:
			ops = append(ops, EVPNOp{VNI: v, Want: &want})
		case !have.Equal(want):
			ops = append(ops, EVPNOp{VNI: v, Have: &have, Want: &want})
		}
	}
	if opt.Prune {
		for _, v := range sortedKeys(cfg.EVPN) {
			if _, ok := d.EVPN[v]; !ok {
				have := cfg.EVPN[v]
				ops = append(ops, EVPNOp{VNI: v, Have: &have})
			}
		}
	}
	return ops
}

func diffMembers(d *Desired, cfg *device.Config, opt Options) []Op {
	if cfg.NVE == nil {
		return membersWithoutNVE(d)
	}
	var ops []Op
	for _, v := range sortedKeys(d.Members) {
		have, ok := cfg.NVE.Members[v]
		want := mergeMember(have, d.Members[v])
		switch {
		case !ok:
			ops = append(ops, NVEMemberOp{VNI: v, NVE: cfg.NVE.Name, Want: &want})
		case have != want:
			ops = append(ops, NVEMemberOp{VNI: v, NVE: cfg.NVE.Name, Have: &have, Want: &want})
		}
	}
	if opt.Prune {
		for _, v := range sortedKeys(cfg.NVE.Members) {
			if _, ok := d.Members[v]; !ok {
				have := cfg.NVE.Members[v]
				ops = append(ops, NVEMemberOp{VNI: v, NVE: cfg.NVE.Name, Have: &have})
			}
		}
	}
	return ops
}

// membersWithoutNVE still emits the operations so that the plan fails its
// prerequisite check visibly instead of silently dropping members.
func membersWithoutNVE(d *Desired) []Op {
	var ops []Op
	for _, v := range sortedKeys(d.Members) {
		want := d.Members[v]
		ops = append(ops, NVEMemberOp{VNI: v, NVE: "nve1", Want: &want})
	}
	return ops
}

func diffSVIs(d *Desired, cfg *device.Config, opt Options) []Op {
	var ops []Op
	for _, v := range sortedKeys(d.SVIs) {
		i, ok := cfg.Interfaces[v.SVI()]
		have := sviOf(i)
		want := mergeSVI(have, d.SVIs[v])
		switch {
		case !ok:
			want = mergeSVI(SVI{}, d.SVIs[v])
			ops = append(ops, SVIOp{VLAN: v, Want: &want})
		case have != want:
			ops = append(ops, SVIOp{VLAN: v, Have: &have, Want: &want})
		}
	}
	if opt.Prune {
		for _, name := range sortedKeys(cfg.Interfaces) {
			i := cfg.Interfaces[name]
			v, ok := sviVLAN(i)
			if !ok || !i.Anycast {
				continue
			}
			if _, want := d.SVIs[v]; !want {
				have := sviOf(i)
				ops = append(ops, SVIOp{VLAN: v, Have: &have})
			}
		}
	}
	return ops
}

func sviVLAN(i device.Interface) (fabric.VLAN, bool) {
	if i.Kind != device.KindSVI {
		return 0, false
	}
	v, err := fabric.ParseVLAN(i.Name[len("Vlan"):])
	return v, err == nil
}

func diffBGPVRFs(d *Desired, cfg *device.Config, opt Options) []Op {
	if cfg.BGP == nil && len(d.BGPVRFs) == 0 {
		return nil
	}
	var ops []Op
	asn := d.BGPASN
	if cfg.BGP != nil {
		asn = cfg.BGP.ASN
	}
	vrfs := map[string]device.BGPVRF{}
	if cfg.BGP != nil {
		vrfs = cfg.BGP.VRFs
	}
	for _, name := range sortedKeys(d.BGPVRFs) {
		have, ok := vrfs[name]
		want := have
		want.AdvertiseL2VPN = true
		switch {
		case !ok:
			ops = append(ops, BGPVRFOp{ASN: asn, VRF: name, Want: &want})
		case !have.AdvertiseL2VPN:
			ops = append(ops, BGPVRFOp{ASN: asn, VRF: name, Have: &have, Want: &want})
		}
	}
	if opt.Prune {
		for _, name := range sortedKeys(vrfs) {
			if _, ok := d.BGPVRFs[name]; ok || cfg.VRFs[name].VNI == 0 || len(vrfs[name].Neighbors) > 0 {
				continue
			}
			have := vrfs[name]
			ops = append(ops, BGPVRFOp{ASN: asn, VRF: name, Have: &have})
		}
	}
	return ops
}

func diffResets(d *Desired, cfg *device.Config) []Op {
	var ops []Op
	for _, name := range slices.Sorted(slices.Values(d.Reset)) {
		i, ok := cfg.Interfaces[name]
		if !ok {
			continue
		}
		if _, managed := d.Ports[name]; managed {
			continue
		}
		ops = append(ops, ResetOp{Interface: name, Saved: i})
	}
	return ops
}

func diffPorts(d *Desired, cfg *device.Config) []Op {
	var ops []Op
	for _, name := range sortedKeys(d.Ports) {
		i, ok := cfg.Interfaces[name]
		if _, kind := device.CanonicalInterface(name); !ok && kind == device.KindPortChannel {
			want := mergePort(Port{}, d.Ports[name])
			ops = append(ops, PortOp{Interface: name, Want: &want})
			continue
		}
		if !ok {
			want := mergePort(Port{}, d.Ports[name])
			ops = append(ops, PortOp{Interface: name, Want: &want})
			continue
		}
		have := portOf(i)
		if !i.Layer2 {
			want := mergePort(Port{}, d.Ports[name])
			ops = append(ops, PortOp{Interface: name, Want: &want})
			continue
		}
		want := mergePort(have, d.Ports[name])
		if !portEqual(have, want) {
			ops = append(ops, PortOp{Interface: name, Have: &have, Want: &want})
		}
	}
	return ops
}
