package plan

import (
	"fmt"
	"maps"
	"net/netip"
	"reflect"
	"slices"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Underlay object kinds.
const (
	KindOSPF         Kind = "ospf"
	KindBGP          Kind = "bgp"
	KindInterface    Kind = "interface"
	KindNVE          Kind = "nve"
	KindBGPTemplate  Kind = "bgp_template"
	KindBGPNeighbor  Kind = "bgp_neighbor"
	KindVPCDomain    Kind = "vpc_domain"
	KindPortChannel  Kind = "port_channel"
	KindInterfaceRst Kind = "interface_reset"
)

// L3 is the managed part of a routed interface: a loopback or a fabric
// link. Zero fields are unmanaged when merged.
type L3 struct {
	Admin     device.AdminState    `json:"admin"`
	MTU       int                  `json:"mtu,omitempty"`
	Address   netip.Prefix         `json:"address,omitzero"`
	Secondary netip.Prefix         `json:"secondary,omitzero"`
	OSPF      device.InterfaceOSPF `json:"ospf,omitzero"`
	// Routed is "no switchport" on an Ethernet port.
	Routed bool `json:"routed,omitempty"`
}

func l3Of(i device.Interface) L3 {
	v := L3{Admin: i.Admin, MTU: i.MTU, Routed: i.Kind == device.KindEthernet && !i.Layer2}
	if p, ok := i.Primary(); ok {
		v.Address = p
	}
	if len(i.Secondary) > 0 {
		v.Secondary = i.Secondary[0]
	}
	if i.OSPF != nil {
		v.OSPF = *i.OSPF
	}
	return v
}

func mergeL3(have, want L3) L3 {
	if want.Admin != device.AdminDefault {
		have.Admin = want.Admin
	}
	if want.MTU != 0 {
		have.MTU = want.MTU
	}
	if want.Address.IsValid() {
		have.Address = want.Address
	}
	if want.Secondary.IsValid() {
		have.Secondary = want.Secondary
	}
	if want.OSPF.Process != "" {
		p2p := have.OSPF.PointToPoint || want.OSPF.PointToPoint
		have.OSPF = want.OSPF
		have.OSPF.PointToPoint = p2p
	}
	have.Routed = have.Routed || want.Routed
	return have
}

// InterfaceOp creates or changes a routed interface.
type InterfaceOp struct {
	Interface string `json:"interface"`
	Have      *L3    `json:"have,omitempty"`
	Want      *L3    `json:"want,omitempty"`
}

// Ref identifies the object.
func (o InterfaceOp) Ref() Ref { return Ref{KindInterface, o.Interface} }

// Action reports what the operation does.
func (o InterfaceOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o InterfaceOp) Apply(cfg *device.Config) error {
	if o.Want == nil {
		delete(cfg.Interfaces, o.Interface)
		return nil
	}
	i, ok := cfg.Interfaces[o.Interface]
	if ok {
		i = cloneInterface(i)
	} else {
		name, kind := device.CanonicalInterface(o.Interface)
		i = device.Interface{Name: name, Kind: kind}
	}
	w := *o.Want
	i.Admin, i.MTU = w.Admin, w.MTU
	i.Addresses, i.Secondary = nil, nil
	if w.Address.IsValid() {
		i.Addresses = []netip.Prefix{w.Address}
	}
	if w.Secondary.IsValid() {
		i.Secondary = []netip.Prefix{w.Secondary}
	}
	i.OSPF = nil
	if w.OSPF.Process != "" {
		ospf := w.OSPF
		i.OSPF = &ospf
	}
	if w.Routed {
		i.Layer2 = false
	}
	cfg.Interfaces[o.Interface] = i
	return nil
}

// Invert returns the operation that restores Have.
func (o InterfaceOp) Invert() Op {
	return InterfaceOp{Interface: o.Interface, Have: o.Want, Want: o.Have}
}

// Current reports whether cfg still holds the planned Have state.
func (o InterfaceOp) Current(cfg *device.Config) error {
	i, ok := cfg.Interfaces[o.Interface]
	return current(o.Ref(), o.Have, l3Of(i), ok)
}

// Holds reports whether cfg shows the Want state.
func (o InterfaceOp) Holds(cfg *device.Config) error {
	i, ok := cfg.Interfaces[o.Interface]
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && l3Of(i) == *o.Want)
}

// OSPFOp creates or changes an OSPF process.
type OSPFOp struct {
	Process string       `json:"process"`
	Have    *device.OSPF `json:"have,omitempty"`
	Want    *device.OSPF `json:"want,omitempty"`
}

// Ref identifies the object.
func (o OSPFOp) Ref() Ref { return Ref{KindOSPF, o.Process} }

// Action reports what the operation does.
func (o OSPFOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o OSPFOp) Apply(cfg *device.Config) error {
	if o.Want == nil {
		delete(cfg.OSPF, o.Process)
		return nil
	}
	cfg.OSPF[o.Process] = *o.Want
	return nil
}

// Invert returns the operation that restores Have.
func (o OSPFOp) Invert() Op { return OSPFOp{Process: o.Process, Have: o.Want, Want: o.Have} }

// Current reports whether cfg still holds the planned Have state.
func (o OSPFOp) Current(cfg *device.Config) error {
	got, ok := cfg.OSPF[o.Process]
	return current(o.Ref(), o.Have, got, ok)
}

// Holds reports whether cfg shows the Want state.
func (o OSPFOp) Holds(cfg *device.Config) error {
	got, ok := cfg.OSPF[o.Process]
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && reflect.DeepEqual(got, *o.Want))
}

// BGPProcess is the managed part of "router bgp".
type BGPProcess struct {
	ASN      fabric.ASN `json:"asn"`
	RouterID string     `json:"router_id,omitempty"`
}

// BGPOp creates the BGP process or sets its router-id.
type BGPOp struct {
	Have *BGPProcess `json:"have,omitempty"`
	Want *BGPProcess `json:"want,omitempty"`
}

// Ref identifies the object.
func (o BGPOp) Ref() Ref { return Ref{KindBGP, "process"} }

// Action reports what the operation does.
func (o BGPOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o BGPOp) Apply(cfg *device.Config) error {
	switch {
	case o.Want == nil:
		cfg.BGP = nil
	case cfg.BGP == nil:
		cfg.BGP = &device.BGP{ASN: o.Want.ASN, RouterID: o.Want.RouterID,
			Templates: map[string]device.BGPPeer{}, Neighbors: map[string]device.BGPPeer{}, VRFs: map[string]device.BGPVRF{}}
	case cfg.BGP.ASN != o.Want.ASN:
		return fmt.Errorf("%w: bgp %s runs as %s", ErrPrerequisite, o.Want.ASN, cfg.BGP.ASN)
	default:
		b := *cfg.BGP
		b.RouterID = o.Want.RouterID
		cfg.BGP = &b
	}
	return nil
}

// Invert returns the operation that restores Have.
func (o BGPOp) Invert() Op { return BGPOp{Have: o.Want, Want: o.Have} }

func bgpOf(cfg *device.Config) (BGPProcess, bool) {
	if cfg.BGP == nil {
		return BGPProcess{}, false
	}
	return BGPProcess{ASN: cfg.BGP.ASN, RouterID: cfg.BGP.RouterID}, true
}

// Current reports whether cfg still holds the planned Have state.
func (o BGPOp) Current(cfg *device.Config) error {
	got, ok := bgpOf(cfg)
	return current(o.Ref(), o.Have, got, ok)
}

// Holds reports whether cfg shows the Want state.
func (o BGPOp) Holds(cfg *device.Config) error {
	got, ok := bgpOf(cfg)
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && got == *o.Want)
}

// BGPPeerOp creates or changes a peer template or a neighbor.
type BGPPeerOp struct {
	Template bool            `json:"template,omitempty"`
	Key      string          `json:"key"`
	ASN      fabric.ASN      `json:"asn"`
	Have     *device.BGPPeer `json:"have,omitempty"`
	Want     *device.BGPPeer `json:"want,omitempty"`
}

// Ref identifies the object.
func (o BGPPeerOp) Ref() Ref {
	if o.Template {
		return Ref{KindBGPTemplate, o.Key}
	}
	return Ref{KindBGPNeighbor, o.Key}
}

// Action reports what the operation does.
func (o BGPPeerOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o BGPPeerOp) Apply(cfg *device.Config) error {
	if cfg.BGP == nil {
		return fmt.Errorf("%w: bgp process for %s", ErrPrerequisite, o.Ref())
	}
	b := *cfg.BGP
	if o.Template {
		b.Templates = maps.Clone(b.Templates)
	} else {
		b.Neighbors = maps.Clone(b.Neighbors)
	}
	cfg.BGP = &b
	t, _ := o.table(cfg)
	if o.Want == nil {
		delete(t, o.Key)
		return nil
	}
	t[o.Key] = *o.Want
	return nil
}

// Invert returns the operation that restores Have.
func (o BGPPeerOp) Invert() Op {
	return BGPPeerOp{Template: o.Template, Key: o.Key, ASN: o.ASN, Have: o.Want, Want: o.Have}
}

// Current reports whether cfg still holds the planned Have state.
func (o BGPPeerOp) Current(cfg *device.Config) error {
	t, err := o.table(cfg)
	if err != nil {
		return err
	}
	got, ok := t[o.Key]
	return current(o.Ref(), o.Have, got, ok)
}

// Holds reports whether cfg shows the Want state.
func (o BGPPeerOp) Holds(cfg *device.Config) error {
	t, err := o.table(cfg)
	if err != nil {
		return holds(o.Ref(), false)
	}
	got, ok := t[o.Key]
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && reflect.DeepEqual(got, *o.Want))
}

// mergePeer returns have changed to satisfy want's managed fields: remote
// AS, inherited template, update source, route-reflector client, extended
// communities, and the l2vpn evpn family.
func mergePeer(have, want device.BGPPeer) device.BGPPeer {
	out := have
	out.Families = slices.Clone(have.Families)
	if want.RemoteAS != 0 {
		out.RemoteAS = want.RemoteAS
	}
	if want.Inherit != "" {
		out.Inherit = want.Inherit
	}
	if want.UpdateSource != "" {
		out.UpdateSource = want.UpdateSource
	}
	out.RouteReflectorClient = out.RouteReflectorClient || want.RouteReflectorClient
	out.SendCommunityExtended = out.SendCommunityExtended || want.SendCommunityExtended
	for _, f := range want.Families {
		if !slices.Contains(out.Families, f) {
			out.Families = append(out.Families, f)
		}
	}
	return out
}

// NVEBase is the managed part of the NVE interface itself.
type NVEBase struct {
	Name                string `json:"name"`
	Up                  bool   `json:"up,omitempty"`
	SourceInterface     string `json:"source_interface,omitempty"`
	HostReachabilityBGP bool   `json:"host_reachability_bgp,omitempty"`
}

func nveOf(cfg *device.Config) (NVEBase, bool) {
	if cfg.NVE == nil {
		return NVEBase{}, false
	}
	n := cfg.NVE
	return NVEBase{Name: n.Name, Up: !n.Shutdown, SourceInterface: n.SourceInterface, HostReachabilityBGP: n.HostReachabilityBGP}, true
}

func mergeNVE(have, want NVEBase) NVEBase {
	have.Name = want.Name
	have.Up = have.Up || want.Up
	if want.SourceInterface != "" {
		have.SourceInterface = want.SourceInterface
	}
	have.HostReachabilityBGP = have.HostReachabilityBGP || want.HostReachabilityBGP
	return have
}

// NVEOp creates or changes the NVE interface.
type NVEOp struct {
	Have *NVEBase `json:"have,omitempty"`
	Want *NVEBase `json:"want,omitempty"`
}

// Ref identifies the object.
func (o NVEOp) Ref() Ref { return Ref{KindNVE, "nve"} }

// Action reports what the operation does.
func (o NVEOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o NVEOp) Apply(cfg *device.Config) error {
	if o.Want == nil {
		cfg.NVE = nil
		return nil
	}
	n := device.NVE{Members: map[fabric.VNI]device.NVEMember{}}
	if cfg.NVE != nil {
		n = *cfg.NVE
	}
	n.Name, n.Shutdown = o.Want.Name, !o.Want.Up
	n.SourceInterface, n.HostReachabilityBGP = o.Want.SourceInterface, o.Want.HostReachabilityBGP
	cfg.NVE = &n
	return nil
}

// Invert returns the operation that restores Have.
func (o NVEOp) Invert() Op { return NVEOp{Have: o.Want, Want: o.Have} }

// Current reports whether cfg still holds the planned Have state.
func (o NVEOp) Current(cfg *device.Config) error {
	got, ok := nveOf(cfg)
	return current(o.Ref(), o.Have, got, ok)
}

// Holds reports whether cfg shows the Want state.
func (o NVEOp) Holds(cfg *device.Config) error {
	got, ok := nveOf(cfg)
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && got == *o.Want)
}

// VPCBase is the managed part of a vPC domain; boolean options are
// settings.
type VPCBase struct {
	ID                    int    `json:"id"`
	KeepaliveDest         string `json:"keepalive_destination,omitempty"`
	KeepaliveSource       string `json:"keepalive_source,omitempty"`
	VirtualPeerLink       string `json:"virtual_peer_link_destination,omitempty"`
	VirtualPeerLinkSource string `json:"virtual_peer_link_source,omitempty"`
	VirtualPeerLinkDSCP   int    `json:"virtual_peer_link_dscp,omitempty"`
}

func vpcOf(cfg *device.Config) (VPCBase, bool) {
	d := cfg.VPC
	if d == nil {
		return VPCBase{}, false
	}
	return VPCBase{ID: d.ID, KeepaliveDest: d.KeepaliveDest, KeepaliveSource: d.KeepaliveSource,
		VirtualPeerLink: d.VirtualPeerLink, VirtualPeerLinkSource: d.VirtualPeerLinkSource, VirtualPeerLinkDSCP: d.VirtualPeerLinkDSCP}, true
}

func mergeVPC(have, want VPCBase) VPCBase {
	have.ID = want.ID
	if want.KeepaliveDest != "" {
		have.KeepaliveDest, have.KeepaliveSource = want.KeepaliveDest, want.KeepaliveSource
	}
	if want.VirtualPeerLink != "" {
		have.VirtualPeerLink, have.VirtualPeerLinkSource = want.VirtualPeerLink, want.VirtualPeerLinkSource
		if want.VirtualPeerLinkDSCP != 0 {
			have.VirtualPeerLinkDSCP = want.VirtualPeerLinkDSCP
		}
	}
	return have
}

// VPCDomainOp creates or changes the vPC domain.
type VPCDomainOp struct {
	Have *VPCBase `json:"have,omitempty"`
	Want *VPCBase `json:"want,omitempty"`
}

// Ref identifies the object.
func (o VPCDomainOp) Ref() Ref { return Ref{KindVPCDomain, "vpc"} }

// Action reports what the operation does.
func (o VPCDomainOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o VPCDomainOp) Apply(cfg *device.Config) error {
	if o.Want == nil {
		cfg.VPC = nil
		return nil
	}
	d := device.VPCDomain{}
	if cfg.VPC != nil {
		d = *cfg.VPC
	}
	w := *o.Want
	d.ID, d.KeepaliveDest, d.KeepaliveSource = w.ID, w.KeepaliveDest, w.KeepaliveSource
	d.VirtualPeerLink, d.VirtualPeerLinkSource, d.VirtualPeerLinkDSCP = w.VirtualPeerLink, w.VirtualPeerLinkSource, w.VirtualPeerLinkDSCP
	cfg.VPC = &d
	return nil
}

// Invert returns the operation that restores Have.
func (o VPCDomainOp) Invert() Op { return VPCDomainOp{Have: o.Want, Want: o.Have} }

// Current reports whether cfg still holds the planned Have state.
func (o VPCDomainOp) Current(cfg *device.Config) error {
	got, ok := vpcOf(cfg)
	return current(o.Ref(), o.Have, got, ok)
}

// Holds reports whether cfg shows the Want state.
func (o VPCDomainOp) Holds(cfg *device.Config) error {
	got, ok := vpcOf(cfg)
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && got == *o.Want)
}

// ResetOp returns a host port to its factory state ("default interface")
// and shuts it, or removes a port-channel; its inverse restores the saved
// configuration as far as the model describes it.
type ResetOp struct {
	Interface string           `json:"interface"`
	Saved     device.Interface `json:"saved"`
	Restore   bool             `json:"restore,omitempty"`
}

// Ref identifies the object.
func (o ResetOp) Ref() Ref { return Ref{KindInterfaceRst, o.Interface} }

// Action reports what the operation does.
func (o ResetOp) Action() Action {
	if o.Restore {
		return ActionCreate
	}
	return ActionDelete
}

// Apply changes the model as the rendered commands change the device.
func (o ResetOp) Apply(cfg *device.Config) error {
	if o.Restore {
		cfg.Interfaces[o.Interface] = o.Saved
		return nil
	}
	if b, ok := o.blank(); ok {
		cfg.Interfaces[o.Interface] = b
	} else {
		delete(cfg.Interfaces, o.Interface)
	}
	return nil
}

// Invert returns the operation that restores Have.
func (o ResetOp) Invert() Op {
	return ResetOp{Interface: o.Interface, Saved: o.Saved, Restore: !o.Restore}
}

// Current reports whether cfg still holds the planned Have state.
func (o ResetOp) Current(cfg *device.Config) error {
	got, ok := cfg.Interfaces[o.Interface]
	if !o.Restore {
		return holds(o.Ref(), ok && reflect.DeepEqual(got, o.Saved))
	}
	b, keep := o.blank()
	return holds(o.Ref(), ok == keep && (!keep || reflect.DeepEqual(got, b)))
}

// Holds reports whether cfg shows the Want state.
func (o ResetOp) Holds(cfg *device.Config) error {
	return ResetOp{Interface: o.Interface, Saved: o.Saved, Restore: !o.Restore}.Current(cfg)
}

func (o BGPPeerOp) table(cfg *device.Config) (map[string]device.BGPPeer, error) {
	if cfg.BGP == nil {
		return nil, fmt.Errorf("%w: bgp process for %s", ErrPrerequisite, o.Ref())
	}
	if o.Template {
		return cfg.BGP.Templates, nil
	}
	return cfg.BGP.Neighbors, nil
}

func (o ResetOp) blank() (device.Interface, bool) {
	if o.Saved.Kind == device.KindPortChannel {
		return device.Interface{}, false
	}
	return device.Interface{Name: o.Saved.Name, Kind: o.Saved.Kind, Admin: device.AdminDown}, true
}
