package cli

import (
	"bytes"
	"strings"
	"testing"
)

const testIntent = "../../testdata/intent.yaml"

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(t.Context(), args, testEnv(&out, &errb))
	return code, out.String(), errb.String()
}

func TestSwitchShow(t *testing.T) {
	t.Parallel()
	snap := snapshotDir(t)
	code, out, errb := runCLI(t, "switch", "show", "DC1-LEF01", "--snapshot", snap, "--intent", testIntent)
	if code != ExitOK {
		t.Fatalf("code %d\n%s\n%s", code, out, errb)
	}
	for _, want := range []string{"DC1-LEF01", "健康：", "发现", "下一步：fabric topology --switch DC1-LEF01"} {
		if !strings.Contains(out, want) {
			t.Errorf("switch show lacks %q:\n%s", want, out)
		}
	}
	code, _, errb = runCLI(t, "switch", "show", "NO-SUCH", "--snapshot", snap, "--intent", testIntent)
	if code != ExitError || !strings.Contains(errb, "Tab") {
		t.Errorf("unknown switch: code %d, %s", code, errb)
	}
}

func TestCheckFilters(t *testing.T) {
	t.Parallel()
	snap := snapshotDir(t)
	_, all, _ := runCLI(t, "check", "--snapshot", snap, "--intent", testIntent, "-v")
	_, one, errb := runCLI(t, "check", "--snapshot", snap, "--intent", testIntent, "-v", "--switch", "DC1-LEF02")
	if !strings.Contains(one, "检查结果") || strings.Contains(one, "DC1-LEF01：") || len(one) >= len(all) {
		t.Errorf("--switch did not narrow the findings:\n%s\n%s", one, errb)
	}
	_, high, _ := runCLI(t, "check", "--snapshot", snap, "--intent", testIntent, "--severity", "high")
	if strings.Contains(high, "[中]") || strings.Contains(high, "[低]") {
		t.Errorf("--severity high kept lower findings:\n%s", high)
	}
	if code, _, errb := runCLI(t, "check", "--snapshot", snap, "--severity", "urgent"); code != ExitError || !strings.Contains(errb, "critical") {
		t.Errorf("invalid severity: code %d, %s", code, errb)
	}
}

func TestTopologyAndStatusGuideTheOperator(t *testing.T) {
	t.Parallel()
	snap := snapshotDir(t)
	_, out, errb := runCLI(t, "topology", "--snapshot", snap, "--intent", testIntent, "--matrix")
	if !strings.Contains(out, "区域") || !strings.Contains(out, "VTEP") {
		t.Errorf("topology:\n%s\n%s", out, errb)
	}
	_, out, _ = runCLI(t, "status", "--snapshot", snap, "--intent", testIntent)
	if !strings.Contains(out, "主要问题") || !strings.Contains(out, "下一步：fabric switch show") {
		t.Errorf("status:\n%s", out)
	}
}

func TestCompletionOffersSwitchesAndRegions(t *testing.T) {
	t.Parallel()
	_, out, _ := runCLI(t, "__complete", "topology", "--intent", testIntent, "--switch", "DC1-L")
	if !strings.Contains(out, "DC1-LEF01\t") || strings.Contains(out, "DC1-SPN") {
		t.Errorf("switch completion:\n%s", out)
	}
	_, out, _ = runCLI(t, "__complete", "switch", "show", "--intent", testIntent, "")
	if !strings.Contains(out, "DC1-LEF02") {
		t.Errorf("positional completion:\n%s", out)
	}
	_, out, _ = runCLI(t, "__complete", "check", "--severity", "")
	if !strings.Contains(out, "critical") {
		t.Errorf("severity completion:\n%s", out)
	}
}

func TestTopologyDrawsTheFabric(t *testing.T) {
	t.Parallel()
	snap := snapshotDir(t)
	_, out, errb := runCLI(t, "topology", "--snapshot", snap, "--intent", testIntent)
	if !strings.Contains(out, "图例") || !strings.Contains(out, "DC1-LEF01") {
		t.Errorf("region diagram:\n%s\n%s", out, errb)
	}
	_, out, _ = runCLI(t, "topology", "--snapshot", snap, "--intent", testIntent, "--switch", "DC1-LEF01")
	if !strings.Contains(out, "└──") || !strings.Contains(out, "DC1-LEF01") {
		t.Errorf("switch tree:\n%s", out)
	}
}

func TestImpactJudgesMaintenance(t *testing.T) {
	t.Parallel()
	snap := snapshotDir(t)
	code, out, errb := runCLI(t, "impact", "DC1-LEF01", "--snapshot", snap, "--intent", testIntent)
	if !strings.Contains(out, "维护影响评估") || code == ExitError {
		t.Errorf("impact: code %d\n%s\n%s", code, out, errb)
	}
	if code, _, errb := runCLI(t, "impact", "NO-SUCH", "--snapshot", snap, "--intent", testIntent); code != ExitError || !strings.Contains(errb, "Tab") {
		t.Errorf("unknown switch: code %d %s", code, errb)
	}
}

func TestPathReportsUnlearnedEndpoints(t *testing.T) {
	t.Parallel()
	snap := snapshotDir(t)
	code, _, errb := runCLI(t, "path", "5254.dead.beef", "10.0.0.1", "--snapshot", snap, "--intent", testIntent)
	if code != ExitFindings || !strings.Contains(errb, "fabric locate") {
		t.Errorf("unlearned endpoint: code %d %s", code, errb)
	}
}
