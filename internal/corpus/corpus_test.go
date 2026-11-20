package corpus_test

import (
	"fmt"
	"maps"
	"net/netip"
	"os"
	"slices"
	"testing"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/governance"
	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/plan"
	"github.com/gatework/fabric/provision"
	"github.com/gatework/fabric/status"
	"github.com/gatework/fabric/vendors/nxos"
)

func corpus(t *testing.T) *workflow.Fleet {
	t.Helper()
	dir := os.Getenv("FABRIC_CORPUS")
	if dir == "" {
		t.Skip("FABRIC_CORPUS not set")
	}
	f, err := workflow.LoadSnapshot(t.Context(), dir, nil, workflow.LoadOptions{SubnetBits: 24})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestCorpusParse requires every dimension of every switch to parse
// completely.
func TestCorpusParse(t *testing.T) {
	f := corpus(t)
	status := map[evidence.Status]int{}
	unparsed := 0
	for _, sw := range f.Switches {
		for _, d := range sw.Coverage.Dimensions() {
			s := sw.Coverage.Status(d)
			status[s]++
			if !s.Complete() && s != evidence.StatusNotRequested {
				t.Errorf("a switch has %s %s", d, s)
			}
		}
		for _, n := range sw.Config.Unparsed {
			unparsed += n
		}
	}
	t.Logf("switches %d, dimensions %v, unmodeled top-level lines %d", len(f.Switches), status, unparsed)
	rows := map[string]int{}
	for _, sw := range f.Switches {
		st := sw.State
		rows["macs"] += len(st.MACs)
		rows["l2routes"] += len(st.L2Routes)
		rows["mac_ips"] += len(st.MACIPs)
		rows["nve_peers"] += len(st.NVEPeers)
		rows["bgp"] += len(st.BGP.Neighbors)
		for _, m := range st.MACs {
			if m.RemoteVTEP != "" {
				rows["macs_remote"]++
			}
		}
		for _, r := range st.L2Routes {
			rows["l2route_hops"] += len(r.NextHops)
		}
	}
	t.Logf("rows %v", rows)
}

// TestCorpusAdoptPlanLoop adopts every region, then plans the draft: every
// plan must converge in simulation, be safe, render, and parse back.
func TestCorpusAdoptPlanLoop(t *testing.T) {
	f := corpus(t)
	in, conflicts := workflow.Import(f)
	if err := in.Validate(); err != nil {
		t.Fatalf("adopted intent invalid: %v", err)
	}
	f.Intent = in
	for _, sw := range f.Switches {
		if r, s, ok := in.Locate(sw.Name); ok {
			sw.Region, sw.Role = r.Name, s.Role
		}
	}
	links, managed := 0, 0
	for _, r := range in.Regions {
		links += len(r.Links)
		if r.Underlay != nil {
			managed++
		}
	}
	t.Logf("regions with a managed underlay %d of %d, fabric links %d", managed, len(in.Regions), links)
	if managed != len(in.Regions) {
		t.Error("some region's underlay could not be adopted")
	}
	kinds := map[string]int{}
	noop := 0
	for _, sw := range f.Switches {
		r, s, _ := in.Locate(sw.Name)
		d := plan.Compile(r, s)
		ops := plan.Diff(d, sw.Config, plan.Options{})
		if len(ops) == 0 {
			noop++
			continue
		}
		if err := plan.Converges(d, sw.Config, ops, plan.Options{}); err != nil {
			t.Errorf("a plan does not converge: %v", err)
		}
		if err := plan.Safe(sw.Config, ops); err != nil {
			t.Errorf("a plan is unsafe: %v", err)
		}
		if _, err := (nxos.Renderer{}).Render(ops); err != nil {
			t.Errorf("a plan does not render: %v", err)
		}
		for _, op := range ops {
			kinds[op.Action().String()+" "+string(op.Ref().Kind)]++
			// Adopting an underlay must never rewrite it.
			if slices.Contains(underlayKinds, op.Ref().Kind) {
				t.Errorf("adopted underlay plans %s %s", op.Action(), op.Ref().Kind)
			}
		}
	}
	t.Logf("regions %d, conflicts %d, switches already conforming %d of %d", len(in.Regions), len(conflicts), noop, len(f.Switches))
	for _, k := range slices.Sorted(maps.Keys(kinds)) {
		t.Logf("  %4d %s", kinds[k], k)
	}
}

var underlayKinds = []plan.Kind{plan.KindInterface, plan.KindOSPF, plan.KindBGPTemplate, plan.KindBGPNeighbor, plan.KindNVE, plan.KindVPCDomain}

// TestCorpusBaselineLoop derives a baseline, plans its remediation, and
// requires the simulated result to satisfy the baseline.
func TestCorpusBaselineLoop(t *testing.T) {
	f := corpus(t)
	in, _ := workflow.Import(f)
	f.Intent = in
	prof := workflow.DeriveBaseline(f)
	b, err := workflow.Plan(workflow.PlanRequest{Fleet: f, Baseline: prof})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range b.Refused {
		t.Errorf("refused: %s", r.Reason)
	}
	if err := checkRollout(b); err != nil {
		t.Error(err)
	}
	t.Logf("baseline remediation: %d switches change, %d conform, %d waves", len(b.Items), b.Unchanged, len(b.Waves))
}

func checkRollout(b *workflow.Bundle) error {
	seen := map[string]bool{}
	for _, w := range b.Waves {
		for _, m := range w.Switches {
			seen[m.Name] = true
		}
	}
	if len(seen) != len(b.Items) {
		return fmt.Errorf("waves cover %d of %d planned switches", len(seen), len(b.Items))
	}
	return nil
}

// TestCorpusCheck runs every rule; none may be skipped for lack of
// evidence or fail.
func TestCorpusCheck(t *testing.T) {
	f := corpus(t)
	in, _ := workflow.Import(f)
	f.Intent = in
	rep, err := workflow.Check(t.Context(), workflow.CheckRequest{Fleet: f, Baseline: workflow.DeriveBaseline(f)})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Incomplete() {
		t.Error("some rules were skipped or failed")
	}
	counts := map[assess.RuleID]int{}
	for _, fd := range rep.Findings() {
		counts[fd.Rule]++
	}
	for _, k := range slices.Sorted(maps.Keys(counts)) {
		t.Logf("  %4d %s", counts[k], k)
	}
}

// TestCorpusProvisioning simulates day-2 work on every adopted region: a
// new distributed gateway and a new leaf dual-homed to both spines on
// free spine ports. Every resulting plan must converge and be safe, and
// the new leaf's day-0 configuration must parse back to its desired state.
func TestCorpusProvisioning(t *testing.T) {
	f := corpus(t)
	in, _ := workflow.Import(f)
	f.Intent = in
	for _, sw := range f.Switches {
		if r, s, ok := in.Locate(sw.Name); ok {
			sw.Region, sw.Role = r.Name, s.Role
		}
	}
	obs := provision.NewObservation(f.Switches)
	planned := 0
	for i, r := range in.Regions {
		tenant := r.Tenants[0].Name
		gw := provision.AddNetwork{Region: r.Name, Name: "corpus-test", Tenant: tenant,
			Gateway: netip.MustParsePrefix(fmt.Sprintf("198.18.%d.1/24", i))}
		if r.Numbering.L2VNIBase == 0 {
			gw.VNI = fabric.VNI(7_000_000 + i)
		}
		p, err := workflow.Provision(workflow.ProvisionRequest{Intent: in, Request: gw, Fleet: f})
		if err != nil {
			t.Errorf("region %d gateway: %v", i, err)
			continue
		}
		planned += checkBundle(t, p)
		if p.Bundle.Risk.Class != governance.ClassStandard || p.Bundle.Risk.Endpoints != 0 {
			t.Errorf("region %d: a new gateway is %s with %d endpoints", i, p.Bundle.Risk.Class, p.Bundle.Risk.Endpoints)
		}
		vlan, vni := allocatedValue(p.Result, "vlan"), allocatedValue(p.Result, "vni")
		if gw.VNI != 0 {
			vni = gw.VNI.String()
		}
		for _, it := range p.Bundle.Items {
			for _, op := range it.Ops {
				// Only the new network's objects, plus overlay features or
				// the anycast MAC on a VTEP that had no network before.
				if k := op.Ref().Key; k != vlan && k != vni && op.Ref().Kind != plan.KindFeature && op.Ref().Kind != plan.KindSetting {
					t.Errorf("gateway plan carries unrelated %s", op.Ref().Kind)
				}
			}
		}
		var uplinks []provision.Uplink
		for j, sp := range r.ByRole(fabric.RoleSpine) {
			free, err := provision.Free(in, obs, sp.Name)
			if err != nil || len(free) == 0 {
				t.Fatalf("region %d spine has no free port: %v", i, err)
			}
			uplinks = append(uplinks, provision.Uplink{Local: fmt.Sprintf("Ethernet1/%d", 49+j), Remote: sp.Name, RemotePort: free[0].Interface})
		}
		leaf := provision.AddSwitch{Region: r.Name, Name: fmt.Sprintf("CORPUS-LEF%02d", i), Role: fabric.RoleLeaf,
			Mgmt: netip.MustParseAddr(fmt.Sprintf("198.19.%d.1", i)), Uplinks: uplinks}
		p, err = workflow.Provision(workflow.ProvisionRequest{Intent: in, Request: leaf, Fleet: f})
		if err != nil {
			t.Errorf("region %d leaf: %v", i, err)
			continue
		}
		planned += checkBundle(t, p)
		if p.Bundle.Risk.Class != governance.ClassMajor {
			t.Errorf("region %d: a new leaf (spine underlay change) is %s", i, p.Bundle.Risk.Class)
		}
		nr, ns, _ := p.Intent.Locate(leaf.Name)
		d := plan.Compile(nr, ns)
		text, err := nxos.Day0(d)
		if err != nil {
			t.Errorf("region %d day-0: %v", i, err)
			continue
		}
		cfg, _ := nxos.ParseConfig(t.Context(), text)
		if rest := plan.Diff(d, cfg, plan.Options{}); len(rest) != 0 {
			t.Errorf("region %d day-0 leaves %d operations", i, len(rest))
		}
	}
	t.Logf("provisioning simulations planned %d switch changes", planned)
}

func allocatedValue(r provision.Result, what string) string {
	for _, a := range r.Allocated {
		if a.What == what {
			return a.Value
		}
	}
	return ""
}

func checkBundle(t *testing.T, p *workflow.Provisioned) int {
	t.Helper()
	if p.Bundle == nil {
		return 0
	}
	for _, r := range p.Bundle.Refused {
		t.Errorf("refused: %s", r.Reason)
	}
	return len(p.Bundle.Items)
}

func BenchmarkCorpusLoad(b *testing.B) {
	dir := os.Getenv("FABRIC_CORPUS")
	if dir == "" {
		b.Skip("FABRIC_CORPUS not set")
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := workflow.LoadSnapshot(b.Context(), dir, nil, workflow.LoadOptions{SubnetBits: 24}); err != nil {
			b.Fatal(err)
		}
	}
}

// TestCorpusAssurance computes health and cabling for every region; with
// complete evidence nothing may be unknown.
func TestCorpusAssurance(t *testing.T) {
	f := corpus(t)
	in, _ := workflow.Import(f)
	f.Intent = in
	for _, sw := range f.Switches {
		if r, s, ok := in.Locate(sw.Name); ok {
			sw.Region, sw.Role = r.Name, s.Role
		}
	}
	rep, _, err := workflow.Status(t.Context(), workflow.CheckRequest{Fleet: f})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rep.Regions {
		if r.Level == status.LevelUnknown || r.Levels["未知"] > 0 {
			t.Errorf("region %d has unknown health", i)
		}
		t.Logf("region %d: %s worst %d mean %d levels %v", i, r.Level, r.Worst, r.Mean, r.Levels)
	}
	states := map[string]int{}
	for _, a := range workflow.Topology(f, "") {
		for _, c := range a.Links {
			states[c.State.String()]++
			if len(c.Mismatch) > 0 {
				states["配置不一致"]++
			}
		}
		states["无互联"] += len(a.Unmeshed)
	}
	t.Logf("cabling %v", states)
	macs, ips := workflow.Endpoints(f).Count()
	t.Logf("endpoints indexed: %d MACs, %d IPs", macs, ips)
}
