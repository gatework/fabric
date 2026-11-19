package snapshot_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gatework/fabric/adapter/snapshot"
)

func TestEncodeReadRoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outputs := map[string]string{
		"show nve peers":      "show nve peers\r\nInterface Peer-IP State\r\nnve1 10.0.0.1 Up\r\nleaf1# ",
		"show running-config": "hostname leaf1\n",
	}
	files, err := snapshot.Encode("192.0.2.1", []string{"show running-config", "show vpc brief", "show nve peers"}, outputs, "leaf1# ", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := files["192.0.2.1/02-show-nve-peers.output"]; !ok {
		t.Fatalf("numbering skips absent commands: %v", files)
	}
	caps, err := snapshot.ReadAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 1 || caps[0].Mgmt.String() != "192.0.2.1" {
		t.Fatalf("captures = %+v", caps)
	}
	if got := caps[0].Outputs["show nve peers"].Text; got != "Interface Peer-IP State\nnve1 10.0.0.1 Up" {
		t.Errorf("cleaned output = %q", got)
	}
}

func TestReadRejectsDigestMismatch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "192.0.2.2")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "01-x.json"), []byte(`{"command":"x","sha256":"00"}`), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "01-x.output"), []byte("y"), 0o600)
	if _, err := snapshot.Read(dir); err == nil {
		t.Fatal("digest mismatch accepted")
	}
}

func TestClean(t *testing.T) {
	t.Parallel()
	got := snapshot.Clean("show x\r\r\nline\r\n\rSW-1(config)# ", "show x", "")
	if got != "line" {
		t.Errorf("clean = %q", got)
	}
}
