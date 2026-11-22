package workflow_test

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/gatework/netops/authz"
	ndevice "github.com/gatework/netops/device"
	"github.com/gatework/netops/lease"
	filelease "github.com/gatework/netops/store/lease"

	"github.com/gatework/fabric/governance"
	"github.com/gatework/fabric/internal/workflow"
)

func TestDeployRefusesUnauthorizedSwitches(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	fakes := switches(t, b)
	p, err := authz.Decode([]byte("roles: [{name: ops, permissions: [deploy], scope: ['id=" + b.Items[0].Member.Name + "']}]\nbindings: {alice: [ops]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	rep := workflow.Deploy(t.Context(), workflow.DeployRequest{
		Bundle: b, Dial: func(a netip.Addr) workflow.Switch { return fakes[a] }, Journal: &memJournal{},
		Authorize: func(d ndevice.Device) error { return p.Authorize("alice", authz.PermDeploy, d) },
		Gate:      governance.GateOptions{AckRisk: true},
	})
	if rep.OK() || len(rep.Denied) != len(b.Items)-1 || len(rep.Results) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	for _, f := range fakes {
		if len(f.checkpts) != 0 || len(f.written) != 0 {
			t.Error("an unauthorized bundle reached a device")
		}
	}
}

func TestDeployHonorsLeasesOfOtherTools(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	fakes := switches(t, b)
	st, err := filelease.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := lease.NewManager(st, nil)
	first := workflow.ItemDevice(b.Items[0])
	other, err := m.Acquire(t.Context(), []string{lease.Key(first)}, lease.Holder{Tool: "firemon", Actor: "bob"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := workflow.DeployRequest{
		Bundle: b, Dial: func(a netip.Addr) workflow.Switch { return fakes[a] }, Journal: &memJournal{},
		Leases: m, Holder: lease.Holder{Tool: "fabric", Actor: "alice"}, Gate: governance.GateOptions{AckRisk: true},
	}
	rep := workflow.Deploy(t.Context(), req)
	if rep.OK() || rep.StoppedAt != 1 || !strings.Contains(rep.Results[0].Detail, "firemon/bob") {
		t.Fatalf("lease conflict not reported: %+v", rep.Results)
	}
	for _, f := range fakes {
		if len(f.written) != 0 {
			t.Fatal("a leased switch was written")
		}
	}
	if err := m.Release(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if rep := workflow.Deploy(t.Context(), req); !rep.OK() {
		t.Fatalf("deploy after release: %+v", rep.Results)
	}
	keys := make([]string, 0, len(b.Items))
	for _, it := range b.Items {
		keys = append(keys, lease.Key(workflow.ItemDevice(it)))
	}
	if held, _ := m.Holders(t.Context(), keys); len(held) != 0 {
		t.Errorf("leases left behind: %+v", held)
	}
}

func TestItemDeviceCarriesRoleName(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	d := workflow.ItemDevice(b.Items[0])
	if d.Role != b.Items[0].Member.Role.String() || d.Role == "" {
		t.Fatalf("role = %q, want %q", d.Role, b.Items[0].Member.Role)
	}
	p, err := authz.Decode([]byte("roles: [{name: leaf, permissions: [deploy], scope: ['role=" + d.Role + "']}]\nbindings: {alice: [leaf]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Authorize("alice", authz.PermDeploy, d); err != nil {
		t.Fatalf("role scope does not match: %v", err)
	}
}
