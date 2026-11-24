package device

import (
	"github.com/gatework/fabric"
)

// State is the operational state of a switch, one table per show command.
type State struct {
	NVEPeers     []NVEPeer         `json:"nve_peers"`
	NVEVNIs      []NVEVNI          `json:"nve_vnis"`
	BGP          BGPSummary        `json:"bgp"`
	OSPF         []OSPFNeighbor    `json:"ospf"`
	VPC          *VPCStatus        `json:"vpc,omitempty"`
	Interfaces   []InterfaceStatus `json:"interfaces"`
	PortChannels []PortChannel     `json:"port_channels"`
	MACs         []MACEntry        `json:"macs"`
	L2Routes     []L2Route         `json:"l2routes"`
	MACIPs       []MACIPRoute      `json:"mac_ips"`
	Neighbors    []CDPNeighbor     `json:"neighbors"`
}

// NVEPeer is one row of "show nve peers".
type NVEPeer struct {
	Interface string     `json:"interface"`
	Peer      string     `json:"peer"`
	Up        bool       `json:"up"`
	State     string     `json:"state"`
	LearnType string     `json:"learn_type,omitempty"`
	Uptime    string     `json:"uptime,omitempty"`
	RouterMAC fabric.MAC `json:"router_mac,omitempty"`
}

// NVEVNI is one row of "show nve vni".
type NVEVNI struct {
	Interface  string     `json:"interface"`
	VNI        fabric.VNI `json:"vni"`
	McastGroup string     `json:"mcast_group,omitempty"`
	Up         bool       `json:"up"`
	State      string     `json:"state"`
	Mode       string     `json:"mode,omitempty"`
	// Layer is "L2" or "L3".
	Layer string `json:"layer"`
	// Domain is the bridge domain (VLAN) or VRF name in brackets.
	Domain string `json:"domain,omitempty"`
	Flags  string `json:"flags,omitempty"`
}

// BGPSummary is "show bgp l2vpn evpn summary".
type BGPSummary struct {
	RouterID     string        `json:"router_id,omitempty"`
	LocalAS      fabric.ASN    `json:"local_as,omitempty"`
	ConfigPeers  int           `json:"config_peers,omitempty"`
	CapablePeers int           `json:"capable_peers,omitempty"`
	Neighbors    []BGPNeighbor `json:"neighbors"`
}

// BGPNeighbor is one session row. Established sessions report a prefix
// count; other sessions report their state.
type BGPNeighbor struct {
	Address     string     `json:"address"`
	AS          fabric.ASN `json:"as"`
	UpDown      string     `json:"up_down,omitempty"`
	Established bool       `json:"established"`
	State       string     `json:"state,omitempty"`
	Prefixes    int        `json:"prefixes"`
}

// OSPFNeighbor is one row of "show ip ospf neighbors".
type OSPFNeighbor struct {
	Process   string `json:"process"`
	VRF       string `json:"vrf"`
	RouterID  string `json:"router_id"`
	State     string `json:"state"`
	Full      bool   `json:"full"`
	Address   string `json:"address"`
	Interface string `json:"interface"`
}

// VPCStatus is "show vpc brief".
type VPCStatus struct {
	DomainID          int         `json:"domain_id"`
	PeerOK            bool        `json:"peer_ok"`
	PeerStatus        string      `json:"peer_status"`
	KeepaliveOK       bool        `json:"keepalive_ok"`
	KeepaliveStatus   string      `json:"keepalive_status"`
	ConsistencyOK     bool        `json:"consistency_ok"`
	PerVLANConsistent bool        `json:"per_vlan_consistent"`
	Type2Consistent   bool        `json:"type2_consistent"`
	Role              string      `json:"role,omitempty"`
	VirtualPeerLink   bool        `json:"virtual_peer_link"`
	PeerLinks         []VPCLink   `json:"peer_links"`
	Members           []VPCMember `json:"members"`
}

// VPCLink is one peer-link row.
type VPCLink struct {
	ID    int    `json:"id"`
	Port  string `json:"port"`
	Up    bool   `json:"up"`
	VLANs string `json:"vlans,omitempty"`
}

// VPCMember is one vPC row.
type VPCMember struct {
	ID         int    `json:"id"`
	Port       string `json:"port"`
	Up         bool   `json:"up"`
	Consistent bool   `json:"consistent"`
	Reason     string `json:"reason,omitempty"`
	VLANs      string `json:"vlans,omitempty"`
}

// InterfaceStatus is one row of "show interface brief".
type InterfaceStatus struct {
	Name        string `json:"name"`
	Up          bool   `json:"up"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	VLAN        string `json:"vlan,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Speed       string `json:"speed,omitempty"`
	PortChannel int    `json:"port_channel,omitempty"`
}

// PortChannel is one row of "show port-channel summary".
type PortChannel struct {
	Group    int                 `json:"group"`
	Name     string              `json:"name"`
	Flags    string              `json:"flags"`
	Up       bool                `json:"up"`
	Protocol string              `json:"protocol,omitempty"`
	Members  []PortChannelMember `json:"members"`
}

// PortChannelMember is one member port and its flag.
type PortChannelMember struct {
	Port    string `json:"port"`
	Flag    string `json:"flag"`
	Bundled bool   `json:"bundled"`
}

// MACEntry is one row of "show mac address-table".
type MACEntry struct {
	VLAN   fabric.VLAN `json:"vlan"`
	MAC    fabric.MAC  `json:"mac"`
	Type   string      `json:"type"`
	Marker string      `json:"marker,omitempty"`
	Port   string      `json:"port"`
	// RemoteVTEP is set for entries learned through nve1(peer).
	RemoteVTEP string `json:"remote_vtep,omitempty"`
}

// L2Route is one row of "show l2route evpn mac all".
type L2Route struct {
	Topology string     `json:"topology"`
	MAC      fabric.MAC `json:"mac"`
	Producer string     `json:"producer"`
	Flags    string     `json:"flags,omitempty"`
	Sequence int        `json:"sequence"`
	NextHops []string   `json:"next_hops"`
}

// MACIPRoute is one row of "show l2route evpn mac-ip all".
type MACIPRoute struct {
	Topology string     `json:"topology"`
	MAC      fabric.MAC `json:"mac"`
	IP       string     `json:"ip"`
	Producer string     `json:"producer"`
	Flags    string     `json:"flags,omitempty"`
	Sequence int        `json:"sequence"`
	NextHops []string   `json:"next_hops"`
}

// CDPNeighbor is one entry of "show cdp neighbors detail".
type CDPNeighbor struct {
	DeviceID    string `json:"device_id"`
	SystemName  string `json:"system_name,omitempty"`
	Platform    string `json:"platform,omitempty"`
	LocalPort   string `json:"local_port"`
	RemotePort  string `json:"remote_port"`
	Address     string `json:"address,omitempty"`
	MgmtAddress string `json:"mgmt_address,omitempty"`
}

// Port returns the "show interface brief" row of a port by any spelling
// of its name ("Eth1/1" and "Ethernet1/1" are the same port).
func (s State) Port(name string) (InterfaceStatus, bool) {
	want, _ := CanonicalInterface(name)
	for _, p := range s.Interfaces {
		if got, _ := CanonicalInterface(p.Name); got == want {
			return p, true
		}
	}
	return InterfaceStatus{}, false
}

// LearnedOn counts the MAC addresses learned locally on a port.
func (s State) LearnedOn(name string) int {
	want, _ := CanonicalInterface(name)
	n := 0
	for _, m := range s.MACs {
		if m.RemoteVTEP != "" {
			continue
		}
		if got, _ := CanonicalInterface(m.Port); got == want {
			n++
		}
	}
	return n
}
