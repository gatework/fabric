package corpus_test

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"testing"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/go-netops/evidence"

	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/plan"
	"github.com/gatework/fabric/vendors/nxos"
)

func corpus(t *testing.T) *workflow.Fleet {
	t.Helper()
	dir := os.Getenv("FABRIC_CORPUS")
	if dir == "" {
		t.Skip("FABRIC_CORPUS not set")
	}
	f, err := workflow.LoadSnapshot(t.Context(), dir, nil, 24)
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
		for d, s := range sw.Coverage {
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
