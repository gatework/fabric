package fabric

import (
	"fmt"
	"net/netip"
	"slices"
)

// Validate checks every invariant of the intent and returns a
// *ValidationError listing all problems, or nil.
func (in *Intent) Validate() error {
	v := &validator{}
	if in.Schema != IntentSchema {
		v.add("schema", "unsupported schema %d, want %d", in.Schema, IntentSchema)
	}
	regions := map[string]bool{}
	mgmt := map[netip.Addr]string{}
	names := map[string]string{}
	if v.nulls(in) {
		return &ValidationError{Problems: v.problems}
	}
	for _, r := range in.Regions {
		path := "regions[" + r.Name + "]"
		if r.Name == "" {
			v.add("regions", "region without a name")
		}
		if regions[r.Name] {
			v.add(path, "duplicate region")
		}
		regions[r.Name] = true
		v.region(path, r, mgmt, names)
	}
	if len(v.problems) == 0 {
		return nil
	}
	return &ValidationError{Problems: v.problems}
}

type validator struct {
	problems []Problem
}

// nulls reports empty list entries ("- " in YAML), which decode as nil
// and would otherwise be dereferenced.
func (v *validator) nulls(in *Intent) bool {
	bad := false
	for i, r := range in.Regions {
		if r == nil {
			v.add(fmt.Sprintf("regions[%d]", i), "empty entry")
			bad = true
			continue
		}
		for _, list := range []struct {
			name  string
			nulls int
		}{
			{"switches", countNil(r.Switches)}, {"tenants", countNil(r.Tenants)},
			{"networks", countNil(r.Networks)}, {"attachments", countNil(r.Attachments)},
			{"vpc_pairs", countNil(r.VPCPairs)}, {"links", countNil(r.Links)}, {"port_profiles", countNil(r.PortProfiles)},
		} {
			if list.nulls > 0 {
				v.add("regions["+r.Name+"]."+list.name, "%d empty entries", list.nulls)
				bad = true
			}
		}
	}
	return bad
}

func countNil[T any](list []*T) int {
	n := 0
	for _, x := range list {
		if x == nil {
			n++
		}
	}
	return n
}

func (v *validator) add(path, format string, args ...any) {
	v.problems = append(v.problems, Problem{Path: path, Detail: fmt.Sprintf(format, args...)})
}

func (v *validator) region(path string, r *Region, mgmt map[netip.Addr]string, names map[string]string) {
	if r.Fabric.ASN == 0 {
		v.add(path+".fabric", "asn is required")
	}
	if r.Fabric.SVIMTU != 0 && (r.Fabric.SVIMTU < 576 || r.Fabric.SVIMTU > 9216) {
		v.add(path+".fabric", "svi_mtu %d out of range 576-9216", r.Fabric.SVIMTU)
	}
	v.switches(path, r, mgmt, names)
	v.underlay(path, r)
	v.pairs(path, r)
	v.links(path, r)
	l3 := v.tenants(path, r)
	v.networks(path, r, l3)
	v.profiles(path, r)
	v.attachments(path, r)
}

func (v *validator) switches(path string, r *Region, mgmt map[netip.Addr]string, names map[string]string) {
	for _, s := range r.Switches {
		sp := path + ".switches[" + s.Name + "]"
		switch {
		case s.Name == "":
			v.add(path+".switches", "switch without a name")
		case names[s.Name] != "":
			v.add(sp, "name already used in region %s", names[s.Name])
		}
		names[s.Name] = r.Name
		if !s.Mgmt.IsValid() {
			v.add(sp, "mgmt address is required")
		} else if other, dup := mgmt[s.Mgmt.Addr]; dup {
			v.add(sp, "mgmt address %s already used by %s", s.Mgmt, other)
		} else {
			mgmt[s.Mgmt.Addr] = s.Name
		}
		if !s.Role.Valid() {
			v.add(sp, "role is required")
		}
		if r.Underlay == nil {
			continue
		}
		if !s.RouterID.IsValid() {
			v.add(sp, "router_id is required when the underlay is managed")
		}
		if s.Role.VTEP() && !s.VTEP.IsValid() {
			v.add(sp, "vtep address is required when the underlay is managed")
		}
	}
	seen := map[netip.Addr]bool{}
	for _, a := range r.Addresses() {
		if seen[a] {
			v.add(path, "underlay address %s is used twice", a)
		}
		seen[a] = true
	}
}

func (v *validator) underlay(path string, r *Region) {
	u := r.Underlay
	if u == nil {
		return
	}
	up := path + ".underlay"
	if u.OSPFProcess == "" || u.OSPFArea == "" {
		v.add(up, "ospf_process and ospf_area are required")
	}
	if u.LinkPrefix != 30 && u.LinkPrefix != 31 {
		v.add(up, "link_prefix must be 30 or 31")
	}
	if u.BGP.VTEPTemplate == "" || u.BGP.SpineTemplate == "" {
		v.add(up, "bgp vtep_template and spine_template are required")
	}
	if u.BGP.Neighbors == NeighborsDynamic && !u.Pools.RouterID.IsValid() {
		v.add(up, "dynamic neighbors need pools.router_id")
	}
	if u.MTU != 0 && (u.MTU < 1500 || u.MTU > 9216) {
		v.add(up, "mtu %d out of range 1500-9216", u.MTU)
	}
}

func (v *validator) pairs(path string, r *Region) {
	member := map[string]bool{}
	// Domain numbers may repeat between pairs (existing sites do);
	// allocation picks unused ones.
	for _, p := range r.VPCPairs {
		pp := fmt.Sprintf("%s.vpc_pairs[%d]", path, p.Domain)
		if p.Domain < 1 || p.Domain > 1000 {
			v.add(pp, "domain must be 1-1000")
		}
		a, okA := r.Switch(p.Members[0].Switch)
		b, okB := r.Switch(p.Members[1].Switch)
		switch {
		case !okA || !okB:
			v.add(pp, "members must be switches of this region")
			continue
		case a == b:
			v.add(pp, "members must differ")
		case a.Role != b.Role || !a.Role.VTEP():
			v.add(pp, "members must be VTEPs with the same role")
		}
		for _, m := range p.Members {
			if member[m.Switch] {
				v.add(pp, "%s is in two vPC pairs", m.Switch)
			}
			member[m.Switch] = true
			if p.PeerLink == PeerLinkPhysical && len(m.PeerLinkPorts) == 0 {
				v.add(pp, "physical peer-link needs ports on %s", m.Switch)
			}
		}
		if p.PeerLink == PeerLinkPhysical && p.PortChannel < 1 {
			v.add(pp, "physical peer-link needs a port_channel")
		}
		if r.Underlay != nil && !p.AnycastVTEP.IsValid() {
			v.add(pp, "anycast_vtep is required when the underlay is managed")
		}
	}
}

func (v *validator) links(path string, r *Region) {
	for i, l := range r.Links {
		lp := fmt.Sprintf("%s.links[%d]", path, i)
		a, okA := r.Switch(l.A.Switch)
		b, okB := r.Switch(l.B.Switch)
		if !okA || !okB || a == b {
			v.add(lp, "ends must be two switches of this region")
			continue
		}
		if l.A.Interface == "" || l.B.Interface == "" {
			v.add(lp, "both ends need an interface")
		}
		pa, pb := l.A.Address, l.B.Address
		switch {
		case !pa.IsValid() || !pb.IsValid():
			v.add(lp, "both ends need an address")
		case pa.Bits() != pb.Bits() || pa.Masked() != pb.Masked():
			v.add(lp, "ends %s and %s are not in one subnet", pa, pb)
		case pa.Addr() == pb.Addr():
			v.add(lp, "ends share address %s", pa.Addr())
		case r.Underlay != nil && pa.Bits() != r.Underlay.LinkPrefix:
			v.add(lp, "link prefix /%d differs from underlay /%d", pa.Bits(), r.Underlay.LinkPrefix)
		}
	}
}

func (v *validator) tenants(path string, r *Region) map[VNI]bool {
	l3 := map[VNI]bool{}
	vlans := map[VLAN]bool{}
	seen := map[string]bool{}
	for _, t := range r.Tenants {
		tp := path + ".tenants[" + t.Name + "]"
		if t.Name == "" || seen[t.Name] {
			v.add(tp, "tenant name is empty or duplicated")
		}
		seen[t.Name] = true
		if !t.L3VNI.Valid() {
			v.add(tp, "l3vni is required")
		} else if l3[t.L3VNI] {
			v.add(tp, "l3vni %s used twice", t.L3VNI)
		}
		l3[t.L3VNI] = true
		if !t.VLAN.Valid() {
			v.add(tp, "core vlan is required")
		} else if vlans[t.VLAN] {
			v.add(tp, "core vlan %s used twice", t.VLAN)
		}
		vlans[t.VLAN] = true
	}
	return l3
}

func (v *validator) networks(path string, r *Region, l3 map[VNI]bool) {
	vlans := map[VLAN]bool{}
	for _, t := range r.Tenants {
		vlans[t.VLAN] = true
	}
	vnis := map[VNI]VLAN{}
	var gateways []netip.Prefix
	for _, n := range r.Networks {
		np := path + ".networks[" + n.VLAN.String() + "]"
		if !n.VLAN.Valid() {
			v.add(np, "vlan is required")
			continue
		}
		if vlans[n.VLAN] {
			v.add(np, "vlan already used by another network or tenant")
		}
		vlans[n.VLAN] = true
		vni, ok := r.VNIOf(n)
		switch {
		case !ok:
			v.add(np, "vni is required (or set numbering.l2vni_base)")
		case l3[vni]:
			v.add(np, "vni %s is a tenant l3vni", vni)
		case vnis[vni] != 0:
			v.add(np, "vni %s already mapped to vlan %s", vni, vnis[vni])
		default:
			vnis[vni] = n.VLAN
		}
		gateways = v.routing(np, r, n, gateways)
	}
}

// routing checks a network's tenant, gateway and placement, and returns
// the gateways seen so far.
func (v *validator) routing(np string, r *Region, n *Network, gateways []netip.Prefix) []netip.Prefix {
	if n.Tenant != "" {
		if _, ok := r.Tenant(n.Tenant); !ok {
			v.add(np, "unknown tenant %s", n.Tenant)
		}
	}
	for _, name := range n.Switches {
		if s, ok := r.Switch(name); !ok || !s.Role.VTEP() {
			v.add(np, "switch %s is not a VTEP of this region", name)
		}
	}
	if !n.Gateway.IsValid() {
		return gateways
	}
	if n.Tenant == "" {
		v.add(np, "a gateway needs a tenant")
	}
	for _, g := range gateways {
		if g.Overlaps(n.Gateway.Prefix) {
			v.add(np, "gateway %s overlaps %s", n.Gateway, g)
		}
	}
	for _, l := range r.Links {
		if n.Gateway.Overlaps(l.A.Address.Masked()) {
			v.add(np, "gateway %s overlaps fabric link %s", n.Gateway, l.A.Address.Masked())
		}
	}
	return append(gateways, n.Gateway.Prefix)
}

func (v *validator) profiles(path string, r *Region) {
	seen := map[string]bool{}
	for _, p := range r.PortProfiles {
		pp := path + ".port_profiles[" + p.Name + "]"
		if p.Name == "" || seen[p.Name] {
			v.add(pp, "profile name is empty or duplicated")
		}
		seen[p.Name] = true
		if p.Mode == PortAccess && len(p.Networks) > 1 {
			v.add(pp, "an access profile carries at most one network")
		}
		for _, vlan := range p.Networks {
			if _, ok := r.Network(vlan); !ok {
				v.add(pp, "vlan %s is not a network of this region", vlan)
			}
		}
	}
}

// occupancy records which object holds each switch port.
type occupancy map[string]string

func (o occupancy) claim(v *validator, path, sw, port, by string) {
	key := sw + " " + port
	if prev, ok := o[key]; ok {
		v.add(path, "%s is already used by %s", key, prev)
		return
	}
	o[key] = by
}

func (v *validator) attachments(path string, r *Region) {
	used := occupancy{}
	for _, l := range r.Links {
		used.claim(v, path+".links", l.A.Switch, l.A.Interface, "a fabric link")
		used.claim(v, path+".links", l.B.Switch, l.B.Interface, "a fabric link")
	}
	for _, p := range r.VPCPairs {
		for _, m := range p.Members {
			for _, port := range m.PeerLinkPorts {
				used.claim(v, path+".vpc_pairs", m.Switch, port, "a vPC peer-link")
			}
			if p.PeerLink == PeerLinkPhysical {
				used.claim(v, path+".vpc_pairs", m.Switch, PortChannelName(p.PortChannel), "a vPC peer-link")
			}
		}
	}
	for _, a := range r.Attachments {
		ap := path + ".attachments[" + a.Key() + "]"
		if !v.attachmentShape(ap, r, a) {
			continue
		}
		for _, sw := range a.Switches() {
			for _, port := range a.Ports(sw) {
				used.claim(v, ap, sw, port, a.Key())
			}
		}
		v.attachmentNetworks(ap, r, a)
	}
}

// attachmentShape checks the kind-specific fields and reports whether the
// attachment is coherent enough to check further.
func (v *validator) attachmentShape(ap string, r *Region, a *Attachment) bool {
	if a.Profile != "" {
		if _, ok := r.Profile(a.Profile); !ok {
			v.add(ap, "unknown profile %s", a.Profile)
		}
	}
	switch a.Kind {
	case AttachPort:
		if a.Interface == "" || len(a.Members) > 0 {
			v.add(ap, "a port attachment names one interface and no members")
			return false
		}
	case AttachPortChannel:
		if a.PortChannel < 1 || len(a.Members) != 1 || len(a.Members[a.Switch]) == 0 {
			v.add(ap, "a port-channel needs a number and members on its switch")
			return false
		}
	case AttachVPC:
		pair, ok := r.Pair(a.Switch)
		if !ok {
			v.add(ap, "%s is not in a vPC pair", a.Switch)
			return false
		}
		if a.PortChannel < 1 || len(a.Members) != 2 {
			v.add(ap, "a vPC needs a number and members on both peers")
			return false
		}
		for _, name := range pair.Names() {
			if len(a.Members[name]) == 0 {
				v.add(ap, "a vPC needs members on %s", name)
				return false
			}
		}
		if len(a.Members[pair.Members[0].Switch]) != len(a.Members[pair.Members[1].Switch]) {
			v.add(ap, "a vPC needs the same number of members on both peers")
		}
	}
	for _, name := range a.Switches() {
		if s, ok := r.Switch(name); !ok || !s.Role.VTEP() {
			v.add(ap, "switch %s is not a VTEP of this region", name)
			return false
		}
	}
	return true
}

func (v *validator) attachmentNetworks(ap string, r *Region, a *Attachment) {
	spec := r.Spec(a)
	if spec.Mode == PortAccess && len(spec.Networks) != 1 {
		v.add(ap, "an access port carries exactly one network")
	}
	for _, vlan := range spec.Networks {
		n, ok := r.Network(vlan)
		if !ok {
			v.add(ap, "vlan %s is not a network of this region", vlan)
			continue
		}
		for _, name := range a.Switches() {
			if s, _ := r.Switch(name); !r.Carries(n, s) {
				v.add(ap, "network %s is not deployed on %s", vlan, name)
			}
		}
	}
	if slices.ContainsFunc(spec.Networks, func(x VLAN) bool { return !x.Valid() }) {
		v.add(ap, "invalid vlan")
	}
}
