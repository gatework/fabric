package nxos

import (
	"context"
	"regexp"
	"strings"

	"github.com/gatework/netops/capture"
)

// promptLine matches an NX-OS prompt such as "leaf1#" or "leaf1(config)#".
var promptLine = regexp.MustCompile(`^[\w.:/-]+(\([\w-]+\))?[#>]\s*$`)

// isPrompt reports a closing prompt line; long lines are output, not
// prompts.
func isPrompt(line string) bool { return len(line) < 80 && promptLine.MatchString(line) }

// Text returns a command response without echo, closing prompt and
// trailing blank lines: the text the show parsers read.
func Text(ctx context.Context, o capture.Output) string {
	lines, err := o.Body(ctx, isPrompt)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l.Text)
	}
	return strings.TrimRight(b.String(), " \t\n")
}

// Outputs returns the text of every successful output of c, by command.
func Outputs(ctx context.Context, c capture.Capture) map[string]string {
	out := make(map[string]string, len(c.Outputs))
	for _, o := range c.Outputs {
		if o.OK() {
			out[o.Command] = Text(ctx, o)
		}
	}
	return out
}
