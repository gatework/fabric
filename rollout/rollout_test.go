package rollout_test

import (
	"slices"
	"testing"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/rollout"
)

func members() []rollout.Member {
	return []rollout.Member{
		{Name: "a-spn1", Region: "a", Role: fabric.RoleSpine},
		{Name: "a-bdr1", Region: "a", Role: fabric.RoleBorder},
		{Name: "a-lef1", Region: "a", Role: fabric.RoleLeaf, VPCPeer: "a-lef2"},
		{Name: "a-lef2", Region: "a", Role: fabric.RoleLeaf, VPCPeer: "a-lef1"},
		{Name: "a-lef3", Region: "a", Role: fabric.RoleLeaf},
		{Name: "a-lef4", Region: "a", Role: fabric.RoleLeaf, VPCPeer: "a-lef5"},
		{Name: "a-lef5", Region: "a", Role: fabric.RoleLeaf, VPCPeer: "a-lef4"},
		{Name: "b-lef1", Region: "b", Role: fabric.RoleLeaf},
	}
}

func TestSchedule(t *testing.T) {
	t.Parallel()
	waves := rollout.Schedule(members(), rollout.Strategy{WaveSize: 2})
	if err := rollout.Validate(waves); err != nil {
		t.Fatal(err)
	}
	var got [][]string
	for _, w := range waves {
		var names []string
		for _, m := range w.Switches {
			names = append(names, m.Name)
		}
		got = append(got, names)
	}
	want := [][]string{{"a-lef3"}, {"a-lef1", "a-lef4"}, {"a-lef2", "a-lef5"}, {"b-lef1"}, {"a-bdr1"}, {"a-spn1"}}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("waves = %v\nwant %v", got, want)
	}
}

func TestValidateRejectsPeersTogether(t *testing.T) {
	t.Parallel()
	ms := members()
	w := []rollout.Wave{{Index: 1, Switches: []rollout.Member{ms[2], ms[3]}}}
	if rollout.Validate(w) == nil {
		t.Fatal("vpc peers in one wave accepted")
	}
}

func TestGate(t *testing.T) {
	t.Parallel()
	before := device.State{
		NVEPeers: []device.NVEPeer{{Peer: "10.0.1.1", Up: true}, {Peer: "10.0.1.2", Up: true}},
		BGP:      device.BGPSummary{Neighbors: []device.BGPNeighbor{{Address: "10.0.0.1", Established: true}}},
		VPC:      &device.VPCStatus{PeerOK: true, KeepaliveOK: true},
	}
	after := before
	after.NVEPeers = before.NVEPeers[:1]
	after.VPC = &device.VPCStatus{PeerOK: false, KeepaliveOK: true}
	lost := rollout.Gate(rollout.HealthOf(before), rollout.HealthOf(after))
	if !slices.Equal(lost, []string{"nve peer 10.0.1.2 lost", "vpc peer lost"}) {
		t.Errorf("lost = %v", lost)
	}
	if got := rollout.Gate(rollout.HealthOf(before), rollout.HealthOf(before)); len(got) != 0 {
		t.Errorf("healthy gate = %v", got)
	}
}
