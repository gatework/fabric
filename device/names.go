package device

import (
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
