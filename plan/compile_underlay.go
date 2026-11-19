package plan

import (
	"net/netip"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Loopback names the fabric uses.
const (
	loopbackRouter = "loopback0"
	loopbackVTEP   = "loopback1"
	nveName        = "nve1"
	familyEVPN     = "l2vpn evpn"
	peerLinkDSCP   = 56
)

func host(a fabric.Addr) netip.Prefix {
	if !a.IsValid() {
		return netip.Prefix{}
	}
	return netip.PrefixFrom(a.Addr, a.BitLen())
}

// vtepLoopback returns the NVE source loopback: loopback0 when the VTEP
// address is the router-id, loopback1 otherwise.
func vtepLoopback(sw *fabric.Switch) string {
	if sw.VTEP == sw.RouterID {
		return loopbackRouter
	}
	return loopbackVTEP
}

// compileUnderlay is the day-0 state of a switch: features, hostname,
// loopbacks, fabric links, OSPF, the BGP process with its templates and
// neighbors, the NVE interface and the vPC domain.
func compileUnderlay(d *Desired, r *fabric.Region, sw *fabric.Switch) {
	u := r.Underlay
	d.Settings[device.SettingHostname] = sw.Name
	for _, f := range []string{"ospf", "bgp"} {
		d.Features[f] = true
	}
	ospf := device.InterfaceOSPF{Process: u.OSPFProcess, Area: u.OSPFArea}
	d.Interfaces[loopbackRouter] = L3{Address: host(sw.RouterID), OSPF: ospf}
	for _, l := range r.LinksOf(sw.Name) {
		p2p := ospf
		p2p.PointToPoint = true
		name, _ := device.CanonicalInterface(l.Local.Interface)
		d.Interfaces[name] = L3{Admin: device.AdminUp, MTU: u.MTU, Address: l.Local.Address.Prefix, OSPF: p2p, Routed: true}
	}
	d.OSPF[u.OSPFProcess] = device.OSPF{RouterID: sw.RouterID.String()}
	d.BGP = &BGPProcess{ASN: r.Fabric.ASN, RouterID: sw.RouterID.String()}
	if sw.Role.VTEP() {
		compileVTEP(d, r, sw, ospf)
		return
	}
	compileSpine(d, r)
}

func compileVTEP(d *Desired, r *fabric.Region, sw *fabric.Switch, ospf device.InterfaceOSPF) {
	u := r.Underlay
	for _, f := range overlayFeatures() {
		d.Features[f] = true
	}
	d.Settings[device.SettingBGPRetainRT] = device.Enabled
	source := vtepLoopback(sw)
	pair, paired := r.Pair(sw.Name)
	if source == loopbackVTEP {
		lo := L3{Address: host(sw.VTEP), OSPF: ospf}
		if paired {
			lo.Secondary = host(pair.AnycastVTEP)
		}
		d.Interfaces[loopbackVTEP] = lo
	}
	d.NVE = &NVEBase{Name: nveName, Up: true, SourceInterface: source, HostReachabilityBGP: true}
	d.BGPTemplates[u.BGP.VTEPTemplate] = device.BGPPeer{
		RemoteAS: r.Fabric.ASN, UpdateSource: loopbackRouter, Families: []string{familyEVPN}, SendCommunityExtended: true,
	}
	for _, spine := range r.ByRole(fabric.RoleSpine) {
		if spine.RouterID.IsValid() {
			d.BGPNeighbors[spine.RouterID.String()] = device.BGPPeer{Inherit: u.BGP.VTEPTemplate}
		}
	}
	if paired {
		compileVPC(d, r, sw, pair)
	}
}

func compileSpine(d *Desired, r *fabric.Region) {
	u := r.Underlay
	d.BGPTemplates[u.BGP.SpineTemplate] = device.BGPPeer{
		RemoteAS: r.Fabric.ASN, UpdateSource: loopbackRouter, Families: []string{familyEVPN},
		SendCommunityExtended: true, RouteReflectorClient: true,
	}
	if u.BGP.Neighbors == fabric.NeighborsDynamic {
		// The template carries remote-as for the prefix neighbor too.
		d.BGPNeighbors[u.Pools.RouterID.Masked().String()] = device.BGPPeer{Inherit: u.BGP.SpineTemplate}
		return
	}
	for _, v := range r.VTEPs() {
		if v.RouterID.IsValid() {
			d.BGPNeighbors[v.RouterID.String()] = device.BGPPeer{Inherit: u.BGP.SpineTemplate}
		}
	}
}

// vpcOptions are the domain options an anycast-VTEP vPC pair needs.
func vpcOptions() []device.Setting {
	return []device.Setting{
		device.SettingVPCPeerSwitch, device.SettingVPCPeerGateway, device.SettingVPCL3PeerRouter,
		device.SettingVPCAutoRecovery, device.SettingVPCARPSynchronize,
		device.SettingNVEVirtualRMAC, device.SettingBGPAdvertisePIP,
	}
}

func compileVPC(d *Desired, r *fabric.Region, sw *fabric.Switch, pair *fabric.VPCPair) {
	self, peer := pair.Member(sw.Name)
	me, _ := r.Switch(self.Switch)
	other, _ := r.Switch(peer.Switch)
	d.Features["vpc"] = true
	for _, s := range vpcOptions() {
		d.Settings[s] = device.Enabled
	}
	keep := func(m fabric.VPCMember, s *fabric.Switch) string {
		if m.Keepalive.IsValid() {
			return m.Keepalive.String()
		}
		return s.Mgmt.String()
	}
	base := &VPCBase{ID: pair.Domain, KeepaliveDest: keep(peer, other), KeepaliveSource: keep(self, me)}
	if pair.PeerLink == fabric.PeerLinkVirtual {
		src := func(m fabric.VPCMember, s *fabric.Switch) string {
			if m.PeerLinkSource.IsValid() {
				return m.PeerLinkSource.String()
			}
			return s.RouterID.String()
		}
		base.VirtualPeerLink, base.VirtualPeerLinkSource = src(peer, other), src(self, me)
		base.VirtualPeerLinkDSCP = peerLinkDSCP
	} else {
		compilePeerLink(d, pair, self)
	}
	d.VPC = base
}

func compilePeerLink(d *Desired, pair *fabric.VPCPair, self fabric.VPCMember) {
	d.Features["lacp"] = true
	po := fabric.PortChannelName(pair.PortChannel)
	trunk := PortIntent{Mode: fabric.PortTrunk, STPPortType: "network", Admin: device.AdminDefault}
	link := trunk
	link.PeerLink = true
	d.Ports[po] = link
	for _, m := range self.PeerLinkPorts {
		name, _ := device.CanonicalInterface(m)
		member := trunk
		member.STPPortType = ""
		member.Admin = device.AdminUp
		member.ChannelGroup, member.ChannelMode = pair.PortChannel, "active"
		d.Ports[name] = member
	}
}
