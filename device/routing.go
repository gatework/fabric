package device

import (
	"maps"
	"slices"

	"github.com/gatework/fabric"
)

// BGP is "router bgp".
type BGP struct {
	ASN                fabric.ASN `json:"asn"`
	RouterID           string     `json:"router_id,omitempty"`
	LogNeighborChanges bool       `json:"log_neighbor_changes,omitempty"`
	// EVPN holds global "address-family l2vpn evpn" options.
	RetainRouteTargetAll bool `json:"retain_route_target_all,omitempty"`
	AdvertisePIP         bool `json:"advertise_pip,omitempty"`
	MaximumPathsIBGP     int  `json:"maximum_paths_ibgp,omitempty"`

	Templates map[string]BGPPeer `json:"templates"`
	Neighbors map[string]BGPPeer `json:"neighbors"`
	VRFs      map[string]BGPVRF  `json:"vrfs"`
}

func (b *BGP) clone() *BGP {
	out := *b
	out.Templates = maps.Clone(b.Templates)
	out.Neighbors = maps.Clone(b.Neighbors)
	out.VRFs = make(map[string]BGPVRF, len(b.VRFs))
	for k, v := range b.VRFs {
		out.VRFs[k] = v.clone()
	}
	return &out
}

// BGPPeer is a neighbor or a peer template. Neighbors are keyed by address
// or, for dynamic peers, by prefix.
type BGPPeer struct {
	RemoteAS     fabric.ASN `json:"remote_as,omitempty"`
	Inherit      string     `json:"inherit,omitempty"`
	UpdateSource string     `json:"update_source,omitempty"`
	Description  string     `json:"description,omitempty"`
	BFD          bool       `json:"bfd,omitempty"`
	Shutdown     bool       `json:"shutdown,omitempty"`
	// Families lists configured address families, for example
	// "ipv4 unicast" and "l2vpn evpn".
	Families []string `json:"families,omitempty"`
	// RouteReflectorClient is set when any family marks the peer a client.
	RouteReflectorClient  bool   `json:"route_reflector_client,omitempty"`
	SendCommunityExtended bool   `json:"send_community_extended,omitempty"`
	KeyDigest             string `json:"key_digest,omitempty"`
}

// HasFamily reports whether the peer activates the address family.
func (p BGPPeer) HasFamily(af string) bool { return slices.Contains(p.Families, af) }

// BGPVRF is "vrf X" under "router bgp".
type BGPVRF struct {
	AdvertiseL2VPN   bool               `json:"advertise_l2vpn_evpn,omitempty"`
	Redistribute     map[string]string  `json:"redistribute,omitempty"`
	Networks         []string           `json:"networks,omitempty"`
	MaximumPathsIBGP int                `json:"maximum_paths_ibgp,omitempty"`
	DefaultOriginate bool               `json:"default_information_originate,omitempty"`
	Neighbors        map[string]BGPPeer `json:"neighbors,omitempty"`
}

func (v BGPVRF) clone() BGPVRF {
	v.Redistribute = maps.Clone(v.Redistribute)
	v.Networks = slices.Clone(v.Networks)
	v.Neighbors = maps.Clone(v.Neighbors)
	return v
}

// OSPF is one "router ospf" process.
type OSPF struct {
	RouterID           string            `json:"router_id,omitempty"`
	LogAdjacency       bool              `json:"log_adjacency_changes,omitempty"`
	ReferenceBandwidth string            `json:"reference_bandwidth,omitempty"`
	DefaultOriginate   bool              `json:"default_information_originate,omitempty"`
	Redistribute       map[string]string `json:"redistribute,omitempty"`
}

// PeerLinkKind is how vPC peers synchronize.
type PeerLinkKind uint8

// Peer-link kinds.
const (
	PeerLinkNone PeerLinkKind = iota
	PeerLinkPhysical
	PeerLinkVirtual
)

// VPCDomain is "vpc domain".
type VPCDomain struct {
	ID              int    `json:"id"`
	RolePriority    int    `json:"role_priority,omitempty"`
	KeepaliveDest   string `json:"keepalive_destination,omitempty"`
	KeepaliveSource string `json:"keepalive_source,omitempty"`
	KeepaliveVRF    string `json:"keepalive_vrf,omitempty"`
	VirtualPeerLink string `json:"virtual_peer_link_destination,omitempty"`
	// VirtualPeerLinkSource and VirtualPeerLinkDSCP complete the virtual
	// peer-link statement.
	VirtualPeerLinkSource string `json:"virtual_peer_link_source,omitempty"`
	VirtualPeerLinkDSCP   int    `json:"virtual_peer_link_dscp,omitempty"`
	PeerSwitch            bool   `json:"peer_switch,omitempty"`
	PeerGateway           bool   `json:"peer_gateway,omitempty"`
	L3PeerRouter          bool   `json:"layer3_peer_router,omitempty"`
	AutoRecovery          bool   `json:"auto_recovery,omitempty"`
	FastConvergence       bool   `json:"fast_convergence,omitempty"`
	ARPSynchronize        bool   `json:"ip_arp_synchronize,omitempty"`
	DelayRestore          int    `json:"delay_restore,omitempty"`
}

// VPCOption names a boolean vPC domain option as configured.
type VPCOption string

// vPC domain options the fabric baseline manages.
const (
	VPCPeerSwitch      VPCOption = "peer-switch"
	VPCPeerGateway     VPCOption = "peer-gateway"
	VPCL3PeerRouter    VPCOption = "layer3 peer-router"
	VPCAutoRecovery    VPCOption = "auto-recovery"
	VPCFastConvergence VPCOption = "fast-convergence"
	VPCARPSynchronize  VPCOption = "ip arp synchronize"
)

// VPCOptions lists the managed options in configuration order.
func VPCOptions() []VPCOption {
	return []VPCOption{VPCPeerSwitch, VPCPeerGateway, VPCL3PeerRouter, VPCAutoRecovery, VPCFastConvergence, VPCARPSynchronize}
}

// Option reports whether a boolean option is enabled.
func (d *VPCDomain) Option(o VPCOption) bool {
	switch o {
	case VPCPeerSwitch:
		return d.PeerSwitch
	case VPCPeerGateway:
		return d.PeerGateway
	case VPCL3PeerRouter:
		return d.L3PeerRouter
	case VPCAutoRecovery:
		return d.AutoRecovery
	case VPCFastConvergence:
		return d.FastConvergence
	case VPCARPSynchronize:
		return d.ARPSynchronize
	default:
		return false
	}
}

// SetOption enables or disables a boolean option.
func (d *VPCDomain) SetOption(o VPCOption, on bool) {
	switch o {
	case VPCPeerSwitch:
		d.PeerSwitch = on
	case VPCPeerGateway:
		d.PeerGateway = on
	case VPCL3PeerRouter:
		d.L3PeerRouter = on
	case VPCAutoRecovery:
		d.AutoRecovery = on
	case VPCFastConvergence:
		d.FastConvergence = on
	case VPCARPSynchronize:
		d.ARPSynchronize = on
	}
}

// PeerLink returns how the domain's peers synchronize, given the interfaces
// of the configuration.
func (c *Config) PeerLink() PeerLinkKind {
	if c.VPC == nil {
		return PeerLinkNone
	}
	if c.VPC.VirtualPeerLink != "" {
		return PeerLinkVirtual
	}
	for _, i := range c.Interfaces {
		if i.PeerLink {
			return PeerLinkPhysical
		}
	}
	return PeerLinkNone
}
