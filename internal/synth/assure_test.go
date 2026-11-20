package synth_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gatework/netops/assess"

	"github.com/gatework/fabric/check"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/endpoint"
	"github.com/gatework/fabric/internal/synth"
	"github.com/gatework/fabric/status"
	"github.com/gatework/fabric/topology"
	"github.com/gatework/fabric/vendors/nxos"
)

func small() *synth.Fleet {
	return synth.Generate(synth.Spec{Regions: 1, Spines: 2, Leaves: 6, Networks: 4, LocalHosts: 5, RemoteHosts: 20})
}

func assemble(f *synth.Fleet) []*device.Switch {
	r := f.Intent.Regions[0]
	var out []*device.Switch
	for _, s := range r.Switches {
		out = append(out, nxos.Assemble(context.Background(), device.Identity{Name: s.Name, Mgmt: s.Mgmt.Addr, Region: r.Name, Role: s.Role}, f.Outputs[s.Mgmt.Addr]))
	}
	return out
}

func outputsOf(f *synth.Fleet, name string) map[string]string {
	_, s, _ := f.Intent.Locate(name)
	return f.Outputs[s.Mgmt.Addr]
}

func healthOf(t *testing.T, f *synth.Fleet) status.Region {
	t.Helper()
	r := f.Intent.Regions[0]
	switches := assemble(f)
	a, err := check.NewAssessor(assess.Policy{}, nxos.Profile{})
	if err != nil {
		t.Fatal(err)
	}
	fab := &check.Fabric{Name: r.Name, Switches: switches, Intent: r}
	rep, err := a.Run(t.Context(), []*check.Fabric{fab})
	if err != nil {
		t.Fatal(err)
	}
	return status.Build([]status.Input{{Name: r.Name, Intent: r, Switches: switches, Findings: rep.Findings()}}).Regions[0]
}

func TestStatusScores(t *testing.T) {
	t.Parallel()
	healthy := healthOf(t, small())
	if healthy.Level != status.LevelHealthy || healthy.Worst != 100 {
		t.Fatalf("healthy region = %s worst %d %+v", healthy.Level, healthy.Worst, healthy.Switches[0])
	}
	f := small()
	out := outputsOf(f, "R00-LEF003")
	lines := strings.Split(out["show nve peers"], "\n")
	out["show nve peers"] = strings.Join(append(lines[:2], lines[3:]...), "\n")
	delete(out, "show bgp l2vpn evpn summary")
	r := healthOf(t, f)
	worst := r.Switches[0]
	if worst.Name != "R00-LEF003" || worst.NVEPeers.Up != worst.NVEPeers.Total-1 || worst.Sessions.Known {
		t.Errorf("worst = %+v", worst)
	}
	if worst.Level == status.LevelHealthy || r.Level == status.LevelHealthy {
		t.Errorf("degraded leaf reads %s, region %s", worst.Level, r.Level)
	}
}

func TestLocate(t *testing.T) {
	t.Parallel()
	f := small()
	switches := assemble(f)
	x := endpoint.Build([]endpoint.Region{{Name: "region-00", Switches: switches}})
	res := x.Lookup("10.0.10.2")
	if len(res.Attached) != 1 || res.Attached[0].Switch != "R00-LEF001" || res.Attached[0].Port != "Ethernet1/1" || len(res.Remote) == 0 {
		t.Fatalf("lookup = %+v", res)
	}
	if mac := x.Lookup(string(res.MACs[0])); len(mac.Attached) != 1 {
		t.Errorf("mac lookup = %+v", mac)
	}
	if len(x.DuplicateIPs()) != 0 {
		t.Errorf("duplicates in a clean fabric: %v", x.DuplicateIPs())
	}
	out := outputsOf(f, "R00-LEF002")
	out["show l2route evpn mac-ip all"] += "100         52ff.0000.0001 10.0.10.2                            HMM    L,                 0         Local\n"
	x = endpoint.Build([]endpoint.Region{{Name: "region-00", Switches: assemble(f)}})
	if res := x.Lookup("10.0.10.2"); len(res.MACs) != 2 || len(res.Anomalies) == 0 {
		t.Errorf("duplicate not reported: %+v", res)
	}
}

func TestTopologyAudit(t *testing.T) {
	t.Parallel()
	f := small()
	r := f.Intent.Regions[0]
	a := topology.Run(r, assemble(f))
	for _, c := range a.Links {
		if c.State != topology.StateOK || len(c.Mismatch) > 0 {
			t.Fatalf("clean link %s = %s %v", c.Name, c.State, c.Mismatch)
		}
	}
	if len(a.Unmeshed) != 0 || len(a.Cells) != 12 {
		t.Fatalf("matrix: %d cells, unmeshed %v", len(a.Cells), a.Unmeshed)
	}
	leaf := outputsOf(f, "R00-LEF001")
	leaf["show cdp neighbors detail"] = strings.Replace(leaf["show cdp neighbors detail"], "Port ID (outgoing port): Ethernet1/1\n", "Port ID (outgoing port): Ethernet1/9\n", 1)
	leaf["show running-config"] = strings.Replace(leaf["show running-config"], "interface Ethernet1/50\n  no switchport\n  mtu 9216", "interface Ethernet1/50\n  no switchport\n  mtu 1500", 1)
	spine := outputsOf(f, "R00-SPN002")
	spine["show cdp neighbors detail"] = strings.Replace(spine["show cdp neighbors detail"], "Device ID:R00-LEF002(", "Device ID:R00-LEF099(", 1)
	two := outputsOf(f, "R00-LEF002")
	two["show cdp neighbors detail"] = strings.Replace(two["show cdp neighbors detail"], "Device ID:R00-SPN002", "Device ID:R00-SPN009", 1)
	states := map[string]string{}
	for _, c := range topology.Run(r, assemble(f)).Links {
		states[c.Name] = c.State.String() + strings.Join(c.Mismatch, ",")
	}
	if got := states["R00-LEF001 Ethernet1/49 ↔ R00-SPN001 Ethernet1/1"]; got != "错接" {
		t.Errorf("miscabled = %q", got)
	}
	if got := states["R00-LEF001 Ethernet1/50 ↔ R00-SPN002 Ethernet1/1"]; !strings.Contains(got, "MTU 1500/9216") {
		t.Errorf("mtu mismatch = %q", got)
	}
}
