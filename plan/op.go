package plan

import (
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"

	"github.com/gatework/netops/set"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Errors returned when an operation does not fit a configuration.
var (
	// ErrStale means the object no longer matches the state planned against.
	ErrStale = errors.New("plan: object changed since planning")
	// ErrUnsatisfied means a readback does not show the target state.
	ErrUnsatisfied = errors.New("plan: target state not reached")
	// ErrPrerequisite means the configuration lacks an object the
	// operation needs, such as the NVE interface or the BGP process.
	ErrPrerequisite = errors.New("plan: prerequisite missing")
)

// Kind is the type of managed object an operation changes.
type Kind string

// Managed object kinds, in creation order.
const (
	KindFeature   Kind = "feature"
	KindSetting   Kind = "setting"
	KindNTPServer Kind = "ntp_server"
	KindLogServer Kind = "logging_server"
	KindVRF       Kind = "vrf"
	KindVLAN      Kind = "vlan"
	KindEVPN      Kind = "evpn"
	KindNVEMember Kind = "nve_member"
	KindSVI       Kind = "svi"
	KindBGPVRF    Kind = "bgp_vrf"
	KindPort      Kind = "port"
)

// Kinds lists every kind in creation order; deletion runs in reverse.
// Settings come late because many live on objects created before them
// (BGP options, vPC options, NVE options).
func Kinds() []Kind {
	return []Kind{KindFeature, KindNTPServer, KindLogServer, KindOSPF, KindBGP, KindVRF, KindVLAN,
		KindEVPN, KindInterface, KindNVE, KindNVEMember, KindSVI, KindBGPTemplate, KindBGPNeighbor,
		KindBGPVRF, KindVPCDomain, KindSetting, KindInterfaceRst, KindPortChannel, KindPort}
}

func (k Kind) rank() int { return slices.Index(Kinds(), k) }

// Action is what an operation does to its object.
type Action uint8

// Actions.
const (
	ActionCreate Action = iota + 1
	ActionUpdate
	ActionDelete
)

// String returns "create", "update" or "delete".
func (a Action) String() string {
	switch a {
	case ActionCreate:
		return "create"
	case ActionUpdate:
		return "update"
	case ActionDelete:
		return "delete"
	default:
		return "unknown"
	}
}

// Ref identifies a managed object on a switch.
type Ref struct {
	Kind Kind   `json:"kind"`
	Key  string `json:"key"`
}

func (r Ref) String() string { return string(r.Kind) + ":" + r.Key }

// Op is one change to one managed object. Have is the object's state when
// planned (nil when absent) and Want its target (nil to delete).
type Op interface {
	Ref() Ref
	Action() Action
	// Apply changes the model as the rendered commands change the device.
	Apply(cfg *device.Config) error
	// Invert returns the operation that restores Have.
	Invert() Op
	// Current reports whether cfg still holds the planned Have state.
	Current(cfg *device.Config) error
	// Holds reports whether cfg shows the Want state.
	Holds(cfg *device.Config) error
}

func action[T any](have, want *T) Action {
	switch {
	case have == nil:
		return ActionCreate
	case want == nil:
		return ActionDelete
	default:
		return ActionUpdate
	}
}

// current compares the observed object with the planned Have exactly.
func current[T any](ref Ref, have *T, got T, present bool) error {
	switch {
	case have == nil && !present:
		return nil
	case have != nil && present && reflect.DeepEqual(*have, got):
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrStale, ref)
	}
}

func holds(ref Ref, ok bool) error {
	if ok {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrUnsatisfied, ref)
}

// FeatureOp enables or disables a feature.
type FeatureOp struct {
	Name   string `json:"name"`
	Enable bool   `json:"enable"`
}

// Ref identifies the object.
func (o FeatureOp) Ref() Ref { return Ref{KindFeature, o.Name} }

// Action reports what the operation does.
func (o FeatureOp) Action() Action {
	if o.Enable {
		return ActionCreate
	}
	return ActionDelete
}

// Apply changes the model as the rendered commands change the device.
func (o FeatureOp) Apply(cfg *device.Config) error {
	if o.Enable {
		cfg.Features[o.Name] = true
	} else {
		delete(cfg.Features, o.Name)
	}
	return nil
}

// Invert returns the operation that restores Have.
func (o FeatureOp) Invert() Op { return FeatureOp{Name: o.Name, Enable: !o.Enable} }

// Current reports whether cfg still holds the planned Have state.
func (o FeatureOp) Current(cfg *device.Config) error {
	if cfg.Features[o.Name] == o.Enable {
		return fmt.Errorf("%w: %s", ErrStale, o.Ref())
	}
	return nil
}

// Holds reports whether cfg shows the Want state.
func (o FeatureOp) Holds(cfg *device.Config) error {
	return holds(o.Ref(), cfg.Features[o.Name] == o.Enable)
}

// SettingOp changes one scalar setting.
type SettingOp struct {
	Setting device.Setting `json:"setting"`
	// Scope names the object the setting lives on when rendering needs it:
	// the BGP ASN, the vPC domain or the NVE interface.
	Scope string `json:"scope,omitempty"`
	Have  string `json:"have"`
	Want  string `json:"want"`
}

// Ref identifies the object.
func (o SettingOp) Ref() Ref { return Ref{KindSetting, string(o.Setting)} }

// Action reports what the operation does.
func (o SettingOp) Action() Action {
	switch {
	case o.Have == "":
		return ActionCreate
	case o.Want == "":
		return ActionDelete
	default:
		return ActionUpdate
	}
}

// Apply changes the model as the rendered commands change the device.
func (o SettingOp) Apply(cfg *device.Config) error {
	if !cfg.Applicable(o.Setting) {
		return fmt.Errorf("%w: %s", ErrPrerequisite, o.Ref())
	}
	cfg.Set(o.Setting, o.Want)
	return nil
}

// Invert returns the operation that restores Have.
func (o SettingOp) Invert() Op {
	return SettingOp{Setting: o.Setting, Scope: o.Scope, Have: o.Want, Want: o.Have}
}

// Current reports whether cfg still holds the planned Have state.
func (o SettingOp) Current(cfg *device.Config) error {
	if !cfg.Applicable(o.Setting) {
		return fmt.Errorf("%w: %s", ErrPrerequisite, o.Ref())
	}
	return holds(o.Ref(), cfg.Get(o.Setting) == o.Have)
}

// Holds reports whether cfg shows the Want state.
func (o SettingOp) Holds(cfg *device.Config) error {
	return holds(o.Ref(), cfg.Get(o.Setting) == o.Want)
}

// NTPServerOp adds, changes or removes an NTP server.
type NTPServerOp struct {
	Address string            `json:"address"`
	Have    *device.NTPServer `json:"have,omitempty"`
	Want    *device.NTPServer `json:"want,omitempty"`
}

// Ref identifies the object.
func (o NTPServerOp) Ref() Ref { return Ref{KindNTPServer, o.Address} }

// Action reports what the operation does.
func (o NTPServerOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o NTPServerOp) Apply(cfg *device.Config) error {
	s := &cfg.Management.NTP
	s.Servers = slices.DeleteFunc(slices.Clone(s.Servers), func(x device.NTPServer) bool { return x.Address == o.Address })
	if o.Want != nil {
		s.Servers = append(s.Servers, *o.Want)
	}
	if len(s.Servers) == 0 {
		s.Servers = nil
	}
	return nil
}

// Invert returns the operation that restores Have.
func (o NTPServerOp) Invert() Op { return NTPServerOp{Address: o.Address, Have: o.Want, Want: o.Have} }

// Current reports whether cfg still holds the planned Have state.
func (o NTPServerOp) Current(cfg *device.Config) error {
	got, ok := cfg.Management.NTP.Server(o.Address)
	return current(o.Ref(), o.Have, got, ok)
}

// Holds reports whether cfg shows the Want state.
func (o NTPServerOp) Holds(cfg *device.Config) error {
	got, ok := cfg.Management.NTP.Server(o.Address)
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && got == *o.Want)
}

// LogServerOp adds, changes or removes a syslog server.
type LogServerOp struct {
	Address string            `json:"address"`
	Have    *device.LogServer `json:"have,omitempty"`
	Want    *device.LogServer `json:"want,omitempty"`
}

// Ref identifies the object.
func (o LogServerOp) Ref() Ref { return Ref{KindLogServer, o.Address} }

// Action reports what the operation does.
func (o LogServerOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o LogServerOp) Apply(cfg *device.Config) error {
	l := &cfg.Management.Logging
	l.Servers = slices.DeleteFunc(slices.Clone(l.Servers), func(x device.LogServer) bool { return x.Address == o.Address })
	if o.Want != nil {
		l.Servers = append(l.Servers, *o.Want)
	}
	if len(l.Servers) == 0 {
		l.Servers = nil
	}
	return nil
}

// Invert returns the operation that restores Have.
func (o LogServerOp) Invert() Op { return LogServerOp{Address: o.Address, Have: o.Want, Want: o.Have} }

// Current reports whether cfg still holds the planned Have state.
func (o LogServerOp) Current(cfg *device.Config) error {
	got, ok := cfg.Management.Logging.Server(o.Address)
	return current(o.Ref(), o.Have, got, ok)
}

// Holds reports whether cfg shows the Want state.
func (o LogServerOp) Holds(cfg *device.Config) error {
	got, ok := cfg.Management.Logging.Server(o.Address)
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && got == *o.Want)
}

// VRFOp creates, extends or removes a tenant VRF context.
type VRFOp struct {
	Name string      `json:"name"`
	Have *device.VRF `json:"have,omitempty"`
	Want *device.VRF `json:"want,omitempty"`
}

// Ref identifies the object.
func (o VRFOp) Ref() Ref { return Ref{KindVRF, o.Name} }

// Action reports what the operation does.
func (o VRFOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o VRFOp) Apply(cfg *device.Config) error {
	if o.Want == nil {
		delete(cfg.VRFs, o.Name)
		return nil
	}
	cfg.VRFs[o.Name] = *o.Want
	return nil
}

// Invert returns the operation that restores Have.
func (o VRFOp) Invert() Op { return VRFOp{Name: o.Name, Have: o.Want, Want: o.Have} }

// Current reports whether cfg still holds the planned Have state.
func (o VRFOp) Current(cfg *device.Config) error {
	got, ok := cfg.VRFs[o.Name]
	return current(o.Ref(), o.Have, got, ok)
}

// Holds reports whether cfg shows the Want state.
func (o VRFOp) Holds(cfg *device.Config) error {
	got, ok := cfg.VRFs[o.Name]
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && reflect.DeepEqual(got, *o.Want))
}

// VLANOp creates, changes or removes a VLAN and its VNI mapping.
type VLANOp struct {
	VLAN fabric.VLAN  `json:"vlan"`
	Have *device.VLAN `json:"have,omitempty"`
	Want *device.VLAN `json:"want,omitempty"`
}

// Ref identifies the object.
func (o VLANOp) Ref() Ref { return Ref{KindVLAN, o.VLAN.String()} }

// Action reports what the operation does.
func (o VLANOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o VLANOp) Apply(cfg *device.Config) error {
	if o.Want == nil {
		delete(cfg.VLANs, o.VLAN)
		return nil
	}
	cfg.VLANs[o.VLAN] = *o.Want
	return nil
}

// Invert returns the operation that restores Have.
func (o VLANOp) Invert() Op { return VLANOp{VLAN: o.VLAN, Have: o.Want, Want: o.Have} }

// Current reports whether cfg still holds the planned Have state.
func (o VLANOp) Current(cfg *device.Config) error {
	got, ok := cfg.VLANs[o.VLAN]
	return current(o.Ref(), o.Have, got, ok)
}

// Holds reports whether cfg shows the Want state.
func (o VLANOp) Holds(cfg *device.Config) error {
	got, ok := cfg.VLANs[o.VLAN]
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && got == *o.Want)
}

// EVPNOp creates or removes an L2 EVPN instance.
type EVPNOp struct {
	VNI  fabric.VNI      `json:"vni"`
	Have *device.EVPNVNI `json:"have,omitempty"`
	Want *device.EVPNVNI `json:"want,omitempty"`
}

// Ref identifies the object.
func (o EVPNOp) Ref() Ref { return Ref{KindEVPN, o.VNI.String()} }

// Action reports what the operation does.
func (o EVPNOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o EVPNOp) Apply(cfg *device.Config) error {
	if o.Want == nil {
		delete(cfg.EVPN, o.VNI)
		return nil
	}
	cfg.EVPN[o.VNI] = *o.Want
	return nil
}

// Invert returns the operation that restores Have.
func (o EVPNOp) Invert() Op { return EVPNOp{VNI: o.VNI, Have: o.Want, Want: o.Have} }

// Current reports whether cfg still holds the planned Have state.
func (o EVPNOp) Current(cfg *device.Config) error {
	got, ok := cfg.EVPN[o.VNI]
	return current(o.Ref(), o.Have, got, ok)
}

// Holds reports whether cfg shows the Want state.
func (o EVPNOp) Holds(cfg *device.Config) error {
	got, ok := cfg.EVPN[o.VNI]
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && got.Equal(*o.Want))
}

// NVEMemberOp adds, changes or removes an NVE member VNI.
type NVEMemberOp struct {
	VNI  fabric.VNI        `json:"vni"`
	NVE  string            `json:"nve"`
	Have *device.NVEMember `json:"have,omitempty"`
	Want *device.NVEMember `json:"want,omitempty"`
}

// Ref identifies the object.
func (o NVEMemberOp) Ref() Ref { return Ref{KindNVEMember, o.VNI.String()} }

// Action reports what the operation does.
func (o NVEMemberOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o NVEMemberOp) Apply(cfg *device.Config) error {
	if cfg.NVE == nil {
		return fmt.Errorf("%w: nve interface for %s", ErrPrerequisite, o.Ref())
	}
	if o.Want == nil {
		delete(cfg.NVE.Members, o.VNI)
		return nil
	}
	cfg.NVE.Members[o.VNI] = *o.Want
	return nil
}

// Invert returns the operation that restores Have.
func (o NVEMemberOp) Invert() Op {
	return NVEMemberOp{VNI: o.VNI, NVE: o.NVE, Have: o.Want, Want: o.Have}
}

// Current reports whether cfg still holds the planned Have state.
func (o NVEMemberOp) Current(cfg *device.Config) error {
	if cfg.NVE == nil {
		return fmt.Errorf("%w: nve interface for %s", ErrPrerequisite, o.Ref())
	}
	got, ok := cfg.NVE.Members[o.VNI]
	return current(o.Ref(), o.Have, got, ok)
}

// Holds reports whether cfg shows the Want state.
func (o NVEMemberOp) Holds(cfg *device.Config) error {
	if cfg.NVE == nil {
		return holds(o.Ref(), false)
	}
	got, ok := cfg.NVE.Members[o.VNI]
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && got == *o.Want)
}

// SVI is the managed part of a VLAN interface.
type SVI struct {
	Admin     device.AdminState `json:"admin"`
	MTU       int               `json:"mtu,omitempty"`
	VRF       string            `json:"vrf,omitempty"`
	Address   netip.Prefix      `json:"address,omitzero"`
	Anycast   bool              `json:"anycast,omitempty"`
	IPForward bool              `json:"ip_forward,omitempty"`
	ACLIn     string            `json:"acl_in,omitempty"`
}

// sviOf extracts the managed view of an interface.
func sviOf(i device.Interface) SVI {
	s := SVI{Admin: i.Admin, MTU: i.MTU, VRF: i.VRF, Anycast: i.Anycast, IPForward: i.IPForward, ACLIn: i.ACLIn}
	if p, ok := i.Primary(); ok {
		s.Address = p
	}
	return s
}

// SVIOp creates, changes or removes a VLAN interface.
type SVIOp struct {
	VLAN fabric.VLAN `json:"vlan"`
	Have *SVI        `json:"have,omitempty"`
	Want *SVI        `json:"want,omitempty"`
}

// Ref identifies the object.
func (o SVIOp) Ref() Ref { return Ref{KindSVI, o.VLAN.String()} }

// Action reports what the operation does.
func (o SVIOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o SVIOp) Apply(cfg *device.Config) error {
	name := o.VLAN.SVI()
	if o.Want == nil {
		delete(cfg.Interfaces, name)
		return nil
	}
	i, ok := cfg.Interfaces[name]
	if !ok {
		i = device.Interface{Name: name, Kind: device.KindSVI}
	} else {
		i = cloneInterface(i)
	}
	applySVI(&i, *o.Want)
	cfg.Interfaces[name] = i
	return nil
}

// Invert returns the operation that restores Have.
func (o SVIOp) Invert() Op { return SVIOp{VLAN: o.VLAN, Have: o.Want, Want: o.Have} }

// Current reports whether cfg still holds the planned Have state.
func (o SVIOp) Current(cfg *device.Config) error {
	i, ok := cfg.Interfaces[o.VLAN.SVI()]
	return current(o.Ref(), o.Have, sviOf(i), ok)
}

// Holds reports whether cfg shows the Want state.
func (o SVIOp) Holds(cfg *device.Config) error {
	i, ok := cfg.Interfaces[o.VLAN.SVI()]
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && sviOf(i) == *o.Want)
}

// applySVI makes the interface show exactly the managed view want.
func applySVI(i *device.Interface, want SVI) {
	if want.VRF != i.VRF {
		// Changing the VRF removes layer-3 configuration on NX-OS.
		i.Addresses, i.Secondary = nil, nil
	}
	i.Admin, i.MTU, i.VRF = want.Admin, want.MTU, want.VRF
	i.Addresses = nil
	if want.Address.IsValid() {
		i.Addresses = []netip.Prefix{want.Address}
	}
	i.Anycast, i.IPForward, i.ACLIn = want.Anycast, want.IPForward, want.ACLIn
}

func cloneInterface(i device.Interface) device.Interface {
	i.Addresses = slices.Clone(i.Addresses)
	i.Secondary = slices.Clone(i.Secondary)
	return i
}

// BGPVRFOp enables EVPN advertisement for a VRF under the BGP process.
type BGPVRFOp struct {
	ASN  fabric.ASN     `json:"asn"`
	VRF  string         `json:"vrf"`
	Have *device.BGPVRF `json:"have,omitempty"`
	Want *device.BGPVRF `json:"want,omitempty"`
}

// Ref identifies the object.
func (o BGPVRFOp) Ref() Ref { return Ref{KindBGPVRF, o.VRF} }

// Action reports what the operation does.
func (o BGPVRFOp) Action() Action { return action(o.Have, o.Want) }

// Apply changes the model as the rendered commands change the device.
func (o BGPVRFOp) Apply(cfg *device.Config) error {
	if cfg.BGP == nil || cfg.BGP.ASN != o.ASN {
		return fmt.Errorf("%w: bgp %s for %s", ErrPrerequisite, o.ASN, o.Ref())
	}
	if o.Want == nil {
		delete(cfg.BGP.VRFs, o.VRF)
		return nil
	}
	cfg.BGP.VRFs[o.VRF] = *o.Want
	return nil
}

// Invert returns the operation that restores Have.
func (o BGPVRFOp) Invert() Op { return BGPVRFOp{ASN: o.ASN, VRF: o.VRF, Have: o.Want, Want: o.Have} }

// Current reports whether cfg still holds the planned Have state.
func (o BGPVRFOp) Current(cfg *device.Config) error {
	if cfg.BGP == nil || cfg.BGP.ASN != o.ASN {
		return fmt.Errorf("%w: bgp %s for %s", ErrPrerequisite, o.ASN, o.Ref())
	}
	got, ok := cfg.BGP.VRFs[o.VRF]
	return current(o.Ref(), o.Have, got, ok)
}

// Holds reports whether cfg shows the Want state.
func (o BGPVRFOp) Holds(cfg *device.Config) error {
	if cfg.BGP == nil {
		return holds(o.Ref(), false)
	}
	got, ok := cfg.BGP.VRFs[o.VRF]
	if o.Want == nil {
		return holds(o.Ref(), !ok)
	}
	return holds(o.Ref(), ok && reflect.DeepEqual(got, *o.Want))
}

// Port is the managed part of a host-facing layer-2 port or bundle.
type Port struct {
	Mode   fabric.PortMode `json:"mode"`
	Access fabric.VLAN     `json:"access,omitempty"`
	// Allowed is the trunk allowed list; nil means all VLANs.
	Allowed      *set.Set[uint16]  `json:"allowed,omitempty"`
	Admin        device.AdminState `json:"admin,omitempty"`
	Description  string            `json:"description,omitempty"`
	MTU          int               `json:"mtu,omitempty"`
	STPPortType  string            `json:"stp_port_type,omitempty"`
	ChannelGroup int               `json:"channel_group,omitempty"`
	ChannelMode  string            `json:"channel_mode,omitempty"`
	VPC          int               `json:"vpc,omitempty"`
	PeerLink     bool              `json:"peer_link,omitempty"`
}

func portOf(i device.Interface) Port {
	p := Port{Mode: i.Mode, Access: i.AccessVLAN, Admin: i.Admin, Description: i.Description, MTU: i.MTU,
		STPPortType: i.STPPortType, ChannelGroup: i.ChannelGroup, ChannelMode: i.ChannelMode, VPC: i.VPC, PeerLink: i.PeerLink}
	if i.Mode == fabric.PortTrunk {
		p.Access = 0
		if i.TrunkAllowed != nil {
			p.Allowed = i.TrunkAllowed.Clone()
		}
	}
	return p
}

func applyPort(i *device.Interface, p Port) {
	i.Layer2, i.Mode = true, p.Mode
	if p.Mode == fabric.PortAccess {
		i.AccessVLAN = p.Access
	} else if p.Allowed != nil {
		i.TrunkAllowed = p.Allowed.Clone()
	} else {
		i.TrunkAllowed = nil
	}
	i.Admin, i.Description, i.MTU, i.STPPortType = p.Admin, p.Description, p.MTU, p.STPPortType
	i.ChannelGroup, i.ChannelMode, i.VPC, i.PeerLink = p.ChannelGroup, p.ChannelMode, p.VPC, p.PeerLink
}

// PortOp changes how an interface carries networks. Have nil means the
// interface is not a layer-2 port; Want nil turns it back into one that
// is not (the inverse of converting it).
type PortOp struct {
	Interface string `json:"interface"`
	Have      *Port  `json:"have,omitempty"`
	Want      *Port  `json:"want,omitempty"`
}

// Ref identifies the object.
func (o PortOp) Ref() Ref {
	if _, kind := device.CanonicalInterface(o.Interface); kind == device.KindPortChannel {
		return Ref{KindPortChannel, o.Interface}
	}
	return Ref{KindPort, o.Interface}
}

// Action reports what the operation does.
func (o PortOp) Action() Action {
	if o.Have == nil || o.Want == nil {
		return ActionCreate
	}
	return ActionUpdate
}

// Apply changes the model as the rendered commands change the device.
func (o PortOp) Apply(cfg *device.Config) error {
	i, ok := cfg.Interfaces[o.Interface]
	name, kind := device.CanonicalInterface(o.Interface)
	switch {
	case ok:
		i = cloneInterface(i)
	case kind == device.KindPortChannel && o.Want != nil:
		i = device.Interface{Name: name, Kind: kind}
	default:
		return fmt.Errorf("%w: interface %s", ErrPrerequisite, o.Interface)
	}
	if o.Want == nil {
		if kind == device.KindPortChannel && o.Have != nil {
			delete(cfg.Interfaces, o.Interface)
			return nil
		}
		i.Layer2, i.Mode, i.AccessVLAN, i.TrunkAllowed = false, fabric.PortAccess, 0, nil
		cfg.Interfaces[o.Interface] = i
		return nil
	}
	applyPort(&i, *o.Want)
	cfg.Interfaces[o.Interface] = i
	return nil
}

// Invert returns the operation that restores Have.
func (o PortOp) Invert() Op { return PortOp{Interface: o.Interface, Have: o.Want, Want: o.Have} }

// Current reports whether cfg still holds the planned Have state.
func (o PortOp) Current(cfg *device.Config) error {
	i, ok := cfg.Interfaces[o.Interface]
	if !ok {
		if _, kind := device.CanonicalInterface(o.Interface); kind == device.KindPortChannel && o.Have == nil {
			return nil
		}
		return fmt.Errorf("%w: interface %s", ErrPrerequisite, o.Interface)
	}
	if o.Have == nil {
		return holds(o.Ref(), !i.Layer2)
	}
	return holds(o.Ref(), i.Layer2 && portEqual(portOf(i), *o.Have))
}
func portEqual(a, b Port) bool {
	x, y := a, b
	x.Allowed, y.Allowed = nil, nil
	if x != y {
		return false
	}
	if a.Allowed == nil || b.Allowed == nil {
		return a.Allowed == nil && b.Allowed == nil
	}
	return a.Allowed.Equal(b.Allowed)
}

// Holds reports whether cfg shows the Want state.
func (o PortOp) Holds(cfg *device.Config) error {
	i, ok := cfg.Interfaces[o.Interface]
	if o.Want == nil {
		return holds(o.Ref(), ok && !i.Layer2)
	}
	return holds(o.Ref(), ok && i.Layer2 && portEqual(portOf(i), *o.Want))
}
