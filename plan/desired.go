package plan

import (
	"maps"
	"slices"
	"strings"

	"github.com/gatework/set"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Route-target statements the fabric requires.
const (
	RTBothAuto     = "both auto"
	RTBothAutoEVPN = "both auto evpn"
	RTImportAuto   = "import auto"
	RTExportAuto   = "export auto"
	RDAuto         = "auto"
)

// Desired is the target state of one switch's managed objects. Fields
// left zero in an object are not managed: an existing value is kept and
// never compared.
type Desired struct {
	// Features maps a feature to whether it must be on (true) or off.
	Features map[string]bool
	Settings map[device.Setting]string
	VRFs     map[string]device.VRF
	VLANs    map[fabric.VLAN]device.VLAN
	SVIs     map[fabric.VLAN]SVI
	Members  map[fabric.VNI]device.NVEMember
	EVPN     map[fabric.VNI]device.EVPNVNI
	// BGPVRFs lists VRFs that must advertise into EVPN, under BGPASN.
	BGPVRFs map[string]bool
	BGPASN  fabric.ASN
	// Ports maps an interface to the networks it must carry.
	Ports map[string]PortIntent

	NTPServers map[string]device.NTPServer
	LogServers map[string]device.LogServer
	// ExclusiveServers removes NTP and syslog servers not listed.
	ExclusiveServers bool
}

// PortIntent is what an attachment requires of a port.
type PortIntent struct {
	Mode  fabric.PortMode
	VLANs []fabric.VLAN
}

// NewDesired returns an empty desired state.
func NewDesired() *Desired {
	return &Desired{
		Features:   map[string]bool{},
		Settings:   map[device.Setting]string{},
		VRFs:       map[string]device.VRF{},
		VLANs:      map[fabric.VLAN]device.VLAN{},
		SVIs:       map[fabric.VLAN]SVI{},
		Members:    map[fabric.VNI]device.NVEMember{},
		EVPN:       map[fabric.VNI]device.EVPNVNI{},
		BGPVRFs:    map[string]bool{},
		Ports:      map[string]PortIntent{},
		NTPServers: map[string]device.NTPServer{},
		LogServers: map[string]device.LogServer{},
	}
}

// Merge adds other's objects; other wins on conflicts.
func (d *Desired) Merge(other *Desired) {
	maps.Copy(d.Features, other.Features)
	maps.Copy(d.Settings, other.Settings)
	maps.Copy(d.VRFs, other.VRFs)
	maps.Copy(d.VLANs, other.VLANs)
	maps.Copy(d.SVIs, other.SVIs)
	maps.Copy(d.Members, other.Members)
	maps.Copy(d.EVPN, other.EVPN)
	maps.Copy(d.BGPVRFs, other.BGPVRFs)
	maps.Copy(d.Ports, other.Ports)
	maps.Copy(d.NTPServers, other.NTPServers)
	maps.Copy(d.LogServers, other.LogServers)
	if other.BGPASN != 0 {
		d.BGPASN = other.BGPASN
	}
	d.ExclusiveServers = d.ExclusiveServers || other.ExclusiveServers
}

// mergeVRF returns have changed to satisfy want's managed fields.
func mergeVRF(have device.VRF, want device.VRF) device.VRF {
	out := have
	out.RouteTargets = slices.Clone(have.RouteTargets)
	if want.VNI != 0 {
		out.VNI = want.VNI
	}
	if want.RD != "" {
		out.RD = want.RD
	}
	for _, rt := range want.RouteTargets {
		if !slices.Contains(out.RouteTargets, rt) {
			out.RouteTargets = append(out.RouteTargets, rt)
		}
	}
	return out
}

func mergeVLAN(have, want device.VLAN) device.VLAN {
	if want.Name != "" {
		have.Name = want.Name
	}
	if want.VNI != 0 {
		have.VNI = want.VNI
	}
	return have
}

// effectiveRTs expands "both" into import and export.
func effectiveRTs(rts []string) map[string]bool {
	out := map[string]bool{}
	for _, rt := range rts {
		if rest, ok := strings.CutPrefix(rt, "both "); ok {
			out["import "+rest] = true
			out["export "+rest] = true
			continue
		}
		out[rt] = true
	}
	return out
}

func mergeEVPN(have, want device.EVPNVNI) device.EVPNVNI {
	out := have
	out.RouteTargets = slices.Clone(have.RouteTargets)
	if want.RD != "" {
		out.RD = want.RD
	}
	eff := effectiveRTs(out.RouteTargets)
	for _, rt := range want.RouteTargets {
		if !eff[rt] {
			out.RouteTargets = append(out.RouteTargets, rt)
		}
	}
	return out
}

func mergeMember(have, want device.NVEMember) device.NVEMember {
	have.AssociateVRF = want.AssociateVRF
	if want.SuppressARP {
		have.SuppressARP = true
	}
	if want.IngressBGP {
		have.IngressBGP, have.McastGroup = true, ""
	}
	if want.McastGroup != "" {
		have.McastGroup, have.IngressBGP = want.McastGroup, false
	}
	return have
}

func mergeSVI(have, want SVI) SVI {
	if want.Admin != device.AdminDefault {
		have.Admin = want.Admin
	}
	if want.MTU != 0 {
		have.MTU = want.MTU
	}
	if want.VRF != "" {
		if want.VRF != have.VRF {
			have.Address = SVI{}.Address
		}
		have.VRF = want.VRF
	}
	if want.Address.IsValid() {
		have.Address = want.Address
	}
	have.Anycast = have.Anycast || want.Anycast
	have.IPForward = have.IPForward || want.IPForward
	if want.ACLIn != "" {
		have.ACLIn = want.ACLIn
	}
	return have
}

// mergePort returns the port state that carries the intent's VLANs. Trunks
// gain missing VLANs and keep the others.
func mergePort(have Port, want PortIntent) Port {
	if want.Mode == fabric.PortAccess {
		out := Port{Mode: fabric.PortAccess}
		if len(want.VLANs) > 0 {
			out.Access = want.VLANs[0]
		}
		return out
	}
	out := Port{Mode: fabric.PortTrunk}
	need := set.New[uint16]()
	for _, v := range want.VLANs {
		need.Add(uint16(v), uint16(v))
	}
	switch {
	case have.Mode != fabric.PortTrunk:
		out.Allowed = need
	case have.Allowed == nil:
		out.Allowed = nil
	default:
		out.Allowed = have.Allowed.Union(need)
	}
	return out
}
