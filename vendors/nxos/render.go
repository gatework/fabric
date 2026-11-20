package nxos

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gatework/netops/set"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/plan"
)

// ErrUnrenderable reports an operation NX-OS cannot express.
var ErrUnrenderable = errors.New("nxos: operation cannot be rendered")

// Renderer renders plan operations as NX-OS configuration commands. It
// implements plan.Renderer.
type Renderer struct{}

var _ plan.Renderer = Renderer{}

// script collects commands with their configuration depth.
type script struct {
	lines []line
}

type line struct {
	depth int
	text  string
}

func (s *script) add(depth int, format string, args ...any) {
	s.lines = append(s.lines, line{depth, fmt.Sprintf(format, args...)})
}

// Render returns the commands for ops, wrapped in configuration mode.
func (Renderer) Render(ops []plan.Op) ([]string, error) {
	s, err := render(ops)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(s.lines)+2)
	out = append(out, "configure terminal")
	for _, l := range s.lines {
		out = append(out, l.text)
	}
	return append(out, "end"), nil
}

// RenderConfig returns ops as indented configuration text, the form an
// operator reviews and the form ParseConfig reads back.
func RenderConfig(ops []plan.Op) (string, error) {
	s, err := render(ops)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	top := ""
	for i, l := range s.lines {
		// Consecutive blocks under the same top-level context (several
		// EVPN instances, several BGP neighbors) read as one block.
		if l.depth == 0 {
			opens := i+1 < len(s.lines) && s.lines[i+1].depth > 0
			if opens && l.text == top {
				continue
			}
			top = ""
			if opens {
				top = l.text
			}
		}
		_, _ = b.WriteString(strings.Repeat("  ", l.depth))
		_, _ = b.WriteString(l.text)
		_ = b.WriteByte('\n')
	}
	return b.String(), nil
}

func render(ops []plan.Op) (*script, error) {
	s := &script{}
	for _, op := range ops {
		if err := renderOp(s, op); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func renderOp(s *script, op plan.Op) error {
	switch o := op.(type) {
	case plan.FeatureOp:
		renderFeature(s, o)
	case plan.SettingOp:
		return renderSetting(s, o)
	case plan.NTPServerOp:
		renderNTP(s, o)
	case plan.LogServerOp:
		renderLogServer(s, o)
	case plan.VRFOp:
		renderVRF(s, o)
	case plan.VLANOp:
		renderVLAN(s, o)
	case plan.EVPNOp:
		renderEVPN(s, o)
	case plan.NVEMemberOp:
		renderMember(s, o)
	case plan.SVIOp:
		renderSVI(s, o)
	case plan.BGPVRFOp:
		renderBGPVRF(s, o)
	case plan.PortOp:
		renderPort(s, o)
	case plan.InterfaceOp:
		renderInterface(s, o)
	case plan.OSPFOp:
		renderOSPF(s, o)
	case plan.BGPOp:
		renderBGP(s, o)
	case plan.BGPPeerOp:
		renderPeer(s, o)
	case plan.NVEOp:
		renderNVE(s, o)
	case plan.VPCDomainOp:
		renderVPCDomain(s, o)
	case plan.ResetOp:
		renderReset(s, o)
	default:
		return fmt.Errorf("%w: %T", ErrUnrenderable, op)
	}
	return nil
}

func renderFeature(s *script, o plan.FeatureOp) {
	cmd := "feature " + o.Name
	if o.Name == "nv overlay evpn" {
		cmd = o.Name
	}
	if !o.Enable {
		cmd = "no " + cmd
	}
	s.add(0, "%s", cmd)
}

// renderSetting renders the context lines and the command that sets a
// value; an empty value renders the negation.
func renderSetting(s *script, o plan.SettingOp) error {
	syn := settingSyntax(o.Setting)
	if syn.cmd == "" {
		return fmt.Errorf("%w: setting %s", ErrUnrenderable, o.Setting)
	}
	var ctx []string
	if syn.context != "" {
		if strings.Contains(syn.context, "%s") {
			if o.Scope == "" {
				return fmt.Errorf("%w: %s without scope", ErrUnrenderable, o.Setting)
			}
			ctx = strings.Split(fmt.Sprintf(syn.context, o.Scope), "|")
		} else {
			ctx = strings.Split(syn.context, "|")
		}
	}
	for i, c := range ctx {
		s.add(i, "%s", c)
	}
	switch {
	case o.Want == "" && syn.negateValue:
		s.add(len(ctx), "no %s %s", syn.cmd, o.Have)
	case o.Want == "":
		s.add(len(ctx), "no %s", syn.cmd)
	case o.Setting.Boolean():
		s.add(len(ctx), "%s", syn.cmd)
	default:
		s.add(len(ctx), "%s %s", syn.cmd, o.Want)
	}
	return nil
}

// syntax is how one setting is written: the context lines ("|"-separated,
// "%s" for the scope), the command, and whether the negation repeats the
// old value.
type syntax struct {
	context     string
	cmd         string
	negateValue bool
}

func settingSyntax(st device.Setting) syntax {
	if opt, ok := st.VPCOption(); ok {
		return syntax{context: "vpc domain %s", cmd: string(opt)}
	}
	switch st {
	case device.SettingHostname:
		return syntax{cmd: "hostname"}
	case device.SettingTimezone:
		return syntax{cmd: "clock timezone", negateValue: true}
	case device.SettingDomainName:
		return syntax{cmd: "ip domain-name", negateValue: true}
	case device.SettingAnycastGatewayMAC:
		return syntax{cmd: "fabric forwarding anycast-gateway-mac"}
	case device.SettingNTPSource:
		return syntax{cmd: "ntp source-interface"}
	case device.SettingNTPLogging:
		return syntax{cmd: "ntp logging"}
	case device.SettingLoggingSource:
		return syntax{cmd: "logging source-interface"}
	case device.SettingLoggingOrigin:
		return syntax{cmd: "logging origin-id hostname"}
	case device.SettingLoggingTimestamp:
		return syntax{cmd: "logging timestamp"}
	case device.SettingTACACSSource:
		return syntax{cmd: "ip tacacs source-interface"}
	case device.SettingVTYExecTimeout:
		return syntax{context: "line vty", cmd: "exec-timeout"}
	case device.SettingVTYSessionLimit:
		return syntax{context: "line vty", cmd: "session-limit"}
	case device.SettingConsoleExecTimeout:
		return syntax{context: "line console", cmd: "exec-timeout"}
	case device.SettingNVEVirtualRMAC:
		return syntax{context: "interface %s", cmd: "advertise virtual-rmac"}
	case device.SettingBGPAdvertisePIP:
		return syntax{context: "router bgp %s|address-family l2vpn evpn", cmd: "advertise-pip"}
	case device.SettingBGPRetainRT:
		return syntax{context: "router bgp %s|address-family l2vpn evpn", cmd: "retain route-target all"}
	case device.SettingBGPLogNeighbor:
		return syntax{context: "router bgp %s", cmd: "log-neighbor-changes"}
	case device.SettingVPCPeerSwitch, device.SettingVPCPeerGateway, device.SettingVPCL3PeerRouter,
		device.SettingVPCAutoRecovery, device.SettingVPCFastConvergence, device.SettingVPCARPSynchronize:
		return syntax{}
	default:
		return syntax{}
	}
}

func renderNTP(s *script, o plan.NTPServerOp) {
	if o.Have != nil {
		s.add(0, "no ntp server %s", o.Address)
	}
	if o.Want == nil {
		return
	}
	cmd := "ntp server " + o.Address
	if o.Want.Prefer {
		cmd += " prefer"
	}
	if o.Want.VRF != "" {
		cmd += " use-vrf " + o.Want.VRF
	}
	s.add(0, "%s", cmd)
}

func renderLogServer(s *script, o plan.LogServerOp) {
	if o.Have != nil {
		s.add(0, "no logging server %s", o.Address)
	}
	if o.Want == nil {
		return
	}
	w := o.Want
	cmd := "logging server " + w.Address
	if w.Severity != 0 {
		cmd += " " + strconv.Itoa(w.Severity)
	}
	if w.Port != 0 {
		cmd += " port " + strconv.Itoa(w.Port)
	}
	if w.VRF != "" {
		cmd += " use-vrf " + w.VRF
	}
	if w.Facility != "" {
		cmd += " facility " + w.Facility
	}
	s.add(0, "%s", cmd)
}

func renderVRF(s *script, o plan.VRFOp) {
	if o.Want == nil {
		s.add(0, "no vrf context %s", o.Name)
		return
	}
	var have device.VRF
	if o.Have != nil {
		have = *o.Have
	}
	w := *o.Want
	s.add(0, "vrf context %s", o.Name)
	if w.VNI != have.VNI {
		if have.VNI != 0 {
			s.add(1, "no vni %s", have.VNI)
		}
		if w.VNI != 0 {
			s.add(1, "vni %s", w.VNI)
		}
	}
	for _, r := range w.Routes {
		if !slices.Contains(have.Routes, r) {
			s.add(1, "%s", routeCommand(r))
		}
	}
	for _, r := range have.Routes {
		if !slices.Contains(w.Routes, r) {
			s.add(1, "no %s", routeCommand(r))
		}
	}
	if w.RD != have.RD {
		if w.RD == "" {
			s.add(1, "no rd %s", have.RD)
		} else {
			s.add(1, "rd %s", w.RD)
		}
	}
	add, del := listDiff(have.RouteTargets, w.RouteTargets)
	if len(add)+len(del) > 0 {
		s.add(1, "address-family ipv4 unicast")
		for _, rt := range add {
			s.add(2, "route-target %s", rt)
		}
		for _, rt := range del {
			s.add(2, "no route-target %s", rt)
		}
	}
}

func routeCommand(r device.StaticRoute) string {
	cmd := "ip route " + r.Prefix.String() + " " + r.NextHop
	if r.NextHopVRF != "" {
		cmd += " vrf " + r.NextHopVRF
	}
	if r.Name != "" {
		cmd += " name " + r.Name
	}
	return cmd
}

// listDiff returns what want adds to and removes from have, in order.
func listDiff(have, want []string) (add, del []string) {
	for _, w := range want {
		if !slices.Contains(have, w) {
			add = append(add, w)
		}
	}
	for _, h := range have {
		if !slices.Contains(want, h) {
			del = append(del, h)
		}
	}
	return add, del
}

func renderVLAN(s *script, o plan.VLANOp) {
	if o.Want == nil {
		s.add(0, "no vlan %s", o.VLAN)
		return
	}
	var have device.VLAN
	if o.Have != nil {
		have = *o.Have
	}
	s.add(0, "vlan %s", o.VLAN)
	switch o.Want.Name {
	case have.Name:
	case "":
		s.add(1, "no name")
	default:
		s.add(1, "name %s", o.Want.Name)
	}
	if o.Want.VNI != have.VNI {
		if have.VNI != 0 {
			s.add(1, "no vn-segment")
		}
		if o.Want.VNI != 0 {
			s.add(1, "vn-segment %s", o.Want.VNI)
		}
	}
}

func renderEVPN(s *script, o plan.EVPNOp) {
	s.add(0, "evpn")
	if o.Want == nil {
		s.add(1, "no vni %s l2", o.VNI)
		return
	}
	var have device.EVPNVNI
	if o.Have != nil {
		have = *o.Have
	}
	s.add(1, "vni %s l2", o.VNI)
	if o.Want.RD != have.RD {
		if o.Want.RD == "" {
			s.add(2, "no rd %s", have.RD)
		} else {
			s.add(2, "rd %s", o.Want.RD)
		}
	}
	add, del := listDiff(have.RouteTargets, o.Want.RouteTargets)
	for _, rt := range add {
		s.add(2, "route-target %s", rt)
	}
	for _, rt := range del {
		s.add(2, "no route-target %s", rt)
	}
}

func memberLine(vni fabric.VNI, m device.NVEMember) string {
	if m.AssociateVRF {
		return "member vni " + vni.String() + " associate-vrf"
	}
	return "member vni " + vni.String()
}

func renderMember(s *script, o plan.NVEMemberOp) {
	s.add(0, "interface %s", o.NVE)
	if o.Have != nil && (o.Want == nil || o.Want.AssociateVRF != o.Have.AssociateVRF) {
		s.add(1, "no %s", memberLine(o.VNI, *o.Have))
		if o.Want == nil {
			return
		}
		o.Have = nil
	}
	var have device.NVEMember
	if o.Have != nil {
		have = *o.Have
	}
	w := *o.Want
	s.add(1, "%s", memberLine(o.VNI, w))
	if w.AssociateVRF {
		return
	}
	toggle(s, 2, have.SuppressARP, w.SuppressARP, "suppress-arp")
	if w.McastGroup != have.McastGroup {
		if w.McastGroup == "" {
			s.add(2, "no mcast-group %s", have.McastGroup)
		} else {
			s.add(2, "mcast-group %s", w.McastGroup)
		}
	}
	toggle(s, 2, have.IngressBGP, w.IngressBGP, "ingress-replication protocol bgp")
}

// toggle renders cmd or its negation when the boolean changes.
func toggle(s *script, depth int, have, want bool, cmd string) {
	switch {
	case want && !have:
		s.add(depth, "%s", cmd)
	case have && !want:
		s.add(depth, "no %s", cmd)
	}
}

func renderSVI(s *script, o plan.SVIOp) {
	name := o.VLAN.SVI()
	if o.Want == nil {
		s.add(0, "no interface %s", name)
		return
	}
	var have plan.SVI
	if o.Have != nil {
		have = *o.Have
	}
	w := *o.Want
	s.add(0, "interface %s", name)
	if w.VRF != have.VRF {
		if w.VRF == "" {
			s.add(1, "no vrf member %s", have.VRF)
		} else {
			s.add(1, "vrf member %s", w.VRF)
		}
		// NX-OS drops layer-3 settings on a VRF change.
		have.Address = plan.SVI{}.Address
	}
	if w.MTU != have.MTU {
		if w.MTU == 0 {
			s.add(1, "no mtu")
		} else {
			s.add(1, "mtu %d", w.MTU)
		}
	}
	if w.Address != have.Address {
		if w.Address.IsValid() {
			s.add(1, "ip address %s", w.Address)
		} else {
			s.add(1, "no ip address")
		}
	}
	toggle(s, 1, have.IPForward, w.IPForward, "ip forward")
	toggle(s, 1, have.Anycast, w.Anycast, "fabric forwarding mode anycast-gateway")
	if w.ACLIn != have.ACLIn {
		if w.ACLIn == "" {
			s.add(1, "no ip access-group %s in", have.ACLIn)
		} else {
			s.add(1, "ip access-group %s in", w.ACLIn)
		}
	}
	if w.Admin != have.Admin {
		switch w.Admin {
		case device.AdminUp:
			s.add(1, "no shutdown")
		case device.AdminDown, device.AdminDefault:
			s.add(1, "shutdown")
		}
	}
}

func renderBGPVRF(s *script, o plan.BGPVRFOp) {
	s.add(0, "router bgp %s", o.ASN)
	if o.Want == nil {
		s.add(1, "no vrf %s", o.VRF)
		return
	}
	var have device.BGPVRF
	if o.Have != nil {
		have = *o.Have
	}
	w := *o.Want
	s.add(1, "vrf %s", o.VRF)
	if have.AdvertiseL2VPN == w.AdvertiseL2VPN && o.Have != nil {
		return
	}
	s.add(2, "address-family ipv4 unicast")
	toggle(s, 3, have.AdvertiseL2VPN, w.AdvertiseL2VPN, "advertise l2vpn evpn")
	if o.Have != nil {
		return
	}
	// Restoring a removed VRF brings back its routing policy.
	for _, proto := range slices.Sorted(maps.Keys(w.Redistribute)) {
		s.add(3, "redistribute %s route-map %s", proto, w.Redistribute[proto])
	}
	for _, n := range w.Networks {
		s.add(3, "network %s", n)
	}
	if w.MaximumPathsIBGP != 0 {
		s.add(3, "maximum-paths ibgp %d", w.MaximumPathsIBGP)
	}
	if w.DefaultOriginate {
		s.add(3, "default-information originate")
	}
}

func renderPort(s *script, o plan.PortOp) {
	_, kind := device.CanonicalInterface(o.Interface)
	if o.Want == nil && kind == device.KindPortChannel && o.Have != nil {
		s.add(0, "no interface %s", o.Interface)
		return
	}
	s.add(0, "interface %s", o.Interface)
	if o.Want == nil {
		s.add(1, "no switchport")
		return
	}
	defer renderPortExtras(s, o)
	w := *o.Want
	if o.Have == nil {
		s.add(1, "switchport")
	}
	have := plan.Port{Mode: fabric.PortAccess}
	if o.Have != nil {
		have = *o.Have
	}
	if o.Have == nil || have.Mode != w.Mode {
		s.add(1, "switchport mode %s", w.Mode)
	}
	if w.Mode == fabric.PortAccess {
		if w.Access != have.Access || o.Have == nil {
			s.add(1, "switchport access vlan %s", w.Access)
		}
		return
	}
	renderAllowed(s, have, w, o.Have == nil || have.Mode != fabric.PortTrunk)
}

func renderAllowed(s *script, have, want plan.Port, fresh bool) {
	switch {
	case want.Allowed == nil:
		if !fresh && have.Allowed == nil {
			return
		}
		s.add(1, "switchport trunk allowed vlan all")
	case fresh || have.Allowed == nil:
		s.add(1, "switchport trunk allowed vlan %s", allowedText(want.Allowed))
	default:
		if add := want.Allowed.Difference(have.Allowed); !add.IsEmpty() {
			s.add(1, "switchport trunk allowed vlan add %s", add.Compact())
		}
		if del := have.Allowed.Difference(want.Allowed); !del.IsEmpty() {
			s.add(1, "switchport trunk allowed vlan remove %s", del.Compact())
		}
	}
}

func allowedText(s *set.Set[uint16]) string {
	if s.IsEmpty() {
		return "none"
	}
	return s.Compact()
}

// renderPortExtras renders the attributes beyond VLAN membership, in the
// order NX-OS needs: bundle membership after the switchport settings,
// the administrative state last.
func renderPortExtras(s *script, o plan.PortOp) {
	var have plan.Port
	if o.Have != nil {
		have = *o.Have
	}
	w := *o.Want
	text := func(old, cur, cmd string) {
		switch cur {
		case old:
		case "":
			s.add(1, "no %s", cmd)
		default:
			s.add(1, "%s %s", cmd, cur)
		}
	}
	text(have.Description, w.Description, "description")
	if w.MTU != have.MTU {
		if w.MTU == 0 {
			s.add(1, "no mtu")
		} else {
			s.add(1, "mtu %d", w.MTU)
		}
	}
	text(have.STPPortType, w.STPPortType, "spanning-tree port type")
	toggle(s, 1, have.PeerLink, w.PeerLink, "vpc peer-link")
	if w.VPC != have.VPC {
		if w.VPC == 0 {
			s.add(1, "no vpc %d", have.VPC)
		} else {
			s.add(1, "vpc %d", w.VPC)
		}
	}
	if w.ChannelGroup != have.ChannelGroup || w.ChannelMode != have.ChannelMode {
		switch {
		case w.ChannelGroup == 0:
			s.add(1, "no channel-group")
		case w.ChannelMode != "":
			s.add(1, "channel-group %d mode %s", w.ChannelGroup, w.ChannelMode)
		default:
			s.add(1, "channel-group %d", w.ChannelGroup)
		}
	}
	renderAdmin(s, have.Admin, w.Admin)
}

func renderAdmin(s *script, have, want device.AdminState) {
	if want == have {
		return
	}
	switch want {
	case device.AdminUp:
		s.add(1, "no shutdown")
	case device.AdminDown:
		s.add(1, "shutdown")
	case device.AdminDefault:
	}
}
