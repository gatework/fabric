package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// ErrCorrupt reports a capture whose output does not match its sidecar.
var ErrCorrupt = errors.New("snapshot: corrupt capture")

// maxOutput bounds one receipt.
const maxOutput = 64 << 20

// Sidecar is the metadata written next to each receipt.
type Sidecar struct {
	Command      string    `json:"command"`
	Success      bool      `json:"success"`
	Bytes        int       `json:"bytes"`
	SHA256       string    `json:"sha256"`
	PromptBefore string    `json:"prompt_before"`
	PromptAfter  string    `json:"prompt_after"`
	RecordedAt   time.Time `json:"recorded_at"`
}

// Output is one command's cleaned output.
type Output struct {
	Command    string
	Text       string
	Digest     string
	RecordedAt time.Time
}

// Capture is everything captured from one switch.
type Capture struct {
	Mgmt    netip.Addr
	Outputs map[string]Output
}

// ReadAll reads every switch directory under root, in address order.
// Directories whose name is not an address are ignored.
func ReadAll(ctx context.Context, root string) ([]Capture, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	var out []Capture
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("snapshot: %w", err)
		}
		addr, err := netip.ParseAddr(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		c, err := Read(filepath.Join(root, e.Name()))
		if err != nil {
			return nil, err
		}
		c.Mgmt = addr
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Capture) int { return a.Mgmt.Compare(b.Mgmt) })
	return out, nil
}

// Read reads one switch directory. The directory name is parsed as the
// management address when it is one.
func Read(dir string) (Capture, error) {
	c := Capture{Outputs: map[string]Output{}}
	if addr, err := netip.ParseAddr(filepath.Base(dir)); err == nil {
		c.Mgmt = addr
	}
	sidecars, err := fs.Glob(os.DirFS(dir), "*.json")
	if err != nil {
		return c, fmt.Errorf("snapshot: %w", err)
	}
	slices.Sort(sidecars)
	for _, name := range sidecars {
		out, err := readOne(dir, name)
		if err != nil {
			return c, err
		}
		if out.Command == "" {
			continue
		}
		c.Outputs[out.Command] = out
	}
	return c, nil
}

func readOne(dir, name string) (Output, error) {
	meta, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return Output{}, fmt.Errorf("snapshot: %w", err)
	}
	var sc Sidecar
	if err := json.Unmarshal(meta, &sc); err != nil {
		return Output{}, fmt.Errorf("snapshot: %s: %w", name, err)
	}
	rawPath := filepath.Join(dir, strings.TrimSuffix(name, ".json")+".output")
	info, err := os.Stat(rawPath)
	if err != nil {
		return Output{}, fmt.Errorf("snapshot: %w", err)
	}
	if info.Size() > maxOutput {
		return Output{}, fmt.Errorf("%w: %s exceeds %d bytes", ErrCorrupt, rawPath, maxOutput)
	}
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		return Output{}, fmt.Errorf("snapshot: %w", err)
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if sc.SHA256 != "" && !strings.EqualFold(sc.SHA256, digest) {
		return Output{}, fmt.Errorf("%w: %s digest mismatch", ErrCorrupt, rawPath)
	}
	return Output{
		Command:    sc.Command,
		Text:       Clean(string(raw), sc.Command, sc.PromptAfter),
		Digest:     digest,
		RecordedAt: sc.RecordedAt,
	}, nil
}

var promptLine = regexp.MustCompile(`^[\w.:/-]+(\([\w-]+\))?[#>]\s*$`)

// Clean turns a terminal receipt into command output: carriage returns are
// removed, and so are the echoed command on the first line and the closing
// prompt on the last.
func Clean(raw, command, prompt string) string {
	text := strings.ReplaceAll(raw, "\r", "")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == strings.TrimSpace(command) {
		lines = lines[1:]
	}
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last == "" || (prompt != "" && last == strings.TrimSpace(prompt)) || promptLine.MatchString(last) {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.Join(lines, "\n")
}
