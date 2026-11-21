package bootstrap_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gatework/fabric/internal/bootstrap"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ".gatework")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "intent: fabric/intent.yaml\nbaseline: ~/b.yaml\nsnapshot: /snap\nbackup_snapshot: fabric/backups\n"
	if err := os.WriteFile(filepath.Join(dir, "fabric.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("NC_DEVICE_USERNAME=ops\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := bootstrap.LoadConfig(t.Context(), map[string]string{"NC_DEVICE_PASSWORD": "x"}, home)
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(home, ".local", "share", "gatework", "fabric")
	tests := []struct{ name, got, want string }{
		{"path", c.Path, filepath.Join(dir, "fabric.yml")},
		{"intent relative to the file", c.Intent, filepath.Join(dir, "fabric", "intent.yaml")},
		{"baseline from home", c.Baseline, filepath.Join(home, "b.yaml")},
		{"snapshot", c.Snapshot, "/snap"},
		{"backup snapshot", c.BackupSnapshot, filepath.Join(dir, "fabric", "backups")},
		{"snapshots default", c.Snapshots, filepath.Join(data, "snapshots")},
		{"plans default", c.Plans, filepath.Join(data, "plans")},
		{"journal default", c.Journal, filepath.Join(data, "executions")},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, tt.got, tt.want)
		}
	}
	if v, _ := c.Lookup("NC_DEVICE_USERNAME"); v != "ops" {
		t.Errorf("credentials file not read: %q", v)
	}
	if v, _ := c.Lookup("NC_DEVICE_PASSWORD"); v != "x" {
		t.Errorf("process environment not kept: %q", v)
	}
}

func TestLoadConfig_missing(t *testing.T) {
	t.Parallel()
	if _, err := bootstrap.LoadConfig(t.Context(), map[string]string{}, t.TempDir()); err != nil {
		t.Fatalf("missing discovered file: %v", err)
	}
	env := map[string]string{"FABRIC_CONFIG": filepath.Join(t.TempDir(), "none.yml")}
	if _, err := bootstrap.LoadConfig(t.Context(), env, t.TempDir()); err == nil {
		t.Fatal("missing explicit file accepted")
	}
}
