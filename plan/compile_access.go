package plan

import (
	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// stpType returns the spanning-tree port type of an edge host port.
func stpType(spec fabric.PortSpec) string {
	switch {
	case !spec.Edge:
		return ""
	case spec.Mode == fabric.PortTrunk:
		return "edge trunk"
	default:
		return "edge"
	}
}

// compileAccess turns the switch's attachments into port intents: the
// port itself, or the port-channel (with its vPC number) and its LACP
// members configured identically.
func compileAccess(d *Desired, r *fabric.Region, sw *fabric.Switch) {
	for _, a := range r.Attachments {
		if a.Kind == fabric.AttachPort && a.Switch != sw.Name {
			continue
		}
		members, onSwitch := a.Members[sw.Name]
		if a.Kind != fabric.AttachPort && !onSwitch {
			continue
		}
		spec := r.Spec(a)
		base := PortIntent{
			Mode: spec.Mode, VLANs: spec.Networks, MTU: spec.MTU,
			STPPortType: stpType(spec), Description: spec.Description,
		}
		if a.Kind == fabric.AttachPort {
			name, kind := device.CanonicalInterface(a.Interface)
			port := base
			// Ethernet ports default to shutdown; bundles default to up.
			if kind == device.KindEthernet {
				port.Admin = device.AdminUp
			}
			d.Ports[name] = port
			continue
		}
		d.Features["lacp"] = true
		bundle := base
		if a.Kind == fabric.AttachVPC {
			bundle.VPC = a.PortChannel
		}
		d.Ports[fabric.PortChannelName(a.PortChannel)] = bundle
		for _, m := range members {
			name, _ := device.CanonicalInterface(m)
			member := base
			member.Admin = device.AdminUp
			member.ChannelGroup, member.ChannelMode = a.PortChannel, "active"
			d.Ports[name] = member
		}
	}
}
