package synth

import (
	"fmt"
	"net/netip"
	"strings"
)

func (g *gen) paired(i int) (peer int, ok bool) {
	peer = i ^ 1
	return peer, peer < g.spec.Leaves
}

// vtep is the address peers see for leaf i: the pair's anycast address.
func (g *gen) vtep(i int) netip.Addr {
	if _, ok := g.paired(i); ok {
		return g.anycast(i / 2)
	}
	return g.lo1(i)
}

func (g *gen) hostIP(leaf, host int) (net int, ip string) {
	s := g.spec
	net = host % s.Networks
	perNet := (s.LocalHosts + s.Networks - 1) / s.Networks
	idx := leaf*perNet + host/s.Networks + 2
	return net, fmt.Sprintf("10.%d.%d.%d", g.r, 10+net, idx)
}

func (g *gen) leaf(i int) map[string]string {
	s := g.spec
	peer, paired := g.paired(i)
	var c strings.Builder
	w := func(format string, args ...any) { _, _ = fmt.Fprintf(&c, format, args...) }
	w("version 10.4(2)\nhostname %s\n", g.name("LEF", i))
	for _, f := range []string{"ospf", "bgp", "interface-vlan", "vn-segment-vlan-based", "lacp", "nv overlay"} {
		w("feature %s\n", f)
	}
	w("nv overlay evpn\n")
	if paired {
		w("feature vpc\nhardware access-list tcam region ing-flow-redirect 512\n")
	}
	w("fabric forwarding anycast-gateway-mac 0000.2222.3333\n")
	for k := range s.Networks {
		w("vlan %d\n  name net%d\n  vn-segment %d\n", 100+k, k, 10100+k)
	}
	w("vlan 999\n  vn-segment 50000\n")
	w("vrf context PROD\n  vni 50000\n  rd auto\n  address-family ipv4 unicast\n    route-target both auto\n    route-target both auto evpn\n")
	if paired {
		w("vpc domain %d\n  peer-switch\n  peer-keepalive destination %s source %s\n  virtual peer-link destination %s source %s dscp 56\n  peer-gateway\n  layer3 peer-router\n  auto-recovery\n  ip arp synchronize\n",
			i/2+1, g.mgmt(peer), g.mgmt(i), g.lo0(peer), g.lo0(i))
	}
	for k := range s.Networks {
		w("interface Vlan%d\n  no shutdown\n  mtu 9216\n  vrf member PROD\n  ip address 10.%d.%d.1/24\n  fabric forwarding mode anycast-gateway\n", 100+k, g.r, 10+k)
	}
	w("interface Vlan999\n  no shutdown\n  mtu 9216\n  vrf member PROD\n  ip forward\n")
	w("interface nve1\n  no shutdown\n  host-reachability protocol bgp\n")
	if paired {
		w("  advertise virtual-rmac\n")
	}
	w("  source-interface loopback1\n")
	for k := range s.Networks {
		w("  member vni %d\n    suppress-arp\n    ingress-replication protocol bgp\n", 10100+k)
	}
	w("  member vni 50000 associate-vrf\n")
	for h := range 20 {
		w("interface Ethernet1/%d\n  switchport\n  switchport access vlan %d\n  spanning-tree port type edge\n  no shutdown\n", h+1, 100+h%s.Networks)
	}
	for sp := range s.Spines {
		w("interface Ethernet1/%d\n  no switchport\n  mtu 9216\n  ip address %s\n  ip ospf network point-to-point\n  ip router ospf 10 area 0.0.0.0\n  no shutdown\n", 49+sp, g.linkEnd(i, sp, 0))
	}
	w("interface mgmt0\n  vrf member management\n  ip address %s/16\n", g.mgmt(i))
	w("interface loopback0\n  ip address %s/32\n  ip router ospf 10 area 0.0.0.0\n", g.lo0(i))
	w("interface loopback1\n  ip address %s/32\n", g.lo1(i))
	if paired {
		w("  ip address %s/32 secondary\n", g.anycast(i/2))
	}
	w("  ip router ospf 10 area 0.0.0.0\n")
	g.leafRouting(&c, i, paired)
	out := map[string]string{"show running-config": c.String()}
	g.leafState(i, out)
	return out
}

func (g *gen) leafRouting(c *strings.Builder, i int, paired bool) {
	s := g.spec
	w := func(format string, args ...any) { _, _ = fmt.Fprintf(c, format, args...) }
	w("router ospf 10\n  router-id %s\n", g.lo0(i))
	w("router bgp %d\n  router-id %s\n  address-family l2vpn evpn\n    retain route-target all\n", 65000+g.r, g.lo0(i))
	if paired {
		w("    advertise-pip\n")
	}
	w("  template peer SPINES\n    remote-as %d\n    update-source loopback0\n    address-family l2vpn evpn\n      send-community\n      send-community extended\n", 65000+g.r)
	for sp := range s.Spines {
		w("  neighbor %s\n    inherit peer SPINES\n", g.lo0(200+sp))
	}
	w("  vrf PROD\n    address-family ipv4 unicast\n      advertise l2vpn evpn\n")
	w("evpn\n")
	for k := range s.Networks {
		w("  vni %d l2\n    rd auto\n    route-target import auto\n    route-target export auto\n", 10100+k)
	}
}

func (g *gen) leafState(i int, out map[string]string) {
	s := g.spec
	var peers, vnis, bgp, ospf, ifs, cdp strings.Builder
	peers.WriteString("Interface Peer-IP          State LearnType Uptime   Router-Mac\n--------- ---------------  ----- --------- -------- -----------------\n")
	seen := map[netip.Addr]bool{g.vtep(i): true}
	for l := range s.Leaves {
		if a := g.vtep(l); !seen[a] {
			seen[a] = true
			fmt.Fprintf(&peers, "nve1      %s Up    CP        1w0d     5254.0000.%04x\n", a, l)
		}
	}
	vnis.WriteString("Interface VNI      Multicast-group   State Mode Type [BD/VRF]      Flags\n--------- -------- ----------------- ----- ---- ------------------ -----\n")
	for k := range s.Networks {
		fmt.Fprintf(&vnis, "nve1      %d    UnicastBGP        Up    CP   L2 [%d]            SA\n", 10100+k, 100+k)
	}
	vnis.WriteString("nve1      50000    n/a               Up    CP   L3 [PROD]\n")
	fmt.Fprintf(&bgp, "BGP router identifier %s, local AS number %d\nBGP table version is 1, L2VPN EVPN config peers %d, capable peers %d\n\nNeighbor        V    AS    MsgRcvd    MsgSent   TblVer  InQ OutQ Up/Down  State/PfxRcd\n", g.lo0(i), 65000+g.r, s.Spines, s.Spines)
	fmt.Fprintf(&ospf, " OSPF Process ID 10 VRF default\n Total number of neighbors: %d\n Neighbor ID     Pri State            Up Time  Address         Interface\n", s.Spines)
	ifs.WriteString("Ethernet        VLAN    Type Mode   Status  Reason                 Speed     Port\n")
	for sp := range s.Spines {
		fmt.Fprintf(&bgp, "%s 4 %d 100 100 1 0 0 1w0d 900\n", g.lo0(200+sp), 65000+g.r)
		fmt.Fprintf(&ospf, " %s 1 FULL/ -  1w0d %s Eth1/%d\n", g.lo0(200+sp), g.linkEnd(i, sp, 1).Addr(), 49+sp)
		fmt.Fprintf(&ifs, "Eth1/%d          --      eth  routed up      none                      100G(D) --\n", 49+sp)
		fmt.Fprintf(&cdp, "----------------------------------------\nDevice ID:%s(FDO%07d)\nPlatform: N9K-C9336C-FX2, Capabilities: Router Switch\nInterface: Ethernet1/%d, Port ID (outgoing port): Ethernet1/%d\n", g.name("SPN", sp), sp, 49+sp, i+1)
	}
	for h := range 20 {
		fmt.Fprintf(&ifs, "Eth1/%d          %d     eth  access up      none                      10G(D) --\n", h+1, 100+h%s.Networks)
	}
	g.endpoints(i, out)
	out["show nve peers"] = peers.String()
	out["show nve vni"] = vnis.String()
	out["show bgp l2vpn evpn summary"] = bgp.String()
	out["show ip ospf neighbors"] = ospf.String()
	out["show interface brief"] = ifs.String()
	out["show port-channel summary"] = "Group Port-       Type     Protocol  Member Ports\n"
	out["show cdp neighbors detail"] = cdp.String()
	if _, ok := g.paired(i); ok {
		out["show vpc brief"] = fmt.Sprintf("vPC domain id                     : %d\nPeer status                       : peer adjacency formed ok\nvPC keep-alive status             : peer is alive\nConfiguration consistency status  : success\nPer-vlan consistency status       : success\nType-2 consistency status         : success\nVirtual-peerlink mode             : Enabled\n", i/2+1)
	}
}

// endpoints writes the MAC, l2route and mac-ip tables: local hosts on
// access ports and a sample of remote hosts behind other VTEPs.
func (g *gen) endpoints(i int, out map[string]string) {
	s := g.spec
	var macs, l2r, macip strings.Builder
	macs.WriteString("   VLAN     MAC Address      Type      age     Secure NTFY Ports\n---------+-----------------+--------+---------+------+----+------------------\n")
	l2r.WriteString("Topology    Mac Address    Prod   Flags              Seq No     Next-Hops\n----------- -------------- ------ ------------------- ---------- ---------\n")
	macip.WriteString("Topology    Mac Address    Host IP                                 Prod   Flags              Seq No     Next-Hops\n----------- -------------- ---------------------------------------- ------ ----------------- ---------- ---------\n")
	for h := range s.LocalHosts {
		net, ip := g.hostIP(i, h)
		port := fmt.Sprintf("Eth1/%d", h%20+1)
		fmt.Fprintf(&macs, "*  %d     %s   dynamic  0          F      F    %s\n", 100+net, g.mac(i, h), port)
		fmt.Fprintf(&l2r, "%d         %s Local  L,                 0          %s\n", 100+net, g.mac(i, h), port)
		fmt.Fprintf(&macip, "%d         %s %s                            HMM    L,                 0         Local\n", 100+net, g.mac(i, h), ip)
	}
	for j := range s.RemoteHosts {
		other := (i + 1 + j%(s.Leaves-1)) % s.Leaves
		h := j / (s.Leaves - 1) % s.LocalHosts
		if other == i {
			continue
		}
		net, ip := g.hostIP(other, h)
		fmt.Fprintf(&macs, "C  %d     %s   dynamic  NA         F      F    nve1(%s)\n", 100+net, g.mac(other, h), g.vtep(other))
		fmt.Fprintf(&l2r, "%d         %s BGP    SplRcv             0          %s (Label: %d)\n", 100+net, g.mac(other, h), g.vtep(other), 10100+net)
		fmt.Fprintf(&macip, "%d         %s %s                            BGP    --                 0         %s (Label: %d)\n", 100+net, g.mac(other, h), ip, g.vtep(other), 10100+net)
	}
	out["show mac address-table"] = macs.String()
	out["show l2route evpn mac all"] = l2r.String()
	out["show l2route evpn mac-ip all"] = macip.String()
}
