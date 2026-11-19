package fabric

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalid reports a value outside its domain.
var ErrInvalid = errors.New("fabric: invalid value")

// VLAN is an 802.1Q VLAN identifier usable for a network, 1 through 4094.
type VLAN uint16

// VLAN bounds.
const (
	MinVLAN VLAN = 1
	MaxVLAN VLAN = 4094
)

// ParseVLAN reads a decimal VLAN identifier.
func ParseVLAN(text string) (VLAN, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(text), 10, 16)
	if err != nil || VLAN(n) < MinVLAN || VLAN(n) > MaxVLAN {
		return 0, fmt.Errorf("%w: vlan %q", ErrInvalid, text)
	}
	return VLAN(n), nil
}

// Valid reports whether v is a usable VLAN identifier.
func (v VLAN) Valid() bool { return v >= MinVLAN && v <= MaxVLAN }

// String returns the decimal identifier.
func (v VLAN) String() string { return strconv.Itoa(int(v)) }

// SVI returns the name of the VLAN interface, for example "Vlan100".
func (v VLAN) SVI() string { return "Vlan" + v.String() }

// UnmarshalText parses a decimal VLAN identifier.
func (v *VLAN) UnmarshalText(text []byte) error {
	parsed, err := ParseVLAN(string(text))
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// MarshalText formats the identifier.
func (v VLAN) MarshalText() ([]byte, error) {
	if !v.Valid() {
		return nil, fmt.Errorf("%w: vlan %d", ErrInvalid, v)
	}
	return []byte(v.String()), nil
}

// VNI is a 24-bit VXLAN network identifier, 1 through 16777214.
type VNI uint32

// VNI bounds.
const (
	MinVNI VNI = 1
	MaxVNI VNI = 1<<24 - 2
)

// ParseVNI reads a decimal VNI.
func ParseVNI(text string) (VNI, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(text), 10, 32)
	if err != nil || VNI(n) < MinVNI || VNI(n) > MaxVNI {
		return 0, fmt.Errorf("%w: vni %q", ErrInvalid, text)
	}
	return VNI(n), nil
}

// Valid reports whether v is a usable VNI.
func (v VNI) Valid() bool { return v >= MinVNI && v <= MaxVNI }

// String returns the decimal identifier.
func (v VNI) String() string { return strconv.FormatUint(uint64(v), 10) }

// UnmarshalText parses a decimal VNI.
func (v *VNI) UnmarshalText(text []byte) error {
	parsed, err := ParseVNI(string(text))
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// MarshalText formats the identifier.
func (v VNI) MarshalText() ([]byte, error) {
	if !v.Valid() {
		return nil, fmt.Errorf("%w: vni %d", ErrInvalid, v)
	}
	return []byte(v.String()), nil
}

// ASN is a BGP autonomous system number in asplain notation.
type ASN uint32

// ParseASN reads an asplain or asdot ("1.10") autonomous system number.
func ParseASN(text string) (ASN, error) {
	text = strings.TrimSpace(text)
	if hi, lo, ok := strings.Cut(text, "."); ok {
		h, err1 := strconv.ParseUint(hi, 10, 16)
		l, err2 := strconv.ParseUint(lo, 10, 16)
		if err1 != nil || err2 != nil {
			return 0, fmt.Errorf("%w: asn %q", ErrInvalid, text)
		}
		return ASN(h<<16 | l), nil
	}
	n, err := strconv.ParseUint(text, 10, 32)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("%w: asn %q", ErrInvalid, text)
	}
	return ASN(n), nil
}

// String returns the asplain form.
func (a ASN) String() string { return strconv.FormatUint(uint64(a), 10) }

// UnmarshalText parses an ASN.
func (a *ASN) UnmarshalText(text []byte) error {
	parsed, err := ParseASN(string(text))
	if err != nil {
		return err
	}
	*a = parsed
	return nil
}

// MarshalText formats the ASN.
func (a ASN) MarshalText() ([]byte, error) {
	if a == 0 {
		return nil, fmt.Errorf("%w: asn 0", ErrInvalid)
	}
	return []byte(a.String()), nil
}

// MAC is a hardware address in Cisco dotted notation, "xxxx.xxxx.xxxx",
// lower case. The zero value is the empty string and means unset.
type MAC string

// ParseMAC accepts dotted, colon or dash notation and returns the dotted form.
func ParseMAC(text string) (MAC, error) {
	hex := strings.Map(func(r rune) rune {
		switch {
		case r == '.' || r == ':' || r == '-':
			return -1
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			return r
		case r >= 'A' && r <= 'F':
			return r + ('a' - 'A')
		default:
			return '!'
		}
	}, strings.TrimSpace(text))
	if len(hex) != 12 || strings.ContainsRune(hex, '!') {
		return "", fmt.Errorf("%w: mac %q", ErrInvalid, text)
	}
	return MAC(hex[0:4] + "." + hex[4:8] + "." + hex[8:12]), nil
}

// UnmarshalText parses a MAC address.
func (m *MAC) UnmarshalText(text []byte) error {
	parsed, err := ParseMAC(string(text))
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// Role is the function of a switch in its fabric.
type Role uint8

// Switch roles. The zero value is RoleUnset and never valid in an intent.
const (
	RoleUnset Role = iota
	// RoleSpine is an underlay transit switch that reflects EVPN routes.
	RoleSpine
	// RoleLeaf is a VTEP that attaches hosts.
	RoleLeaf
	// RoleBorder is a VTEP that also connects the fabric to the outside.
	RoleBorder
)

var roleNames = [...]string{RoleUnset: "", RoleSpine: "spine", RoleLeaf: "leaf", RoleBorder: "border"}

// Roles lists the valid roles.
func Roles() []Role { return []Role{RoleSpine, RoleLeaf, RoleBorder} }

// ParseRole reads a role name.
func ParseRole(text string) (Role, error) {
	for _, r := range Roles() {
		if roleNames[r] == text {
			return r, nil
		}
	}
	return RoleUnset, fmt.Errorf("%w: role %q", ErrInvalid, text)
}

// Valid reports whether r is a declared role.
func (r Role) Valid() bool { return r >= RoleSpine && r <= RoleBorder }

// VTEP reports whether switches in this role terminate VXLAN tunnels.
func (r Role) VTEP() bool { return r == RoleLeaf || r == RoleBorder }

// String returns the role name.
func (r Role) String() string {
	if int(r) < len(roleNames) {
		return roleNames[r]
	}
	return "role(" + strconv.Itoa(int(r)) + ")"
}

// MarshalText formats the role.
func (r Role) MarshalText() ([]byte, error) {
	if !r.Valid() {
		return nil, fmt.Errorf("%w: role %d", ErrInvalid, r)
	}
	return []byte(r.String()), nil
}

// UnmarshalText parses the role.
func (r *Role) UnmarshalText(text []byte) error {
	parsed, err := ParseRole(string(text))
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}

// Replication is how a fabric floods broadcast, unknown unicast and
// multicast traffic between VTEPs.
type Replication uint8

// Replication modes. The zero value is ReplicationIngress, the mode every
// observed fabric uses.
const (
	// ReplicationIngress replicates at the source VTEP to peers learned by BGP.
	ReplicationIngress Replication = iota
	// ReplicationMulticast floods through an underlay multicast group.
	ReplicationMulticast
)

// String returns the mode name.
func (r Replication) String() string {
	if r == ReplicationMulticast {
		return "multicast"
	}
	return "ingress"
}

// MarshalText formats the mode.
func (r Replication) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

// UnmarshalText parses the mode.
func (r *Replication) UnmarshalText(text []byte) error {
	switch string(text) {
	case "ingress", "":
		*r = ReplicationIngress
	case "multicast":
		*r = ReplicationMulticast
	default:
		return fmt.Errorf("%w: replication %q", ErrInvalid, text)
	}
	return nil
}

// PortMode is how an attachment carries networks.
type PortMode uint8

// Port modes.
const (
	// PortAccess carries exactly one network untagged.
	PortAccess PortMode = iota
	// PortTrunk carries its networks tagged.
	PortTrunk
)

// String returns the mode name.
func (m PortMode) String() string {
	if m == PortTrunk {
		return "trunk"
	}
	return "access"
}

// MarshalText formats the mode.
func (m PortMode) MarshalText() ([]byte, error) { return []byte(m.String()), nil }

// UnmarshalText parses the mode.
func (m *PortMode) UnmarshalText(text []byte) error {
	switch string(text) {
	case "access", "":
		*m = PortAccess
	case "trunk":
		*m = PortTrunk
	default:
		return fmt.Errorf("%w: port mode %q", ErrInvalid, text)
	}
	return nil
}
