package device

import (
	"cmp"
	"slices"
	"strings"
)

var interfacePrefixes = []struct {
	short, long string
	kind        InterfaceKind
}{
	{"ethernet", "Ethernet", KindEthernet},
	{"eth", "Ethernet", KindEthernet},
	{"port-channel", "port-channel", KindPortChannel},
	{"po", "port-channel", KindPortChannel},
	{"vlan", "Vlan", KindSVI},
	{"loopback", "loopback", KindLoopback},
	{"lo", "loopback", KindLoopback},
	{"nve", "nve", KindNVE},
	{"mgmt", "mgmt", KindMgmt},
}

// CanonicalInterface returns the configuration spelling of an interface
// name in any common abbreviation ("Eth1/1" becomes "Ethernet1/1", "Po10"
// becomes "port-channel10") and its kind. Unknown names are returned as is.
func CanonicalInterface(name string) (string, InterfaceKind) {
	name = strings.TrimSpace(name)
	lower := strings.ToLower(name)
	for _, p := range interfacePrefixes {
		if !strings.HasPrefix(lower, p.short) {
			continue
		}
		rest := strings.TrimSpace(name[len(p.short):])
		if rest == "" || rest[0] < '0' || rest[0] > '9' {
			continue
		}
		return p.long + rest, p.kind
	}
	return name, KindOther
}

// ComparePorts orders interface names naturally: "Ethernet1/9" before
// "Ethernet1/10", and by name for different kinds.
func ComparePorts(a, b string) int {
	pa, pb := portNumbers(a), portNumbers(b)
	if c := cmp.Compare(strings.TrimRight(a, "0123456789/"), strings.TrimRight(b, "0123456789/")); c != 0 && (len(pa) == 0 || len(pb) == 0) {
		return c
	}
	if c := slices.Compare(pa, pb); c != 0 {
		return c
	}
	return cmp.Compare(a, b)
}

func portNumbers(s string) []int {
	var out []int
	n, in := 0, false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n, in = n*10+int(r-'0'), true
			continue
		}
		if in {
			out = append(out, n)
		}
		n, in = 0, false
	}
	if in {
		out = append(out, n)
	}
	return out
}
