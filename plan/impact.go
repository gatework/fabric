package plan

import (
	"slices"
	"strings"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Impact is what a switch's operations touch, in operator terms.
type Impact struct {
	VLANs   []fabric.VLAN `json:"vlans,omitempty"`
	Tenants []string      `json:"tenants,omitempty"`
	Ports   []string      `json:"ports,omitempty"`
	VPCs    []int         `json:"vpcs,omitempty"`
	// Underlay is set when routing, NVE or vPC domain state changes.
	Underlay bool `json:"underlay,omitempty"`
	Deletes  int  `json:"deletes,omitempty"`
	// Disruptive lists VLANs and ports whose existing state changes or
	// goes away (creations disturb nobody); endpoints are counted there.
	Disruptive []string `json:"disruptive,omitempty"`
	Endpoints  int      `json:"endpoints"`
}

func underlayKind(k Kind) bool {
	return slices.Contains([]Kind{KindInterface, KindOSPF, KindBGP, KindBGPTemplate, KindBGPNeighbor, KindNVE, KindVPCDomain}, k)
}

// ImpactOf summarizes ops against the configuration they apply to.
func ImpactOf(ops []Op, cfg *device.Config) Impact {
	var im Impact
	vlanOf := map[fabric.VNI]fabric.VLAN{}
	for v, def := range cfg.VLANs {
		vlanOf[def.VNI] = v
	}
	for _, op := range ops {
		ref := op.Ref()
		if op.Action() == ActionDelete {
			im.Deletes++
		}
		if underlayKind(ref.Kind) || (ref.Kind == KindSetting && isUnderlaySetting(ref.Key)) {
			im.Underlay = true
		}
		disruptive := op.Action() != ActionCreate
		switch o := op.(type) {
		case VLANOp:
			im.addVLAN(o.VLAN, disruptive)
			if o.Want != nil && o.Want.VNI != 0 {
				vlanOf[o.Want.VNI] = o.VLAN
			}
		case SVIOp:
			im.addVLAN(o.VLAN, disruptive)
			im.addTenant(o.Have, o.Want)
		case NVEMemberOp:
			if v, ok := vlanOf[o.VNI]; ok {
				im.addVLAN(v, disruptive)
			}
		case EVPNOp:
			if v, ok := vlanOf[o.VNI]; ok {
				im.addVLAN(v, disruptive)
			}
		case VRFOp:
			im.Tenants = appendOnce(im.Tenants, o.Name)
		case BGPVRFOp:
			im.Tenants = appendOnce(im.Tenants, o.VRF)
		case PortOp:
			im.addPort(o.Interface, disruptive && o.Have != nil)
			for _, p := range []*Port{o.Have, o.Want} {
				if p != nil && p.VPC != 0 && !slices.Contains(im.VPCs, p.VPC) {
					im.VPCs = append(im.VPCs, p.VPC)
				}
			}
		case ResetOp:
			im.addPort(o.Interface, true)
		}
	}
	slices.Sort(im.VLANs)
	slices.Sort(im.Tenants)
	slices.Sort(im.Ports)
	slices.Sort(im.VPCs)
	return im
}

func isUnderlaySetting(key string) bool {
	return strings.HasPrefix(key, "vpc.") || strings.HasPrefix(key, "nve.") || strings.HasPrefix(key, "bgp.")
}

func appendOnce[T comparable](list []T, v T) []T {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

func (im *Impact) addVLAN(v fabric.VLAN, disruptive bool) {
	im.VLANs = appendOnce(im.VLANs, v)
	if disruptive {
		im.Disruptive = appendOnce(im.Disruptive, "vlan:"+v.String())
	}
}

func (im *Impact) addPort(p string, disruptive bool) {
	im.Ports = appendOnce(im.Ports, p)
	if disruptive {
		im.Disruptive = appendOnce(im.Disruptive, "port:"+p)
	}
}

func (im *Impact) addTenant(svis ...*SVI) {
	for _, s := range svis {
		if s != nil && s.VRF != "" {
			im.Tenants = appendOnce(im.Tenants, s.VRF)
		}
	}
}
