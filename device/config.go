package device

import (
	"maps"
	"net/netip"
	"slices"

	"github.com/gatework/netops/set"

	"github.com/gatework/fabric"
)

// Config is the typed running configuration of one switch.
type Config struct {
	Hostname string `json:"hostname,omitempty"`
	Version  string `json:"version,omitempty"`
	// Fingerprint is the SHA-256 of the configuration text with volatile
	// header lines removed; it binds plans to the configuration they were
	// computed from.
	Fingerprint string `json:"fingerprint"`

	// Features holds enabled features by their configuration name, for
	// example "bgp", "nv overlay" or "vn-segment-vlan-based". The EVPN
	// control plane ("nv overlay evpn") is recorded as "nv overlay evpn".
	Features map[string]bool `json:"features"`

	AnycastGatewayMAC fabric.MAC `json:"anycast_gateway_mac,omitempty"`
	// NVEInfraVLANs is "system nve infra-vlans".
	NVEInfraVLANs *set.Set[uint16] `json:"nve_infra_vlans,omitempty"`
	// TCAMRegions maps "hardware access-list tcam region" names to sizes.
	TCAMRegions map[string]int `json:"tcam_regions,omitempty"`

	VLANs      map[fabric.VLAN]VLAN   `json:"vlans"`
	VRFs       map[string]VRF         `json:"vrfs"`
	Interfaces map[string]Interface   `json:"interfaces"`
	NVE        *NVE                   `json:"nve,omitempty"`
	EVPN       map[fabric.VNI]EVPNVNI `json:"evpn"`
	BGP        *BGP                   `json:"bgp,omitempty"`
	OSPF       map[string]OSPF        `json:"ospf"`
	VPC        *VPCDomain             `json:"vpc,omitempty"`
	Management Management             `json:"management"`

	// Policy object names and their references, for orphan detection.
	ACLs        map[string]int `json:"acls"`
	PrefixLists map[string]int `json:"prefix_lists"`
	RouteMaps   map[string]int `json:"route_maps"`

	// Unparsed counts top-level configuration keywords the parser did not
	// model. Text is never stored.
	Unparsed map[string]int `json:"unparsed,omitempty"`
}

// NewConfig returns an empty configuration with every map allocated.
func NewConfig() *Config {
	return &Config{
		Features:    map[string]bool{},
		VLANs:       map[fabric.VLAN]VLAN{},
		VRFs:        map[string]VRF{},
		Interfaces:  map[string]Interface{},
		EVPN:        map[fabric.VNI]EVPNVNI{},
		OSPF:        map[string]OSPF{},
		ACLs:        map[string]int{},
		PrefixLists: map[string]int{},
		RouteMaps:   map[string]int{},
		TCAMRegions: map[string]int{},
	}
}

// Clone returns a deep copy.
func (c *Config) Clone() *Config {
	out := *c
	out.Features = maps.Clone(c.Features)
	out.TCAMRegions = maps.Clone(c.TCAMRegions)
	if c.NVEInfraVLANs != nil {
		out.NVEInfraVLANs = c.NVEInfraVLANs.Clone()
	}
	out.VLANs = maps.Clone(c.VLANs)
	out.VRFs = make(map[string]VRF, len(c.VRFs))
	for k, v := range c.VRFs {
		out.VRFs[k] = v.clone()
	}
	out.Interfaces = make(map[string]Interface, len(c.Interfaces))
	for k, v := range c.Interfaces {
		out.Interfaces[k] = v.clone()
	}
	if c.NVE != nil {
		n := *c.NVE
		n.Members = maps.Clone(c.NVE.Members)
		out.NVE = &n
	}
	out.EVPN = maps.Clone(c.EVPN)
	if c.BGP != nil {
		out.BGP = c.BGP.clone()
	}
	out.OSPF = maps.Clone(c.OSPF)
	if c.VPC != nil {
		v := *c.VPC
		out.VPC = &v
	}
	out.Management = c.Management.clone()
	out.ACLs = maps.Clone(c.ACLs)
	out.PrefixLists = maps.Clone(c.PrefixLists)
	out.RouteMaps = maps.Clone(c.RouteMaps)
	out.Unparsed = maps.Clone(c.Unparsed)
	return &out
}

// VLAN is one "vlan N" block.
type VLAN struct {
	Name string     `json:"name,omitempty"`
	VNI  fabric.VNI `json:"vni,omitempty"`
}

// VRF is one "vrf context" block.
type VRF struct {
	VNI fabric.VNI `json:"vni,omitempty"`
	RD  string     `json:"rd,omitempty"`
	// RouteTargets holds IPv4 unicast route-target statements such as
	// "both auto" and "both auto evpn".
	RouteTargets []string      `json:"route_targets,omitempty"`
	Routes       []StaticRoute `json:"routes,omitempty"`
	// Exports holds "export vrf default ... map M" route-map names.
	Exports []string `json:"exports,omitempty"`
}

// HasRouteTarget reports whether the VRF carries the statement, for example
// "both auto evpn".
func (v VRF) HasRouteTarget(stmt string) bool { return slices.Contains(v.RouteTargets, stmt) }

// StaticRoute is one "ip route".
type StaticRoute struct {
	Prefix  netip.Prefix `json:"prefix"`
	NextHop string       `json:"next_hop"`
	// NextHopVRF is the "vrf X" leak target, when present.
	NextHopVRF string `json:"next_hop_vrf,omitempty"`
	Name       string `json:"name,omitempty"`
}

// InterfaceKind classifies interfaces by name.
type InterfaceKind uint8

// Interface kinds.
const (
	KindOther InterfaceKind = iota
	KindEthernet
	KindPortChannel
	KindSVI
	KindLoopback
	KindNVE
	KindMgmt
)

// Interface is one "interface" block.
type Interface struct {
	Name        string         `json:"name"`
	Kind        InterfaceKind  `json:"kind"`
	Description string         `json:"description,omitempty"`
	Admin       AdminState     `json:"admin"`
	MTU         int            `json:"mtu,omitempty"`
	VRF         string         `json:"vrf,omitempty"`
	Addresses   []netip.Prefix `json:"addresses,omitempty"`
	Secondary   []netip.Prefix `json:"secondary,omitempty"`
	Anycast     bool           `json:"anycast,omitempty"`
	IPForward   bool           `json:"ip_forward,omitempty"`
	NoRedirects bool           `json:"no_redirects,omitempty"`
	ACLIn       string         `json:"acl_in,omitempty"`
	ACLOut      string         `json:"acl_out,omitempty"`
	OSPF        *InterfaceOSPF `json:"ospf,omitempty"`
	DHCPRelays  []netip.Addr   `json:"dhcp_relays,omitempty"`
	// Layer2 is set when "switchport" is configured.
	Layer2       bool             `json:"layer2,omitempty"`
	Mode         fabric.PortMode  `json:"mode"`
	AccessVLAN   fabric.VLAN      `json:"access_vlan,omitempty"`
	TrunkAllowed *set.Set[uint16] `json:"trunk_allowed,omitempty"`
	NativeVLAN   fabric.VLAN      `json:"native_vlan,omitempty"`
	ChannelGroup int              `json:"channel_group,omitempty"`
	ChannelMode  string           `json:"channel_mode,omitempty"`
	VPC          int              `json:"vpc,omitempty"`
	PeerLink     bool             `json:"peer_link,omitempty"`
	STPPortType  string           `json:"stp_port_type,omitempty"`
}

// Primary returns the first IPv4 address, if any.
func (i Interface) Primary() (netip.Prefix, bool) {
	if len(i.Addresses) == 0 {
		return netip.Prefix{}, false
	}
	return i.Addresses[0], true
}

// Carries reports whether a layer-2 port forwards vlan.
func (i Interface) Carries(vlan fabric.VLAN) bool {
	if !i.Layer2 {
		return false
	}
	if i.Mode == fabric.PortAccess {
		return i.AccessVLAN == vlan || (i.AccessVLAN == 0 && vlan == 1)
	}
	return i.TrunkAllowed == nil || i.TrunkAllowed.Contains(uint16(vlan))
}

// AdminState is the configured administrative state of an interface.
// NX-OS omits the default from the running configuration, so the parser
// records only what is written.
type AdminState uint8

// Administrative states.
const (
	// AdminDefault means neither "shutdown" nor "no shutdown" is written.
	AdminDefault AdminState = iota
	// AdminUp is an explicit "no shutdown".
	AdminUp
	// AdminDown is an explicit "shutdown".
	AdminDown
)

// String returns "default", "up" or "down".
func (a AdminState) String() string {
	switch a {
	case AdminUp:
		return "up"
	case AdminDown:
		return "down"
	case AdminDefault:
		return "default"
	default:
		return "default"
	}
}

// Up reports whether the interface is administratively enabled given the
// platform default for its kind: SVIs and Ethernet ports default to down,
// everything else to up.
func (i Interface) Up() bool {
	switch i.Admin {
	case AdminUp:
		return true
	case AdminDown:
		return false
	case AdminDefault:
		return i.Kind != KindSVI && i.Kind != KindEthernet
	default:
		return false
	}
}

// InterfaceOSPF is the OSPF binding of an interface.
type InterfaceOSPF struct {
	Process      string `json:"process"`
	Area         string `json:"area"`
	PointToPoint bool   `json:"point_to_point,omitempty"`
}

// NVE is "interface nve1".
type NVE struct {
	Name                 string                   `json:"name"`
	Shutdown             bool                     `json:"shutdown"`
	Description          string                   `json:"description,omitempty"`
	SourceInterface      string                   `json:"source_interface,omitempty"`
	HostReachabilityBGP  bool                     `json:"host_reachability_bgp,omitempty"`
	AdvertiseVirtualRMAC bool                     `json:"advertise_virtual_rmac,omitempty"`
	GlobalSuppressARP    bool                     `json:"global_suppress_arp,omitempty"`
	GlobalIngressBGP     bool                     `json:"global_ingress_bgp,omitempty"`
	GlobalMcastGroup     string                   `json:"global_mcast_group,omitempty"`
	Members              map[fabric.VNI]NVEMember `json:"members"`
}

// NVEMember is one "member vni".
type NVEMember struct {
	AssociateVRF bool   `json:"associate_vrf,omitempty"`
	SuppressARP  bool   `json:"suppress_arp,omitempty"`
	IngressBGP   bool   `json:"ingress_bgp,omitempty"`
	McastGroup   string `json:"mcast_group,omitempty"`
}

// EVPNVNI is one "vni N l2" under "evpn".
type EVPNVNI struct {
	RD           string   `json:"rd,omitempty"`
	RouteTargets []string `json:"route_targets,omitempty"`
}

// Equal reports whether both instances are configured identically.
func (e EVPNVNI) Equal(o EVPNVNI) bool {
	return e.RD == o.RD && slices.Equal(e.RouteTargets, o.RouteTargets)
}

func (v VRF) clone() VRF {
	v.RouteTargets = slices.Clone(v.RouteTargets)
	v.Routes = slices.Clone(v.Routes)
	v.Exports = slices.Clone(v.Exports)
	return v
}

func (i Interface) clone() Interface {
	i.Addresses = slices.Clone(i.Addresses)
	i.Secondary = slices.Clone(i.Secondary)
	i.DHCPRelays = slices.Clone(i.DHCPRelays)
	if i.TrunkAllowed != nil {
		i.TrunkAllowed = i.TrunkAllowed.Clone()
	}
	if i.OSPF != nil {
		o := *i.OSPF
		i.OSPF = &o
	}
	return i
}
