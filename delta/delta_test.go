package delta_test

import (
	"fmt"
	"testing"

	"github.com/gatework/netops/diff"
	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/delta"
	"github.com/gatework/fabric/device"
)

func observed(name string, ports map[string]bool, bgp map[string]bool, macs map[string]string) *device.Switch {
	sw := &device.Switch{Identity: device.Identity{Name: name}}
	for p, up := range ports {
		sw.State.Interfaces = append(sw.State.Interfaces, device.InterfaceStatus{Name: p, Up: up})
	}
	for a, up := range bgp {
		sw.State.BGP.Neighbors = append(sw.State.BGP.Neighbors, device.BGPNeighbor{Address: a, Established: up})
	}
	for mac, port := range macs {
		sw.State.MACs = append(sw.State.MACs, device.MACEntry{MAC: fabric.MAC(mac), Port: port})
	}
	for _, d := range []evidence.Dimension{device.DimInterfaces, device.DimBGP, device.DimMACs} {
		sw.Coverage = sw.Coverage.With(d, evidence.StatusComplete)
	}
	return sw
}

func TestCompareReportsLossesFirst(t *testing.T) {
	t.Parallel()
	before := observed("L1", map[string]bool{"Eth1/1": true, "Eth1/2": false}, map[string]bool{"10.0.0.1": true, "10.0.0.2": true},
		map[string]string{"5254.0000.0001": "Eth1/5", "5254.0000.0002": "Eth1/6"})
	after := observed("L1", map[string]bool{"Eth1/1": false, "Eth1/2": true}, map[string]bool{"10.0.0.1": true},
		map[string]string{"5254.0000.0001": "Eth1/7"})
	quiet := observed("L2", nil, nil, nil)
	rep := delta.Compare("a", "b", []delta.Pair{
		{Name: "L1", Before: before, After: after, ConfigKnown: true,
			Config: []diff.Change{{Kind: diff.KindAdded, Path: []string{"interface Ethernet1/1"}, Line: "description X"}}},
		{Name: "L2", Before: quiet, After: quiet, ConfigKnown: true},
		{Name: "L3", After: quiet},
	})
	changed := rep.Changed()
	if len(changed) != 2 || changed[0].Name != "L1" {
		t.Fatalf("changed = %+v", changed)
	}
	got := map[string]delta.Severity{}
	for _, c := range changed[0].State {
		got[c.Area+" "+c.What] = c.Severity
	}
	want := map[string]delta.Severity{
		"端口 Ethernet1/1":        delta.SeverityLoss,
		"端口 Ethernet1/2":        delta.SeverityGain,
		"BGP EVPN 10.0.0.2":     delta.SeverityLoss,
		"终端 5254.0000.0001 换端口": delta.SeverityInfo,
		"终端 1 个本地终端不再出现":        delta.SeverityLoss,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v (all %v)", k, got[k], v, got)
		}
	}
	if changed[0].Kinds() != "interface" || changed[0].Added != 1 {
		t.Errorf("config summary = %q +%d", changed[0].Kinds(), changed[0].Added)
	}
	if changed[1].Presence != "added" {
		t.Errorf("new switch = %+v", changed[1])
	}
}

func TestStateUnknownWithoutEvidence(t *testing.T) {
	t.Parallel()
	bare := &device.Switch{Identity: device.Identity{Name: "L1"}}
	rep := delta.Compare("a", "b", []delta.Pair{{Name: "L1", Before: bare, After: observed("L1", nil, nil, nil)}})
	if rep.Switches[0].StateKnown || rep.Switches[0].Changed() {
		t.Fatalf("switch without state = %+v", rep.Switches[0])
	}
}

func ExampleSwitch_Kinds() {
	rep := delta.Compare("a", "b", []delta.Pair{{Name: "L1", Before: &device.Switch{}, After: &device.Switch{}, ConfigKnown: true, Config: []diff.Change{
		{Kind: diff.KindAdded, Path: []string{"interface Ethernet1/1"}, Line: "description A"},
		{Kind: diff.KindAdded, Path: []string{"interface Ethernet1/2"}, Line: "description B"},
		{Kind: diff.KindRemoved, Line: "ntp server 10.0.0.1"},
	}}})
	fmt.Println(rep.Switches[0].Kinds())
	// Output: interface ×2，ntp
}
