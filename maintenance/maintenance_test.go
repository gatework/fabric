package maintenance_test

import (
	"errors"
	"fmt"
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
