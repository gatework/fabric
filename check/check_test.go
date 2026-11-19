package check_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gatework/go-netops/assess"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/check"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/vendors/nxos"
)

func fixture(tb testing.TB, name string) string {
	tb.Helper()
	b, err := os.ReadFile("../vendors/nxos/testdata/" + name)
	if err != nil {
		tb.Fatal(err)
	}
	return string(b)
}

func leaf(tb testing.TB, name, mgmt string, mutate func(string) string) *device.Switch {
	tb.Helper()
	outs := map[string]string{
		"show running-config":          mutate(fixture(tb, "leaf.cfg")),
		"show nve peers":               fixture(tb, "nve_peers.txt"),
		"show nve vni":                 fixture(tb, "nve_vni.txt"),
		"show bgp l2vpn evpn summary":  fixture(tb, "bgp.txt"),
		"show ip ospf neighbors":       fixture(tb, "ospf.txt"),
		"show vpc brief":               fixture(tb, "vpc.txt"),
		"show interface brief":         fixture(tb, "interfaces.txt"),
		"show port-channel summary":    fixture(tb, "port_channels.txt"),
		"show mac address-table":       fixture(tb, "macs.txt"),
		"show l2route evpn mac all":    fixture(tb, "l2routes.txt"),
		"show l2route evpn mac-ip all": fixture(tb, "macip_new.txt"),
		"show cdp neighbors detail":    fixture(tb, "cdp.txt"),
	}
	id := device.Identity{Name: name, Region: "dc1", Role: fabric.RoleLeaf}
	id.Mgmt, _ = parseAddr(mgmt)
	return nxos.Assemble(id, outs)
}

func run(tb testing.TB, f *check.Fabric) map[assess.RuleID][]string {
	tb.Helper()
	a, err := check.NewAssessor(assess.Policy{}, nxos.Profile{})
	if err != nil {
		tb.Fatal(err)
	}
	rep, err := a.Run(tb.Context(), []*check.Fabric{f})
	if err != nil {
		tb.Fatal(err)
	}
	if rep.Incomplete() {
		tb.Fatal("assessment incomplete")
	}
	out := map[assess.RuleID][]string{}
	for _, fd := range rep.Findings() {
		out[fd.Rule] = append(out[fd.Rule], fd.Subject.String())
	}
	return out
}

func TestSwitchRules(t *testing.T) {
	t.Parallel()
	sw := leaf(t, "DC1-LEF01", "198.51.100.11", func(s string) string {
		return strings.Replace(s, "  vni 10101 l2\n    rd auto\n    route-target import auto\n    route-target export auto\n", "", 1)
	})
	got := run(t, &check.Fabric{Name: "dc1", Switches: []*device.Switch{sw}})
	want := map[assess.RuleID]int{
		check.RuleSegmentIncomplete: 1, // VNI 10101 lost its EVPN instance
		check.RuleNVEPeerDown:       1,
		check.RuleNVEVNIDown:        1,
		check.RuleBGPSessionDown:    1,
		check.RuleOSPFNotFull:       1,
		check.RuleVPCUnhealthy:      1,
		check.RulePortChannelDown:   1,
		check.RuleDuplicateMAC:      1,
		check.RuleVPCOptions:        1, // fast-convergence is optional, peer options present
		check.RuleSNMPWeak:          2,
		check.RuleVNINotOperational: 1, // 10200 configured, absent from show nve vni
	}
	for rule, n := range want {
		if rule == check.RuleVPCOptions {
			if len(got[rule]) != 0 {
				t.Errorf("%s = %v", rule, got[rule])
			}
			continue
		}
		if len(got[rule]) != n {
			t.Errorf("%s = %d %v, want %d", rule, len(got[rule]), got[rule], n)
		}
	}
	if len(got[check.RuleUndefinedPolicy]) != 0 || len(got[check.RuleVirtualPeerLinkTCAM]) != 0 {
		t.Errorf("false positives: %v %v", got[check.RuleUndefinedPolicy], got[check.RuleVirtualPeerLinkTCAM])
	}
}

func TestFabricConsensus(t *testing.T) {
	t.Parallel()
	same := func(s string) string { return s }
	a := leaf(t, "A", "198.51.100.11", same)
	b := leaf(t, "B", "198.51.100.12", same)
	c := leaf(t, "C", "198.51.100.13", func(s string) string {
		s = strings.Replace(s, "vn-segment 10100", "vn-segment 20100", 1)
		return strings.Replace(s, "anycast-gateway-mac 0000.2222.3333", "anycast-gateway-mac 0000.2222.4444", 1)
	})
	got := run(t, &check.Fabric{Name: "dc1", Switches: []*device.Switch{a, b, c}})
	if s := got[check.RuleVLANMapping]; len(s) != 1 || !strings.HasSuffix(s[0], "vlan:100") {
		t.Errorf("vlan mapping = %v", s)
	}
	if s := got[check.RuleAnycastMAC]; len(s) != 1 {
		t.Errorf("anycast mac = %v", s)
	}
	if s := got[check.RuleAddressUnique]; len(s) == 0 {
		t.Error("identical router-ids not reported")
	}
}

func TestPolicyDisablesRule(t *testing.T) {
	t.Parallel()
	off := false
	p := assess.Policy{Rules: map[assess.RuleID]assess.RulePolicy{check.RuleSNMPWeak: {Enabled: &off}}}
	if _, err := check.NewAssessor(p, nxos.Profile{}); err != nil {
		t.Fatal(err)
	}
	p.Rules["fabric.nope"] = assess.RulePolicy{}
	if _, err := check.NewAssessor(p, nxos.Profile{}); err == nil {
		t.Fatal("unknown rule accepted")
	}
	if ids := check.RuleIDs(); !slices.Contains(ids, check.RuleIntentDrift) || len(ids) < 30 {
		t.Errorf("rule ids = %d", len(ids))
	}
}
