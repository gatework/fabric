package capacity

import (
	"math"
	"net/netip"

	"github.com/gatework/netops/set"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Usage is used out of total; Total zero means unbounded or unknown.
type Usage struct {
	Name  string `json:"name"`
	Used  int    `json:"used"`
	Total int    `json:"total"`
}

// Percent returns the utilization, or -1 when Total is unknown.
func (u Usage) Percent() float64 {
	if u.Total <= 0 {
		return -1
	}
	return math.Round(1000*float64(u.Used)/float64(u.Total)) / 10
}

// Limits are per-switch platform limits.
type Limits struct {
	VLANs        int `json:"vlans"`
	MACs         int `json:"macs"`
	NVEPeers     int `json:"nve_peers"`
	PortChannels int `json:"port_channels"`
}

// DefaultLimits are conservative Nexus 9000 figures.
func DefaultLimits() Limits {
	return Limits{VLANs: 3967, MACs: 90000, NVEPeers: 256, PortChannels: 511}
}

// Switch is one switch's scale.
type Switch struct {
	Name         string `json:"name"`
	VLANs        Usage  `json:"vlans"`
	Ports        Usage  `json:"ports"`
	MACs         Usage  `json:"macs"`
	NVEPeers     Usage  `json:"nve_peers"`
	PortChannels Usage  `json:"port_channels"`
	// PortsUp and PortsReserved split the used Ethernet ports: up, or
	// down but described, routed or bundled. Reserved ports are the
	// first to reclaim when the switch runs out.
	PortsUp       int `json:"ports_up"`
	PortsReserved int `json:"ports_reserved"`
}

// All returns the switch's measures.
func (s Switch) All() []Usage { return []Usage{s.VLANs, s.Ports, s.MACs, s.NVEPeers, s.PortChannels} }

// Region is one region's capacity.
type Region struct {
	Name     string   `json:"name"`
	Pools    []Usage  `json:"pools"`
	Switches []Switch `json:"switches"`
}

// Compute measures a region. intent may be nil, which skips the pools.
func Compute(name string, intent *fabric.Region, switches []*device.Switch, lim Limits) Region {
	r := Region{Name: name}
	if intent != nil {
		r.Pools = pools(intent, switches)
	}
	for _, sw := range switches {
		if sw.Config != nil {
			r.Switches = append(r.Switches, scale(sw, lim))
		}
	}
	return r
}

func scale(sw *device.Switch, lim Limits) Switch {
	s := Switch{Name: sw.Name}
	cfg := sw.Config
	s.VLANs = Usage{Name: "VLAN", Used: len(cfg.VLANs), Total: lim.VLANs}
	eth, used, pcs := 0, 0, 0
	for name, i := range cfg.Interfaces {
		switch i.Kind {
		case device.KindEthernet:
			eth++
			// A port is taken when it is up, described, bundled or routed:
			// the same test as assigning a free port.
			st, _ := sw.State.Port(name)
			switch {
			case st.Up:
				used++
				s.PortsUp++
			case i.Description != "" || len(i.Addresses) > 0 || i.ChannelGroup != 0:
				used++
				s.PortsReserved++
			}
		case device.KindPortChannel:
			pcs++
		case device.KindOther, device.KindSVI, device.KindLoopback, device.KindNVE, device.KindMgmt:
		}
	}
	s.Ports = Usage{Name: "以太网端口", Used: used, Total: eth}
	s.PortChannels = Usage{Name: "port-channel", Used: pcs, Total: lim.PortChannels}
	s.MACs = Usage{Name: "MAC 表", Total: lim.MACs, Used: -1}
	if sw.Has(device.DimMACs) {
		s.MACs.Used = len(sw.State.MACs)
	}
	s.NVEPeers = Usage{Name: "NVE 对端", Total: lim.NVEPeers, Used: -1}
	if sw.Has(device.DimNVEPeers) && sw.Role.VTEP() {
		s.NVEPeers.Used = len(sw.State.NVEPeers)
	}
	return s
}

func hosts(p fabric.Prefix) int {
	if !p.IsValid() {
		return 0
	}
	bits := p.Addr().BitLen() - p.Bits()
	if bits >= 31 {
		return math.MaxInt32
	}
	return max(1<<bits-1, 0)
}

func inPool(p fabric.Prefix, addrs []netip.Addr) int {
	n := 0
	for _, a := range addrs {
		if p.IsValid() && p.Contains(a) {
			n++
		}
	}
	return n
}

func pools(r *fabric.Region, switches []*device.Switch) []Usage {
	out := []Usage{vlanPool(r)}
	if u := l3vniPool(r); u.Used > 0 || (r.Underlay != nil && r.Underlay.Pools.L3VNIs != nil) {
		out = append(out, u)
	}
	if u := r.Underlay; u != nil {
		var ids, vteps, anycast []netip.Addr
		for _, s := range r.Switches {
			ids = append(ids, s.RouterID.Addr)
			vteps = append(vteps, s.VTEP.Addr)
		}
		for _, p := range r.VPCPairs {
			anycast = append(anycast, p.AnycastVTEP.Addr)
		}
		out = append(out,
			Usage{Name: "router-id 地址池", Used: inPool(u.Pools.RouterID, ids), Total: hosts(u.Pools.RouterID)},
			Usage{Name: "VTEP 地址池", Used: inPool(u.Pools.VTEP, vteps), Total: hosts(u.Pools.VTEP)},
			Usage{Name: "anycast VTEP 地址池", Used: inPool(u.Pools.AnycastVTEP, anycast), Total: hosts(u.Pools.AnycastVTEP)},
			linkPool(r),
		)
	}
	out = append(out, Usage{Name: "vPC 域号", Used: len(r.VPCPairs), Total: 1000}, portChannelPool(r, switches))
	return out
}

func size16(s *set.Set[uint16]) int {
	n, _ := s.Size()
	return int(n)
}

func vlanPool(r *fabric.Region) Usage {
	pool := set.New(set.Range[uint16]{Min: 2, Max: 3967})
	if r.Underlay != nil && r.Underlay.Pools.VLANs != nil {
		pool = r.Underlay.Pools.VLANs
	}
	used := set.New[uint16]()
	for _, n := range r.Networks {
		used.Add(uint16(n.VLAN), uint16(n.VLAN))
	}
	for _, t := range r.Tenants {
		used.Add(uint16(t.VLAN), uint16(t.VLAN))
	}
	return Usage{Name: "VLAN", Used: size16(pool.Intersection(used)), Total: size16(pool)}
}

func l3vniPool(r *fabric.Region) Usage {
	pool := set.New(set.Range[uint32]{Min: 50000, Max: 59999})
	if r.Underlay != nil && r.Underlay.Pools.L3VNIs != nil {
		pool = r.Underlay.Pools.L3VNIs
	}
	used := 0
	for _, t := range r.Tenants {
		if pool.Contains(uint32(t.L3VNI)) {
			used++
		}
	}
	n, _ := pool.Size()
	return Usage{Name: "L3VNI", Used: used, Total: int(n)}
}

func linkPool(r *fabric.Region) Usage {
	u := r.Underlay
	total := 0
	if u.Pools.Links.IsValid() && u.LinkPrefix >= u.Pools.Links.Bits() {
		total = 1 << (u.LinkPrefix - u.Pools.Links.Bits())
	}
	used := 0
	for _, l := range r.Links {
		if u.Pools.Links.IsValid() && u.Pools.Links.Contains(l.A.Address.Addr()) {
			used++
		}
	}
	return Usage{Name: "互联子网", Used: used, Total: total}
}

// portChannelPool counts the largest number of bundles any one switch uses
// against the numbering pool.
func portChannelPool(r *fabric.Region, switches []*device.Switch) Usage {
	total := 4096
	if r.Underlay != nil && r.Underlay.Pools.PortChannels != nil {
		total = size16(r.Underlay.Pools.PortChannels)
	}
	most := 0
	for _, sw := range switches {
		if sw.Config == nil {
			continue
		}
		n := 0
		for _, i := range sw.Config.Interfaces {
			if i.Kind == device.KindPortChannel {
				n++
			}
		}
		most = max(most, n)
	}
	return Usage{Name: "port-channel 编号（单机最多）", Used: most, Total: total}
}
