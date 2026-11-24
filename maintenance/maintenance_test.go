package maintenance_test

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"testing"

	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/maintenance"
	"github.com/gatework/fabric/topology"
)

// pair builds a region of two spines and the vPC pair L1/L2. L1 has lost
// its uplink and its EVPN session to SPN2; it learns two MACs on vPC 10
// (port-channel10) and one on the orphan port Ethernet1/5.
func pair(keepalive bool) (*fabric.Region, topology.Audit, []*device.Switch) {
	r := &fabric.Region{Name: "r", Switches: []*fabric.Switch{
		{Name: "SPN1", Role: fabric.RoleSpine}, {Name: "SPN2", Role: fabric.RoleSpine},
		{Name: "L1", Role: fabric.RoleLeaf}, {Name: "L2", Role: fabric.RoleLeaf},
	}}
	ok, bad := topology.VerdictOK, topology.VerdictFail
	a := topology.Audit{
		Region: "r", Spines: []string{"SPN1", "SPN2"}, VTEPs: []string{"L1", "L2"},
		Uplinks: []topology.Uplinks{
			{Switch: "L1", Declared: 2, Healthy: 1, Spines: []string{"SPN1"}, Lost: []string{"SPN2"}, Redundancy: topology.RedundancySinglePath,
				BGP: []topology.Session{{Spine: "SPN1", Verdict: ok}, {Spine: "SPN2", Verdict: bad}}},
			{Switch: "L2", Declared: 2, Healthy: 2, Spines: []string{"SPN1", "SPN2"}, Redundancy: topology.RedundancyFull,
				BGP: []topology.Session{{Spine: "SPN1", Verdict: ok}, {Spine: "SPN2", Verdict: ok}}},
		},
		VPCPairs: []topology.VPCPair{{Domain: 1, Switches: [2]string{"L1", "L2"}}},
	}
	full := func(sw *device.Switch, dims ...evidence.Dimension) {
		for _, d := range dims {
			sw.Coverage = sw.Coverage.With(d, evidence.StatusComplete)
		}
	}
	l1 := &device.Switch{Identity: device.Identity{Name: "L1", Role: fabric.RoleLeaf}}
	l1.Config = &device.Config{Interfaces: map[string]device.Interface{
		"port-channel10": {Name: "port-channel10", Kind: device.KindPortChannel, VPC: 10, Description: "DB-01"},
		"Ethernet1/5":    {Name: "Ethernet1/5", Kind: device.KindEthernet, Description: "LAB"},
	}}
	l1.State.PortChannels = []device.PortChannel{{Group: 10, Name: "Po10", Up: true, Members: []device.PortChannelMember{{Port: "Eth1/1", Bundled: true}}}}
	l1.State.MACs = []device.MACEntry{
		{VLAN: 100, MAC: "5254.0000.0001", Port: "Eth1/1"},
		{VLAN: 100, MAC: "5254.0000.0002", Port: "Po10"},
		{VLAN: 200, MAC: "5254.0000.0003", Port: "Eth1/5"},
		{VLAN: 100, MAC: "5254.0000.0009", Port: "nve1(10.0.0.2)", RemoteVTEP: "10.0.0.2"},
	}
	l1.State.MACIPs = []device.MACIPRoute{{MAC: "5254.0000.0003", IP: "10.2.0.3", NextHops: []string{"Local"}}}
	full(l1, device.DimConfig, device.DimMACs, device.DimPortChannels)
	l2 := &device.Switch{Identity: device.Identity{Name: "L2", Role: fabric.RoleLeaf}}
	l2.State.VPC = &device.VPCStatus{PeerOK: true, KeepaliveOK: keepalive, KeepaliveStatus: "peer is not reachable", ConsistencyOK: true,
		PerVLANConsistent: true, Type2Consistent: true, Members: []device.VPCMember{{ID: 10, Up: true}}}
	full(l2, device.DimConfig, device.DimVPC)
	return r, a, []*device.Switch{l1, l2}
}

func TestSpineThatIsTheLastPathIsBlocked(t *testing.T) {
	t.Parallel()
	r, a, sws := pair(true)
	rep, err := maintenance.Assess(r, a, sws, "SPN1")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != maintenance.VerdictBlocked || !slices.Equal(rep.Isolated, []string{"L1"}) || !slices.Equal(rep.OverlayOnly, []string{"L1"}) || !slices.Equal(rep.Degraded, []string{"L2"}) {
		t.Fatalf("SPN1 = %+v", rep)
	}
	rep, _ = maintenance.Assess(r, a, sws, "SPN2")
	if rep.Verdict != maintenance.VerdictRisky || len(rep.Isolated) != 0 || !slices.Equal(rep.Degraded, []string{"L2"}) {
		t.Fatalf("SPN2 = %+v", rep)
	}
}

func TestLeafSplitsSingleAndDualHomedEndpoints(t *testing.T) {
	t.Parallel()
	r, a, sws := pair(true)
	rep, err := maintenance.Assess(r, a, sws, "L1")
	if err != nil {
		t.Fatal(err)
	}
	single, dual := rep.Endpoints()
	if rep.Verdict != maintenance.VerdictRisky || single != 1 || dual != 2 || rep.Peer != "L2" {
		t.Fatalf("L1 = %+v", rep)
	}
	if p := rep.SingleHomed[0]; p.Name != "Ethernet1/5" || p.Description != "LAB" || !slices.Equal(p.IPs, []string{"10.2.0.3"}) {
		t.Errorf("single-homed = %+v", p)
	}
	if p := rep.DualHomed[0]; p.Name != "port-channel10" || p.VPC != 10 {
		t.Errorf("dual-homed = %+v", p)
	}
}

func TestLeafWhosePeerCannotCarryIsBlocked(t *testing.T) {
	t.Parallel()
	r, a, sws := pair(false)
	rep, _ := maintenance.Assess(r, a, sws, "L1")
	if rep.Verdict != maintenance.VerdictBlocked || len(rep.PeerIssues) == 0 {
		t.Fatalf("L1 with a broken peer = %+v", rep)
	}
}

func TestMissingEvidenceIsNotReady(t *testing.T) {
	t.Parallel()
	r, a, sws := pair(true)
	sws[0].Coverage = evidence.Coverage{}
	rep, _ := maintenance.Assess(r, a, sws, "L1")
	if rep.Verdict != maintenance.VerdictUnknown || len(rep.Missing) == 0 {
		t.Fatalf("L1 without evidence = %+v", rep)
	}
	if _, err := maintenance.Assess(r, a, sws, "NOPE"); !errors.Is(err, maintenance.ErrUnknownSwitch) {
		t.Errorf("unknown switch: %v", err)
	}
}

func ExampleAssess() {
	r, a, sws := pair(true)
	rep, _ := maintenance.Assess(r, a, sws, "SPN1")
	fmt.Println(rep.Verdict, rep.Isolated)
	// Output: 不可维护 [L1]
}

// borders builds two borders with the same VRF default route. B1 peers
// with the core over Ethernet1/54. B2 has, as configured by kind, its own
// core adjacency, an external link up without adjacency, or no external
// link at all (its only peer outside the underlay is B1).
func borders(kind string) (*fabric.Region, topology.Audit, []*device.Switch) {
	r := &fabric.Region{Name: "r", Underlay: &fabric.Underlay{OSPFProcess: "10"}, Switches: []*fabric.Switch{
		{Name: "B1", Role: fabric.RoleBorder, RouterID: fabric.Addr{Addr: netip.MustParseAddr("10.0.0.1")}},
		{Name: "B2", Role: fabric.RoleBorder, RouterID: fabric.Addr{Addr: netip.MustParseAddr("10.0.0.2")}},
	}}
	route := map[string]device.VRF{"VXLAN": {VNI: 20000, Routes: []device.StaticRoute{{Prefix: netip.MustParsePrefix("0.0.0.0/0"), NextHop: "192.0.2.1", NextHopVRF: "default"}}}}
	mk := func(name string, neighbors []device.OSPFNeighbor, ifs map[string]device.Interface, ports []device.InterfaceStatus) *device.Switch {
		sw := &device.Switch{Identity: device.Identity{Name: name, Role: fabric.RoleBorder}}
		sw.Config = &device.Config{VRFs: route, Interfaces: ifs}
		sw.State.OSPF, sw.State.Interfaces = neighbors, ports
		for _, d := range []evidence.Dimension{device.DimConfig, device.DimOSPF, device.DimInterfaces, device.DimMACs} {
			sw.Coverage = sw.Coverage.With(d, evidence.StatusComplete)
		}
		return sw
	}
	core := device.OSPFNeighbor{Process: "1", RouterID: "198.51.100.1", Interface: "Eth1/54", Full: true}
	b1 := mk("B1", []device.OSPFNeighbor{core, {Process: "1", RouterID: "10.0.0.2", Interface: "Po10", Full: true}}, nil, nil)
	toB1 := device.OSPFNeighbor{Process: "1", RouterID: "10.0.0.1", Interface: "Po10", Full: true}
	var b2 *device.Switch
	switch kind {
	case "own":
		b2 = mk("B2", []device.OSPFNeighbor{toB1, core}, nil, nil)
	case "standby":
		ifs := map[string]device.Interface{"Ethernet1/54": {Name: "Ethernet1/54", Kind: device.KindEthernet,
			Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.6/30")}, OSPF: &device.InterfaceOSPF{Process: "1", Area: "0"}}}
		b2 = mk("B2", []device.OSPFNeighbor{toB1}, ifs, []device.InterfaceStatus{{Name: "Eth1/54", Up: true, Status: "up"}})
	default:
		b2 = mk("B2", []device.OSPFNeighbor{toB1}, nil, nil)
	}
	return r, topology.Audit{Region: "r"}, []*device.Switch{b1, b2}
}

func TestBorderExitBackups(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind string
		want maintenance.Verdict
	}{{"own", maintenance.VerdictRisky}, {"standby", maintenance.VerdictRisky}, {"through", maintenance.VerdictBlocked}} {
		r, a, sws := borders(tc.kind)
		rep, err := maintenance.Assess(r, a, sws, "B1")
		if err != nil {
			t.Fatal(err)
		}
		if rep.Verdict != tc.want || len(rep.Exits) != 1 {
			t.Errorf("%s: verdict %s, exits %+v, blockers %v, risks %v", tc.kind, rep.Verdict, rep.Exits, rep.Blockers, rep.Risks)
			continue
		}
		e := rep.Exits[0]
		switch tc.kind {
		case "own":
			if !slices.Equal(e.Also, []string{"B2"}) {
				t.Errorf("own: %+v", e)
			}
		case "standby":
			if !slices.Equal(e.Standby, []string{"B2"}) {
				t.Errorf("standby: %+v", e)
			}
		default:
			if !slices.Equal(e.Through, []string{"B2"}) {
				t.Errorf("through: %+v", e)
			}
		}
	}
}
