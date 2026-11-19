package nxos

import "strings"

// tokens splits table rows into whitespace-separated fields, reusing one
// backing slice across rows; the hot show parsers call it per line.
type tokens struct {
	buf []string
}

// split returns the fields of s. The result is valid until the next call.
func (t *tokens) split(s string) []string {
	t.buf = t.buf[:0]
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return t.buf
		}
		end := strings.IndexAny(s, " \t")
		if end < 0 {
			return append(t.buf, s)
		}
		t.buf = append(t.buf, s[:end])
		s = s[end:]
	}
}

// isMAC reports Cisco dotted notation, "xxxx.xxxx.xxxx", lower case.
func isMAC(s string) bool {
	if len(s) != 14 || s[4] != '.' || s[9] != '.' {
		return false
	}
	for i := range len(s) {
		if i == 4 || i == 9 {
			continue
		}
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isMACMarker reports the flag column of "show mac address-table":
// combinations of * C G + R O ~.
func isMACMarker(s string) bool {
	return s != "" && strings.Trim(s, "*CG+RO~") == ""
}
