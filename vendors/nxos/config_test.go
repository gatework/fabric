package nxos_test

import (
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/vendors/nxos"
)

func readFixture(tb testing.TB, name string) string {
	tb.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		tb.Fatal(err)
	}
	return string(b)
}

func parseLeaf(tb testing.TB) *device.Config {
	tb.Helper()
	cfg, err := nxos.ParseConfig(readFixture(tb, "leaf.cfg"))
	if err != nil {
		tb.Fatal(err)
	}
	return cfg
}

func TestParseConfigOverlay(t *testing.T) {
	t.Parallel()
	cfg := parseLeaf(t)
	if cfg.Hostname != "DC1-LEF01" || cfg.Version != "10.4(2)" {
		t.Errorf("identity = %q %q", cfg.Hostname, cfg.Version)
	}
	for _, f := range []string{"bgp", "nv overlay", "nv overlay evpn", "vn-segment-vlan-based", "vpc"} {
		if !cfg.Features[f] {
			t.Errorf("feature %q missing", f)
		}
	}
	if cfg.AnycastGatewayMAC != "0000.2222.3333" {
		t.Errorf("anycast mac = %q", cfg.AnycastGatewayMAC)
	}
	wantVLANs := map[fabric.VLAN]device.VLAN{
		1: {}, 100: {Name: "web", VNI: 10100}, 101: {VNI: 10101}, 200: {Name: "l2only", VNI: 10200}, 999: {VNI: 50000},
	}
	if d := cmp.Diff(wantVLANs, cfg.VLANs); d != "" {
		t.Errorf("vlans (-want +got):\n%s", d)
	}
	prod := cfg.VRFs["PROD"]
	if prod.VNI != 50000 || prod.RD != "auto" || !prod.HasRouteTarget("both auto evpn") {
		t.Errorf("vrf PROD = %+v", prod)
	}
	if r := cfg.VRFs["management"].Routes; len(r) != 1 || r[0].NextHop != "198.51.100.1" {
		t.Errorf("management routes = %+v", r)
	}
	nve := cfg.NVE
	if nve == nil || nve.SourceInterface != "loopback1" || !nve.HostReachabilityBGP || !nve.AdvertiseVirtualRMAC {
		t.Fatalf("nve = %+v", nve)
	}
	wantMembers := map[fabric.VNI]device.NVEMember{
		10100: {SuppressARP: true, IngressBGP: true},
		10101: {SuppressARP: true, IngressBGP: true},
		10200: {IngressBGP: true},
		50000: {AssociateVRF: true},
	}
	if d := cmp.Diff(wantMembers, nve.Members); d != "" {
		t.Errorf("members (-want +got):\n%s", d)
	}
	if e := cfg.EVPN[10200]; e.RD != "auto" || len(e.RouteTargets) != 1 || e.RouteTargets[0] != "both auto" {
		t.Errorf("evpn 10200 = %+v", e)
	}
	if a, ok := cfg.VTEPAddress(); !ok || a != netip.MustParseAddr("10.0.1.100") {
		t.Errorf("vtep address = %v %v", a, ok)
	}
	if a, ok := cfg.PIPAddress(); !ok || a != netip.MustParseAddr("10.0.1.11") {
		t.Errorf("pip address = %v %v", a, ok)
	}
}

func TestParseConfigInterfaces(t *testing.T) {
	t.Parallel()
	cfg := parseLeaf(t)
	svi := cfg.Interfaces["Vlan100"]
	if svi.Kind != device.KindSVI || svi.Admin != device.AdminUp || svi.MTU != 9216 || svi.VRF != "PROD" ||
		!svi.Anycast || svi.ACLIn != "WEB-IN" || !svi.NoRedirects {
		t.Errorf("Vlan100 = %+v", svi)
	}
	if !cfg.Interfaces["Vlan999"].IPForward {
		t.Error("Vlan999 lacks ip forward")
	}
	po := cfg.Interfaces["port-channel10"]
	if !po.Layer2 || po.Mode != fabric.PortTrunk || po.VPC != 10 || po.TrunkAllowed.Compact() != "100-101" {
		t.Errorf("port-channel10 = %+v", po)
	}
	if e := cfg.Interfaces["Ethernet1/2"]; e.AccessVLAN != 200 || e.Mode != fabric.PortAccess || !e.Carries(200) || e.Carries(100) {
		t.Errorf("Ethernet1/2 = %+v", e)
	}
	if e := cfg.Interfaces["Ethernet1/49"]; e.OSPF == nil || e.OSPF.Area != "0.0.0.0" || !e.OSPF.PointToPoint {
		t.Errorf("Ethernet1/49 ospf = %+v", e.OSPF)
	}
	if cfg.Interfaces["Ethernet1/3"].Layer2 {
		t.Error("Ethernet1/3 must be routed")
	}
	if cfg.PeerLink() != device.PeerLinkVirtual {
		t.Errorf("peer link = %v", cfg.PeerLink())
	}
}

func TestParseConfigRoutingAndManagement(t *testing.T) {
	t.Parallel()
	cfg := parseLeaf(t)
	b := cfg.BGP
	if b == nil || b.ASN != 65001 || b.RouterID != "10.0.0.11" || !b.AdvertisePIP || !b.RetainRouteTargetAll {
		t.Fatalf("bgp = %+v", b)
	}
	if b.Neighbors["10.0.0.1"].Inherit != "SPINES" || b.Templates["SPINES"].UpdateSource != "loopback0" {
		t.Errorf("bgp peers = %+v %+v", b.Neighbors, b.Templates)
	}
	if v := b.VRFs["PROD"]; !v.AdvertiseL2VPN || v.Redistribute["direct"] != "DIRECT" {
		t.Errorf("bgp vrf = %+v", v)
	}
	d := cfg.VPC
	if d == nil || d.ID != 10 || !d.PeerGateway || d.FastConvergence || d.KeepaliveDest != "198.51.100.12" {
		t.Errorf("vpc = %+v", d)
	}
	m := cfg.Management
	if m.Timezone != "UTC 0 0" || m.DomainLookup || m.VTYExecTimeout != 5 || m.VTYSessionLimit != 10 {
		t.Errorf("management = %+v", m)
	}
	if s, ok := m.NTP.Server("192.0.2.21"); !ok || !s.Prefer || s.VRF != "management" {
		t.Errorf("ntp = %+v", m.NTP)
	}
	if s, ok := m.Logging.Server("192.0.2.30"); !ok || s.Severity != 5 || s.Port != 5514 || s.Facility != "auth" {
		t.Errorf("logging = %+v", m.Logging)
	}
	if u := m.SNMP.Users; len(u) != 1 || u[0].Auth != "md5" || u[0].Priv != "aes-128" {
		t.Errorf("snmp users = %+v", u)
	}
	if cfg.Unparsed["rmon"] != 1 || cfg.Unparsed["vdc"] != 1 {
		t.Errorf("unparsed = %v", cfg.Unparsed)
	}
}

func TestParseConfigNeverStoresSecrets(t *testing.T) {
	t.Parallel()
	cfg := parseLeaf(t)
	for _, s := range []string{cfg.Management.TACACS.Servers[0].KeyDigest, cfg.Management.SNMP.Communities[0].Digest} {
		if s == "" || strings.Contains(s, "synthetic") {
			t.Errorf("secret kept or missing: %q", s)
		}
	}
}

func TestFingerprintIgnoresVolatileHeader(t *testing.T) {
	t.Parallel()
	text := readFixture(t, "leaf.cfg")
	a, err := nxos.ParseConfig(text)
	if err != nil {
		t.Fatal(err)
	}
	b, err := nxos.ParseConfig(strings.Replace(text, "10:00:01", "11:59:59", 1))
	if err != nil {
		t.Fatal(err)
	}
	c, err := nxos.ParseConfig(strings.Replace(text, "mtu 9216", "mtu 9000", 1))
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint != b.Fingerprint {
		t.Error("fingerprint changed with the !Time header")
	}
	if a.Fingerprint == c.Fingerprint {
		t.Error("fingerprint ignored a configuration change")
	}
}

func TestParseConfigRejectsInvalidUTF8(t *testing.T) {
	t.Parallel()
	if _, err := nxos.ParseConfig("hostname \xff"); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func FuzzParseConfig(f *testing.F) {
	f.Add(readFixture(f, "leaf.cfg"))
	f.Add(readFixture(f, "spine.cfg"))
	f.Add("interface nve1\n  member vni 1 associate-vrf\nvlan 1-5,x\n")
	f.Fuzz(func(t *testing.T, text string) {
		cfg, err := nxos.ParseConfig(text)
		if err != nil {
			return
		}
		_ = cfg.Segments()
		_ = cfg.Clone()
	})
}
