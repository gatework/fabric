package workflow_test

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gatework/netops/apply"
	"github.com/gatework/netops/change"
	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/governance"
	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/plan"
	"github.com/gatework/fabric/rollout"
	"github.com/gatework/fabric/vendors/nxos"
)

func read(tb testing.TB, path string) string {
	tb.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	return string(b)
}

// fleet builds both vPC leaves of the synthetic intent from one fixture.
func fleet(tb testing.TB) *workflow.Fleet {
	tb.Helper()
	in, err := fabric.DecodeIntent([]byte(read(tb, "../../testdata/intent.yaml")))
	if err != nil {
		tb.Fatal(err)
	}
	cfgText := read(tb, "../../vendors/nxos/testdata/leaf.cfg")
	f := &workflow.Fleet{Intent: in}
	r := in.Regions[0]
	for _, name := range []string{"DC1-LEF01", "DC1-LEF02"} {
		s, _ := r.Switch(name)
		sw := nxos.Assemble(tb.Context(), device.Identity{Name: s.Name, Mgmt: s.Mgmt.Addr, Region: r.Name, Role: s.Role},
			map[string]string{"show running-config": cfgText})
		f.Switches = append(f.Switches, sw)
	}
	return f
}

func bundle(tb testing.TB) *workflow.Bundle {
	tb.Helper()
	b, err := workflow.Plan(workflow.PlanRequest{Fleet: fleet(tb), Strategy: rollout.Strategy{NoCanary: true}, Now: time.Unix(0, 0)})
	if err != nil {
		tb.Fatal(err)
	}
	if len(b.Items) != 2 || len(b.Waves) != 2 {
		tb.Fatalf("bundle: %d items, %d waves", len(b.Items), len(b.Waves))
	}
	return b
}

// fakeSwitch simulates a device: writes are recorded and, once "end" is
// sent, the planned operations take effect on its configuration.
type fakeSwitch struct {
	mu       sync.Mutex
	cfg      *device.Config
	ops      []plan.Op
	written  []string
	failAt   int
	drift    bool
	losePeer bool
	changed  bool
	checkpts []string
}

func (f *fakeSwitch) Run(_ context.Context, cmds []string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for _, c := range cmds {
		if c == "show nve peers" {
			out[c] = f.state()
		}
	}
	return out, nil
}

func (f *fakeSwitch) Checkpoint(_ context.Context, name string) (*device.Config, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkpts = append(f.checkpts, name)
	return f.current(), nil
}

func (f *fakeSwitch) Rollback(context.Context, string) (*device.Config, error) { return f.cfg, nil }
func (f *fakeSwitch) Save(context.Context) error                               { return nil }

func (f *fakeSwitch) Apply(ctx context.Context, run func(context.Context, apply.Session[*device.Config]) error) (error, error) {
	return run(ctx, f), nil
}

func (f *fakeSwitch) Read(context.Context) (*device.Config, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current(), nil
}

var errLink = errors.New("link dropped")

func (f *fakeSwitch) Write(_ context.Context, c change.Command) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := c.Text
	f.written = append(f.written, cmd)
	if f.failAt > 0 && len(f.written) == f.failAt {
		return errLink
	}
	if cmd == "end" {
		after, err := plan.Simulate(f.cfg, f.ops)
		if err != nil {
			return err
		}
		after.Fingerprint = string(evidence.DigestString(strings.Join(f.written, "\n")))
		f.cfg, f.changed = after, true
	}
	return nil
}

// memJournal keeps every execution record in memory.
type memJournal struct {
	mu      sync.Mutex
	records []apply.Record
}

func (j *memJournal) Execution(string, string) apply.Journal { return j }

func (j *memJournal) Write(_ context.Context, r apply.Record) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.records = append(j.records, r)
	return nil
}

func switches(tb testing.TB, b *workflow.Bundle) map[netip.Addr]*fakeSwitch {
	tb.Helper()
	f := fleet(tb)
	out := map[netip.Addr]*fakeSwitch{}
	for _, it := range b.Items {
		sw, err := f.Switch(it.Member.Name)
		if err != nil {
			tb.Fatal(err)
		}
		out[sw.Mgmt] = &fakeSwitch{cfg: sw.Config, ops: it.Ops}
	}
	return out
}

func deploy(tb testing.TB, b *workflow.Bundle, fakes map[netip.Addr]*fakeSwitch) workflow.DeployReport {
	tb.Helper()
	return workflow.Deploy(tb.Context(), workflow.DeployRequest{
		Bundle:  b,
		Dial:    func(a netip.Addr) workflow.Switch { return fakes[a] },
		Journal: &memJournal{},
		Gate:    governance.GateOptions{AckRisk: true},
		Now:     func() time.Time { return time.Unix(0, 0) },
	})
}

func TestGateRefusalContactsNoDevice(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	b.Risk.Class = governance.ClassMajor
	fakes := switches(t, b)
	rep := workflow.Deploy(t.Context(), workflow.DeployRequest{
		Bundle: b, Dial: func(a netip.Addr) workflow.Switch { return fakes[a] }, Journal: &memJournal{},
		Now: func() time.Time { return time.Unix(0, 0) },
	})
	if rep.OK() || rep.Decision.Allowed || len(rep.Results) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	for _, f := range fakes {
		if len(f.checkpts) != 0 || len(f.written) != 0 {
			t.Error("a refused gate reached a device")
		}
	}
}

func TestDeployRecordsHealthForReview(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	rep := deploy(t, b, switches(t, b))
	for _, r := range rep.Results {
		if r.Before == nil || r.After == nil || len(r.Before.NVEPeers) != 1 {
			t.Errorf("%s health = %+v %+v", r.Switch, r.Before, r.After)
		}
	}
}

func outcomes(rep workflow.DeployReport) []workflow.Outcome {
	var out []workflow.Outcome
	for _, r := range rep.Results {
		out = append(out, r.Outcome)
	}
	return out
}

func TestDeployVerifiesEveryWave(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	fakes := switches(t, b)
	rep := deploy(t, b, fakes)
	if !rep.OK() {
		t.Fatalf("report = %+v", rep)
	}
	for _, f := range fakes {
		if len(f.checkpts) != 1 || !strings.HasPrefix(f.checkpts[0], "fabric-"+b.ID) {
			t.Errorf("checkpoints = %v", f.checkpts)
		}
		if f.written[0] != "configure terminal" {
			t.Errorf("first write = %q", f.written[0])
		}
	}
}

func TestDeployStopsOnDrift(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	fakes := switches(t, b)
	first, _ := b.Item(b.Waves[0].Switches[0].Name)
	fakes[netip.MustParseAddr(first.Mgmt)].drift = true
	rep := deploy(t, b, fakes)
	got := outcomes(rep)
	if got[0] != workflow.OutcomeDrifted || got[1] != workflow.OutcomeSkipped || rep.StoppedAt != 1 {
		t.Fatalf("outcomes = %v stopped %d", got, rep.StoppedAt)
	}
	if n := len(fakes[netip.MustParseAddr(first.Mgmt)].written); n != 0 {
		t.Errorf("drifted switch received %d writes", n)
	}
}

func TestDeployUncertainAfterPartialWrite(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	fakes := switches(t, b)
	first, _ := b.Item(b.Waves[0].Switches[0].Name)
	fakes[netip.MustParseAddr(first.Mgmt)].failAt = 3
	rep := deploy(t, b, fakes)
	got := outcomes(rep)
	if got[0] != workflow.OutcomeUncertain || got[1] != workflow.OutcomeSkipped {
		t.Fatalf("outcomes = %v", got)
	}
	if rep.Results[0].Checkpoint == "" {
		t.Error("uncertain result lost its checkpoint name")
	}
}

func TestDeployStopsOnHealthLoss(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	fakes := switches(t, b)
	first, _ := b.Item(b.Waves[0].Switches[0].Name)
	fakes[netip.MustParseAddr(first.Mgmt)].losePeer = true
	rep := deploy(t, b, fakes)
	got := outcomes(rep)
	if got[0] != workflow.OutcomeDegraded || got[1] != workflow.OutcomeSkipped {
		t.Fatalf("outcomes = %v", got)
	}
	if len(rep.Results[0].Lost) != 1 {
		t.Errorf("lost = %v", rep.Results[0].Lost)
	}
}

func TestBundleRoundTripAndTamper(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	dir := writeBundle(t, b)
	back, err := workflow.ReadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != b.ID || len(back.Items) != 2 || len(back.Items[0].Ops) == 0 {
		t.Fatalf("read back %+v", back)
	}
	path := dir + "/plans/DC1-LEF01.json"
	body := read(t, path)
	if err := os.WriteFile(path, []byte(strings.Replace(body, "\"vlan 300\"", "\"vlan 301\"", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.ReadBundle(dir); !errors.Is(err, workflow.ErrTampered) {
		t.Fatalf("tampered bundle: %v", err)
	}
}

func (f *fakeSwitch) state() string {
	peers := "Interface Peer-IP State LearnType Uptime Router-Mac\n--------- ------- -----\nnve1 10.0.1.200 Up CP 1d n/a\n"
	if f.changed && f.losePeer {
		peers = "Interface Peer-IP State LearnType Uptime Router-Mac\n--------- ------- -----\n"
	}
	return peers
}

func (f *fakeSwitch) current() *device.Config {
	c := f.cfg
	if f.drift {
		c = c.Clone()
		c.Fingerprint = "drifted"
	}
	return c
}
