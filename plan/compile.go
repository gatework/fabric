package plan

import (
	"slices"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// overlayFeatures are what a VTEP needs to carry any network.
func overlayFeatures() []string {
	return []string{"bgp", "interface-vlan", "vn-segment-vlan-based", "nv overlay", "nv overlay evpn"}
}

// Compile returns the desired overlay state of one switch of the region:
// its tenants, networks and attachments. Spines get an empty state.
func Compile(r *fabric.Region, sw *fabric.Switch) *Desired {
	d := NewDesired()
	if !sw.Role.VTEP() {
		return d
	}
	d.BGPASN = r.Fabric.ASN
	for _, t := range r.Tenants {
		if r.Hosts(t, sw) {
			compileTenant(d, r, t)
		}
	}
	for _, n := range r.Networks {
		if r.Carries(n, sw) {
			compileNetwork(d, r, n)
		}
	}
	for _, a := range r.Attachments {
		if a.Switch == sw.Name {
			name, _ := device.CanonicalInterface(a.Interface)
			d.Ports[name] = PortIntent{Mode: a.Mode, VLANs: slices.Clone(a.Networks)}
		}
	}
	if len(d.VLANs) > 0 {
		for _, f := range overlayFeatures() {
			d.Features[f] = true
		}
		if r.Fabric.AnycastGatewayMAC != "" {
			d.Settings[device.SettingAnycastGatewayMAC] = string(r.Fabric.AnycastGatewayMAC)
		}
	}
	return d
}

func compileTenant(d *Desired, r *fabric.Region, t *fabric.Tenant) {
	d.VRFs[t.Name] = device.VRF{VNI: t.L3VNI, RD: RDAuto, RouteTargets: []string{RTBothAuto, RTBothAutoEVPN}}
	d.VLANs[t.VLAN] = device.VLAN{VNI: t.L3VNI}
	d.SVIs[t.VLAN] = SVI{Admin: device.AdminUp, MTU: r.Fabric.SVIMTU, VRF: t.Name, IPForward: true}
	d.Members[t.L3VNI] = device.NVEMember{AssociateVRF: true}
	d.BGPVRFs[t.Name] = true
}

func compileNetwork(d *Desired, r *fabric.Region, n *fabric.Network) {
	vni, ok := r.VNIOf(n)
	if !ok {
		return
	}
	d.VLANs[n.VLAN] = device.VLAN{Name: n.Name, VNI: vni}
	m := device.NVEMember{SuppressARP: r.Fabric.SuppressARP}
	if r.Fabric.Replication == fabric.ReplicationIngress {
		m.IngressBGP = true
	}
	d.Members[vni] = m
	d.EVPN[vni] = device.EVPNVNI{RD: RDAuto, RouteTargets: []string{RTImportAuto, RTExportAuto}}
	if n.Gateway.IsValid() {
		d.SVIs[n.VLAN] = SVI{
			Admin: device.AdminUp, MTU: r.Fabric.SVIMTU, VRF: n.Tenant,
			Address: n.Gateway, Anycast: true, ACLIn: n.ACLIn,
		}
	}
}
