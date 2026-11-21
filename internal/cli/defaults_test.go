package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/fabric/internal/bootstrap"
)

func TestApplyDefaults(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	root := newRoot(env{stdin: strings.NewReader(""), stdout: &out, stderr: &out, lookup: func(string) (string, bool) { return "", false }})
	cfg := bootstrap.Config{Intent: "/i.yaml", Snapshot: "/snap", BackupSnapshot: "/backups", Plans: "/plans", Journal: "/journal", Snapshots: "/snaps"}
	applyDefaults(root, cfg)
	tests := []struct{ cmd, flag, want string }{
		{"check", "intent", "/i.yaml"},
		{"check", "snapshot", "/snap"},
		{"backup", "snapshot", "/backups"},
		{"plan", "output", "/plans"},
		{"deploy", "journal", "/journal"},
		{"collect", "output", "/snaps"},
		{"import", "output", "-"},
	}
	for _, tt := range tests {
		c, _, err := root.Find([]string{tt.cmd})
		if err != nil {
			t.Fatal(err)
		}
		if got := c.Flags().Lookup(tt.flag).Value.String(); got != tt.want {
			t.Errorf("%s --%s = %q, want %q", tt.cmd, tt.flag, got, tt.want)
		}
	}
}

func TestBackupRequiresBackupSnapshot(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	root := newRoot(env{stdout: &out, stderr: &out})
	applyDefaults(root, bootstrap.Config{Snapshot: "/snap"})
	root.SetArgs([]string{"backup"})
	err := root.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), `required flag(s) "snapshot" not set`) {
		t.Fatalf("backup without backup_snapshot: %v", err)
	}
}

func TestBackupSnapshotConfigAndFlag(t *testing.T) {
	t.Parallel()
	snapshot := snapshotDir(t)
	config := filepath.Join(t.TempDir(), "fabric.yml")
	var out, errb bytes.Buffer
	e := testEnv(&out, &errb)
	e.environ = map[string]string{"FABRIC_CONFIG": config}
	for _, explicit := range []bool{false, true} {
		configured := snapshot
		args := []string{"backup", "--json"}
		if explicit {
			configured = filepath.Join(t.TempDir(), "missing-backup")
			args = append(args, "--snapshot", snapshot)
		}
		body := fmt.Sprintf("snapshot: /unused\nbackup_snapshot: %q\n", configured)
		if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		errb.Reset()
		code := run(t.Context(), args, e)
		if code == ExitError || !strings.Contains(out.String(), "DC1-LEF01") || errb.Len() != 0 {
			t.Errorf("explicit flag %v: code %d, stdout %.200q, stderr %q", explicit, code, out.String(), errb.String())
		}
	}
}
