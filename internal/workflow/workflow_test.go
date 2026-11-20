package workflow_test

import (
	"context"
	"testing"

	"github.com/gatework/netops/store"

	"github.com/gatework/fabric/internal/workflow"
)

func writeBundle(tb testing.TB, b *workflow.Bundle) string {
	tb.Helper()
	st, err := store.Open(tb.TempDir())
	if err != nil {
		tb.Fatal(err)
	}
	if err := workflow.WriteBundle(context.Background(), st, b); err != nil {
		tb.Fatal(err)
	}
	return st.Root()
}

func TestPlanRefusesIncompleteEvidence(t *testing.T) {
	t.Parallel()
	f := fleet(t)
	f.Switches[0].Config = nil
	b, err := workflow.Plan(workflow.PlanRequest{Fleet: f})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Refused) != 1 || b.Refused[0].Switch != "DC1-LEF01" {
		t.Errorf("refused = %+v", b.Refused)
	}
}

func TestPlanNeedsIntent(t *testing.T) {
	t.Parallel()
	f := fleet(t)
	f.Intent = nil
	if _, err := workflow.Plan(workflow.PlanRequest{Fleet: f}); err == nil {
		t.Fatal("planned without intent")
	}
}

func TestImportAndCheck(t *testing.T) {
	t.Parallel()
	f := fleet(t)
	in, conflicts := workflow.Import(&workflow.Fleet{Switches: f.Switches})
	if len(in.Regions) != 1 || len(conflicts) != 0 {
		t.Fatalf("import: %d regions, conflicts %+v", len(in.Regions), conflicts)
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("imported intent invalid: %v", err)
	}
	rep, err := workflow.Check(t.Context(), workflow.CheckRequest{Fleet: f, Baseline: workflow.DeriveBaseline(f)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Switches) != 2 || len(rep.Fabrics) != 1 {
		t.Errorf("report = %d switches %d fabrics", len(rep.Switches), len(rep.Fabrics))
	}
}
