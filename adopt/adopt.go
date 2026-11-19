package adopt

import (
	"cmp"
	"fmt"
	"maps"
	"net/netip"
	"slices"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Conflict is one disagreement the operator must resolve.
type Conflict struct {
	Region   string   `json:"region"`
	Subject  string   `json:"subject"`
	Detail   string   `json:"detail"`
	Chosen   string   `json:"chosen,omitempty"`
	Outliers []string `json:"outliers,omitempty"`
}

// Report is the result of adopting one region.
type Report struct {
	Region    *fabric.Region `json:"region"`
	Conflicts []Conflict     `json:"conflicts"`
	// Skipped lists switches whose configuration was not observed.
	Skipped []string `json:"skipped,omitempty"`
}

// Region drafts the intent of one region from its switches. Switch roles
// come from the identities when set and are inferred otherwise.
func Region(name string, switches []*device.Switch) *Report {
	a := &adopter{rep: &Report{Region: &fabric.Region{Name: name}, Conflicts: []Conflict{}}}
	for _, sw := range switches {
		if !sw.Configured() {
			a.rep.Skipped = append(a.rep.Skipped, sw.Key())
			continue
		}
		a.switches = append(a.switches, sw)
	}
	slices.SortFunc(a.switches, func(x, y *device.Switch) int { return cmp.Compare(x.Name, y.Name) })
	a.inventory()
	a.settings()
	a.tenants()
	a.networks()
	return a.rep
}

type adopter struct {
	rep      *Report
	switches []*device.Switch
	vteps    []*device.Switch
}

func (a *adopter) conflict(subject, chosen string, outliers []string, format string, args ...any) {
	a.rep.Conflicts = append(a.rep.Conflicts, Conflict{
		Region: a.rep.Region.Name, Subject: subject, Chosen: chosen,
		Outliers: outliers, Detail: fmt.Sprintf(format, args...),
	})
}

func (a *adopter) inventory() {
	r := a.rep.Region
	for _, sw := range a.switches {
		role := sw.Role
		if !role.Valid() {
			role = InferRole(sw.Config)
		}
		s := &fabric.Switch{Name: sw.Name, Mgmt: sw.Mgmt, Role: role, Model: sw.Model}
		r.Switches = append(r.Switches, s)
		if role.VTEP() {
			a.vteps = append(a.vteps, sw)
		}
	}
	pairVPC(r, a.switches)
}

// InferRole classifies a switch from its configuration: route-reflector
// clients or no NVE interface make a spine; a VTEP that originates a
// default route, leaks into the default VRF or runs a second OSPF process
// is a border; any other VTEP is a leaf.
func InferRole(cfg *device.Config) fabric.Role {
	if reflectsRoutes(cfg) || cfg.NVE == nil {
		return fabric.RoleSpine
	}
	if len(cfg.OSPF) > 1 {
		return fabric.RoleBorder
	}
	for _, v := range cfg.VRFs {
		if len(v.Exports) > 0 {
			return fabric.RoleBorder
		}
	}
	if cfg.BGP != nil {
		for _, v := range cfg.BGP.VRFs {
			if v.DefaultOriginate {
				return fabric.RoleBorder
			}
		}
	}
	return fabric.RoleLeaf
}

// reflectsRoutes reports a BGP route reflector; a stray NVE interface on
// a spine does not make it a VTEP.
func reflectsRoutes(cfg *device.Config) bool {
	if cfg.BGP == nil {
		return false
	}
	for _, p := range cfg.BGP.Templates {
		if p.RouteReflectorClient {
			return true
		}
	}
	for _, p := range cfg.BGP.Neighbors {
		if p.RouteReflectorClient {
			return true
		}
	}
	return false
}

// pairVPC links switches whose vPC domains share an ID and point their
// keepalives at each other.
func pairVPC(r *fabric.Region, switches []*device.Switch) {
	byName := map[string]*device.Switch{}
	for _, sw := range switches {
		byName[sw.Name] = sw
	}
	for _, s := range r.Switches {
		mine := byName[s.Name].Config.VPC
		if mine == nil || s.VPCPeer != "" {
			continue
		}
		for _, t := range r.Switches {
			theirs := byName[t.Name].Config.VPC
			if t == s || theirs == nil || t.VPCPeer != "" || theirs.ID != mine.ID {
				continue
			}
			if mine.KeepaliveDest == theirs.KeepaliveSource && theirs.KeepaliveDest == mine.KeepaliveSource && s.Role == t.Role {
				s.VPCPeer, t.VPCPeer = t.Name, s.Name
				break
			}
		}
	}
}

// vote counts values held by switches and returns the majority, the
// switches that differ and whether there was a strict winner.
type vote[V comparable] struct {
	by map[V][]string
}

func (v *vote[V]) add(value V, who string) {
	if v.by == nil {
		v.by = map[V][]string{}
	}
	v.by[value] = append(v.by[value], who)
}

func (v *vote[V]) winner() (V, []string, bool) {
	var best V
	var bestN, ties int
	for val, who := range v.by {
		switch {
		case len(who) > bestN:
			best, bestN, ties = val, len(who), 0
		case len(who) == bestN:
			ties++
		}
	}
	if bestN == 0 || ties > 0 {
		var zero V
		return zero, v.all(), false
	}
	var out []string
	for val, who := range v.by {
		if val != best {
			out = append(out, who...)
		}
	}
	slices.Sort(out)
	return best, out, true
}

func (v *vote[V]) all() []string {
	var out []string
	for _, who := range v.by {
		out = append(out, who...)
	}
	slices.Sort(out)
	return out
}

func (a *adopter) settings() {
	r := a.rep.Region
	var asn vote[fabric.ASN]
	var mac vote[fabric.MAC]
	for _, sw := range a.switches {
		if sw.Config.BGP != nil {
			asn.add(sw.Config.BGP.ASN, sw.Name)
		}
	}
	for _, sw := range a.vteps {
		if sw.Config.AnycastGatewayMAC != "" {
			mac.add(sw.Config.AnycastGatewayMAC, sw.Name)
		}
	}
	if v, out, ok := asn.winner(); ok {
		r.Fabric.ASN = v
		if len(out) > 0 {
			a.conflict("fabric.asn", v.String(), out, "switches run a different BGP AS")
		}
	} else {
		a.conflict("fabric.asn", "", out, "no BGP AS majority")
	}
	if v, out, ok := mac.winner(); ok {
		r.Fabric.AnycastGatewayMAC = v
		if len(out) > 0 {
			a.conflict("fabric.anycast_gateway_mac", string(v), out, "switches use a different anycast gateway MAC")
		}
	}
	a.overlaySettings()
}

// overlaySettings decides replication, ARP suppression and SVI MTU. A
// setting is declared only when every VTEP agrees; otherwise it is left
// unmanaged so planning changes nothing.
func (a *adopter) overlaySettings() {
	r := a.rep.Region
	allSuppress, anyMember := true, false
	var mtu vote[int]
	for _, sw := range a.vteps {
		cfg := sw.Config
		for _, seg := range cfg.Segments() {
			if seg.Layer != device.LayerL2 || seg.Member == nil {
				continue
			}
			anyMember = true
			if !seg.Member.SuppressARP {
				allSuppress = false
			}
			if seg.Member.McastGroup != "" {
				r.Fabric.Replication = fabric.ReplicationMulticast
			}
		}
		for _, i := range cfg.Interfaces {
			if i.Kind == device.KindSVI && i.Anycast && i.MTU != 0 {
				mtu.add(i.MTU, sw.Name+" "+i.Name)
			}
		}
	}
	r.Fabric.SuppressARP = anyMember && allSuppress
	if v, out, ok := mtu.winner(); ok {
		r.Fabric.SVIMTU = v
		if len(out) > 0 {
			a.conflict("fabric.svi_mtu", fmt.Sprint(v), out, "anycast SVIs with a different MTU will be changed")
		}
	}
}

func (a *adopter) tenants() {
	type key struct {
		vni  fabric.VNI
		vlan fabric.VLAN
	}
	votes := map[string]*vote[key]{}
	for _, sw := range a.vteps {
		cfg := sw.Config
		vlanOf := map[fabric.VNI]fabric.VLAN{}
		for v, def := range cfg.VLANs {
			if def.VNI != 0 {
				vlanOf[def.VNI] = v
			}
		}
		for name, vrf := range cfg.VRFs {
			if vrf.VNI == 0 {
				continue
			}
			if votes[name] == nil {
				votes[name] = &vote[key]{}
			}
			votes[name].add(key{vrf.VNI, vlanOf[vrf.VNI]}, sw.Name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(votes)) {
		k, out, ok := votes[name].winner()
		if !ok || k.vlan == 0 {
			a.conflict("tenants["+name+"]", "", out, "no agreed L3VNI and core VLAN")
			continue
		}
		if len(out) > 0 {
			a.conflict("tenants["+name+"]", fmt.Sprintf("l3vni %s vlan %s", k.vni, k.vlan), out, "switches map the tenant differently")
		}
		a.rep.Region.Tenants = append(a.rep.Region.Tenants, &fabric.Tenant{Name: name, L3VNI: k.vni, VLAN: k.vlan})
	}
}

// observed is what one VTEP configures for one VLAN.
type observed struct {
	// routed is set when the switch routes the VLAN with its own
	// (non-anycast) SVI address.
	routed bool
	// bare is set for an anycast SVI without an address, a gateway that
	// was deliberately emptied.
	bare    bool
	vni     fabric.VNI
	name    string
	gateway netip.Prefix
	tenant  string
	acl     string
}

func (a *adopter) networks() {
	r := a.rep.Region
	l3 := map[fabric.VNI]bool{}
	core := map[fabric.VLAN]bool{}
	for _, t := range r.Tenants {
		l3[t.L3VNI], core[t.VLAN] = true, true
	}
	per := map[fabric.VLAN]map[string]observed{}
	for _, sw := range a.vteps {
		for v, def := range sw.Config.VLANs {
			if def.VNI == 0 || l3[def.VNI] || core[v] {
				continue
			}
			o := observed{vni: def.VNI, name: def.Name}
			if svi, ok := sw.Config.Interfaces[v.SVI()]; ok {
				_, addressed := svi.Primary()
				switch {
				case svi.Anycast && addressed:
					o.gateway, _ = svi.Primary()
					o.tenant, o.acl = svi.VRF, svi.ACLIn
				case addressed:
					o.routed = true
				case svi.Anycast:
					o.bare = true
				}
			}
			if per[v] == nil {
				per[v] = map[string]observed{}
			}
			per[v][sw.Name] = o
		}
	}
	for _, v := range slices.Sorted(maps.Keys(per)) {
		if n := a.network(v, per[v]); n != nil {
			r.Networks = append(r.Networks, n)
		}
	}
	a.dedupeVNIs()
}

func (a *adopter) network(v fabric.VLAN, seen map[string]observed) *fabric.Network {
	subject := "networks[" + v.String() + "]"
	var routed, bare []string
	for who, o := range seen {
		switch {
		case o.routed:
			routed = append(routed, who)
			delete(seen, who)
		case o.bare:
			bare = append(bare, who)
			delete(seen, who)
		}
	}
	if len(routed) > 0 {
		slices.Sort(routed)
		a.conflict(subject, "", routed, "switches route the VLAN with their own SVI address; left unmanaged there")
	}
	if len(bare) > 0 {
		slices.Sort(bare)
		a.conflict(subject, "", bare, "switches keep the gateway SVI without an address; left unmanaged there")
	}
	if len(seen) == 0 {
		return nil
	}
	var vni vote[fabric.VNI]
	var name, acl vote[string]
	var gw vote[netip.Prefix]
	var tenant vote[string]
	for who, o := range seen {
		vni.add(o.vni, who)
		name.add(o.name, who)
		if o.gateway.IsValid() {
			gw.add(o.gateway, who)
			tenant.add(o.tenant, who)
			acl.add(o.acl, who)
		}
	}
	id, out, ok := vni.winner()
	if !ok {
		a.conflict(subject, "", out, "no VNI majority; network left out")
		return nil
	}
	n := &fabric.Network{VLAN: v, VNI: id}
	if len(out) > 0 {
		a.conflict(subject, id.String(), out, "switches map the VLAN to another VNI")
	}
	if nm, _, ok := name.winner(); ok {
		n.Name = nm
	}
	a.gateway(n, subject, &gw, &tenant, &acl, len(seen))
	members := slices.Sorted(maps.Keys(seen))
	members = slices.DeleteFunc(members, func(s string) bool { return slices.Contains(out, s) })
	if len(members) < len(a.vteps) {
		n.Switches = members
	}
	return n
}

func (a *adopter) gateway(n *fabric.Network, subject string, gw *vote[netip.Prefix], tenant, acl *vote[string], carriers int) {
	g, out, ok := gw.winner()
	if !ok {
		if len(gw.by) > 0 {
			a.conflict(subject, "", out, "no gateway majority; left as L2-only")
		}
		return
	}
	n.Gateway = g
	if len(out) > 0 {
		a.conflict(subject+".gateway", g.String(), out, "switches use another gateway address")
	}
	if missing := carriers - len(gw.all()); missing > 0 {
		a.conflict(subject+".gateway", g.String(), nil, "%d switches carry the VLAN without the gateway; planning adds it", missing)
	}
	if t, _, ok := tenant.winner(); ok {
		n.Tenant = t
	}
	if l, out, ok := acl.winner(); ok {
		n.ACLIn = l
		if len(out) > 0 && l != "" {
			a.conflict(subject+".acl_in", l, out, "switches bind another inbound ACL")
		}
	}
}

// dedupeVNIs drops later networks that reuse a VNI, which the intent
// forbids, and reports them.
func (a *adopter) dedupeVNIs() {
	r := a.rep.Region
	seen := map[fabric.VNI]fabric.VLAN{}
	kept := r.Networks[:0]
	for _, n := range r.Networks {
		if first, dup := seen[n.VNI]; dup {
			a.conflict("networks["+n.VLAN.String()+"]", "", nil,
				"VNI %s is already mapped to VLAN %s; network left out", n.VNI, first)
			continue
		}
		seen[n.VNI] = n.VLAN
		kept = append(kept, n)
	}
	r.Networks = kept
	var gateways []*fabric.Network
	for _, n := range r.Networks {
		if !n.Gateway.IsValid() {
			continue
		}
		for _, g := range gateways {
			if g.Gateway.Overlaps(n.Gateway) {
				a.conflict("networks["+n.VLAN.String()+"].gateway", "", nil,
					"gateway overlaps VLAN %s; left as L2-only", g.VLAN)
				n.Gateway, n.Tenant, n.ACLIn = netip.Prefix{}, "", ""
				break
			}
		}
		if n.Gateway.IsValid() {
			gateways = append(gateways, n)
		}
	}
	for _, n := range r.Networks {
		if n.Tenant != "" {
			if _, ok := r.Tenant(n.Tenant); !ok {
				a.conflict("networks["+n.VLAN.String()+"]", "", nil, "gateway VRF %s is not a tenant; left as L2-only", n.Tenant)
				n.Gateway, n.Tenant, n.ACLIn = netip.Prefix{}, "", ""
			}
		}
	}
}
