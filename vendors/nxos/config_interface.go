package nxos

import (
	"net/netip"

	"github.com/gatework/netops/set"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

func (p *configParser) iface(n *node) {
	name, kind := device.CanonicalInterface(n.rest(1))
	if kind == device.KindNVE {
		p.nve(n, name)
		return
	}
	i, ok := p.cfg.Interfaces[name]
	if !ok {
		i = device.Interface{Name: name, Kind: kind}
	}
	for _, c := range n.children {
		if !p.ifaceL3(&i, c) && !p.ifaceL2(&i, c) {
			p.ifaceMisc(&i, c)
		}
	}
	p.cfg.Interfaces[name] = i
}

func (p *configParser) ifaceL3(i *device.Interface, c *node) bool {
	switch {
	case c.has("ip", "address"):
		pfx, err := netip.ParsePrefix(c.arg(2))
		if err != nil {
			return true
		}
		if contains(c.fields, "secondary") {
			i.Secondary = append(i.Secondary, pfx)
		} else {
			i.Addresses = append(i.Addresses, pfx)
		}
	case c.has("vrf", "member"):
		i.VRF = c.arg(2)
	case c.has("fabric", "forwarding", "mode", "anycast-gateway"):
		i.Anycast = true
	case c.has("ip", "forward"):
		i.IPForward = true
	case c.has("no", "ip", "redirects"):
		i.NoRedirects = true
	case c.has("ip", "access-group"):
		if c.arg(3) == "out" {
			i.ACLOut = c.arg(2)
		} else {
			i.ACLIn = c.arg(2)
		}
	case c.has("ip", "router", "ospf"):
		o := ospfOf(i)
		o.Process, o.Area = c.arg(3), after(c.fields, "area")
	case c.has("ip", "ospf", "network", "point-to-point"):
		ospfOf(i).PointToPoint = true
	case c.has("ip", "dhcp", "relay", "address"):
		if a, err := netip.ParseAddr(c.arg(4)); err == nil {
			i.DHCPRelays = append(i.DHCPRelays, a)
		}
	default:
		return false
	}
	return true
}

func ospfOf(i *device.Interface) *device.InterfaceOSPF {
	if i.OSPF == nil {
		i.OSPF = &device.InterfaceOSPF{}
	}
	return i.OSPF
}

func (p *configParser) ifaceL2(i *device.Interface, c *node) bool {
	switch {
	case c.text == "switchport":
		i.Layer2 = true
	case c.text == "no switchport":
		i.Layer2 = false
	case c.has("switchport", "mode"):
		i.Layer2 = true
		if c.arg(2) == "trunk" {
			i.Mode = fabric.PortTrunk
		} else {
			i.Mode = fabric.PortAccess
		}
	case c.has("switchport", "access", "vlan"):
		i.Layer2 = true
		if v, err := fabric.ParseVLAN(c.arg(3)); err == nil {
			i.AccessVLAN = v
		}
	case c.has("switchport", "trunk", "native", "vlan"):
		if v, err := fabric.ParseVLAN(c.arg(4)); err == nil {
			i.NativeVLAN = v
		}
	case c.has("switchport", "trunk", "allowed", "vlan"):
		p.trunkAllowed(i, c)
	case c.has("channel-group"):
		i.ChannelGroup = atoi(c.arg(1))
		i.ChannelMode = after(c.fields, "mode")
	case c.has("vpc", "peer-link"):
		i.PeerLink = true
	case c.has("vpc") && len(c.fields) == 2:
		i.VPC = atoi(c.arg(1))
	case c.has("spanning-tree", "port", "type"):
		i.STPPortType = joinFrom(c.fields, 3)
	default:
		return false
	}
	return true
}

// trunkAllowed applies "switchport trunk allowed vlan [add|remove|except|none|all] LIST".
// NX-OS writes long lists as one base statement followed by "add" lines.
func (p *configParser) trunkAllowed(i *device.Interface, c *node) {
	op, list := c.arg(4), c.arg(5)
	switch op {
	case "none":
		i.TrunkAllowed = set.New[uint16]()
	case "all":
		i.TrunkAllowed = nil
	case "add", "remove":
		s, err := set.Parse[uint16](list)
		if err != nil {
			return
		}
		cur := i.TrunkAllowed
		if cur == nil {
			cur = set.New(set.Range[uint16]{Min: 1, Max: 4094})
		}
		if op == "add" {
			i.TrunkAllowed = cur.Union(s)
		} else {
			i.TrunkAllowed = cur.Difference(s)
		}
	default:
		s, err := set.Parse[uint16](op)
		if err == nil {
			i.TrunkAllowed = s
		}
	}
}

func (p *configParser) ifaceMisc(i *device.Interface, c *node) {
	switch {
	case c.text == "shutdown":
		i.Admin = device.AdminDown
	case c.text == "no shutdown":
		i.Admin = device.AdminUp
	case c.has("description"):
		i.Description = c.rest(1)
	case c.has("mtu"):
		i.MTU = atoi(c.arg(1))
	}
}
