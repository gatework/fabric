package baseline_test

import (
	"testing"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/go-netops/drift"
	"github.com/gatework/go-netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/baseline"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/plan"
)

const profileYAML = `schema: 1
default:
  features: [bgp, lldp]
  settings:
    timezone: UTC 0 0
    line.vty.exec_timeout: "5"
  ntp_servers:
    - {address: 192.0.2.1, vrf: management}
roles:
  leaf:
    settings:
      vpc.fast_convergence: enabled
regions:
  dc2:
    settings:
      timezone: GMT 8 0
    forbidden_features: [pim]
    exclusive_servers: true
`

func TestResolveLayers(t *testing.T) {
	t.Parallel()
	p, err := baseline.Decode([]byte(profileYAML))
	if err != nil {
		t.Fatal(err)
	}
	leaf := p.Resolve("dc2", fabric.RoleLeaf)
	if leaf.Settings[device.SettingTimezone] != "GMT 8 0" || leaf.Settings[device.SettingVPCFastConvergence] != device.Enabled {
		t.Errorf("leaf dc2 = %+v", leaf.Settings)
	}
	spine := p.Resolve("dc1", fabric.RoleSpine)
	if spine.Settings[device.SettingTimezone] != "UTC 0 0" || spine.Settings[device.SettingVPCFastConvergence] != "" {
		t.Errorf("spine dc1 = %+v", spine.Settings)
	}
	if _, err := baseline.Decode([]byte("schema: 1\ndefault:\n  settings:\n    nope: x\n")); err == nil {
		t.Error("unknown setting accepted")
	}
}

func switchConfig() *device.Config {
	c := device.NewConfig()
	c.Features["bgp"] = true
	c.Features["pim"] = true
	c.Management.Timezone = "BeiJing 8 0"
	c.Management.NTP.Servers = []device.NTPServer{{Address: "192.0.2.1", VRF: "default"}, {Address: "192.0.2.9"}}
	c.VPC = &device.VPCDomain{ID: 1}
	return c
}

func TestCheckAndRemediate(t *testing.T) {
	t.Parallel()
	p, _ := baseline.Decode([]byte(profileYAML))
	exp := p.Resolve("dc2", fabric.RoleLeaf)
	cfg := switchConfig()
	devs := baseline.Check(assess.NewSubject("switch", "s1"), exp, cfg)
	paths := map[string]drift.Kind{}
	for _, d := range devs {
		paths[d.Path] = d.Kind
	}
	for path, kind := range map[string]drift.Kind{
		"feature.lldp": drift.KindMissing, "feature.pim": drift.KindUnexpected,
		"timezone": drift.KindMismatch, "line.vty.exec_timeout": drift.KindMissing,
		"vpc.fast_convergence": drift.KindMissing, "ntp_server.192.0.2.1": drift.KindMismatch,
		"ntp_server.192.0.2.9": drift.KindUnexpected,
	} {
		if paths[path] != kind {
			t.Errorf("%s = %q, want %q (all %v)", path, paths[path], kind, paths)
		}
	}
	d := exp.Desired(cfg)
	ops := plan.Diff(d, cfg, plan.Options{})
	after, err := plan.Simulate(cfg, ops)
	if err != nil {
		t.Fatal(err)
	}
	if rest := baseline.Check(assess.NewSubject("switch", "s1"), exp, after); len(rest) != 0 {
		t.Errorf("remediation leaves %v", rest)
	}
}

func TestDeriveMajority(t *testing.T) {
	t.Parallel()
	mk := func(tz string, vpc bool) *device.Switch {
		c := device.NewConfig()
		c.Features["bgp"] = true
		c.Management.Timezone = tz
		if vpc {
			c.VPC = &device.VPCDomain{ID: 1, PeerGateway: true}
		}
		sw := &device.Switch{Identity: device.Identity{Role: fabric.RoleLeaf}, Config: c}
		sw.Coverage = map[evidence.Dimension]evidence.Status{device.DimConfig: evidence.StatusComplete}
		return sw
	}
	p := baseline.Derive([]baseline.Group{
		{Region: "a", Switches: []*device.Switch{mk("UTC 0 0", true), mk("UTC 0 0", false), mk("GMT 8 0", true)}},
		{Region: "b", Switches: []*device.Switch{mk("UTC 0 0", false), mk("UTC 0 0", false)}},
	})
	if p.Default.Settings[device.SettingTimezone] != "UTC 0 0" {
		t.Errorf("default = %+v", p.Default.Settings)
	}
	if _, ok := p.Regions["a"].Settings[device.SettingVPCPeerGateway]; !ok {
		t.Errorf("region a vpc option not derived: %+v", p.Regions["a"].Settings)
	}
	if _, ok := p.Default.Settings[device.SettingVPCPeerGateway]; ok {
		t.Error("vpc option promoted although region b has no vpc")
	}
	if got := p.Roles["leaf"].Features; len(got) != 1 || got[0] != "bgp" {
		t.Errorf("leaf features = %v", got)
	}
	body, err := baseline.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := baseline.Decode(body); err != nil {
		t.Fatalf("round trip: %v\n%s", err, body)
	}
}
