package device

import (
	"net/netip"

	"github.com/gatework/go-netops/evidence"

	"github.com/gatework/fabric"
)

// Dimensions of evidence collected from a switch. Each maps to one command.
const (
	DimConfig       evidence.Dimension = "configuration"
	DimNVEPeers     evidence.Dimension = "nve_peers"
	DimNVEVNIs      evidence.Dimension = "nve_vnis"
	DimBGP          evidence.Dimension = "bgp_evpn"
	DimOSPF         evidence.Dimension = "ospf"
	DimVPC          evidence.Dimension = "vpc"
	DimInterfaces   evidence.Dimension = "interfaces"
	DimPortChannels evidence.Dimension = "port_channels"
	DimMACs         evidence.Dimension = "macs"
	DimL2Routes     evidence.Dimension = "l2routes"
	DimMACIPs       evidence.Dimension = "mac_ips"
	DimCDP          evidence.Dimension = "cdp"
)

// Dimensions lists every dimension in collection order.
func Dimensions() []evidence.Dimension {
	return []evidence.Dimension{
		DimConfig, DimCDP, DimNVEPeers, DimNVEVNIs, DimBGP, DimOSPF, DimVPC,
		DimInterfaces, DimPortChannels, DimMACs, DimL2Routes, DimMACIPs,
	}
}

// Identity names a switch.
type Identity struct {
	Name string     `json:"name"`
	Mgmt netip.Addr `json:"mgmt"`
	// Region and Role come from the intent or inventory; they are empty for
	// a switch nobody declared.
	Region string      `json:"region,omitempty"`
	Role   fabric.Role `json:"role"`
	Model  string      `json:"model,omitempty"`
}

// Key returns the stable identifier used in subjects and file names: the
// management address.
func (id Identity) Key() string { return id.Mgmt.String() }

// Switch is everything observed on one switch.
type Switch struct {
	Identity
	Config   *Config           `json:"config,omitempty"`
	State    State             `json:"state"`
	Coverage evidence.Coverage `json:"coverage"`
	// Issues holds safe parsing and collection problems per dimension.
	Issues map[evidence.Dimension][]evidence.Issue `json:"issues,omitempty"`
}

// Has reports whether the dimension was observed completely.
func (s *Switch) Has(d evidence.Dimension) bool { return s.Coverage.Status(d).Complete() }

// Configured reports whether the configuration was observed completely.
func (s *Switch) Configured() bool { return s.Config != nil && s.Has(DimConfig) }

// VTEPAddress returns the address peers use to reach this VTEP: the
// secondary (anycast) address of the NVE source loopback when it has one,
// otherwise its primary address.
func (c *Config) VTEPAddress() (netip.Addr, bool) {
	if c.NVE == nil || c.NVE.SourceInterface == "" {
		return netip.Addr{}, false
	}
	lo, ok := c.Interfaces[c.NVE.SourceInterface]
	if !ok {
		return netip.Addr{}, false
	}
	if len(lo.Secondary) > 0 {
		return lo.Secondary[0].Addr(), true
	}
	if p, ok := lo.Primary(); ok {
		return p.Addr(), true
	}
	return netip.Addr{}, false
}

// PIPAddress returns the primary address of the NVE source loopback, which
// identifies the switch itself even inside a vPC pair.
func (c *Config) PIPAddress() (netip.Addr, bool) {
	if c.NVE == nil {
		return netip.Addr{}, false
	}
	lo, ok := c.Interfaces[c.NVE.SourceInterface]
	if !ok {
		return netip.Addr{}, false
	}
	p, ok := lo.Primary()
	return p.Addr(), ok
}
