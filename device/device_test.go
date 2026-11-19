package device_test

import (
	"testing"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

func TestCanonicalInterface(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Eth1/1": "Ethernet1/1", "Po10": "port-channel10", "Lo0": "loopback0", "Vlan100": "Vlan100",
		"nve1": "nve1", "mgmt0": "mgmt0", "vPC Peer-Link": "vPC Peer-Link", "Ethernet1/1.100": "Ethernet1/1.100",
	} {
		if got, _ := device.CanonicalInterface(in); got != want {
			t.Errorf("%s = %s, want %s", in, got, want)
		}
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	t.Parallel()
	c := device.NewConfig()
	c.VPC = &device.VPCDomain{ID: 1}
	c.NVE = &device.NVE{Name: "nve1"}
	c.BGP = &device.BGP{ASN: 65001}
	for _, s := range device.Settings() {
		if !s.Valid() || !c.Applicable(s) {
			t.Fatalf("%s not applicable", s)
		}
		v := "x"
		switch {
		case s.Boolean():
			v = device.Enabled
		case s == device.SettingVTYExecTimeout || s == device.SettingVTYSessionLimit || s == device.SettingConsoleExecTimeout:
			v = "7"
		}
		c.Set(s, v)
		if got := c.Get(s); got != v {
			t.Errorf("%s = %q, want %q", s, got, v)
		}
		c.Set(s, "")
		if got := c.Get(s); got != "" {
			t.Errorf("%s not cleared: %q", s, got)
		}
	}
	bare := device.NewConfig()
	if bare.Applicable(device.SettingVPCPeerGateway) || bare.Get(device.SettingBGPAdvertisePIP) != "" {
		t.Error("settings on absent objects must not apply")
	}
	bare.Set(device.SettingVPCPeerGateway, device.Enabled)
	if device.Setting("nope").Valid() {
		t.Error("unknown setting valid")
	}
}

func TestSegmentGaps(t *testing.T) {
	t.Parallel()
	c := device.NewConfig()
	c.VLANs[10] = device.VLAN{VNI: 1010}
	c.VLANs[20] = device.VLAN{VNI: 1020}
	c.VRFs["T"] = device.VRF{VNI: 5000}
	c.NVE = &device.NVE{Members: map[fabric.VNI]device.NVEMember{1010: {}, 5000: {}, 1030: {}}}
	c.EVPN[1010] = device.EVPNVNI{}
	gaps := map[fabric.VNI][]string{}
	for _, s := range c.Segments() {
		gaps[s.VNI] = s.Gaps()
	}
	if len(gaps[1010]) != 0 {
		t.Errorf("1010 = %v", gaps[1010])
	}
	if g := gaps[1020]; len(g) != 2 {
		t.Errorf("1020 = %v", g)
	}
	if g := gaps[5000]; len(g) != 3 {
		t.Errorf("5000 = %v", g)
	}
	if g := gaps[1030]; len(g) != 2 || g[0] != "vlan" {
		t.Errorf("1030 = %v", g)
	}
}
