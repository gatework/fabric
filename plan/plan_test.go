package plan_test

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/gatework/set"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/plan"
)

// base is a VTEP with one tenant and one routed network.
func base() *device.Config {
	c := device.NewConfig()
	c.Features["bgp"] = true
	c.VRFs["PROD"] = device.VRF{VNI: 50000, RD: "auto", RouteTargets: []string{"both auto", "both auto evpn"}}
	c.VLANs[999] = device.VLAN{VNI: 50000}
	c.VLANs[100] = device.VLAN{Name: "web", VNI: 10100}
	c.VLANs[150] = device.VLAN{VNI: 10150}
	c.NVE = &device.NVE{Name: "nve1", Members: map[fabric.VNI]device.NVEMember{
		50000: {AssociateVRF: true}, 10100: {IngressBGP: true}, 10150: {IngressBGP: true},
	}}
	c.EVPN[10100] = device.EVPNVNI{RD: "auto", RouteTargets: []string{"import auto", "export auto"}}
	c.EVPN[10150] = device.EVPNVNI{RD: "auto", RouteTargets: []string{"both auto"}}
	c.Interfaces["Vlan100"] = device.Interface{Name: "Vlan100", Kind: device.KindSVI, Admin: device.AdminUp, VRF: "PROD",
		Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.100.1/24")}, Anycast: true}
	c.Interfaces["Ethernet1/1"] = device.Interface{Name: "Ethernet1/1", Kind: device.KindEthernet, Layer2: true,
		Mode: fabric.PortTrunk, TrunkAllowed: set.New(set.Range[uint16]{Min: 100, Max: 100})}
	c.BGP = &device.BGP{ASN: 65001, VRFs: map[string]device.BGPVRF{"PROD": {AdvertiseL2VPN: true}}}
	c.Management.NTP.Servers = []device.NTPServer{{Address: "192.0.2.1", VRF: "management"}}
	return c
}

func desired() *plan.Desired {
	d := plan.NewDesired()
	d.VRFs["PROD"] = device.VRF{VNI: 50000, RD: plan.RDAuto, RouteTargets: []string{plan.RTBothAuto, plan.RTBothAutoEVPN}}
	d.VLANs[999] = device.VLAN{VNI: 50000}
	d.VLANs[100] = device.VLAN{Name: "web", VNI: 10100}
	d.Members[50000] = device.NVEMember{AssociateVRF: true}
	d.Members[10100] = device.NVEMember{IngressBGP: true}
	d.EVPN[10100] = device.EVPNVNI{RD: plan.RDAuto, RouteTargets: []string{plan.RTImportAuto, plan.RTExportAuto}}
	d.SVIs[100] = plan.SVI{Admin: device.AdminUp, VRF: "PROD", Address: netip.MustParsePrefix("10.0.100.1/24"), Anycast: true}
	d.BGPVRFs["PROD"] = true
	return d
}

func refs(ops []plan.Op) string {
	var out []string
	for _, op := range ops {
		out = append(out, op.Action().String()+" "+op.Ref().String())
	}
	return strings.Join(out, ", ")
}

func TestDiffInSyncIsEmpty(t *testing.T) {
	t.Parallel()
	if ops := plan.Diff(desired(), base(), plan.Options{}); len(ops) != 0 {
		t.Fatalf("ops = %s", refs(ops))
	}
}

func TestDiffAddsNetworkInDependencyOrder(t *testing.T) {
	t.Parallel()
	d := desired()
	d.VLANs[200] = device.VLAN{VNI: 10200}
	d.Members[10200] = device.NVEMember{IngressBGP: true, SuppressARP: true}
	d.EVPN[10200] = device.EVPNVNI{RD: plan.RDAuto, RouteTargets: []string{plan.RTImportAuto, plan.RTExportAuto}}
	d.SVIs[200] = plan.SVI{Admin: device.AdminUp, VRF: "PROD", Address: netip.MustParsePrefix("10.0.200.1/24"), Anycast: true, MTU: 9216}
	d.Ports["Ethernet1/1"] = plan.PortIntent{Mode: fabric.PortTrunk, VLANs: []fabric.VLAN{200}}
	cfg := base()
	ops := plan.Diff(d, cfg, plan.Options{})
	want := "create vlan:200, create evpn:10200, create nve_member:10200, create svi:200, update port:Ethernet1/1"
	if got := refs(ops); got != want {
		t.Fatalf("ops = %s\nwant %s", got, want)
	}
	if err := plan.Converges(d, cfg, ops, plan.Options{}); err != nil {
		t.Fatal(err)
	}
	after, _ := plan.Simulate(cfg, ops)
	if got := after.Interfaces["Ethernet1/1"].TrunkAllowed.Compact(); got != "100,200" {
		t.Errorf("trunk allowed = %s", got)
	}
	if cfg.Interfaces["Ethernet1/1"].TrunkAllowed.Compact() != "100" {
		t.Error("simulation mutated the observed configuration")
	}
}

func TestDiffPruneDeletesUndeclaredInReverseOrder(t *testing.T) {
	t.Parallel()
	ops := plan.Diff(desired(), base(), plan.Options{Prune: true})
	want := "delete nve_member:10150, delete evpn:10150, delete vlan:150"
	if got := refs(ops); got != want {
		t.Fatalf("ops = %s\nwant %s", got, want)
	}
	if ops := plan.Diff(desired(), base(), plan.Options{}); len(ops) != 0 {
		t.Errorf("without prune: %s", refs(ops))
	}
}

func TestEffectiveRouteTargets(t *testing.T) {
	t.Parallel()
	d := plan.NewDesired()
	d.EVPN[10150] = device.EVPNVNI{RD: plan.RDAuto, RouteTargets: []string{plan.RTImportAuto, plan.RTExportAuto}}
	if ops := plan.Diff(d, base(), plan.Options{}); len(ops) != 0 {
		t.Errorf("both auto should satisfy import/export auto: %s", refs(ops))
	}
}

func TestChangesetChecks(t *testing.T) {
	t.Parallel()
	cfg := base()
	d := desired()
	d.Settings[device.SettingTimezone] = "UTC 0 0"
	d.NTPServers["192.0.2.2"] = device.NTPServer{Address: "192.0.2.2", VRF: "management"}
	ops := plan.Diff(d, cfg, plan.Options{})
	cs, err := plan.NewChangeset(ops, fakeRenderer{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Eligible(cfg); err != nil {
		t.Fatalf("eligible: %v", err)
	}
	after, err := plan.Simulate(cfg, ops)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Satisfied(after); err != nil {
		t.Fatalf("satisfied: %v", err)
	}
	if err := cs.Preserved(cfg, after); err != nil {
		t.Fatalf("preserved: %v", err)
	}
	if err := cs.Eligible(after); !errors.Is(err, plan.ErrStale) {
		t.Errorf("applied twice must be stale: %v", err)
	}
	collateral := after.Clone()
	collateral.VLANs[150] = device.VLAN{VNI: 99999}
	if err := cs.Preserved(cfg, collateral); !errors.Is(err, plan.ErrCollateral) {
		t.Errorf("collateral change accepted: %v", err)
	}
	script, err := cs.Render()
	if err != nil || len(script.Rollback) != len(ops) {
		t.Errorf("render: %v rollback %v", err, script.Rollback)
	}
}

func TestSafeRefusesOverlap(t *testing.T) {
	t.Parallel()
	d := desired()
	d.VLANs[300] = device.VLAN{VNI: 10300}
	d.SVIs[300] = plan.SVI{Admin: device.AdminUp, VRF: "PROD", Address: netip.MustParsePrefix("10.0.100.2/24"), Anycast: true}
	cfg := base()
	ops := plan.Diff(d, cfg, plan.Options{})
	if err := plan.Safe(cfg, ops); !errors.Is(err, plan.ErrUnsafe) {
		t.Fatalf("overlap accepted: %v", err)
	}
}

func TestMissingPrerequisites(t *testing.T) {
	t.Parallel()
	cfg := base()
	cfg.NVE = nil
	d := desired()
	ops := plan.Diff(d, cfg, plan.Options{})
	if _, err := plan.Simulate(cfg, ops); !errors.Is(err, plan.ErrPrerequisite) {
		t.Fatalf("missing nve: %v", err)
	}
}

type fakeRenderer struct{}

func (fakeRenderer) Render(ops []plan.Op) ([]string, error) {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.Action().String()+" "+op.Ref().String())
	}
	return out, nil
}
