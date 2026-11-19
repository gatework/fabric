package device

import (
	"cmp"
	"slices"

	"github.com/gatework/fabric"
)

// Layer distinguishes bridged from routed VNIs.
type Layer uint8

// VNI layers.
const (
	LayerUnknown Layer = iota
	LayerL2
	LayerL3
)

// String returns "l2", "l3" or "unknown".
func (l Layer) String() string {
	switch l {
	case LayerL2:
		return "l2"
	case LayerL3:
		return "l3"
	case LayerUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// Segment joins everything a switch configures for one VNI: the VLAN that
// maps to it, the VRF that claims it, its NVE membership, its EVPN
// instance and the VLAN's SVI. A complete L2 segment has a VLAN, an NVE
// member and an EVPN instance; a complete L3 segment has a VRF, a VLAN, an
// associate-vrf NVE member and an SVI in that VRF.
type Segment struct {
	VNI    fabric.VNI    `json:"vni"`
	Layer  Layer         `json:"layer"`
	VLANs  []fabric.VLAN `json:"vlans,omitempty"`
	VRF    string        `json:"vrf,omitempty"`
	Member *NVEMember    `json:"member,omitempty"`
	EVPN   *EVPNVNI      `json:"evpn,omitempty"`
	SVI    *Interface    `json:"svi,omitempty"`
}

// VLAN returns the single VLAN mapped to the segment.
func (s Segment) VLAN() (fabric.VLAN, bool) {
	if len(s.VLANs) != 1 {
		return 0, false
	}
	return s.VLANs[0], true
}

// Gaps names the parts a segment of its layer lacks, for example
// "evpn" or "nve_member". An empty result means the segment is complete.
func (s Segment) Gaps() []string {
	var gaps []string
	if len(s.VLANs) == 0 {
		gaps = append(gaps, "vlan")
	}
	if s.Member == nil {
		gaps = append(gaps, "nve_member")
	}
	switch s.Layer {
	case LayerL2:
		if s.EVPN == nil {
			gaps = append(gaps, "evpn")
		}
	case LayerL3:
		if s.SVI == nil {
			gaps = append(gaps, "svi")
		}
		if s.Member != nil && !s.Member.AssociateVRF {
			gaps = append(gaps, "associate_vrf")
		}
	case LayerUnknown:
	}
	return gaps
}

// Segments returns every VNI referenced anywhere in the configuration,
// ordered by VNI.
func (c *Config) Segments() []Segment {
	byVNI := map[fabric.VNI]*Segment{}
	get := func(v fabric.VNI) *Segment {
		s, ok := byVNI[v]
		if !ok {
			s = &Segment{VNI: v}
			byVNI[v] = s
		}
		return s
	}
	for vlan, def := range c.VLANs {
		if def.VNI != 0 {
			s := get(def.VNI)
			s.VLANs = append(s.VLANs, vlan)
		}
	}
	for name, vrf := range c.VRFs {
		if vrf.VNI != 0 {
			s := get(vrf.VNI)
			s.VRF = name
			s.Layer = LayerL3
		}
	}
	if c.NVE != nil {
		for vni, m := range c.NVE.Members {
			s := get(vni)
			s.Member = &m
			if m.AssociateVRF {
				s.Layer = LayerL3
			}
		}
	}
	for vni, e := range c.EVPN {
		s := get(vni)
		s.EVPN = &e
	}
	out := make([]Segment, 0, len(byVNI))
	for _, s := range byVNI {
		slices.Sort(s.VLANs)
		if s.Layer == LayerUnknown && (s.EVPN != nil || s.Member != nil) {
			s.Layer = LayerL2
		}
		if v, ok := s.VLAN(); ok {
			if svi, ok := c.Interfaces[v.SVI()]; ok {
				s.SVI = &svi
			}
		}
		if s.Layer == LayerUnknown && len(s.VLANs) > 0 {
			s.Layer = LayerL2
		}
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b Segment) int { return cmp.Compare(a.VNI, b.VNI) })
	return out
}

// L3VNIs returns the VNIs claimed by VRFs.
func (c *Config) L3VNIs() map[fabric.VNI]string {
	out := map[fabric.VNI]string{}
	for name, v := range c.VRFs {
		if v.VNI != 0 {
			out[v.VNI] = name
		}
	}
	return out
}
