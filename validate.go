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
	l3 := v.tenants(path, r)
	v.networks(path, r, l3)
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
		} else if other, dup := mgmt[s.Mgmt]; dup {
			v.add(sp, "mgmt address %s already used by %s", s.Mgmt, other)
		} else {
			mgmt[s.Mgmt] = s.Name
		}
		if !s.Role.Valid() {
			v.add(sp, "role is required")
		}
		if s.VPCPeer == "" {
			continue
		}
		peer, ok := r.Switch(s.VPCPeer)
		switch {
		case !ok:
			v.add(sp, "vpc peer %s is not a switch of this region", s.VPCPeer)
		case peer.VPCPeer != s.Name:
			v.add(sp, "vpc peer %s does not name %s back", s.VPCPeer, s.Name)
		case peer.Role != s.Role || !s.Role.VTEP():
			v.add(sp, "vpc peers must be VTEPs with the same role")
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
		if g.Overlaps(n.Gateway) {
			v.add(np, "gateway %s overlaps %s", n.Gateway, g)
		}
	}
	return append(gateways, n.Gateway)
}

func (v *validator) attachments(path string, r *Region) {
	seen := map[string]bool{}
	for _, a := range r.Attachments {
		key := a.Switch + " " + a.Interface
		ap := path + ".attachments[" + key + "]"
		if seen[key] {
			v.add(ap, "duplicate attachment")
		}
		seen[key] = true
		s, ok := r.Switch(a.Switch)
		if !ok || !s.Role.VTEP() {
			v.add(ap, "switch is not a VTEP of this region")
			continue
		}
		if a.Interface == "" {
			v.add(ap, "interface is required")
		}
		if a.Mode == PortAccess && len(a.Networks) != 1 {
			v.add(ap, "an access port carries exactly one network")
		}
		for _, vlan := range a.Networks {
			n, ok := r.Network(vlan)
			if !ok {
				v.add(ap, "vlan %s is not a network of this region", vlan)
				continue
			}
			if !r.Carries(n, s) {
				v.add(ap, "network %s is not deployed on %s", vlan, a.Switch)
			}
		}
		if slices.ContainsFunc(a.Networks, func(x VLAN) bool { return !x.Valid() }) {
			v.add(ap, "invalid vlan")
		}
	}
}
