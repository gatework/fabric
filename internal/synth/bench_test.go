package synth_test

import (
	"sync"
	"testing"

	"github.com/gatework/fabric/internal/synth"
	"github.com/gatework/fabric/internal/workflow"
)

var (
	thousandOnce sync.Once
	thousand     *synth.Fleet
	thousandDir  string
	errThousand  error
)

// fleet1000 generates and writes the 1000-switch snapshot once per run.
func fleet1000(b *testing.B) (*synth.Fleet, string) {
	b.Helper()
	thousandOnce.Do(func() {
		thousand = synth.Generate(synth.Thousand())
		thousandDir, errThousand = mkdirTemp()
		if errThousand == nil {
			errThousand = thousand.Write(thousandDir)
		}
	})
	if errThousand != nil {
		b.Fatal(errThousand)
	}
	return thousand, thousandDir
}

func BenchmarkLoadSnapshot1000(b *testing.B) {
	f, dir := fleet1000(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := workflow.LoadSnapshot(b.Context(), dir, f.Intent, workflow.LoadOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadConfig1000(b *testing.B) {
	f, dir := fleet1000(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := workflow.LoadSnapshot(b.Context(), dir, f.Intent, workflow.LoadOptions{Dimensions: workflow.ConfigOnly()}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCheck1000(b *testing.B) {
	f, dir := fleet1000(b)
	fleet, err := workflow.LoadSnapshot(b.Context(), dir, f.Intent, workflow.LoadOptions{})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := workflow.Check(b.Context(), workflow.CheckRequest{Fleet: fleet}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPlan1000(b *testing.B) {
	f, dir := fleet1000(b)
	fleet, err := workflow.LoadSnapshot(b.Context(), dir, f.Intent, workflow.LoadOptions{})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := workflow.Plan(workflow.PlanRequest{Fleet: fleet}); err != nil {
			b.Fatal(err)
		}
	}
}
