package nxos

import (
	"strings"
	"unicode/utf8"

	"github.com/gatework/parser"
)

// node is one configuration line and the lines indented beneath it.
type node struct {
	line     int
	text     string
	fields   []string
	children []*node
}

// arg returns the i-th field or "".
func (n *node) arg(i int) string {
	if i < len(n.fields) {
		return n.fields[i]
	}
	return ""
}

// rest returns the text after the first k fields.
func (n *node) rest(k int) string {
	return strings.Join(n.fields[min(k, len(n.fields)):], " ")
}

// has reports whether the line starts with the given words.
func (n *node) has(words ...string) bool {
	if len(n.fields) < len(words) {
		return false
	}
	for i, w := range words {
		if n.fields[i] != w {
			return false
		}
	}
	return true
}

// buildTree turns indented configuration into a forest. Blank lines,
// comments ("!") and the volatile header are skipped. Indentation of any
// width nests a line under the closest less-indented line.
func buildTree(content *parser.Content) []*node {
	type frame struct {
		indent int
		n      *node
	}
	var roots []*node
	var stack []frame
	for line := range content.All() {
		text := strings.TrimRight(line.Text, " \t\r")
		trimmed := strings.TrimLeft(text, " \t")
		if trimmed == "" || trimmed[0] == '!' || !utf8.ValidString(trimmed) {
			continue
		}
		indent := len(text) - len(trimmed)
		n := &node{line: line.Number, text: trimmed, fields: strings.Fields(trimmed)}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, n)
		} else {
			parent := stack[len(stack)-1].n
			parent.children = append(parent.children, n)
		}
		stack = append(stack, frame{indent: indent, n: n})
	}
	return roots
}

// fingerprintText normalizes configuration text for fingerprinting: line
// endings and trailing blanks are dropped, and so are the echoed command
// and the header lines NX-OS rewrites on every display or change.
func fingerprintText(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	first := true
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if first {
			first = false
			if strings.HasPrefix(strings.TrimSpace(line), "show running-config") {
				continue
			}
		}
		if strings.HasPrefix(line, "!Time:") || strings.HasPrefix(line, "!Running configuration last done") ||
			strings.HasPrefix(line, "!No configuration change") || strings.HasPrefix(line, "!Command:") {
			continue
		}
		_, _ = b.WriteString(line)
		_ = b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
