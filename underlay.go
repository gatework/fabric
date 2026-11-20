package fabric

import (
	"fmt"
	"net/netip"
	"slices"

	"github.com/gatework/netops/set"
)

// Underlay is the routed fabric beneath the overlay: OSPF between
// loopbacks over point-to-point links, iBGP EVPN with spines as route
// reflectors, and the pools new switches draw their addresses from.
type Underlay struct {
	OSPFProcess string `yaml:"ospf_process" json:"ospf_process"`
	OSPFArea    string `yaml:"ospf_area" json:"ospf_area"`
	// MTU of fabric links; zero leaves it unmanaged.
	MTU int `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	// LinkPrefix is the prefix length of fabric links, 31 or 30.
	LinkPrefix int     `yaml:"link_prefix" json:"link_prefix"`
	BGP        Peering `yaml:"bgp" json:"bgp"`
	Pools      Pools   `yaml:"pools" json:"pools"`
}

// NeighborStyle is how spines peer with VTEPs.
type NeighborStyle uint8

// Neighbor styles.
const (
	// NeighborsDynamic peers with the whole router-id pool as one prefix
	// neighbor, so adding a VTEP does not touch spine BGP.
	NeighborsDynamic NeighborStyle = iota
	// NeighborsExplicit lists every VTEP's router-id on every spine.
	NeighborsExplicit
)

// String returns "dynamic" or "explicit".
func (n NeighborStyle) String() string {
	if n == NeighborsExplicit {
		return "explicit"
	}
	return "dynamic"
}

// MarshalText formats the style.
func (n NeighborStyle) MarshalText() ([]byte, error) { return []byte(n.String()), nil }

// UnmarshalText parses the style.
func (n *NeighborStyle) UnmarshalText(text []byte) error {
	switch string(text) {
	case "dynamic", "":
		*n = NeighborsDynamic
	case "explicit":
		*n = NeighborsExplicit
	default:
		return fmt.Errorf("%w: neighbor style %q", ErrInvalid, text)
	}
	return nil
}

// Peering is the iBGP EVPN design of a region.
type Peering struct {
	Neighbors NeighborStyle `yaml:"neighbors" json:"neighbors"`
	// VTEPTemplate is the peer template VTEPs use toward spines.
	VTEPTemplate string `yaml:"vtep_template" json:"vtep_template"`
	// SpineTemplate is the peer template spines use toward VTEPs.
	SpineTemplate string `yaml:"spine_template" json:"spine_template"`
}

// Pools are the address and number ranges allocation draws from.
type Pools struct {
	RouterID    Prefix `yaml:"router_id" json:"router_id"`
	VTEP        Prefix `yaml:"vtep" json:"vtep"`
	AnycastVTEP Prefix `yaml:"anycast_vtep,omitempty" json:"anycast_vtep,omitzero"`
	Links       Prefix `yaml:"links" json:"links"`
	// VPCDomains and PortChannels are number ranges such as "1-999".
	VPCDomains   *set.Set[uint16] `yaml:"vpc_domains,omitempty" json:"vpc_domains,omitempty"`
	PortChannels *set.Set[uint16] `yaml:"port_channels,omitempty" json:"port_channels,omitempty"`
	// L3VNIs and VLANs bound automatic tenant and network numbering.
	L3VNIs *set.Set[uint32] `yaml:"l3vnis,omitempty" json:"l3vnis,omitempty"`
	VLANs  *set.Set[uint16] `yaml:"vlans,omitempty" json:"vlans,omitempty"`
}

// PeerLinkKind is how vPC peers synchronize.
type PeerLinkKind uint8

// Peer-link kinds.
const (
	// PeerLinkVirtual carries vPC peer traffic over the fabric.
	PeerLinkVirtual PeerLinkKind = iota
	// PeerLinkPhysical uses a dedicated port-channel between the peers.
	PeerLinkPhysical
)

// String returns "virtual" or "physical".
func (k PeerLinkKind) String() string {
	if k == PeerLinkPhysical {
		return "physical"
	}
	return "virtual"
}

// MarshalText formats the kind.
func (k PeerLinkKind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// UnmarshalText parses the kind.
func (k *PeerLinkKind) UnmarshalText(text []byte) error {
	switch string(text) {
	case "virtual", "":
		*k = PeerLinkVirtual
	case "physical":
		*k = PeerLinkPhysical
	default:
		return fmt.Errorf("%w: peer-link %q", ErrInvalid, text)
	}
	return nil
}

// VPCPair is two VTEPs of one role sharing an anycast VTEP address and
// dual-homing hosts.
type VPCPair struct {
	Domain      int          `yaml:"domain" json:"domain"`
	AnycastVTEP Addr         `yaml:"anycast_vtep,omitempty" json:"anycast_vtep,omitzero"`
	PeerLink    PeerLinkKind `yaml:"peer_link" json:"peer_link"`
	// PortChannel is the physical peer-link's port-channel number.
	PortChannel int          `yaml:"port_channel,omitempty" json:"port_channel,omitempty"`
	Members     [2]VPCMember `yaml:"members" json:"members"`
}

// VPCMember is one side of a vPC pair. Zero addresses default to the
// switch's management address (keepalive) and router-id (peer-link).
type VPCMember struct {
	Switch    string `yaml:"switch" json:"switch"`
	Keepalive Addr   `yaml:"keepalive,omitempty" json:"keepalive,omitzero"`
	// PeerLinkSource is the virtual peer-link source address.
	PeerLinkSource Addr `yaml:"peer_link_source,omitempty" json:"peer_link_source,omitzero"`
	// PeerLinkPorts are the physical peer-link member ports.
	PeerLinkPorts []string `yaml:"peer_link_ports,omitempty" json:"peer_link_ports,omitempty"`
}

// Has reports whether the switch is a member.
func (p *VPCPair) Has(name string) bool {
	return p.Members[0].Switch == name || p.Members[1].Switch == name
}

// Other returns the other member's name.
func (p *VPCPair) Other(name string) string {
	if p.Members[0].Switch == name {
		return p.Members[1].Switch
	}
	return p.Members[0].Switch
}

// Member returns the named side and the other side.
func (p *VPCPair) Member(name string) (self, peer VPCMember) {
	if p.Members[0].Switch == name {
		return p.Members[0], p.Members[1]
	}
	return p.Members[1], p.Members[0]
}

// Names returns both member names in order.
func (p *VPCPair) Names() []string { return []string{p.Members[0].Switch, p.Members[1].Switch} }

// Link is one routed point-to-point fabric link.
type Link struct {
	A LinkEnd `yaml:"a" json:"a"`
	B LinkEnd `yaml:"b" json:"b"`
}

// LinkEnd is one end of a link: a switch port and its address.
type LinkEnd struct {
	Switch    string `yaml:"switch" json:"switch"`
	Interface string `yaml:"interface" json:"interface"`
	Address   Prefix `yaml:"address" json:"address"`
}

// LinkView is a link seen from one of its ends.
type LinkView struct {
	Local, Remote LinkEnd
}

// Uses reports whether the link ends on the switch port.
func (l *Link) Uses(sw, iface string) bool {
	return (l.A.Switch == sw && l.A.Interface == iface) || (l.B.Switch == sw && l.B.Interface == iface)
}

// Addresses returns every address the region's underlay declares:
// router-ids, VTEP and anycast VTEP addresses, and link ends.
func (r *Region) Addresses() []netip.Addr {
	var out []netip.Addr
	for _, s := range r.Switches {
		if s.RouterID.IsValid() {
			out = append(out, s.RouterID.Addr)
		}
		// A VTEP may source NVE from loopback0, sharing the router-id.
		if s.VTEP.IsValid() && s.VTEP != s.RouterID {
			out = append(out, s.VTEP.Addr)
		}
	}
	for _, p := range r.VPCPairs {
		if p.AnycastVTEP.IsValid() {
			out = append(out, p.AnycastVTEP.Addr)
		}
	}
	for _, l := range r.Links {
		out = append(out, l.A.Address.Addr(), l.B.Address.Addr())
	}
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	return out
}
