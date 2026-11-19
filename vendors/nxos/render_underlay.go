package nxos

import (
	"slices"
	"strconv"

	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/plan"
)

func renderInterface(s *script, o plan.InterfaceOp) {
	_, kind := device.CanonicalInterface(o.Interface)
	if o.Want == nil {
		if kind == device.KindEthernet {
			s.add(0, "default interface %s", o.Interface)
		} else {
			s.add(0, "no interface %s", o.Interface)
		}
		return
	}
	var have plan.L3
	if o.Have != nil {
		have = *o.Have
	} else {
		have.Routed = kind != device.KindEthernet
	}
	w := *o.Want
	s.add(0, "interface %s", o.Interface)
	if kind == device.KindEthernet && w.Routed != have.Routed {
		if w.Routed {
			s.add(1, "no switchport")
		} else {
			s.add(1, "switchport")
		}
	}
	if w.MTU != have.MTU {
		if w.MTU == 0 {
			s.add(1, "no mtu")
		} else {
			s.add(1, "mtu %d", w.MTU)
		}
	}
	renderAddresses(s, have, w)
	renderInterfaceOSPF(s, have.OSPF, w.OSPF)
	renderAdmin(s, have.Admin, w.Admin)
}

func renderAddresses(s *script, have, w plan.L3) {
	if w.Address != have.Address {
		if w.Address.IsValid() {
			s.add(1, "ip address %s", w.Address)
		} else {
			s.add(1, "no ip address")
		}
	}
	if w.Secondary != have.Secondary {
		if have.Secondary.IsValid() {
			s.add(1, "no ip address %s secondary", have.Secondary)
		}
		if w.Secondary.IsValid() {
			s.add(1, "ip address %s secondary", w.Secondary)
		}
	}
}

func renderInterfaceOSPF(s *script, have, want device.InterfaceOSPF) {
	toggle(s, 1, have.PointToPoint, want.PointToPoint, "ip ospf network point-to-point")
	if have.Process == want.Process && have.Area == want.Area {
		return
	}
	if have.Process != "" {
		s.add(1, "no ip router ospf %s area %s", have.Process, have.Area)
	}
	if want.Process != "" {
		s.add(1, "ip router ospf %s area %s", want.Process, want.Area)
	}
}

func renderOSPF(s *script, o plan.OSPFOp) {
	if o.Want == nil {
		s.add(0, "no router ospf %s", o.Process)
		return
	}
	var have device.OSPF
	if o.Have != nil {
		have = *o.Have
	}
	s.add(0, "router ospf %s", o.Process)
	if o.Want.RouterID != have.RouterID {
		if o.Want.RouterID == "" {
			s.add(1, "no router-id")
		} else {
			s.add(1, "router-id %s", o.Want.RouterID)
		}
	}
}

func renderBGP(s *script, o plan.BGPOp) {
	if o.Want == nil {
		s.add(0, "no router bgp %s", o.Have.ASN)
		return
	}
	var have plan.BGPProcess
	if o.Have != nil {
		have = *o.Have
	}
	s.add(0, "router bgp %s", o.Want.ASN)
	if o.Want.RouterID != have.RouterID {
		if o.Want.RouterID == "" {
			s.add(1, "no router-id")
		} else {
			s.add(1, "router-id %s", o.Want.RouterID)
		}
	}
}

func renderPeer(s *script, o plan.BGPPeerOp) {
	head := "neighbor " + o.Key
	if o.Template {
		head = "template peer " + o.Key
	}
	s.add(0, "router bgp %s", o.ASN)
	if o.Want == nil {
		s.add(1, "no %s", head)
		return
	}
	var have device.BGPPeer
	if o.Have != nil {
		have = *o.Have
	}
	w := *o.Want
	s.add(1, "%s", head)
	if w.RemoteAS != have.RemoteAS {
		if w.RemoteAS == 0 {
			s.add(2, "no remote-as")
		} else {
			s.add(2, "remote-as %s", w.RemoteAS)
		}
	}
	if w.Inherit != have.Inherit {
		if have.Inherit != "" {
			s.add(2, "no inherit peer %s", have.Inherit)
		}
		if w.Inherit != "" {
			s.add(2, "inherit peer %s", w.Inherit)
		}
	}
	if w.UpdateSource != have.UpdateSource {
		if w.UpdateSource == "" {
			s.add(2, "no update-source")
		} else {
			s.add(2, "update-source %s", w.UpdateSource)
		}
	}
	renderFamilies(s, have, w)
}

// renderFamilies adds or removes address families; options the model
// keeps (extended communities, route-reflector client) live in l2vpn evpn.
func renderFamilies(s *script, have, w device.BGPPeer) {
	for _, f := range w.Families {
		fresh := !slices.Contains(have.Families, f)
		evpn := f == "l2vpn evpn"
		ext := evpn && w.SendCommunityExtended && (fresh || !have.SendCommunityExtended)
		rr := evpn && w.RouteReflectorClient && (fresh || !have.RouteReflectorClient)
		lostRR := evpn && !fresh && have.RouteReflectorClient && !w.RouteReflectorClient
		if !fresh && !ext && !rr && !lostRR {
			continue
		}
		s.add(2, "address-family %s", f)
		if ext {
			s.add(3, "send-community")
			s.add(3, "send-community extended")
		}
		if rr {
			s.add(3, "route-reflector-client")
		}
		if lostRR {
			s.add(3, "no route-reflector-client")
		}
	}
	for _, f := range have.Families {
		if !slices.Contains(w.Families, f) {
			s.add(2, "no address-family %s", f)
		}
	}
}

func renderNVE(s *script, o plan.NVEOp) {
	if o.Want == nil {
		s.add(0, "no interface %s", o.Have.Name)
		return
	}
	var have plan.NVEBase
	if o.Have != nil {
		have = *o.Have
	}
	w := *o.Want
	s.add(0, "interface %s", w.Name)
	// The source interface of an active NVE changes only while shut.
	bounce := have.Up && have.SourceInterface != "" && w.SourceInterface != have.SourceInterface
	if bounce {
		s.add(1, "shutdown")
	}
	toggle(s, 1, have.HostReachabilityBGP, w.HostReachabilityBGP, "host-reachability protocol bgp")
	if w.SourceInterface != have.SourceInterface {
		if w.SourceInterface == "" {
			s.add(1, "no source-interface")
		} else {
			s.add(1, "source-interface %s", w.SourceInterface)
		}
	}
	switch {
	case w.Up && (!have.Up || bounce):
		s.add(1, "no shutdown")
	case !w.Up && have.Up:
		s.add(1, "shutdown")
	}
}

func renderVPCDomain(s *script, o plan.VPCDomainOp) {
	if o.Want == nil {
		s.add(0, "no vpc domain %d", o.Have.ID)
		return
	}
	var have plan.VPCBase
	if o.Have != nil {
		have = *o.Have
	}
	w := *o.Want
	s.add(0, "vpc domain %d", w.ID)
	if w.KeepaliveDest != have.KeepaliveDest || w.KeepaliveSource != have.KeepaliveSource {
		if w.KeepaliveDest == "" {
			s.add(1, "no peer-keepalive")
		} else {
			s.add(1, "peer-keepalive destination %s source %s", w.KeepaliveDest, w.KeepaliveSource)
		}
	}
	if w.VirtualPeerLink != have.VirtualPeerLink || w.VirtualPeerLinkSource != have.VirtualPeerLinkSource || w.VirtualPeerLinkDSCP != have.VirtualPeerLinkDSCP {
		if w.VirtualPeerLink == "" {
			s.add(1, "no virtual peer-link")
		} else {
			line := "virtual peer-link destination " + w.VirtualPeerLink + " source " + w.VirtualPeerLinkSource
			if w.VirtualPeerLinkDSCP != 0 {
				line += " dscp " + strconv.Itoa(w.VirtualPeerLinkDSCP)
			}
			s.add(1, "%s", line)
		}
	}
}

func renderReset(s *script, o plan.ResetOp) {
	if !o.Restore {
		if o.Saved.Kind == device.KindPortChannel {
			s.add(0, "no interface %s", o.Interface)
			return
		}
		s.add(0, "default interface %s", o.Interface)
		s.add(0, "interface %s", o.Interface)
		s.add(1, "shutdown")
		return
	}
	renderSavedInterface(s, o.Saved)
}

// renderSavedInterface writes every modeled attribute of an interface,
// to restore one that a reset cleared.
func renderSavedInterface(s *script, i device.Interface) {
	s.add(0, "interface %s", i.Name)
	if i.Description != "" {
		s.add(1, "description %s", i.Description)
	}
	if i.Layer2 {
		s.add(1, "switchport")
		s.add(1, "switchport mode %s", i.Mode)
		switch {
		case i.Mode.String() == "access" && i.AccessVLAN != 0:
			s.add(1, "switchport access vlan %s", i.AccessVLAN)
		case i.TrunkAllowed != nil:
			s.add(1, "switchport trunk allowed vlan %s", allowedText(i.TrunkAllowed))
		}
	} else if i.Kind == device.KindEthernet {
		s.add(1, "no switchport")
	}
	if i.MTU != 0 {
		s.add(1, "mtu %d", i.MTU)
	}
	if i.STPPortType != "" {
		s.add(1, "spanning-tree port type %s", i.STPPortType)
	}
	if i.VRF != "" {
		s.add(1, "vrf member %s", i.VRF)
	}
	for _, a := range i.Addresses {
		s.add(1, "ip address %s", a)
	}
	for _, a := range i.Secondary {
		s.add(1, "ip address %s secondary", a)
	}
	if i.OSPF != nil {
		renderInterfaceOSPF(s, device.InterfaceOSPF{}, *i.OSPF)
	}
	if i.PeerLink {
		s.add(1, "vpc peer-link")
	} else if i.VPC != 0 {
		s.add(1, "vpc %d", i.VPC)
	}
	if i.ChannelGroup != 0 {
		line := "channel-group " + strconv.Itoa(i.ChannelGroup)
		if i.ChannelMode != "" {
			line += " mode " + i.ChannelMode
		}
		s.add(1, "%s", line)
	}
	renderAdmin(s, device.AdminDefault, i.Admin)
}
