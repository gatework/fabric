package adopt

import (
	"cmp"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// underlay infers the region's underlay: loopback addresses, fabric links,
// OSPF and BGP design and the pools that contain them. When a switch lacks
// a loopback or the design is ambiguous the underlay stays unmanaged.
func (a *adopter) underlay() {
	r := a.rep.Region
	if len(r.ByRole(fabric.RoleSpine)) == 0 {
		return
	}
	byName := map[string]*device.Switch{}
	for _, sw := range a.switches {
		byName[sw.Name] = sw
	}
	missing := a.loopbacks(byName)
	if len(missing) > 0 {
		a.conflict("underlay", "", missing, "switches without loopback0 or NVE source address; underlay left unmanaged")
		return
	}
	u := &fabric.Underlay{}
	if !a.ospfDesign(u) || !a.linkDesign(u, byName) || !a.bgpDesign(u, byName) {
		return
	}
	a.pools(u)
	r.Underlay = u
}

func primary(cfg *device.Config, name string) (netip.Addr, bool) {
	i, ok := cfg.Interfaces[name]
	if !ok {
		return netip.Addr{}, false
	}
	p, ok := i.Primary()
	return p.Addr(), ok
}

func (a *adopter) loopbacks(byName map[string]*device.Switch) []string {
	var missing []string
	for _, s := range a.rep.Region.Switches {
		cfg := byName[s.Name].Config
		lo0, ok := primary(cfg, "loopback0")
		if !ok {
			missing = append(missing, s.Name)
			continue
		}
		s.RouterID = fabric.AddrOf(lo0)
		if !s.Role.VTEP() {
			continue
		}
		vtep, ok := cfg.PIPAddress()
		if !ok {
			missing = append(missing, s.Name)
			continue
		}
		s.VTEP = fabric.AddrOf(vtep)
	}
	return missing
}

func (a *adopter) ospfDesign(u *fabric.Underlay) bool {
	var bind vote[device.InterfaceOSPF]
	for _, sw := range a.switches {
		if lo, ok := sw.Config.Interfaces["loopback0"]; ok && lo.OSPF != nil {
			bind.add(device.InterfaceOSPF{Process: lo.OSPF.Process, Area: lo.OSPF.Area}, sw.Name)
		}
	}
	b, out, ok := bind.winner()
	if !ok || len(out) > 0 {
		a.conflict("underlay.ospf", "", out, "loopback0 OSPF process or area differs; underlay left unmanaged")
		return false
	}
	u.OSPFProcess, u.OSPFArea = b.Process, b.Area
	return true
}

type linkEnd struct {
	sw    string
	iface string
	addr  netip.Prefix
	mtu   int
}

// linkDesign pairs routed point-to-point ports whose addresses share a
// /30 or /31 into fabric links.
func (a *adopter) linkDesign(u *fabric.Underlay, byName map[string]*device.Switch) bool {
	r := a.rep.Region
	bySubnet := map[netip.Prefix][]linkEnd{}
	for _, s := range r.Switches {
		cfg := byName[s.Name].Config
		for _, name := range slices.Sorted(maps.Keys(cfg.Interfaces)) {
			i := cfg.Interfaces[name]
			p, ok := i.Primary()
			// Only ports in the underlay's own OSPF process are fabric
			// links; a border's external OSPF link is not.
			if i.Kind != device.KindEthernet || i.Layer2 || !ok || p.Bits() < 30 || i.OSPF == nil ||
				i.OSPF.Process != u.OSPFProcess || i.OSPF.Area != u.OSPFArea {
				continue
			}
			bySubnet[p.Masked()] = append(bySubnet[p.Masked()], linkEnd{s.Name, name, p, i.MTU})
		}
	}
	var bits, mtu vote[int]
	for _, subnet := range slices.SortedFunc(maps.Keys(bySubnet), func(x, y netip.Prefix) int { return x.Addr().Compare(y.Addr()) }) {
		ends := bySubnet[subnet]
		if len(ends) != 2 || ends[0].sw == ends[1].sw {
			continue
		}
		slices.SortFunc(ends, func(x, y linkEnd) int { return cmp.Compare(roleOrder(r, x.sw), roleOrder(r, y.sw)) })
		r.Links = append(r.Links, &fabric.Link{
			A: fabric.LinkEnd{Switch: ends[0].sw, Interface: ends[0].iface, Address: fabric.PrefixOf(ends[0].addr)},
			B: fabric.LinkEnd{Switch: ends[1].sw, Interface: ends[1].iface, Address: fabric.PrefixOf(ends[1].addr)},
		})
		key := subnet.String()
		bits.add(subnet.Bits(), key)
		for _, e := range ends {
			mtu.add(e.mtu, e.sw+" "+e.iface)
		}
	}
	b, out, ok := bits.winner()
	if !ok || len(out) > 0 {
		a.conflict("underlay.links", "", out, "fabric links mix prefix lengths; underlay left unmanaged")
		r.Links = nil
		return false
	}
	u.LinkPrefix = b
	if m, out, ok := mtu.winner(); ok && m != 0 {
		u.MTU = m
		if len(out) > 0 {
			a.conflict("underlay.mtu", fmt.Sprint(m), out, "fabric link ports with another MTU will be changed")
		}
	}
	return true
}

// roleOrder puts VTEPs before spines so links read leaf → spine.
func roleOrder(r *fabric.Region, name string) int {
	if s, ok := r.Switch(name); ok && s.Role == fabric.RoleSpine {
		return 1
	}
	return 0
}

func (a *adopter) bgpDesign(u *fabric.Underlay, byName map[string]*device.Switch) bool {
	r := a.rep.Region
	spineIDs := map[string]bool{}
	for _, s := range r.ByRole(fabric.RoleSpine) {
		spineIDs[s.RouterID.String()] = true
	}
	// A spine's VTEP peering prefix is the one holding VTEP router-ids;
	// other prefix neighbors (external peers) are not fabric design.
	holdsVTEPs := func(p netip.Prefix) bool {
		return slices.ContainsFunc(r.VTEPs(), func(v *fabric.Switch) bool { return p.Contains(v.RouterID.Addr) })
	}
	var vtepTmpl, spineTmpl vote[string]
	var style vote[fabric.NeighborStyle]
	var dynamic vote[netip.Prefix]
	for _, s := range r.Switches {
		b := byName[s.Name].Config.BGP
		if b == nil {
			continue
		}
		for _, addr := range slices.Sorted(maps.Keys(b.Neighbors)) {
			n := b.Neighbors[addr]
			switch {
			case s.Role.VTEP() && spineIDs[addr]:
				vtepTmpl.add(n.Inherit, s.Name+" "+addr)
			case s.Role == fabric.RoleSpine && strings.Contains(addr, "/"):
				if p, err := netip.ParsePrefix(addr); err == nil && holdsVTEPs(p) {
					spineTmpl.add(n.Inherit, s.Name+" "+addr)
					dynamic.add(p, s.Name)
				}
			case s.Role == fabric.RoleSpine:
				spineTmpl.add(n.Inherit, s.Name+" "+addr)
			}
		}
		if s.Role == fabric.RoleSpine {
			if _, dyn := dynamicNeighbor(b); dyn {
				style.add(fabric.NeighborsDynamic, s.Name)
			} else {
				style.add(fabric.NeighborsExplicit, s.Name)
			}
		}
	}
	vt, out1, ok1 := vtepTmpl.winner()
	st, out2, ok2 := spineTmpl.winner()
	sy, out3, ok3 := style.winner()
	if !ok1 || !ok2 || !ok3 || vt == "" || st == "" || len(out3) > 0 {
		a.conflict("underlay.bgp", "", append(append(out1, out2...), out3...), "BGP templates or spine neighbor style differ; underlay left unmanaged")
		return false
	}
	u.BGP = fabric.Peering{Neighbors: sy, VTEPTemplate: vt, SpineTemplate: st}
	if sy == fabric.NeighborsDynamic {
		p, out, ok := dynamic.winner()
		if !ok || len(out) > 0 {
			a.conflict("underlay.bgp", "", out, "spines peer with different prefixes; underlay left unmanaged")
			return false
		}
		u.Pools.RouterID = fabric.PrefixOf(p)
	}
	return true
}

func dynamicNeighbor(b *device.BGP) (string, bool) {
	for addr := range b.Neighbors {
		if strings.Contains(addr, "/") {
			return addr, true
		}
	}
	return "", false
}

// pools records the smallest prefixes that hold today's addresses, so new
// switches are numbered next to the existing ones.
func (a *adopter) pools(u *fabric.Underlay) {
	r := a.rep.Region
	var ids, vteps, anycast, links []netip.Addr
	for _, s := range r.Switches {
		ids = append(ids, s.RouterID.Addr)
		if s.VTEP.IsValid() {
			vteps = append(vteps, s.VTEP.Addr)
		}
	}
	for _, p := range r.VPCPairs {
		if p.AnycastVTEP.IsValid() {
			anycast = append(anycast, p.AnycastVTEP.Addr)
		}
	}
	for _, l := range r.Links {
		links = append(links, l.A.Address.Addr(), l.B.Address.Addr())
	}
	if !u.Pools.RouterID.IsValid() {
		u.Pools.RouterID = fabric.PrefixOf(covering(ids))
	}
	u.Pools.VTEP = fabric.PrefixOf(covering(vteps))
	u.Pools.AnycastVTEP = fabric.PrefixOf(covering(anycast))
	u.Pools.Links = fabric.PrefixOf(covering(links))
}

// covering returns the longest prefix containing every address, or the
// zero prefix for none.
func covering(addrs []netip.Addr) netip.Prefix {
	if len(addrs) == 0 {
		return netip.Prefix{}
	}
	for bits := addrs[0].BitLen(); bits >= 0; bits-- {
		p, err := addrs[0].Prefix(bits)
		if err != nil {
			return netip.Prefix{}
		}
		if !slices.ContainsFunc(addrs, func(a netip.Addr) bool { return !p.Contains(a) }) {
			return p
		}
	}
	return netip.Prefix{}
}
