package synth_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gatework/netops/assess"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/check"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/internal/synth"
	"github.com/gatework/fabric/plan"
	"github.com/gatework/fabric/vendors/nxos"
)

// TestGeneratedFabricIsHealthy proves the generator: every dimension
// parses complete, the intent plans nothing, and no rule fires except
// the ones a synthetic fabric legitimately triggers.
func TestGeneratedFabricIsHealthy(t *testing.T) {
	t.Parallel()
	f := synth.Generate(synth.Spec{Regions: 1, Spines: 2, Leaves: 6, Networks: 4, LocalHosts: 5, RemoteHosts: 20})
	if err := f.Intent.Validate(); err != nil {
		t.Fatal(err)
	}
	r := f.Intent.Regions[0]
	var switches []*device.Switch
	for _, s := range r.Switches {
		sw := nxos.Assemble(t.Context(), device.Identity{Name: s.Name, Mgmt: s.Mgmt.Addr, Region: r.Name, Role: s.Role}, f.Outputs[s.Mgmt.Addr])
		for _, d := range sw.Coverage.Dimensions() {
			st := sw.Coverage.Status(d)
			if !st.Complete() && st != "not_requested" {
				t.Fatalf("%s %s = %s %v", s.Name, d, st, sw.Issues[d])
			}
		}
		if ops := plan.Diff(plan.Compile(r, s), sw.Config, plan.Options{}); len(ops) != 0 {
			t.Errorf("%s plans %d operations, first %s", s.Name, len(ops), ops[0].Ref())
		}
		switches = append(switches, sw)
	}
	a, err := check.NewAssessor(assess.Policy{}, nxos.Profile{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := a.Run(t.Context(), []*check.Fabric{{Name: r.Name, Switches: switches, Intent: r}})
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range rep.Findings() {
		t.Errorf("finding %s: %s", fd.Rule, fd.Detail)
	}
}

// TestWriteFleet writes the 1000-switch fabric for manual runs:
// FABRIC_SYNTH_OUT=/tmp/x go test -run WriteFleet ./internal/synth
func TestWriteFleet(t *testing.T) {
	out := os.Getenv("FABRIC_SYNTH_OUT")
	if out == "" {
		t.Skip("FABRIC_SYNTH_OUT not set")
	}
	f := synth.Generate(synth.Thousand())
	if err := f.Write(filepath.Join(out, "snapshot")); err != nil {
		t.Fatal(err)
	}
	body, err := fabric.EncodeIntent(f.Intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "intent.yaml"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}
