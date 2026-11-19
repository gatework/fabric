package provision

import (
	"fmt"
	"net/netip"
	"slices"

	"github.com/gatework/set"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/numbering"
)

// Uplink is one fabric link of a new switch: its port and the port on the
// switch at the other end.
type Uplink struct {
	Local      string
	Remote     string
	RemotePort string
}

// AddSwitch brings a new leaf, border or spine into a region whose
// underlay is managed: it allocates the router-id, the VTEP address and
// one subnet per uplink, and optionally pairs the switch with an existing
// unpaired leaf as a vPC pair.
type AddSwitch struct {
	Region  string
	Name    string
	Mgmt    netip.Addr
	Role    fabric.Role
	Model   string
	Uplinks []Uplink
	// VPCPeer pairs the new switch with this existing switch.
	VPCPeer  string
	PeerLink fabric.PeerLinkKind
	// PeerLinkPorts are physical peer-link ports, per member.
	PeerLinkPorts map[string][]string
	// RouterID and VTEP are allocated when invalid.
	RouterID netip.Addr
	VTEP     netip.Addr
}

// Apply validates, allocates and adds the switch.
func (q AddSwitch) Apply(in *fabric.Intent, obs *Observation) (Result, error) {
	r, err := region(in, q.Region)
	if err != nil {
		return Result{}, err
	}
	res := Result{Region: r.Name}
	if r.Underlay == nil {
		return res, fmt.Errorf("%w: %s", ErrUnderlay, r.Name)
	}
	if _, _, taken := in.Locate(q.Name); taken || q.Name == "" {
		return res, fmt.Errorf("%w: switch name %q is empty or taken", ErrConflict, q.Name)
	}
	if !q.Role.Valid() || !q.Mgmt.IsValid() {
		return res, fmt.Errorf("%w: role and management address are required", ErrConflict)
	}
	used := underlayAddresses(r, obs)
	sw := &fabric.Switch{Name: q.Name, Mgmt: fabric.AddrOf(q.Mgmt), Role: q.Role, Model: q.Model}
	if sw.RouterID, err = host(r.Underlay.Pools.RouterID, q.RouterID, &used, "router-id", &res); err != nil {
		return res, err
	}
	if q.Role.VTEP() {
		if sw.VTEP, err = host(r.Underlay.Pools.VTEP, q.VTEP, &used, "vtep", &res); err != nil {
			return res, err
		}
	}
	r.Switches = append(r.Switches, sw)
	res.change("switches["+q.Name+"]", "新增 %s %s，管理地址 %s，router-id %s%s", q.Role, q.Name, q.Mgmt, sw.RouterID, vtepText(sw))
	res.affect(q.Name)
	if err := q.uplinks(r, obs, used, &res); err != nil {
		return res, err
	}
	if r.Underlay.BGP.Neighbors == fabric.NeighborsExplicit && q.Role.VTEP() {
		for _, sp := range r.ByRole(fabric.RoleSpine) {
			res.affect(sp.Name)
		}
	}
	if q.Role == fabric.RoleSpine {
		for _, v := range r.VTEPs() {
			res.affect(v.Name)
		}
	}
	if q.VPCPeer != "" {
		return res, PairVPC{Region: r.Name, Switches: [2]string{q.VPCPeer, q.Name}, PeerLink: q.PeerLink, PeerLinkPorts: q.PeerLinkPorts}.pair(r, obs, used, &res)
	}
	return res, nil
}

func vtepText(sw *fabric.Switch) string {
	if !sw.VTEP.IsValid() {
		return ""
	}
	return "，VTEP " + sw.VTEP.String()
}

// underlayAddresses is every address the region uses: declared underlay
// addresses and every address configured on its switches.
func underlayAddresses(r *fabric.Region, obs *Observation) []netip.Addr {
	out := r.Addresses()
	for _, s := range r.Switches {
		out = append(out, s.Mgmt.Addr)
	}
	for _, p := range obs.addresses(r) {
		out = append(out, p.Addr())
	}
	return out
}

// host returns want, or allocates from pool, and marks the address used
// so later allocations of the same request avoid it.
func host(pool fabric.Prefix, want netip.Addr, used *[]netip.Addr, what string, res *Result) (fabric.Addr, error) {
	if want.IsValid() {
		if slices.Contains(*used, want) {
			return fabric.Addr{}, fmt.Errorf("%w: %s %s is in use", ErrConflict, what, want)
		}
		*used = append(*used, want)
		return fabric.AddrOf(want), nil
	}
	p, err := numbering.NewAddresses(pool.Prefix, *used)
	if err != nil {
		return fabric.Addr{}, fmt.Errorf("provision: %s pool: %w", what, err)
	}
	a, err := p.Host()
	if err != nil {
		return fabric.Addr{}, err
	}
	*used = append(*used, a)
	res.allocate(what, a)
	return fabric.AddrOf(a), nil
}

func (q AddSwitch) uplinks(r *fabric.Region, obs *Observation, used []netip.Addr, res *Result) error {
	links, err := numbering.NewAddresses(r.Underlay.Pools.Links.Prefix, used)
	if err != nil {
		return fmt.Errorf("provision: link pool: %w", err)
	}
	for _, u := range q.Uplinks {
		remote, ok := r.Switch(u.Remote)
		if !ok {
			return fmt.Errorf("%w: uplink peer %s", ErrNotFound, u.Remote)
		}
		if remote.Role.VTEP() == q.Role.VTEP() {
			return fmt.Errorf("%w: %s links to %s of the same tier", ErrConflict, q.Name, u.Remote)
		}
		local, _ := device.CanonicalInterface(u.Local)
		port, _ := device.CanonicalInterface(u.RemotePort)
		if err := portFree(r, obs, u.Remote, port); err != nil {
			return err
		}
		sub, err := links.Subnet(r.Underlay.LinkPrefix)
		if err != nil {
			return err
		}
		a, b := numbering.LinkEnds(sub)
		r.Links = append(r.Links, &fabric.Link{
			A: fabric.LinkEnd{Switch: q.Name, Interface: local, Address: fabric.PrefixOf(a)},
			B: fabric.LinkEnd{Switch: u.Remote, Interface: port, Address: fabric.PrefixOf(b)},
		})
		res.allocate("link "+local+"↔"+u.Remote+":"+port, sub)
		res.change("links["+q.Name+" "+local+"]", "新增互联 %s %s (%s) ↔ %s %s (%s)", q.Name, local, a, u.Remote, port, b)
		res.affect(u.Remote)
	}
	return nil
}

// PairVPC makes two existing VTEPs of one role a vPC pair.
type PairVPC struct {
	Region        string
	Switches      [2]string
	PeerLink      fabric.PeerLinkKind
	PeerLinkPorts map[string][]string
	// Domain is allocated when zero.
	Domain int
}

// Apply validates, allocates and pairs.
func (q PairVPC) Apply(in *fabric.Intent, obs *Observation) (Result, error) {
	r, err := region(in, q.Region)
	if err != nil {
		return Result{}, err
	}
	res := Result{Region: r.Name}
	return res, q.pair(r, obs, underlayAddresses(r, obs), &res)
}

func (q PairVPC) pair(r *fabric.Region, obs *Observation, used []netip.Addr, res *Result) error {
	for _, name := range q.Switches {
		s, ok := r.Switch(name)
		if !ok {
			return fmt.Errorf("%w: switch %s", ErrNotFound, name)
		}
		if !s.Role.VTEP() {
			return fmt.Errorf("%w: %s is not a VTEP", ErrConflict, name)
		}
		if _, paired := r.Pair(name); paired {
			return fmt.Errorf("%w: %s is already in a vPC pair", ErrConflict, name)
		}
	}
	p := &fabric.VPCPair{Domain: q.Domain, PeerLink: q.PeerLink}
	if p.Domain == 0 {
		d, err := numbering.Lowest(domainPool(r), usedDomains(r, obs))
		if err != nil {
			return err
		}
		p.Domain = int(d)
		res.allocate("vpc domain", d)
	}
	if r.Underlay != nil {
		pool := r.Underlay.Pools.AnycastVTEP
		if !pool.IsValid() {
			pool = r.Underlay.Pools.VTEP
		}
		a, err := host(pool, netip.Addr{}, &used, "anycast vtep", res)
		if err != nil {
			return err
		}
		p.AnycastVTEP = a
	}
	if q.PeerLink == fabric.PeerLinkPhysical {
		po, err := lowestPortChannel(r, obs, q.Switches[:])
		if err != nil {
			return err
		}
		p.PortChannel = po
		res.allocate("peer-link port-channel", po)
	}
	for i, name := range q.Switches {
		var ports []string
		for _, port := range q.PeerLinkPorts[name] {
			c, _ := device.CanonicalInterface(port)
			if err := portFree(r, obs, name, c); err != nil {
				return err
			}
			ports = append(ports, c)
		}
		p.Members[i] = fabric.VPCMember{Switch: name, PeerLinkPorts: ports}
	}
	r.VPCPairs = append(r.VPCPairs, p)
	res.change(fmt.Sprintf("vpc_pairs[%d]", p.Domain), "%s 与 %s 组成 vPC（域 %d，%s peer-link，anycast VTEP %s）",
		q.Switches[0], q.Switches[1], p.Domain, p.PeerLink, p.AnycastVTEP)
	res.affect(q.Switches[:]...)
	return nil
}

func domainPool(r *fabric.Region) *set.Set[uint16] {
	if r.Underlay != nil && r.Underlay.Pools.VPCDomains != nil {
		return r.Underlay.Pools.VPCDomains
	}
	return set.New(set.Range[uint16]{Min: 1, Max: 1000})
}

func usedDomains(r *fabric.Region, obs *Observation) *set.Set[uint16] {
	used := set.New[uint16]()
	for _, p := range r.VPCPairs {
		used.Add(uint16(p.Domain), uint16(p.Domain))
	}
	for _, s := range r.Switches {
		if cfg, ok := obs.config(s.Name); ok && cfg.VPC != nil {
			used.Add(uint16(cfg.VPC.ID), uint16(cfg.VPC.ID))
		}
	}
	return used
}

// RemoveSwitch retires a switch. It refuses while host ports are assigned
// on it; its links are released on the switches at the other end.
type RemoveSwitch struct {
	Name string
}

// Apply validates and removes the switch.
func (q RemoveSwitch) Apply(in *fabric.Intent, _ *Observation) (Result, error) {
	r, sw, err := regionOf(in, q.Name)
	if err != nil {
		return Result{}, err
	}
	res := Result{Region: r.Name}
	for _, a := range r.Attachments {
		if slices.Contains(a.Switches(), q.Name) {
			return res, fmt.Errorf("%w: %s still has %s", ErrInUse, q.Name, a.Key())
		}
	}
	for _, n := range r.Networks {
		n.Switches = slices.DeleteFunc(n.Switches, func(s string) bool { return s == q.Name })
	}
	for _, l := range r.LinksOf(q.Name) {
		res.reset(l.Remote.Switch, l.Remote.Interface)
		res.change("links["+q.Name+" "+l.Local.Interface+"]", "释放 %s %s 上的互联口", l.Remote.Switch, l.Remote.Interface)
	}
	r.Links = slices.DeleteFunc(r.Links, func(l *fabric.Link) bool { return l.A.Switch == q.Name || l.B.Switch == q.Name })
	if p, ok := r.Pair(q.Name); ok {
		other := p.Other(q.Name)
		r.VPCPairs = slices.DeleteFunc(r.VPCPairs, func(x *fabric.VPCPair) bool { return x == p })
		res.change(fmt.Sprintf("vpc_pairs[%d]", p.Domain), "解除 vPC：%s 成为单机", other)
		res.affect(other)
	}
	if r.Underlay != nil && r.Underlay.BGP.Neighbors == fabric.NeighborsExplicit && sw.Role.VTEP() {
		res.RemovedNeighbors = map[string][]string{}
		for _, sp := range r.ByRole(fabric.RoleSpine) {
			res.RemovedNeighbors[sp.Name] = []string{sw.RouterID.String()}
			res.affect(sp.Name)
		}
	}
	r.Switches = slices.DeleteFunc(r.Switches, func(s *fabric.Switch) bool { return s == sw })
	res.change("switches["+q.Name+"]", "下线 %s %s", sw.Role, q.Name)
	return res, nil
}
