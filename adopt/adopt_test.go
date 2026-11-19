package adopt_test

import (
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/adopt"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/plan"
	"github.com/gatework/fabric/vendors/nxos"
)

func sw(tb testing.TB, name, mgmt string, edit func(string) string) *device.Switch {
	tb.Helper()
	b, err := os.ReadFile("../vendors/nxos/testdata/leaf.cfg")
	if err != nil {
		tb.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "DC1-LEF01", name)
	text = strings.Replace(text, "peer-keepalive destination 198.51.100.12 source 198.51.100.11",
		"peer-keepalive destination "+peerOf(mgmt)+" source "+mgmt, 1)
	return nxos.Assemble(device.Identity{Mgmt: netip.MustParseAddr(mgmt)}, map[string]string{"show running-config": edit(text)})
}

func peerOf(mgmt string) string {
	if strings.HasSuffix(mgmt, ".11") {
		return "198.51.100.12"
	}
	return "198.51.100.11"
}

func keep(s string) string { return s }

func TestAdoptRoundTripPlansNothing(t *testing.T) {
	t.Parallel()
	switches := []*device.Switch{sw(t, "L1", "198.51.100.11", keep), sw(t, "L2", "198.51.100.12", keep)}
	rep := adopt.Region("dc1", switches)
	r := rep.Region
	if len(rep.Conflicts) != 0 {
		t.Fatalf("conflicts = %+v", rep.Conflicts)
	}
	l1, _ := r.Switch("L1")
	if l1.Role != fabric.RoleLeaf || l1.VPCPeer != "L2" {
		t.Errorf("L1 = %+v", l1)
	}
	if r.Fabric.ASN != 65001 || r.Fabric.AnycastGatewayMAC != "0000.2222.3333" || r.Fabric.SVIMTU != 9216 || r.Fabric.SuppressARP {
		t.Errorf("fabric = %+v", r.Fabric)
	}
	if len(r.Tenants) != 1 || r.Tenants[0].VLAN != 999 || len(r.Networks) != 3 {
		t.Fatalf("tenants %+v networks %d", r.Tenants, len(r.Networks))
	}
	in := &fabric.Intent{Schema: fabric.IntentSchema, Regions: []*fabric.Region{r}}
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, s := range switches {
		desc, _ := r.Switch(s.Name)
		if ops := plan.Diff(plan.Compile(r, desc), s.Config, plan.Options{}); len(ops) != 0 {
			t.Errorf("%s: adopted intent plans %d operations", s.Name, len(ops))
		}
	}
}

func TestAdoptReportsDisagreement(t *testing.T) {
	t.Parallel()
	odd := func(s string) string {
		return strings.Replace(s, "ip address 10.10.101.1/24", "ip address 10.10.101.254/24", 1)
	}
	switches := []*device.Switch{
		sw(t, "L1", "198.51.100.11", keep), sw(t, "L2", "198.51.100.12", keep), sw(t, "L3", "198.51.100.13", odd),
	}
	rep := adopt.Region("dc1", switches)
	found := false
	for _, c := range rep.Conflicts {
		if strings.Contains(c.Subject, "networks[101]") && len(c.Outliers) == 1 && c.Outliers[0] == "L3" {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflicts = %+v", rep.Conflicts)
	}
}

func TestInferRole(t *testing.T) {
	t.Parallel()
	b, _ := os.ReadFile("../vendors/nxos/testdata/spine.cfg")
	cfg, err := nxos.ParseConfig(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if r := adopt.InferRole(cfg); r != fabric.RoleSpine {
		t.Errorf("spine inferred as %s", r)
	}
}
