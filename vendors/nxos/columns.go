package nxos

import (
	"strings"
)

// columns slices fixed-width table rows by the offsets of the header's
// labels. NX-OS wraps long cells onto continuation rows whose first cell is
// blank; merge joins such a row into the previous one.
type columns struct {
	starts []int
}

// newColumns locates labels in header, left to right. It reports false if
// any label is missing.
func newColumns(header string, labels ...string) (columns, bool) {
	c := columns{starts: make([]int, len(labels))}
	from := 0
	for i, l := range labels {
		at := strings.Index(header[from:], l)
		if at < 0 {
			return columns{}, false
		}
		c.starts[i] = from + at
		from += at + len(l)
	}
	return c, true
}

// cells returns the trimmed cell text of each column.
func (c columns) cells(line string) []string {
	out := make([]string, len(c.starts))
	for i, start := range c.starts {
		if start >= len(line) {
			break
		}
		end := len(line)
		if i+1 < len(c.starts) && c.starts[i+1] < end {
			end = c.starts[i+1]
		}
		out[i] = strings.TrimSpace(line[start:end])
	}
	return out
}
