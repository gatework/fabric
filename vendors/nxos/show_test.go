package nxos_test

import (
	"testing"

	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/vendors/nxos"
)

func observe(tb testing.TB, dim evidence.Dimension, fixture string) *device.Switch {
	tb.Helper()
	sw := &device.Switch{}
	nxos.Observe(tb.Context(), sw, dim, readFixture(tb, fixture))
	if s := sw.Coverage.Status(dim); s != evidence.StatusComplete {
		tb.Fatalf("%s status %s issues %+v", dim, s, sw.Issues[dim])
	}
	return sw
}

func TestObserveOverlay(t *testing.T) {
	t.Parallel()
	peers := observe(t, device.DimNVEPeers, "nve_peers.txt").State.NVEPeers
	if len(peers) != 2 || !peers[0].Up || peers[1].Up || peers[0].RouterMAC != "5254.0000.0001" {
		t.Errorf("nve peers = %+v", peers)
	}
	vnis := observe(t, device.DimNVEVNIs, "nve_vni.txt").State.NVEVNIs
	if len(vnis) != 3 || vnis[1].Up || vnis[2].Layer != "L3" || vnis[2].Domain != "PROD" || vnis[0].Flags != "SA" {
		t.Errorf("nve vnis = %+v", vnis)
	}
	bgp := observe(t, device.DimBGP, "bgp.txt").State.BGP
	if bgp.LocalAS != 65001 || bgp.CapablePeers != 1 || len(bgp.Neighbors) != 2 {
		t.Fatalf("bgp = %+v", bgp)
	}
	if n := bgp.Neighbors[0]; !n.Established || n.Prefixes != 2212 {
		t.Errorf("established neighbor = %+v", n)
	}
	if n := bgp.Neighbors[1]; n.Established || n.State != "Idle" {
		t.Errorf("idle neighbor = %+v", n)
	}
	ospf := observe(t, device.DimOSPF, "ospf.txt").State.OSPF
	if len(ospf) != 2 || !ospf[0].Full || ospf[1].Full || ospf[0].Interface != "Ethernet1/49" || ospf[0].State != "FULL/ -" {
		t.Errorf("ospf = %+v", ospf)
	}
}

func TestObserveVPCWrappedRows(t *testing.T) {
	t.Parallel()
	v := observe(t, device.DimVPC, "vpc.txt").State.VPC
	if v == nil || v.DomainID != 10 || !v.PeerOK || !v.KeepaliveOK || v.PerVLANConsistent || !v.VirtualPeerLink {
		t.Fatalf("vpc = %+v", v)
	}
	if len(v.PeerLinks) != 1 || v.PeerLinks[0].Port != "port-channel100" {
		t.Errorf("peer links = %+v", v.PeerLinks)
	}
	if len(v.Members) != 2 {
		t.Fatalf("members = %+v", v.Members)
	}
	if m := v.Members[0]; m.VLANs != "100-101,999" || !m.Consistent || !m.Up {
		t.Errorf("wrapped vlans = %+v", m)
	}
	if m := v.Members[1]; m.Up || m.Consistent || m.Reason != "Consistency Check Not Performed" {
		t.Errorf("wrapped reason = %+v", m)
	}
}

func TestObserveTables(t *testing.T) {
	t.Parallel()
	ifs := observe(t, device.DimInterfaces, "interfaces.txt").State.Interfaces
	byName := map[string]device.InterfaceStatus{}
	for _, i := range ifs {
		byName[i.Name] = i
	}
	if e := byName["Ethernet1/3"]; e.Up || e.Reason != "XCVR not inserted" {
		t.Errorf("Ethernet1/3 = %+v", e)
	}
	if e := byName["Ethernet1/1"]; !e.Up || e.PortChannel != 10 {
		t.Errorf("Ethernet1/1 = %+v", e)
	}
	if len(byName) != 8 {
		t.Errorf("interfaces = %d %+v", len(byName), ifs)
	}
	pcs := observe(t, device.DimPortChannels, "port_channels.txt").State.PortChannels
	if len(pcs) != 2 || len(pcs[0].Members) != 3 || pcs[0].Members[1].Bundled || !pcs[0].Up {
		t.Errorf("port-channels = %+v", pcs)
	}
	macs := observe(t, device.DimMACs, "macs.txt").State.MACs
	if len(macs) != 4 || macs[0].RemoteVTEP != "10.0.1.200" || macs[1].Port != "port-channel10" || macs[3].VLAN != 0 {
		t.Errorf("macs = %+v", macs)
	}
}

func TestObserveRoutes(t *testing.T) {
	t.Parallel()
	routes := observe(t, device.DimL2Routes, "l2routes.txt").State.L2Routes
	if len(routes) != 3 || routes[0].NextHops[0] != "10.0.1.200" || len(routes[0].NextHops) != 1 || routes[2].Sequence != 12 {
		t.Errorf("l2routes = %+v", routes)
	}
	for _, f := range []string{"macip_new.txt", "macip_old.txt"} {
		ips := observe(t, device.DimMACIPs, f).State.MACIPs
		if len(ips) == 0 || ips[0].IP != "10.10.100.21" || ips[0].Producer != "BGP" || ips[0].NextHops[0] != "10.0.1.200" {
			t.Errorf("%s = %+v", f, ips)
		}
	}
	cdp := observe(t, device.DimCDP, "cdp.txt").State.Neighbors
	if len(cdp) != 2 || cdp[0].LocalPort != "Ethernet1/49" || cdp[0].RemotePort != "Ethernet1/1" ||
		cdp[0].MgmtAddress != "198.51.100.1" || cdp[0].Address != "10.1.0.2" || cdp[0].Platform != "N9K-C9336C-FX2" {
		t.Errorf("cdp = %+v", cdp)
	}
}

func TestObserveRejectsAndUnknownLines(t *testing.T) {
	t.Parallel()
	sw := &device.Switch{}
	nxos.Observe(t.Context(), sw, device.DimVPC, "% Invalid command at '^' marker.")
	if s := sw.Coverage.Status(device.DimVPC); s != evidence.StatusUnsupported {
		t.Errorf("rejected status = %s", s)
	}
	nxos.Observe(t.Context(), sw, device.DimNVEPeers, "garbage without header")
	if s := sw.Coverage.Status(device.DimNVEPeers); s != evidence.StatusPartial {
		t.Errorf("headerless status = %s", s)
	}
	nxos.Observe(t.Context(), sw, device.DimNVEPeers, "")
	if s := sw.Coverage.Status(device.DimNVEPeers); s != evidence.StatusComplete {
		t.Errorf("empty status = %s", s)
	}
}

func TestAssembleSkipsAbsentFeatures(t *testing.T) {
	t.Parallel()
	sw := nxos.Assemble(t.Context(), device.Identity{}, map[string]string{"show running-config": readFixture(t, "spine.cfg")})
	if sw.Name != "DC1-SPN01" {
		t.Errorf("name = %q", sw.Name)
	}
	if s := sw.Coverage.Status(device.DimVPC); s != evidence.StatusComplete {
		t.Errorf("absent vpc = %s", s)
	}
	if s := sw.Coverage.Status(device.DimMACs); s != evidence.StatusNotRequested {
		t.Errorf("missing macs = %s", s)
	}
}

func FuzzObserve(f *testing.F) {
	for _, name := range []string{"nve_peers.txt", "nve_vni.txt", "bgp.txt", "ospf.txt", "vpc.txt", "interfaces.txt", "port_channels.txt", "macs.txt", "l2routes.txt", "macip_new.txt", "cdp.txt"} {
		f.Add(uint8(0), readFixture(f, name))
	}
	f.Fuzz(func(t *testing.T, pick uint8, text string) {
		probes := nxos.Probes()
		p := probes[int(pick)%len(probes)]
		sw := &device.Switch{}
		nxos.Observe(t.Context(), sw, p.Dimension, text)
		if !sw.Coverage.Status(p.Dimension).Valid() {
			t.Fatalf("invalid status for %s", p.Dimension)
		}
	})
}
