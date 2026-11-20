package nxos_test

import (
	"encoding/json"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gatework/netops/change"
	ndevice "github.com/gatework/netops/device"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/plan"
	"github.com/gatework/fabric/vendors/nxos"
)

func intentRegion(tb testing.TB) *fabric.Region {
	tb.Helper()
	body, err := os.ReadFile("../../testdata/intent.yaml")
	if err != nil {
		tb.Fatal(err)
	}
	in, err := fabric.DecodeIntent(body)
	if err != nil {
		tb.Fatal(err)
	}
	return in.Regions[0]
}

// TestRenderParsesBack proves the renderer and the parser agree: the
// configuration rendered for a fresh switch, read back by ParseConfig,
// satisfies every operation.
func TestRenderParsesBack(t *testing.T) {
	t.Parallel()
	r := intentRegion(t)
	sw, _ := r.Switch("DC1-LEF01")
	base := "interface Ethernet1/3\n  no shutdown\ninterface Ethernet1/20\ninterface Ethernet1/49\ninterface port-channel10\n  switchport\n  switchport mode trunk\n  switchport trunk allowed vlan 1\n" +
		"interface nve1\n  host-reachability protocol bgp\nrouter bgp 65001\n"
	cfg, err := nxos.ParseConfig(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	d := plan.Compile(r, sw)
	ops := plan.Diff(d, cfg, plan.Options{})
	if err := plan.Converges(d, cfg, ops, plan.Options{}); err != nil {
		t.Fatal(err)
	}
	text, err := nxos.RenderConfig(ops)
	if err != nil {
		t.Fatal(err)
	}
	back, err := nxos.ParseConfig(t.Context(), base+text)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if err := op.Holds(back); err != nil {
			t.Errorf("%v after parse-back: %v\n%s", op.Ref(), err, text)
		}
	}
	if rest := plan.Diff(d, back, plan.Options{}); len(rest) != 0 {
		t.Errorf("parse-back leaves %d operations", len(rest))
	}
}

func TestRenderAdoptedLeafIsNoop(t *testing.T) {
	t.Parallel()
	r := intentRegion(t)
	sw, _ := r.Switch("DC1-LEF01")
	cfg := parseLeaf(t)
	ops := plan.Diff(plan.Compile(r, sw), cfg, plan.Options{})
	var refs []string
	for _, op := range ops {
		refs = append(refs, op.Action().String()+" "+op.Ref().String())
	}
	want := []string{
		"create vlan:300", "create evpn:10300", "update nve_member:10200",
		"create nve_member:10300", "create svi:300", "create port:Ethernet1/3", "update port_channel:port-channel10",
		"create port:Ethernet1/20", "create port_channel:port-channel20",
	}
	slices.Sort(refs)
	slices.Sort(want)
	if !slices.Equal(refs, want) {
		t.Errorf("ops = %v\nwant %v", refs, want)
	}
}

func TestChangesetPlanRoundTrip(t *testing.T) {
	t.Parallel()
	r := intentRegion(t)
	sw, _ := r.Switch("DC1-LEF01")
	cfg := parseLeaf(t)
	ops := plan.Diff(plan.Compile(r, sw), cfg, plan.Options{})
	cs, err := plan.NewChangeset(ops, nxos.Renderer{})
	if err != nil {
		t.Fatal(err)
	}
	target := ndevice.Device{ID: sw.Name, Name: sw.Name, Mgmt: netip.MustParseAddr("198.51.100.11"), Platform: nxos.Platform}
	p, err := plan.NewPlan(change.Meta{ID: "b1-" + sw.Name, Created: time.Unix(0, 0).UTC()}, target, cfg, cs)
	if err != nil {
		t.Fatal(err)
	}
	cmds := texts(p.Commands())
	if cmds[0] != "configure terminal" || cmds[len(cmds)-1] != "end" {
		t.Errorf("commands not wrapped: %v", cmds)
	}
	rb := strings.Join(texts(p.Script().Rollback), "\n")
	for _, want := range []string{"no interface Vlan300", "no vlan 300", "switchport trunk allowed vlan remove 300"} {
		if !strings.Contains(rb, want) {
			t.Errorf("rollback lacks %q:\n%s", want, rb)
		}
	}
	e, err := p.Envelope()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(e)
	codec, _ := plan.Codec(nxos.Renderer{})
	back, err := codec.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(texts(back.Commands()), cmds) {
		t.Error("decoded plan renders differently")
	}
	if r, err := back.Precheck(cfg); err != nil || r != change.Ready {
		t.Errorf("precheck: %v %v", r, err)
	}
	after, err := plan.Simulate(cfg, ops)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := back.Verify(cfg, after); err != nil {
		t.Errorf("verify: %v", err)
	}
	tampered := strings.Replace(string(body), "vlan 300", "vlan 301", 1)
	if _, err := codec.Decode([]byte(tampered)); err == nil {
		t.Error("tampered plan accepted")
	}
}

func TestRenderSettingsAndServers(t *testing.T) {
	t.Parallel()
	ops := []plan.Op{
		plan.SettingOp{Setting: device.SettingTimezone, Have: "GMT 8 0", Want: "UTC 0 0"},
		plan.SettingOp{Setting: device.SettingVPCFastConvergence, Scope: "10", Want: device.Enabled},
		plan.SettingOp{Setting: device.SettingBGPAdvertisePIP, Scope: "65001", Want: device.Enabled},
		plan.SettingOp{Setting: device.SettingVTYExecTimeout, Have: "5"},
		plan.NTPServerOp{Address: "192.0.2.9", Want: &device.NTPServer{Address: "192.0.2.9", VRF: "management"}},
		plan.LogServerOp{Address: "192.0.2.30", Have: &device.LogServer{Address: "192.0.2.30"}},
		plan.FeatureOp{Name: "nv overlay evpn", Enable: true},
		plan.FeatureOp{Name: "pim"},
	}
	got, err := nxos.RenderConfig(ops)
	if err != nil {
		t.Fatal(err)
	}
	want := `clock timezone UTC 0 0
vpc domain 10
  fast-convergence
router bgp 65001
  address-family l2vpn evpn
    advertise-pip
line vty
  no exec-timeout
ntp server 192.0.2.9 use-vrf management
no logging server 192.0.2.30
nv overlay evpn
no feature pim
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if _, err := nxos.RenderConfig([]plan.Op{plan.SettingOp{Setting: device.SettingVPCPeerGateway, Want: device.Enabled}}); err == nil {
		t.Error("vpc setting without scope rendered")
	}
}

func texts(cs []change.Command) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Text
	}
	return out
}
