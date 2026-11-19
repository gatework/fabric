package governance_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/governance"
	"github.com/gatework/fabric/plan"
)

func TestAssess(t *testing.T) {
	t.Parallel()
	p := governance.DefaultPolicy()
	add := governance.Item{Switch: "L1", Region: "a", Role: fabric.RoleLeaf, Impact: plan.Impact{VLANs: []fabric.VLAN{300}}}
	if r := governance.Assess([]governance.Item{add}, p); r.Class != governance.ClassStandard {
		t.Errorf("new network = %s %v", r.Class, r.Reasons)
	}
	change := add
	change.Impact.Disruptive = []string{"vlan:100"}
	if r := governance.Assess([]governance.Item{change}, p); r.Class != governance.ClassNormal {
		t.Errorf("gateway change = %s", r.Class)
	}
	busy := change
	busy.Impact.Deletes, busy.Impact.Endpoints = 1, 3
	under := add
	under.Impact.Underlay = true
	other := add
	other.Region = "b"
	for name, items := range map[string][]governance.Item{"delete in use": {busy}, "underlay": {under}, "two regions": {add, other}} {
		if r := governance.Assess(items, p); r.Class != governance.ClassMajor {
			t.Errorf("%s = %s", name, r.Class)
		}
	}
}

func TestWindow(t *testing.T) {
	t.Parallel()
	w, err := governance.ParseWindow("22:00-06:00", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	sh, _ := time.LoadLocation("Asia/Shanghai")
	for at, want := range map[string]bool{"23:30": true, "05:59": true, "06:00": false, "12:00": false} {
		tm, _ := time.ParseInLocation("15:04", at, sh)
		if got := w.Contains(tm); got != want {
			t.Errorf("%s = %v", at, got)
		}
	}
	if _, err := governance.ParseWindow("22-6", ""); err == nil {
		t.Error("bad window accepted")
	}
}

func TestGate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	w, _ := governance.ParseWindow("22:00-06:00", "")
	rec := governance.Record{Window: w, ExpiresAt: now.Add(time.Hour)}
	major := governance.Risk{Class: governance.ClassMajor}
	_, err := governance.Gate(rec, major, governance.GateOptions{Now: now, RequireTicket: true})
	for _, want := range []error{governance.ErrOutsideWindow, governance.ErrNoTicket, governance.ErrUnacknowledged} {
		if !errors.Is(err, want) {
			t.Errorf("gate lacks %v: %v", want, err)
		}
	}
	rec.Ticket = "CHG-1"
	d, err := governance.Gate(rec, major, governance.GateOptions{Now: now, AckRisk: true, Emergency: true, Reason: "outage"})
	if err != nil || !d.Allowed || len(d.Bypassed) != 1 {
		t.Errorf("emergency = %+v %v", d, err)
	}
	if _, err := governance.Gate(rec, major, governance.GateOptions{Now: now, AckRisk: true, Emergency: true}); !errors.Is(err, governance.ErrNoReason) {
		t.Errorf("emergency without reason: %v", err)
	}
	if _, err := governance.Gate(rec, governance.Risk{}, governance.GateOptions{Now: now.Add(2 * time.Hour), Emergency: true, Reason: "x"}); !errors.Is(err, governance.ErrExpired) {
		t.Errorf("expired: %v", err)
	}
}
