package fabric

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"go.yaml.in/yaml/v3"
)

// IntentSchema is the version of the intent document this package reads.
const IntentSchema = 1

// Intent is the desired state of every managed fabric.
type Intent struct {
	Schema  int       `yaml:"schema" json:"schema"`
	Regions []*Region `yaml:"regions" json:"regions"`
}

// Region is one site and the single fabric it runs.
type Region struct {
	Name      string     `yaml:"name" json:"name"`
	Fabric    Settings   `yaml:"fabric" json:"fabric"`
	Numbering Numbering  `yaml:"numbering,omitempty" json:"numbering,omitzero"`
	Switches  []*Switch  `yaml:"switches" json:"switches"`
	Tenants   []*Tenant  `yaml:"tenants,omitempty" json:"tenants,omitempty"`
	Networks  []*Network `yaml:"networks,omitempty" json:"networks,omitempty"`
	// Attachments are the host ports the intent manages. Ports not listed
	// are left as they are.
	Attachments []*Attachment `yaml:"attachments,omitempty" json:"attachments,omitempty"`
}

// Settings are the fabric-wide overlay parameters of a region.
type Settings struct {
	ASN               ASN         `yaml:"asn" json:"asn"`
	AnycastGatewayMAC MAC         `yaml:"anycast_gateway_mac" json:"anycast_gateway_mac"`
	Replication       Replication `yaml:"replication,omitempty" json:"replication"`
	// SVIMTU is the MTU of tenant SVIs; zero leaves it unmanaged.
	SVIMTU int `yaml:"svi_mtu,omitempty" json:"svi_mtu,omitempty"`
	// SuppressARP enables ARP suppression on every L2 VNI of the region.
	SuppressARP bool `yaml:"suppress_arp,omitempty" json:"suppress_arp,omitempty"`
}

// Numbering derives identifiers the intent leaves out.
type Numbering struct {
	// L2VNIBase derives a network VNI as L2VNIBase + VLAN when the network
	// declares no VNI. Zero disables derivation.
	L2VNIBase VNI `yaml:"l2vni_base,omitempty" json:"l2vni_base,omitempty"`
}

// Switch is one managed Nexus switch.
type Switch struct {
	Name string     `yaml:"name" json:"name"`
	Mgmt netip.Addr `yaml:"mgmt" json:"mgmt"`
	Role Role       `yaml:"role" json:"role"`
	// VPCPeer names the other member of this switch's vPC pair, if any.
	VPCPeer string `yaml:"vpc_peer,omitempty" json:"vpc_peer,omitempty"`
	Model   string `yaml:"model,omitempty" json:"model,omitempty"`
}

// Tenant is a routed VRF stretched over the VTEPs of a region.
type Tenant struct {
	Name  string `yaml:"name" json:"name"`
	L3VNI VNI    `yaml:"l3vni" json:"l3vni"`
	// VLAN is the core VLAN that carries the L3VNI.
	VLAN VLAN `yaml:"vlan" json:"vlan"`
}

// Network is an L2 segment: a VLAN mapped to an L2VNI, optionally routed by
// an anycast gateway in a tenant.
type Network struct {
	Name   string `yaml:"name,omitempty" json:"name,omitempty"`
	VLAN   VLAN   `yaml:"vlan" json:"vlan"`
	VNI    VNI    `yaml:"vni,omitempty" json:"vni,omitempty"`
	Tenant string `yaml:"tenant,omitempty" json:"tenant,omitempty"`
	// Gateway is the anycast gateway address and prefix; unset means an
	// L2-only network.
	Gateway netip.Prefix `yaml:"gateway,omitempty" json:"gateway,omitzero"`
	ACLIn   string       `yaml:"acl_in,omitempty" json:"acl_in,omitempty"`
	// Switches restricts the network to these switches; empty means every
	// VTEP of the region.
	Switches []string `yaml:"switches,omitempty" json:"switches,omitempty"`
}

// Attachment is one host-facing port and the networks it carries.
type Attachment struct {
	Switch    string   `yaml:"switch" json:"switch"`
	Interface string   `yaml:"interface" json:"interface"`
	Mode      PortMode `yaml:"mode,omitempty" json:"mode"`
	// Networks lists VLANs carried by the port; an access port carries one.
	Networks []VLAN `yaml:"networks" json:"networks"`
}

// DecodeIntent strictly decodes a YAML intent document and validates it.
// Unknown fields are rejected.
func DecodeIntent(body []byte) (*Intent, error) {
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	var in Intent
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("fabric: decode intent: %w", err)
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return &in, nil
}

// EncodeIntent renders the intent as YAML.
func EncodeIntent(in *Intent) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(in); err != nil {
		return nil, fmt.Errorf("fabric: encode intent: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("fabric: encode intent: %w", err)
	}
	return buf.Bytes(), nil
}

// Region returns the named region.
func (in *Intent) Region(name string) (*Region, bool) {
	for _, r := range in.Regions {
		if r.Name == name {
			return r, true
		}
	}
	return nil, false
}

// Locate returns the switch and its region.
func (in *Intent) Locate(name string) (*Region, *Switch, bool) {
	for _, r := range in.Regions {
		if s, ok := r.Switch(name); ok {
			return r, s, true
		}
	}
	return nil, nil, false
}

// Switch returns the named switch of the region.
func (r *Region) Switch(name string) (*Switch, bool) {
	for _, s := range r.Switches {
		if s.Name == name {
			return s, true
		}
	}
	return nil, false
}

// Tenant returns the named tenant of the region.
func (r *Region) Tenant(name string) (*Tenant, bool) {
	for _, t := range r.Tenants {
		if t.Name == name {
			return t, true
		}
	}
	return nil, false
}

// Network returns the network on vlan.
func (r *Region) Network(vlan VLAN) (*Network, bool) {
	for _, n := range r.Networks {
		if n.VLAN == vlan {
			return n, true
		}
	}
	return nil, false
}

// VTEPs returns the leaf and border switches in declaration order.
func (r *Region) VTEPs() []*Switch {
	var out []*Switch
	for _, s := range r.Switches {
		if s.Role.VTEP() {
			out = append(out, s)
		}
	}
	return out
}

// ByRole returns the switches with role in declaration order.
func (r *Region) ByRole(role Role) []*Switch {
	var out []*Switch
	for _, s := range r.Switches {
		if s.Role == role {
			out = append(out, s)
		}
	}
	return out
}

// VNIOf returns the network's declared VNI or the one the numbering derives.
func (r *Region) VNIOf(n *Network) (VNI, bool) {
	if n.VNI != 0 {
		return n.VNI, true
	}
	if r.Numbering.L2VNIBase == 0 {
		return 0, false
	}
	v := r.Numbering.L2VNIBase + VNI(n.VLAN)
	return v, v.Valid()
}

// Carries reports whether the network is present on the switch.
func (r *Region) Carries(n *Network, s *Switch) bool {
	if !s.Role.VTEP() {
		return false
	}
	return len(n.Switches) == 0 || slices.Contains(n.Switches, s.Name)
}

// Hosts reports whether the tenant is present on the switch: every VTEP that
// carries one of its routed networks, or every VTEP when it has none.
func (r *Region) Hosts(t *Tenant, s *Switch) bool {
	if !s.Role.VTEP() {
		return false
	}
	routed := false
	for _, n := range r.Networks {
		if n.Tenant != t.Name {
			continue
		}
		routed = true
		if r.Carries(n, s) {
			return true
		}
	}
	return !routed || s.Role == RoleBorder
}

// Problem is one violated invariant of an intent.
type Problem struct {
	// Path locates the offending object, for example "regions[dc1].networks[100]".
	Path   string
	Detail string
}

func (p Problem) String() string { return p.Path + ": " + p.Detail }

// ValidationError lists every problem found in an intent.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return "fabric: invalid intent: " + e.Problems[0].String()
	}
	return fmt.Sprintf("fabric: invalid intent: %d problems, first: %s", len(e.Problems), e.Problems[0])
}

// ErrIntent is matched by every ValidationError.
var ErrIntent = errors.New("fabric: invalid intent")

// Is makes errors.Is(err, ErrIntent) true.
func (e *ValidationError) Is(target error) bool { return target == ErrIntent }
