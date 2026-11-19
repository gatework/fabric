package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/fabric"
)

func intentCopy(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("../../testdata/intent.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "intent.yaml")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func runOK(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(context.Background(), args, testEnv(&out, &errb)); code != ExitOK {
		t.Fatalf("%v: code %d\n%s\n%s", args, code, out.String(), errb.String())
	}
	return out.String()
}

func TestNetworkAddPlansAffectedLeaves(t *testing.T) {
	t.Parallel()
	intent, snap, plans := intentCopy(t), snapshotDir(t), t.TempDir()
	out := runOK(t, "network", "add", "--intent", intent, "--snapshot", snap, "-o", plans,
		"--region", "dc1", "--name", "db", "--tenant", "PROD", "--gateway", "10.10.60.1/24")
	for _, want := range []string{"新增网络 db：VLAN 2 → L2VNI 10002", "DC1-BDR01 无采集数据", "2 台需要变更", "意图已更新"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	body, _ := os.ReadFile(intent)
	in, err := fabric.DecodeIntent(body)
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := in.Regions[0].Network(2); !ok || n.Gateway.String() != "10.10.60.1/24" {
		t.Errorf("network not written: %+v", n)
	}
	if _, err := os.Stat(intent + ".bak"); err != nil {
		t.Errorf("no backup: %v", err)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	intent := intentCopy(t)
	before, _ := os.ReadFile(intent)
	runOK(t, "tenant", "add", "--intent", intent, "--region", "dc1", "--name", "DMZ", "--dry-run")
	after, _ := os.ReadFile(intent)
	if !bytes.Equal(before, after) {
		t.Error("dry run changed the intent")
	}
}

func TestPortAssignAndRelease(t *testing.T) {
	t.Parallel()
	intent, snap := intentCopy(t), snapshotDir(t)
	out := runOK(t, "port", "assign", "--intent", intent, "--snapshot", snap, "-o", t.TempDir(),
		"--vpc", "DC1-LEF01", "--member", "DC1-LEF01:Eth1/2", "--member", "DC1-LEF02:Eth1/2",
		"--vlans", "100,300", "--mode", "trunk", "--description", "APP-07")
	if !strings.Contains(out, "port-channel = 1") || !strings.Contains(out, "2 台需要变更") {
		t.Errorf("assign output:\n%s", out)
	}
	out = runOK(t, "port", "release", "--intent", intent, "--snapshot", snap, "-o", t.TempDir(), "--switch", "DC1-LEF02", "--port", "Ethernet1/2")
	if !strings.Contains(out, "回收 vpc port-channel1") {
		t.Errorf("release output:\n%s", out)
	}
	free := runOK(t, "port", "free", "--intent", intent, "--snapshot", snap, "--switch", "DC1-LEF01")
	if !strings.Contains(free, "Ethernet1/2") || strings.Contains(free, "Ethernet1/49") {
		t.Errorf("free output:\n%s", free)
	}
}

func TestSwitchAddAndDay0(t *testing.T) {
	t.Parallel()
	intent := intentCopy(t)
	out := runOK(t, "switch", "add", "--intent", intent, "--region", "dc1", "--name", "DC1-LEF03",
		"--mgmt", "198.51.100.13", "--role", "leaf", "--uplink", "Ethernet1/49=DC1-SPN01:Ethernet1/3")
	if !strings.Contains(out, "router-id = 10.0.0.2") || !strings.Contains(out, "新增互联") {
		t.Errorf("add output:\n%s", out)
	}
	day0 := runOK(t, "switch", "day0", "--intent", intent, "--name", "DC1-LEF03")
	for _, want := range []string{"hostname DC1-LEF03", "ip address 10.1.0.5/30", "interface Vlan100"} {
		if !strings.Contains(day0, want) {
			t.Errorf("day-0 lacks %q", want)
		}
	}
	var o, eb bytes.Buffer
	if code := run(context.Background(), []string{"switch", "add", "--intent", intent, "--region", "dc1", "--name", "DC1-LEF03", "--mgmt", "198.51.100.14"}, testEnv(&o, &eb)); code == ExitOK {
		t.Error("duplicate switch accepted")
	}
}
