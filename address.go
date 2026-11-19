package fabric

import "net/netip"

// Addr is a netip.Addr that YAML omits only when unset. (yaml.v3 treats
// every netip value as empty, so omitempty would drop valid addresses.)
type Addr struct{ netip.Addr }

// Prefix is a netip.Prefix that YAML omits only when unset.
type Prefix struct{ netip.Prefix }

// AddrOf wraps an address.
func AddrOf(a netip.Addr) Addr { return Addr{a} }

// PrefixOf wraps a prefix.
func PrefixOf(p netip.Prefix) Prefix { return Prefix{p} }

// MustParseAddr parses an address or panics; for tests and constants.
func MustParseAddr(s string) Addr { return Addr{netip.MustParseAddr(s)} }

// MustParsePrefix parses a prefix or panics; for tests and constants.
func MustParsePrefix(s string) Prefix { return Prefix{netip.MustParsePrefix(s)} }

// IsZero reports an unset address, for YAML omitempty.
func (a Addr) IsZero() bool { return !a.IsValid() }

// IsZero reports an unset prefix, for YAML omitempty.
func (p Prefix) IsZero() bool { return !p.IsValid() }
