package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testEnv(stdout, stderr *bytes.Buffer) env {
	return env{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, lookup: func(string) (string, bool) { return "", false }}
}

func TestHelpIsChinese(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--help"}, testEnv(&out, &errb)); code != ExitOK {
		t.Fatalf("code %d %s", code, errb.String())
	}
	for _, want := range []string{"控制器", "plan", "deploy", "rollback"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

func TestParseCommand(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"parse", "--file", "../../vendors/nxos/testdata/leaf.cfg"}, testEnv(&out, &errb))
	if code != ExitOK || !strings.Contains(out.String(), `"hostname": "DC1-LEF01"`) {
		t.Fatalf("code %d out %.200s err %s", code, out.String(), errb.String())
	}
}

// snapshotDir lays out two leaves in the capture format.
func snapshotDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cfg, err := os.ReadFile("../../vendors/nxos/testdata/leaf.cfg")
	if err != nil {
		t.Fatal(err)
	}
	for i, mgmt := range []string{"198.51.100.11", "198.51.100.12"} {
		dir := filepath.Join(root, mgmt)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(cfg), "DC1-LEF01", []string{"DC1-LEF01", "DC1-LEF02"}[i])
		_ = os.WriteFile(filepath.Join(dir, "03-show-running-config.output"), []byte(text), 0o600)
		_ = os.WriteFile(filepath.Join(dir, "03-show-running-config.json"), []byte(`{"command":"show running-config"}`), 0o600)
	}
	return root
}

func TestPlanAndDryRunDeploy(t *testing.T) {
	t.Parallel()
	snap := snapshotDir(t)
	plans := t.TempDir()
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"plan", "--snapshot", snap, "--intent", "../../testdata/intent.yaml", "-o", plans}, testEnv(&out, &errb))
	if code != ExitOK || !strings.Contains(out.String(), "2 台需要变更") {
		t.Fatalf("plan code %d\n%s\n%s", code, out.String(), errb.String())
	}
	dirs, _ := filepath.Glob(filepath.Join(plans, "*"))
	if len(dirs) != 1 {
		t.Fatalf("plan dirs = %v", dirs)
	}
	out.Reset()
	if code := run(context.Background(), []string{"deploy", "--plan", dirs[0]}, testEnv(&out, &errb)); code != ExitOK || !strings.Contains(out.String(), "预览模式") {
		t.Fatalf("dry run code %d\n%s", code, out.String())
	}
	errb.Reset()
	if code := run(context.Background(), []string{"deploy", "--plan", dirs[0], "--apply"}, testEnv(&out, &errb)); code != ExitError || !strings.Contains(errb.String(), "--confirm") {
		t.Fatalf("unconfirmed apply code %d err %s", code, errb.String())
	}
}

func TestImportAndCheckExitCodes(t *testing.T) {
	t.Parallel()
	snap := snapshotDir(t)
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"import", "--snapshot", snap}, testEnv(&out, &errb)); code != ExitOK {
		t.Fatalf("import code %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "schema: 1") {
		t.Errorf("import output %.200s", out.String())
	}
	out.Reset()
	code := run(context.Background(), []string{"check", "--snapshot", snap}, testEnv(&out, &errb))
	if code != ExitFindings && code != ExitIncomplete {
		t.Fatalf("check code %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "检查结果") {
		t.Errorf("check output %.300s", out.String())
	}
}
