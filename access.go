package fabric

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// AttachmentKind is how a host connects.
type AttachmentKind uint8

// Attachment kinds.
const (
	// AttachPort is one switch port.
	AttachPort AttachmentKind = iota
	// AttachPortChannel is an LACP bundle on one switch.
	AttachPortChannel
	// AttachVPC is an LACP bundle across both members of a vPC pair.
	AttachVPC
)

// String returns "port", "port-channel" or "vpc".
func (k AttachmentKind) String() string {
	switch k {
	case AttachPortChannel:
		return "port-channel"
	case AttachVPC:
		return "vpc"
	case AttachPort:
		return "port"
	default:
		return "port"
	}
}

// MarshalText formats the kind.
func (k AttachmentKind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// UnmarshalText parses the kind.
func (k *AttachmentKind) UnmarshalText(text []byte) error {
	switch string(text) {
	case "port", "":
		*k = AttachPort
	case "port-channel":
		*k = AttachPortChannel
	case "vpc":
		*k = AttachVPC
	default:
		return fmt.Errorf("%w: attachment kind %q", ErrInvalid, text)
	}
	return nil
}

// PortProfile is a reusable set of host-port settings, the fabric's
// equivalent of a wireless SSID profile.
type PortProfile struct {
	Name     string   `yaml:"name" json:"name"`
	Mode     PortMode `yaml:"mode" json:"mode"`
	Networks []VLAN   `yaml:"networks,omitempty" json:"networks,omitempty"`
	MTU      int      `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	// Edge marks host ports for spanning-tree port type edge (trunk).
	Edge bool `yaml:"edge,omitempty" json:"edge,omitempty"`
}

// Attachment is one host connection and the networks it carries.
type Attachment struct {
	Kind AttachmentKind `yaml:"kind,omitempty" json:"kind"`
	// Switch is the switch of a port or port-channel attachment, and the
	// first member of a vPC attachment.
	Switch string `yaml:"switch" json:"switch"`
	// Interface is the port of a port attachment.
	Interface string `yaml:"interface,omitempty" json:"interface,omitempty"`
	// PortChannel is the bundle number; a vPC uses it as the vPC number.
	PortChannel int `yaml:"port_channel,omitempty" json:"port_channel,omitempty"`
	// Members lists bundle member ports per switch.
	Members     map[string][]string `yaml:"members,omitempty" json:"members,omitempty"`
	Profile     string              `yaml:"profile,omitempty" json:"profile,omitempty"`
	Description string              `yaml:"description,omitempty" json:"description,omitempty"`
	// Mode, Networks, MTU and Edge override the profile.
	Mode     PortMode `yaml:"mode,omitempty" json:"mode"`
	Networks []VLAN   `yaml:"networks,omitempty" json:"networks,omitempty"`
	MTU      int      `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	Edge     bool     `yaml:"edge,omitempty" json:"edge,omitempty"`
}

// PortSpec is the effective configuration of an attachment.
type PortSpec struct {
	Mode        PortMode
	Networks    []VLAN
	MTU         int
	Edge        bool
	Description string
}

// Spec resolves the attachment against its profile; explicit fields win.
func (r *Region) Spec(a *Attachment) PortSpec {
	spec := PortSpec{Mode: a.Mode, Networks: slices.Clone(a.Networks), MTU: a.MTU, Edge: a.Edge, Description: a.Description}
	p, ok := r.Profile(a.Profile)
	if !ok {
		return spec
	}
	if len(a.Networks) == 0 {
		spec.Mode, spec.Networks = p.Mode, slices.Clone(p.Networks)
	}
	if spec.MTU == 0 {
		spec.MTU = p.MTU
	}
	spec.Edge = spec.Edge || p.Edge
	return spec
}

// Switches returns the switches an attachment touches, in order.
func (a *Attachment) Switches() []string {
	if a.Kind == AttachVPC {
		return slices.Sorted(maps.Keys(a.Members))
	}
	return []string{a.Switch}
}

// Ports returns the interfaces the attachment occupies on a switch: the
// port, or the bundle members and the port-channel.
func (a *Attachment) Ports(sw string) []string {
	switch a.Kind {
	case AttachPort:
		if sw == a.Switch {
			return []string{a.Interface}
		}
		return nil
	case AttachPortChannel, AttachVPC:
		members, ok := a.Members[sw]
		if !ok {
			return nil
		}
		return append(slices.Clone(members), PortChannelName(a.PortChannel))
	default:
		return nil
	}
}

// PortChannelName returns the interface name of bundle n.
func PortChannelName(n int) string { return fmt.Sprintf("port-channel%d", n) }

// Key identifies the attachment for messages.
func (a *Attachment) Key() string {
	if a.Kind == AttachPort {
		return a.Switch + " " + a.Interface
	}
	return fmt.Sprintf("%s %s@%s", a.Kind, PortChannelName(a.PortChannel), strings.Join(a.Switches(), "+"))
}
