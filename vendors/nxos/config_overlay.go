package nxos

import (
	"net/netip"
	"strings"

	"github.com/gatework/set"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

func (p *configParser) anycastMAC(n *node) {
	if mac, err := fabric.ParseMAC(n.arg(3)); err == nil {
		p.cfg.AnycastGatewayMAC = mac
	}
}

func (p *configParser) infraVLANs(n *node) {
	s, err := set.Parse[uint16](n.rest(3))
	if err != nil {
		p.unparsed(n)
		return
	}
	if p.cfg.NVEInfraVLANs == nil {
		p.cfg.NVEInfraVLANs = s
		return
	}
	p.cfg.NVEInfraVLANs.Merge(s)
}

func parseVLANList(text string) ([]fabric.VLAN, error) {
	s, err := set.Parse[uint16](text)
	if err != nil {
		return nil, err
	}
	var out []fabric.VLAN
	for r := range s.All() {
		for v := uint32(r.Min); v <= uint32(r.Max); v++ {
			if fabric.VLAN(v).Valid() {
				out = append(out, fabric.VLAN(v))
			}
		}
	}
	return out, nil
}

func (p *configParser) vlan(n *node) {
	if n.has("vlan", "configuration") || len(n.fields) != 2 {
		p.unparsed(n)
		return
	}
	id, err := fabric.ParseVLAN(n.arg(1))
	if err != nil {
		p.vlanList = append(p.vlanList, n.arg(1))
		return
	}
	v := p.cfg.VLANs[id]
	for _, c := range n.children {
		switch {
		case c.has("name"):
			v.Name = c.rest(1)
		case c.has("vn-segment"):
			if vni, err := fabric.ParseVNI(c.arg(1)); err == nil {
				v.VNI = vni
			}
		}
	}
	p.cfg.VLANs[id] = v
}

func (p *configParser) vrf(n *node) {
	name := n.arg(2)
	v := p.cfg.VRFs[name]
	for _, c := range n.children {
		switch {
		case c.has("vni"):
			if vni, err := fabric.ParseVNI(c.arg(1)); err == nil {
				v.VNI = vni
			}
		case c.has("rd"):
			v.RD = c.arg(1)
		case c.has("ip", "route"):
			if r, ok := parseStaticRoute(c.fields[2:]); ok {
				v.Routes = append(v.Routes, r)
			}
		case c.has("address-family", "ipv4", "unicast"):
			for _, af := range c.children {
				switch {
				case af.has("route-target"):
					v.RouteTargets = append(v.RouteTargets, af.rest(1))
				case af.has("export", "vrf"):
					if m := after(af.fields, "map"); m != "" {
						v.Exports = append(v.Exports, m)
					}
				}
			}
		}
	}
	p.cfg.VRFs[name] = v
}

func (p *configParser) staticRoute(vrf string, n *node) {
	r, ok := parseStaticRoute(n.fields[2:])
	if !ok {
		p.unparsed(n)
		return
	}
	v := p.cfg.VRFs[vrf]
	v.Routes = append(v.Routes, r)
	p.cfg.VRFs[vrf] = v
}

// parseStaticRoute reads "PREFIX NEXTHOP [vrf X] [name N] [tag T] [AD]".
func parseStaticRoute(f []string) (device.StaticRoute, bool) {
	if len(f) < 2 {
		return device.StaticRoute{}, false
	}
	prefix, err := netip.ParsePrefix(f[0])
	if err != nil {
		return device.StaticRoute{}, false
	}
	return device.StaticRoute{
		Prefix:     prefix,
		NextHop:    f[1],
		NextHopVRF: after(f, "vrf"),
		Name:       after(f, "name"),
	}, true
}

func (p *configParser) nve(n *node, name string) {
	nve := p.cfg.NVE
	if nve == nil {
		nve = &device.NVE{Name: name, Members: map[fabric.VNI]device.NVEMember{}}
		p.cfg.NVE = nve
	}
	for _, c := range n.children {
		switch {
		case c.has("shutdown"):
			nve.Shutdown = true
		case c.has("no", "shutdown"):
			nve.Shutdown = false
		case c.has("description"):
			nve.Description = c.rest(1)
		case c.has("source-interface"):
			nve.SourceInterface, _ = device.CanonicalInterface(c.arg(1))
		case c.has("host-reachability", "protocol", "bgp"):
			nve.HostReachabilityBGP = true
		case c.has("advertise", "virtual-rmac"):
			nve.AdvertiseVirtualRMAC = true
		case c.has("global", "suppress-arp"):
			nve.GlobalSuppressARP = true
		case c.has("global", "ingress-replication", "protocol", "bgp"):
			nve.GlobalIngressBGP = true
		case c.has("global", "mcast-group"):
			nve.GlobalMcastGroup = c.arg(2)
		case c.has("member", "vni"):
			p.nveMember(nve, c)
		}
	}
}

func (p *configParser) nveMember(nve *device.NVE, c *node) {
	vni, err := fabric.ParseVNI(c.arg(2))
	if err != nil {
		return
	}
	m := device.NVEMember{AssociateVRF: contains(c.fields, "associate-vrf")}
	if g := after(c.fields, "mcast-group"); g != "" {
		m.McastGroup = g
	}
	for _, o := range c.children {
		switch {
		case o.has("suppress-arp"):
			m.SuppressARP = true
		case o.has("ingress-replication", "protocol", "bgp"):
			m.IngressBGP = true
		case o.has("mcast-group"):
			m.McastGroup = o.arg(1)
		}
	}
	nve.Members[vni] = m
}

func (p *configParser) evpn(n *node) {
	for _, c := range n.children {
		if !c.has("vni") {
			continue
		}
		vni, err := fabric.ParseVNI(c.arg(1))
		if err != nil {
			continue
		}
		e := device.EVPNVNI{}
		for _, o := range c.children {
			switch {
			case o.has("rd"):
				e.RD = o.arg(1)
			case o.has("route-target"):
				e.RouteTargets = append(e.RouteTargets, o.rest(1))
			}
		}
		p.cfg.EVPN[vni] = e
	}
}

func (p *configParser) vpc(n *node) {
	d := &device.VPCDomain{ID: atoi(n.arg(2))}
	for _, c := range n.children {
		switch {
		case c.has("role", "priority"):
			d.RolePriority = atoi(c.arg(2))
		case c.has("peer-keepalive"):
			d.KeepaliveDest = after(c.fields, "destination")
			d.KeepaliveSource = after(c.fields, "source")
			d.KeepaliveVRF = after(c.fields, "vrf")
		case c.has("virtual", "peer-link"):
			d.VirtualPeerLink = after(c.fields, "destination")
		case c.has("delay", "restore") && len(c.fields) == 3:
			d.DelayRestore = atoi(c.arg(2))
		default:
			p.vpcOption(d, c)
		}
	}
	p.cfg.VPC = d
}

func (p *configParser) vpcOption(d *device.VPCDomain, c *node) {
	for _, o := range device.VPCOptions() {
		if c.text == string(o) {
			d.SetOption(o, true)
			return
		}
	}
	if strings.HasPrefix(c.text, "auto-recovery") {
		d.AutoRecovery = true
	}
}
