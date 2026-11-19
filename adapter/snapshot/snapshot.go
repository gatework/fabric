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

// List returns the switch directories under root in address order.
// Directories whose name is not an address are ignored.
func List(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	type dir struct {
		addr netip.Addr
		path string
	}
	var dirs []dir
	for _, e := range entries {
		if addr, err := netip.ParseAddr(e.Name()); err == nil && e.IsDir() {
			dirs = append(dirs, dir{addr, filepath.Join(root, e.Name())})
		}
	}
	slices.SortFunc(dirs, func(a, b dir) int { return a.addr.Compare(b.addr) })
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = d.path
	}
	return out, nil
}

// ReadAll reads every switch directory under root, in address order.
func ReadAll(ctx context.Context, root string) ([]Capture, error) {
	dirs, err := List(root)
	if err != nil {
		return nil, err
	}
	out := make([]Capture, 0, len(dirs))
	for _, d := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("snapshot: %w", err)
		}
		c, err := Read(d)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// Read reads one switch directory. The directory name is parsed as the
// management address when it is one.
func Read(dir string) (Capture, error) { return ReadSome(dir, nil) }

// ReadSome reads the commands keep accepts (all when keep is nil); the
// receipts of other commands are not read at all.
func ReadSome(dir string, keep func(command string) bool) (Capture, error) {
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
		out, err := readOne(dir, name, keep)
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

func readOne(dir, name string, keep func(string) bool) (Output, error) {
	meta, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return Output{}, fmt.Errorf("snapshot: %w", err)
	}
	var sc Sidecar
	if err := json.Unmarshal(meta, &sc); err != nil {
		return Output{}, fmt.Errorf("snapshot: %s: %w", name, err)
	}
	if keep != nil && !keep(sc.Command) {
		return Output{}, nil
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
//
// It slices instead of splitting: receipts are megabytes and loaded by
// the thousand.
func Clean(raw, command, prompt string) string {
	text := raw
	if strings.IndexByte(text, '\r') >= 0 {
		text = strings.ReplaceAll(text, "\r", "")
	}
	first, rest, more := strings.Cut(text, "\n")
	if strings.TrimSpace(first) == strings.TrimSpace(command) {
		text = ""
		if more {
			text = rest
		}
	}
	prompt = strings.TrimSpace(prompt)
	for {
		text = strings.TrimRight(text, " \t\n")
		start := strings.LastIndexByte(text, '\n') + 1
		last := strings.TrimSpace(text[start:])
		if last == "" || (prompt != "" && last == prompt) || (len(last) < 80 && promptLine.MatchString(last)) {
			if last == "" {
				return ""
			}
			text = text[:start]
			continue
		}
		return text
	}
}
