package provision_test

import (
	"errors"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/plan"
	"github.com/gatework/fabric/provision"
	"github.com/gatework/fabric/vendors/nxos"
)

func setup(tb testing.TB) (*fabric.Intent, *provision.Observation) {
	tb.Helper()
	body, err := os.ReadFile("../testdata/intent.yaml")
	if err != nil {
		tb.Fatal(err)
	}
	in, err := fabric.DecodeIntent(body)
	if err != nil {
		tb.Fatal(err)
	}
	cfg, err := os.ReadFile("../vendors/nxos/testdata/leaf.cfg")
	if err != nil {
		tb.Fatal(err)
	}
	sw := nxos.Assemble(tb.Context(), device.Identity{Name: "DC1-LEF01"}, map[string]string{"show running-config": string(cfg)})
	return in, provision.NewObservation([]*device.Switch{sw})
}

func apply(tb testing.TB, in *fabric.Intent, req provision.Request, obs *provision.Observation) (*fabric.Intent, provision.Result) {
	tb.Helper()
	out, res, err := provision.Apply(in, req, obs)
	if err != nil {
		tb.Fatalf("%T: %v", req, err)
	}
	return out, res
}

func allocated(res provision.Result, what string) string {
	for _, a := range res.Allocated {
		if a.What == what {
			return a.Value
		}
	}
	return ""
}

func TestAddNetworkAllocatesAndPlans(t *testing.T) {
	t.Parallel()
	in, obs := setup(t)
	out, res := apply(t, in, provision.AddNetwork{Region: "dc1", Name: "db", Tenant: "PROD",
		Gateway: netip.MustParsePrefix("10.10.50.1/24")}, obs)
	if allocated(res, "vlan") != "2" || allocated(res, "vni") != "10002" {
		t.Errorf("allocated = %+v", res.Allocated)
	}
	if !slices.Equal(res.Affected, []string{"DC1-BDR01", "DC1-LEF01", "DC1-LEF02"}) {
		t.Errorf("affected = %v", res.Affected)
	}
	if len(in.Regions[0].Networks) == len(out.Regions[0].Networks) {
		t.Error("Apply modified or ignored the copy")
	}
	r := out.Regions[0]
	leaf, _ := r.Switch("DC1-LEF01")
	var refs []string
	for _, op := range plan.Diff(plan.Compile(r, leaf), parseLeaf(t), plan.Options{}) {
		refs = append(refs, op.Ref().String())
	}
	for _, want := range []string{"vlan:2", "evpn:10002", "nve_member:10002", "svi:2"} {
		if !slices.Contains(refs, want) {
			t.Errorf("plan lacks %s: %v", want, refs)
		}
	}
	if _, _, err := provision.Apply(out, provision.AddNetwork{Region: "dc1", Tenant: "PROD",
		Gateway: netip.MustParsePrefix("10.10.50.128/25")}, obs); !errors.Is(err, provision.ErrConflict) {
		t.Errorf("overlapping gateway: %v", err)
	}
	if _, _, err := provision.Apply(out, provision.AddNetwork{Region: "dc1", VLAN: 100}, obs); !errors.Is(err, provision.ErrConflict) {
		t.Errorf("taken vlan: %v", err)
	}
}

func parseLeaf(tb testing.TB) *device.Config {
	tb.Helper()
	b, _ := os.ReadFile("../vendors/nxos/testdata/leaf.cfg")
	cfg, err := nxos.ParseConfig(tb.Context(), string(b))
	if err != nil {
		tb.Fatal(err)
	}
	return cfg
}

func TestRemoveNetworkGuards(t *testing.T) {
	t.Parallel()
	in, obs := setup(t)
	if _, _, err := provision.Apply(in, provision.RemoveNetwork{Region: "dc1", VLAN: 300}, obs); !errors.Is(err, provision.ErrInUse) {
		t.Errorf("vlan carried by a port: %v", err)
	}
	sw := nxos.Assemble(t.Context(), device.Identity{Name: "DC1-LEF01"}, map[string]string{})
	sw.Config = parseLeaf(t)
	sw.Coverage = sw.Coverage.With(device.DimConfig, evidence.StatusComplete)
	sw.State.MACs = []device.MACEntry{{VLAN: 200, MAC: "5254.0000.0009", Type: "dynamic", Port: "Ethernet1/2"}}
	busy := provision.NewObservation([]*device.Switch{sw})
	if _, _, err := provision.Apply(in, provision.RemoveNetwork{Region: "dc1", VLAN: 200}, busy); !errors.Is(err, provision.ErrInUse) {
		t.Errorf("vlan with learned endpoints: %v", err)
	}
	if _, _, err := provision.Apply(in, provision.RemoveNetwork{Region: "dc1", VLAN: 200, Force: true}, busy); err != nil {
		t.Errorf("forced removal: %v", err)
	}
}

func TestRemoveNetworkTargetsOnlyItsVNI(t *testing.T) {
	t.Parallel()
	in, _ := setup(t)
	out, res := apply(t, in, provision.RemoveNetwork{Region: "dc1", VLAN: 200}, nil)
	if res.RemovedNetworks[200] != 10200 {
		t.Fatalf("removed = %v", res.RemovedNetworks)
	}
	r := out.Regions[0]
	leaf, _ := r.Switch("DC1-LEF01")
	d := plan.Compile(r, leaf)
	d.Remove.Networks = res.RemovedNetworks
	var refs []string
	for _, op := range plan.Diff(d, parseLeaf(t), plan.Options{}) {
		if op.Action() == plan.ActionDelete {
			refs = append(refs, op.Ref().String())
		}
	}
	slices.Sort(refs)
	if !slices.Equal(refs, []string{"evpn:10200", "nve_member:10200", "vlan:200"}) {
		t.Errorf("deletes = %v", refs)
	}
}

func TestAddTenant(t *testing.T) {
	t.Parallel()
	in, obs := setup(t)
	_, res := apply(t, in, provision.AddTenant{Region: "dc1", Name: "DMZ"}, obs)
	if allocated(res, "l3vni") != "50001" || allocated(res, "vlan") != "2" {
		t.Errorf("allocated = %+v", res.Allocated)
	}
}

func TestAddLeafPairWithUplinks(t *testing.T) {
	t.Parallel()
	in, obs := setup(t)
	out, res := apply(t, in, provision.AddSwitch{Region: "dc1", Name: "DC1-LEF03", Mgmt: netip.MustParseAddr("198.51.100.13"),
		Role: fabric.RoleLeaf, Uplinks: []provision.Uplink{{Local: "Eth1/49", Remote: "DC1-SPN01", RemotePort: "Eth1/3"}}}, obs)
	if allocated(res, "router-id") != "10.0.0.2" || allocated(res, "vtep") != "10.0.1.1" {
		t.Errorf("allocated = %+v", res.Allocated)
	}
	if !slices.Equal(res.Affected, []string{"DC1-LEF03", "DC1-SPN01"}) {
		t.Errorf("affected = %v", res.Affected)
	}
	out, res = apply(t, out, provision.AddSwitch{Region: "dc1", Name: "DC1-LEF04", Mgmt: netip.MustParseAddr("198.51.100.14"),
		Role: fabric.RoleLeaf, VPCPeer: "DC1-LEF03",
		Uplinks: []provision.Uplink{{Local: "Ethernet1/49", Remote: "DC1-SPN01", RemotePort: "Ethernet1/4"}}}, obs)
	r := out.Regions[0]
	l := r.LinksOf("DC1-LEF04")
	if len(l) != 1 || l[0].Local.Address.String() != "10.1.0.9/30" || l[0].Remote.Address.String() != "10.1.0.10/30" {
		t.Errorf("links = %+v", l)
	}
	if p, ok := r.Peer("DC1-LEF04"); !ok || p != "DC1-LEF03" || allocated(res, "vpc domain") != "1" {
		t.Errorf("pair: %v %v %+v", p, ok, res.Allocated)
	}
	if _, _, err := provision.Apply(out, provision.AddSwitch{Region: "dc1", Name: "DC1-LEF05", Mgmt: netip.MustParseAddr("198.51.100.15"),
		Role: fabric.RoleLeaf, Uplinks: []provision.Uplink{{Local: "Ethernet1/49", Remote: "DC1-SPN01", RemotePort: "Ethernet1/3"}}}, obs); !errors.Is(err, provision.ErrInUse) {
		t.Errorf("reused spine port: %v", err)
	}
	sw, _ := r.Switch("DC1-LEF04")
	text, err := nxos.Day0(plan.Compile(r, sw))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"vpc domain 1", "peer-keepalive destination 198.51.100.13 source 198.51.100.14", "ip address 10.0.1.3/32 secondary"} {
		if !strings.Contains(text, want) {
			t.Errorf("day-0 lacks %q", want)
		}
	}
}

func TestAssignVPCBundle(t *testing.T) {
	t.Parallel()
	in, obs := setup(t)
	out, res := apply(t, in, provision.AssignPort{Kind: fabric.AttachVPC, Switch: "DC1-LEF02",
		Members: map[string][]string{"DC1-LEF01": {"Eth1/2"}, "DC1-LEF02": {"Eth1/2"}}, Profile: "server-trunk", Description: "APP-07"}, obs)
	if allocated(res, "port-channel") != "1" {
		t.Errorf("allocated = %+v", res.Allocated)
	}
	r := out.Regions[0]
	var texts []string
	for _, name := range []string{"DC1-LEF01", "DC1-LEF02"} {
		sw, _ := r.Switch(name)
		ops := plan.Diff(plan.Compile(r, sw), parseLeaf(t), plan.Options{})
		var bundle []plan.Op
		for _, op := range ops {
			if op.Ref().Key == "port-channel1" {
				bundle = append(bundle, op)
			}
		}
		text, err := nxos.RenderConfig(bundle)
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, text)
	}
	if texts[0] != texts[1] || !strings.Contains(texts[0], "vpc 1") || !strings.Contains(texts[0], "description APP-07") {
		t.Errorf("vPC bundles differ or lack vpc:\n%s\n---\n%s", texts[0], texts[1])
	}
	if _, _, err := provision.Apply(out, provision.AssignPort{Kind: fabric.AttachPort, Switch: "DC1-LEF01", Interface: "Ethernet1/2", Networks: []fabric.VLAN{100}}, obs); !errors.Is(err, provision.ErrInUse) {
		t.Errorf("double assignment: %v", err)
	}
	for _, port := range []string{"Ethernet1/49", "Ethernet1/1"} {
		if _, _, err := provision.Apply(in, provision.AssignPort{Kind: fabric.AttachPort, Switch: "DC1-LEF01", Interface: port, Networks: []fabric.VLAN{100}}, obs); !errors.Is(err, provision.ErrInUse) {
			t.Errorf("%s: %v", port, err)
		}
	}
	back, rel := apply(t, out, provision.ReleasePort{Switch: "DC1-LEF02", Interface: "port-channel1"}, obs)
	if len(back.Regions[0].Attachments) != len(in.Regions[0].Attachments) {
		t.Error("release left the attachment")
	}
	if !slices.Equal(rel.Reset["DC1-LEF01"], []string{"Ethernet1/2", "port-channel1"}) || len(rel.Reset["DC1-LEF02"]) != 2 {
		t.Errorf("reset = %v", rel.Reset)
	}
}

func TestAssignExtendsNetwork(t *testing.T) {
	t.Parallel()
	in, _ := setup(t)
	out, res := apply(t, in, provision.AssignPort{Kind: fabric.AttachPort, Switch: "DC1-BDR01", Interface: "Ethernet1/5",
		Mode: fabric.PortAccess, Networks: []fabric.VLAN{200}}, nil)
	n, _ := out.Regions[0].Network(200)
	if !slices.Contains(n.Switches, "DC1-BDR01") || !strings.Contains(res.Changes[0].Detail, "扩展") {
		t.Errorf("network %+v changes %+v", n.Switches, res.Changes)
	}
}

func TestFreePorts(t *testing.T) {
	t.Parallel()
	in, obs := setup(t)
	free, err := provision.Free(in, obs, "DC1-LEF01")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range free {
		names = append(names, p.Interface)
	}
	if slices.Contains(names, "Ethernet1/49") || slices.Contains(names, "Ethernet1/1") || slices.Contains(names, "Ethernet1/3") || !slices.Contains(names, "Ethernet1/2") {
		t.Errorf("free = %v", names)
	}
}

func TestRemoveSwitch(t *testing.T) {
	t.Parallel()
	in, _ := setup(t)
	if _, _, err := provision.Apply(in, provision.RemoveSwitch{Name: "DC1-LEF01"}, nil); !errors.Is(err, provision.ErrInUse) {
		t.Errorf("switch with ports: %v", err)
	}
	out, res := apply(t, in, provision.RemoveSwitch{Name: "DC1-BDR01"}, nil)
	if _, ok := out.Regions[0].Switch("DC1-BDR01"); ok || res.Changes[len(res.Changes)-1].Subject != "switches[DC1-BDR01]" {
		t.Errorf("result = %+v", res)
	}
}

// connected observes DC1-LEF01 with Ethernet1/2 up and learning a MAC, as
// a switch with an undocumented server on an unconfigured port reports it.
func connected(t *testing.T, up bool, macs bool) *provision.Observation {
	t.Helper()
	sw := nxos.Assemble(t.Context(), device.Identity{Name: "DC1-LEF01"}, map[string]string{})
	sw.Config = parseLeaf(t)
	sw.Coverage = sw.Coverage.With(device.DimConfig, evidence.StatusComplete).
		With(device.DimInterfaces, evidence.StatusComplete).With(device.DimMACs, evidence.StatusComplete)
	status := "down"
	if up {
		status = "up"
	}
	sw.State.Interfaces = []device.InterfaceStatus{{Name: "Eth1/2", Up: up, Status: status}}
	if macs {
		sw.State.MACs = []device.MACEntry{{VLAN: 100, MAC: "5254.0000.0042", Type: "dynamic", Port: "Eth1/2"}}
	}
	return provision.NewObservation([]*device.Switch{sw})
}

func TestAssignRefusesConnectedPorts(t *testing.T) {
	t.Parallel()
	in, _ := setup(t)
	q := provision.AssignPort{Kind: fabric.AttachPort, Switch: "DC1-LEF01", Interface: "Ethernet1/2", Networks: []fabric.VLAN{100}}
	for _, tc := range []struct {
		name     string
		up, macs bool
	}{{"link up", true, false}, {"learns MACs", false, true}} {
		obs := connected(t, tc.up, tc.macs)
		if _, _, err := provision.Apply(in, q, obs); !errors.Is(err, provision.ErrActive) {
			t.Errorf("%s: %v", tc.name, err)
		}
		adopt := q
		adopt.Adopt = true
		if _, _, err := provision.Apply(in, adopt, obs); err != nil {
			t.Errorf("%s with adopt: %v", tc.name, err)
		}
	}
	if _, _, err := provision.Apply(in, q, connected(t, false, false)); err != nil {
		t.Errorf("quiet port: %v", err)
	}
}

func TestFreePortsReportConnected(t *testing.T) {
	t.Parallel()
	in, _ := setup(t)
	free, err := provision.Free(in, connected(t, true, true), "DC1-LEF01")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(free, func(p provision.FreePort) bool { return p.Interface == "Ethernet1/2" })
	if i < 0 || free[i].Status != "up" || free[i].MACs != 1 {
		t.Fatalf("free = %+v", free)
	}
}

func TestComparePortsIsNatural(t *testing.T) {
	t.Parallel()
	ports := []string{"Ethernet1/10", "Ethernet1/2", "Ethernet1/1", "port-channel10", "Ethernet1/9"}
	slices.SortFunc(ports, device.ComparePorts)
	if want := []string{"Ethernet1/1", "Ethernet1/2", "Ethernet1/9", "Ethernet1/10", "port-channel10"}; !slices.Equal(ports, want) {
		t.Errorf("sorted = %v", ports)
	}
}
