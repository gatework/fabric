package synth_test

import (
	"strings"
	"testing"

	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/endpoint"
	"github.com/gatework/fabric/reach"
	"github.com/gatework/fabric/topology"
)

func fabricOf(t *testing.T, switches []*device.Switch) reach.Fabric {
	t.Helper()
	f := small()
	r := f.Intent.Regions[0]
	return reach.Fabric{
		Index:    endpoint.Build([]endpoint.Region{{Name: r.Name, Switches: switches}}),
		Switches: switches,
		Audits:   []topology.Audit{topology.Run(r, switches)},
	}
}

// hosts returns a local host of LEF001 and one of another leaf in the
// same VLAN.
func hosts(t *testing.T, x *endpoint.Index, switches []*device.Switch) (string, string) {
	t.Helper()
	var first endpoint.Location
	for _, sw := range switches {
		for _, m := range sw.State.MACs {
			if m.RemoteVTEP != "" || m.VLAN == 0 {
				continue
			}
			res := x.Lookup(string(m.MAC))
			if len(res.Attached) == 0 {
				continue
			}
			l := res.Attached[0]
			switch {
			case first.Switch == "" && l.Switch == "R00-LEF001":
				first = l
			case first.Switch != "" && l.Switch != first.Switch && l.VLAN == first.VLAN && !strings.HasPrefix(l.Switch, "R00-LEF002"):
				return string(first.MAC), string(l.MAC)
			}
		}
	}
	t.Fatal("no pair of hosts in one VLAN on two leaves")
	return "", ""
}

func TestTraceBridgedPath(t *testing.T) {
	t.Parallel()
	switches := assemble(small())
	fab := fabricOf(t, switches)
	a, b := hosts(t, fab.Index, switches)
	tr, err := fab.Trace(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Mode != reach.ModeBridged || tr.Verdict != topology.VerdictOK || len(tr.Spines) != 2 {
		t.Fatalf("trace = %+v", tr)
	}
}

func TestTraceFindsTheBreak(t *testing.T) {
	t.Parallel()
	f := small()
	switches := assemble(f)
	fab := fabricOf(t, switches)
	a, b := hosts(t, fab.Index, switches)
	tr, _ := fab.Trace(a, b)
	// The ingress VTEP loses its route to the destination.
	leaf := outputsOf(f, "R00-LEF001")
	for cmd, out := range leaf {
		var kept []string
		for line := range strings.SplitSeq(out, "\n") {
			if !strings.Contains(line, string(tr.To.MAC)) {
				kept = append(kept, line)
			}
		}
		leaf[cmd] = strings.Join(kept, "\n")
	}
	switches = assemble(f)
	tr, err := fabricOf(t, switches).Trace(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Verdict != topology.VerdictFail || tr.Break == nil || !strings.HasPrefix(tr.Break.Step, "EVPN R00-LEF001") {
		t.Fatalf("break = %+v, hops %+v", tr.Break, tr.Hops)
	}
}

func TestTraceUnknownEndpoint(t *testing.T) {
	t.Parallel()
	switches := assemble(small())
	if _, err := fabricOf(t, switches).Trace("5254.dead.beef", "10.0.0.1"); err == nil {
		t.Fatal("unknown endpoint traced")
	}
}
