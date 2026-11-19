package nxos

import (
	"slices"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

func (p *configParser) bgp(n *node) {
	asn, err := fabric.ParseASN(n.arg(2))
	if err != nil {
		p.unparsed(n)
		return
	}
	// A repeated block (incremental scripts read back as configuration)
	// continues the same process.
	b := p.cfg.BGP
	if b == nil || b.ASN != asn {
		b = &device.BGP{
			ASN:       asn,
			Templates: map[string]device.BGPPeer{},
			Neighbors: map[string]device.BGPPeer{},
			VRFs:      map[string]device.BGPVRF{},
		}
	}
	for _, c := range n.children {
		switch {
		case c.has("router-id"):
			b.RouterID = c.arg(1)
		case c.has("log-neighbor-changes"):
			b.LogNeighborChanges = true
		case c.has("address-family", "l2vpn", "evpn"):
			for _, o := range c.children {
				switch {
				case o.has("retain", "route-target", "all"):
					b.RetainRouteTargetAll = true
				case o.has("advertise-pip"):
					b.AdvertisePIP = true
				}
			}
		case c.has("address-family", "ipv4", "unicast"):
			for _, o := range c.children {
				if o.has("maximum-paths", "ibgp") {
					b.MaximumPathsIBGP = atoi(o.arg(2))
				}
			}
		case c.has("template", "peer"):
			b.Templates[c.arg(2)] = bgpPeer(b.Templates[c.arg(2)], c)
		case c.has("neighbor"):
			b.Neighbors[c.arg(1)] = bgpPeer(b.Neighbors[c.arg(1)], c)
		case c.has("vrf"):
			b.VRFs[c.arg(1)] = bgpVRF(b.VRFs[c.arg(1)], c)
		}
	}
	p.cfg.BGP = b
}

func bgpPeer(peer device.BGPPeer, n *node) device.BGPPeer {
	if as := after(n.fields, "remote-as"); as != "" {
		peer.RemoteAS, _ = fabric.ParseASN(as)
	}
	for _, c := range n.children {
		switch {
		case c.has("remote-as"):
			peer.RemoteAS, _ = fabric.ParseASN(c.arg(1))
		case c.has("inherit", "peer"):
			peer.Inherit = c.arg(2)
		case c.has("update-source"):
			peer.UpdateSource, _ = device.CanonicalInterface(c.arg(1))
		case c.has("description"):
			peer.Description = c.rest(1)
		case c.has("bfd"):
			peer.BFD = true
		case c.has("shutdown"):
			peer.Shutdown = true
		case c.has("password"):
			peer.KeyDigest = digest(c.rest(1))
		case c.has("address-family"):
			if !slices.Contains(peer.Families, c.rest(1)) {
				peer.Families = append(peer.Families, c.rest(1))
			}
			for _, o := range c.children {
				switch {
				case o.has("route-reflector-client"):
					peer.RouteReflectorClient = true
				case o.has("send-community", "extended"):
					peer.SendCommunityExtended = true
				}
			}
		}
	}
	return peer
}

func bgpVRF(v device.BGPVRF, n *node) device.BGPVRF {
	for _, c := range n.children {
		switch {
		case c.has("neighbor"):
			if v.Neighbors == nil {
				v.Neighbors = map[string]device.BGPPeer{}
			}
			v.Neighbors[c.arg(1)] = bgpPeer(v.Neighbors[c.arg(1)], c)
		case c.has("address-family", "ipv4", "unicast"):
			for _, o := range c.children {
				switch {
				case o.has("advertise", "l2vpn", "evpn"):
					v.AdvertiseL2VPN = true
				case o.has("redistribute"):
					if v.Redistribute == nil {
						v.Redistribute = map[string]string{}
					}
					v.Redistribute[o.arg(1)] = after(o.fields, "route-map")
				case o.has("network"):
					v.Networks = append(v.Networks, o.arg(1))
				case o.has("maximum-paths", "ibgp"):
					v.MaximumPathsIBGP = atoi(o.arg(2))
				case o.has("default-information", "originate"):
					v.DefaultOriginate = true
				}
			}
		}
	}
	return v
}

func (p *configParser) ospf(n *node) {
	o := p.cfg.OSPF[n.arg(2)]
	for _, c := range n.children {
		switch {
		case c.has("router-id"):
			o.RouterID = c.arg(1)
		case c.has("log-adjacency-changes"):
			o.LogAdjacency = true
		case c.has("auto-cost", "reference-bandwidth"):
			o.ReferenceBandwidth = joinFrom(c.fields, 2)
		case c.has("default-information", "originate"):
			o.DefaultOriginate = true
		case c.has("redistribute"):
			if o.Redistribute == nil {
				o.Redistribute = map[string]string{}
			}
			o.Redistribute[c.arg(1)] = after(c.fields, "route-map")
		}
	}
	p.cfg.OSPF[n.arg(2)] = o
}
