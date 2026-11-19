package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"time"
)

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// FileName returns the receipt base name for the n-th command, for example
// "03-show-running-config".
func FileName(n int, command string) string {
	return fmt.Sprintf("%02d-%s", n, unsafeName.ReplaceAllString(command, "-"))
}

// Encode returns the files of one switch directory keyed by path relative
// to the snapshot root: for every command, in the given order, the output
// and its sidecar. Outputs are stored as received.
func Encode(dir string, commands []string, outputs map[string]string, prompt string, at time.Time) (map[string][]byte, error) {
	files := map[string][]byte{}
	n := 0
	for _, cmd := range commands {
		text, ok := outputs[cmd]
		if !ok {
			continue
		}
		n++
		base := path.Join(dir, FileName(n, cmd))
		sum := sha256.Sum256([]byte(text))
		meta, err := json.MarshalIndent(Sidecar{
			Command: cmd, Success: true, Bytes: len(text), SHA256: hex.EncodeToString(sum[:]),
			PromptBefore: prompt, PromptAfter: prompt, RecordedAt: at.UTC(),
		}, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("snapshot: %w", err)
		}
		files[base+".output"] = []byte(text)
		files[base+".json"] = meta
	}
	return files, nil
}
