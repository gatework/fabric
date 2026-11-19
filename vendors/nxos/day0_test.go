package nxos_test

import (
	"strings"
	"testing"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/plan"
	"github.com/gatework/fabric/vendors/nxos"
)

// newSwitches adds a leaf pair and a second spine to the synthetic region.
func newSwitches(t *testing.T) *fabric.Region {
	t.Helper()
	r := intentRegion(t)
	r.Switches = append(r.Switches,
		&fabric.Switch{Name: "DC1-LEF03", Mgmt: fabric.MustParseAddr("198.51.100.13"), Role: fabric.RoleLeaf,
			RouterID: fabric.MustParseAddr("10.0.0.13"), VTEP: fabric.MustParseAddr("10.0.1.13")},
		&fabric.Switch{Name: "DC1-SPN02", Mgmt: fabric.MustParseAddr("198.51.100.2"), Role: fabric.RoleSpine,
			RouterID: fabric.MustParseAddr("10.0.0.2")},
	)
	r.Links = append(r.Links, &fabric.Link{
		A: fabric.LinkEnd{Switch: "DC1-LEF03", Interface: "Ethernet1/49", Address: fabric.MustParsePrefix("10.1.0.9/30")},
		B: fabric.LinkEnd{Switch: "DC1-SPN02", Interface: "Ethernet1/3", Address: fabric.MustParsePrefix("10.1.0.10/30")},
	})
	in := &fabric.Intent{Schema: 1, Regions: []*fabric.Region{r}}
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDay0ParsesBackToDesired(t *testing.T) {
	t.Parallel()
	r := newSwitches(t)
	for _, name := range []string{"DC1-LEF03", "DC1-SPN02", "DC1-LEF02"} {
		sw, _ := r.Switch(name)
		d := plan.Compile(r, sw)
		text, err := nxos.Day0(d)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cfg, err := nxos.ParseConfig(text)
		if err != nil {
			t.Fatal(err)
		}
		if rest := plan.Diff(d, cfg, plan.Options{}); len(rest) != 0 {
			t.Errorf("%s: day-0 leaves %s\n%s", name, refs(rest), text)
		}
		if !strings.Contains(text, "hostname "+name) {
			t.Errorf("%s: no hostname", name)
		}
	}
}

func TestDay0Content(t *testing.T) {
	t.Parallel()
	r := newSwitches(t)
	leaf, _ := r.Switch("DC1-LEF03")
	text, err := nxos.Day0(plan.Compile(r, leaf))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"feature nv overlay", "nv overlay evpn", "router ospf 10\n  router-id 10.0.0.13",
		"interface Ethernet1/49\n  no switchport\n  mtu 9216\n  ip address 10.1.0.9/30\n  ip ospf network point-to-point\n  ip router ospf 10 area 0.0.0.0\n  no shutdown",
		"interface loopback1\n  ip address 10.0.1.13/32", "source-interface loopback1",
		"template peer SPINES", "neighbor 10.0.0.2\n    inherit peer SPINES", "vni 10100 l2",
		"interface Vlan100", "fabric forwarding mode anycast-gateway",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("leaf day-0 lacks %q\n%s", want, text)
		}
	}
	spine, _ := r.Switch("DC1-SPN02")
	text, err = nxos.Day0(plan.Compile(r, spine))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"neighbor 10.0.0.0/24\n    inherit peer VTEPS", "route-reflector-client"} {
		if !strings.Contains(text, want) {
			t.Errorf("spine day-0 lacks %q\n%s", want, text)
		}
	}
	if strings.Contains(text, "nve1") || strings.Contains(text, "Vlan") {
		t.Errorf("spine day-0 carries overlay services\n%s", text)
	}
}

func refs(ops []plan.Op) string {
	var out []string
	for _, op := range ops {
		out = append(out, op.Ref().String())
	}
	return strings.Join(out, ", ")
}
