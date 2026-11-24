// Package maintenance answers the question an operator asks before a
// change window: what breaks while this switch is reloaded, upgraded or
// drained?
//
// For a spine it finds the VTEPs left without any healthy uplink and those
// whose only established BGP EVPN session runs to it. For a leaf or a
// border it splits the endpoints it learns locally into single-homed ones,
// which lose connectivity, and dual-homed ones on vPC members, which the
// vPC peer carries if that peer is fit to: healthy uplinks, the peer
// adjacency and keepalive up, consistency, and the same vPC up.
//
// The package performs no I/O; unknown evidence is reported, never taken
// as safe.
package maintenance

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/topology"
)

// ErrUnknownSwitch reports a switch the region does not declare.
var ErrUnknownSwitch = errors.New("maintenance: unknown switch")

// Verdict is whether the switch can be taken out of service now.
type Verdict uint8

// Verdicts.
const (
	// VerdictUnknown: the evidence needed to judge was not collected.
	VerdictUnknown Verdict = iota
	// VerdictReady: nothing loses connectivity.
	VerdictReady
	// VerdictRisky: some endpoints or sessions are interrupted, but the
	// fabric keeps working; announce it or move them first.
	VerdictRisky
	// VerdictBlocked: VTEPs or dual-homed endpoints would be cut off;
	// restore redundancy first.
	VerdictBlocked
)

// String returns the Chinese label.
func (v Verdict) String() string {
	switch v {
	case VerdictReady:
		return "可以维护"
	case VerdictRisky:
		return "有风险"
	case VerdictBlocked:
		return "不可维护"
	case VerdictUnknown:
		return "无法判断"
	default:
		return "无法判断"
	}
}

// MarshalText encodes the label.
func (v Verdict) MarshalText() ([]byte, error) { return []byte(v.String()), nil }

// Port is a host-facing port and the endpoints learned on it.
type Port struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	VPC         int      `json:"vpc,omitempty"`
	MACs        []string `json:"macs"`
	IPs         []string `json:"ips,omitempty"`
	VLANs       []int    `json:"vlans,omitempty"`
}

// Report is the impact of taking one switch out of service.
type Report struct {
	Switch  string      `json:"switch"`
	Role    fabric.Role `json:"role"`
	Region  string      `json:"region"`
	Verdict Verdict     `json:"verdict"`
	// Blockers and Risks explain the verdict; Before lists what to do
	// before the window.
	Blockers []string `json:"blockers,omitempty"`
	Risks    []string `json:"risks,omitempty"`
	Before   []string `json:"before,omitempty"`

	// Spine impact: VTEPs cut off, VTEPs that lose one path, and VTEPs
	// whose only established EVPN session runs to this spine.
	Isolated    []string `json:"isolated,omitempty"`
	Degraded    []string `json:"degraded,omitempty"`
	OverlayOnly []string `json:"overlay_only,omitempty"`

	// Leaf impact.
	Peer        string   `json:"peer,omitempty"`
	PeerIssues  []string `json:"peer_issues,omitempty"`
	SingleHomed []Port   `json:"single_homed,omitempty"`
	DualHomed   []Port   `json:"dual_homed,omitempty"`
	// External lists devices CDP sees on host ports, such as storage or
	// firewalls, attached to this switch alone.
	External []topology.Neighbor `json:"external,omitempty"`
	// Exits are the border's VRF static routes, the north–south way out of
	// the fabric, with the other borders that carry the same route.
	Exits []Exit `json:"exits,omitempty"`
	// Peerings are OSPF adjacencies outside the fabric underlay, such as
	// the border's links to the core or a firewall.
	Peerings []string `json:"peerings,omitempty"`
	// Missing lists evidence that was not collected.
	Missing []string `json:"missing,omitempty"`
}

// Exit is a VRF route leaving the fabric through this border.
type Exit struct {
	VRF     string `json:"vrf"`
	Prefix  string `json:"prefix"`
	NextHop string `json:"next_hop"`
	// NextHopVRF is the VRF the next hop is resolved in, often "default".
	NextHopVRF string `json:"next_hop_vrf,omitempty"`
	// Also lists the other borders of the region with the same VRF route
	// and an external adjacency of their own.
	Also []string `json:"also,omitempty"`
	// Through lists the borders with the same route whose only external
	// adjacency is this switch: they lose the way out with it.
	Through []string `json:"through,omitempty"`
	// Standby lists the borders with the same route and an external link
	// of their own that is up and runs OSPF but has no adjacency now,
	// typically the link to the standby unit of a firewall pair.
	Standby []string `json:"standby,omitempty"`
}

// Endpoints counts the MAC addresses on single- and dual-homed ports.
func (r Report) Endpoints() (single, dual int) {
	for _, p := range r.SingleHomed {
		single += len(p.MACs)
	}
	for _, p := range r.DualHomed {
		dual += len(p.MACs)
	}
	return single, dual
}

// Assess reports the impact of taking name out of service, from the
// region's topology audit and its observed switches.
func Assess(r *fabric.Region, a topology.Audit, switches []*device.Switch, name string) (Report, error) {
	s, ok := r.Switch(name)
	if !ok {
		return Report{}, fmt.Errorf("%w: %s", ErrUnknownSwitch, name)
	}
	rep := Report{Switch: name, Role: s.Role, Region: r.Name}
	byName := map[string]*device.Switch{}
	for _, sw := range switches {
		byName[sw.Name] = sw
	}
	if s.Role == fabric.RoleSpine {
		spine(&rep, a)
	} else {
		leaf(&rep, a, byName)
	}
	if s.Role == fabric.RoleBorder {
		northSouth(&rep, r, byName)
	}
	rep.Verdict = verdict(rep)
	return rep, nil
}

func spine(rep *Report, a topology.Audit) {
	for _, u := range a.Uplinks {
		switch {
		case u.Redundancy == topology.RedundancyUnknown:
			continue
		case slices.Equal(u.Spines, []string{rep.Switch}):
			rep.Isolated = append(rep.Isolated, u.Switch)
		case slices.Contains(u.Spines, rep.Switch):
			rep.Degraded = append(rep.Degraded, u.Switch)
		}
		var up []string
		for _, s := range u.BGP {
			if s.Verdict == topology.VerdictOK {
				up = append(up, s.Spine)
			}
		}
		if slices.Equal(up, []string{rep.Switch}) {
			rep.OverlayOnly = append(rep.OverlayOnly, u.Switch)
		}
	}
	if len(rep.Isolated) > 0 {
		rep.Blockers = append(rep.Blockers, fmt.Sprintf("%d 台 VTEP 只剩本机一条健康上行，维护期间将与 fabric 失联：%s", len(rep.Isolated), strings.Join(rep.Isolated, "、")))
		rep.Before = append(rep.Before, "先恢复这些 VTEP 到其他 spine 的上行（fabric topology --switch <VTEP>），采集复核后再维护")
	}
	if len(rep.OverlayOnly) > 0 {
		rep.Blockers = append(rep.Blockers, fmt.Sprintf("%d 台 VTEP 的 EVPN 会话只建在本机上，维护期间 overlay 路由会中断：%s", len(rep.OverlayOnly), strings.Join(rep.OverlayOnly, "、")))
		rep.Before = append(rep.Before, "先排查这些 VTEP 到其他 spine 的 BGP EVPN 会话")
	}
	if len(rep.Degraded) > 0 {
		rep.Risks = append(rep.Risks, fmt.Sprintf("%d 台 VTEP 维护期间只剩一条上行（失去冗余，带宽减半）", len(rep.Degraded)))
	}
	rep.Before = append(rep.Before, "维护前对本机做 OSPF max-metric / BGP 隔离（graceful drain），让流量提前离开")
}

func leaf(rep *Report, a topology.Audit, byName map[string]*device.Switch) {
	sw, ok := byName[rep.Switch]
	if !ok {
		rep.Missing = append(rep.Missing, "快照中没有本机")
		return
	}
	for _, d := range []struct {
		dim  evidence.Dimension
		name string
	}{{device.DimMACs, "MAC 表"}, {device.DimConfig, "运行配置"}} {
		if !sw.Has(d.dim) {
			rep.Missing = append(rep.Missing, d.name)
		}
	}
	pair, paired := a.PairOf(rep.Switch)
	var peer *device.Switch
	if paired {
		rep.Peer = pair.Switches[0]
		if rep.Peer == rep.Switch {
			rep.Peer = pair.Switches[1]
		}
		peer = byName[rep.Peer]
		rep.PeerIssues = peerIssues(a, peer, rep.Peer)
	}
	ports := hostPorts(sw, a)
	for _, p := range ports {
		if p.VPC != 0 && peer != nil && vpcUp(peer, p.VPC) {
			rep.DualHomed = append(rep.DualHomed, p)
		} else {
			rep.SingleHomed = append(rep.SingleHomed, p)
		}
	}
	for _, n := range a.Neighbors {
		if n.Switch == rep.Switch && n.Kind == topology.NeighborExternal {
			rep.External = append(rep.External, n)
		}
	}
	single, dual := rep.Endpoints()
	switch {
	case dual > 0 && len(rep.PeerIssues) > 0:
		rep.Blockers = append(rep.Blockers, fmt.Sprintf("%d 个双归终端依赖 vPC 对端 %s 接管，但对端有问题：%s", dual, rep.Peer, strings.Join(rep.PeerIssues, "；")))
		rep.Before = append(rep.Before, "先修复 vPC 对端 "+rep.Peer+"（fabric switch show "+rep.Peer+"）")
	case dual > 0:
		rep.Risks = append(rep.Risks, fmt.Sprintf("%d 个双归终端由 vPC 对端 %s 接管，维护期间只剩单链路", dual, rep.Peer))
	}
	if single > 0 {
		rep.Risks = append(rep.Risks, fmt.Sprintf("%d 个端口上的 %d 个单归终端将中断", len(rep.SingleHomed), single))
		rep.Before = append(rep.Before, "通知单归终端的负责人，或先把它们迁到 vPC 双归")
	}
	if len(rep.External) > 0 {
		rep.Risks = append(rep.Risks, fmt.Sprintf("%d 台外部设备经 CDP 只见于本机", len(rep.External)))
	}
	if paired && dual > 0 {
		rep.Before = append(rep.Before, "维护前在本机执行 vPC 角色切换或关闭 vPC 成员口，让流量先切到对端")
	}
}

// peerIssues lists what keeps the vPC peer from carrying the pair alone.
func peerIssues(a topology.Audit, peer *device.Switch, name string) []string {
	if peer == nil {
		return []string{"快照中没有对端"}
	}
	var out []string
	for _, u := range a.Uplinks {
		if u.Switch == name && u.Redundancy != topology.RedundancyFull && u.Redundancy != topology.RedundancyUnknown {
			out = append(out, fmt.Sprintf("对端上行%s（健康 %d/%d）", u.Redundancy, u.Healthy, u.Declared))
		}
	}
	if !peer.Has(device.DimVPC) || peer.State.VPC == nil {
		return append(out, "未采集对端 vPC 状态")
	}
	v := peer.State.VPC
	if !v.PeerOK {
		out = append(out, "对端 vPC peer 未建立（"+v.PeerStatus+"）")
	}
	if !v.KeepaliveOK {
		out = append(out, "对端 keepalive 异常（"+v.KeepaliveStatus+"）")
	}
	if !v.ConsistencyOK || !v.PerVLANConsistent || !v.Type2Consistent {
		out = append(out, "对端 vPC 一致性检查未通过")
	}
	return out
}

func vpcUp(sw *device.Switch, id int) bool {
	if sw.State.VPC == nil {
		return false
	}
	for _, m := range sw.State.VPC.Members {
		if m.ID == id {
			return m.Up
		}
	}
	return false
}

// hostPorts groups the locally learned MAC addresses by host-facing
// port; member ports roll up into their port-channel.
func hostPorts(sw *device.Switch, a topology.Audit) []Port {
	skip := nonHostPorts(sw, a)
	member := bundleOf(sw)
	ips := localIPs(sw)
	byPort := map[string]*Port{}
	for _, m := range sw.State.MACs {
		port, kind := device.CanonicalInterface(m.Port)
		if m.RemoteVTEP != "" || (kind != device.KindEthernet && kind != device.KindPortChannel) {
			continue
		}
		if p, ok := member[port]; ok {
			port = p
		}
		if skip[port] {
			continue
		}
		p, ok := byPort[port]
		if !ok {
			p = newPort(sw, port)
			byPort[port] = p
		}
		p.add(m, ips)
	}
	out := make([]Port, 0, len(byPort))
	for _, p := range byPort {
		slices.Sort(p.MACs)
		slices.Sort(p.VLANs)
		slices.Sort(p.IPs)
		out = append(out, *p)
	}
	slices.SortFunc(out, func(x, y Port) int {
		return cmp.Or(cmp.Compare(len(y.MACs), len(x.MACs)), device.ComparePorts(x.Name, y.Name))
	})
	return out
}

// nonHostPorts are the switch's fabric links and its vPC peer-link.
func nonHostPorts(sw *device.Switch, a topology.Audit) map[string]bool {
	out := map[string]bool{}
	for _, c := range a.Links {
		for _, e := range []fabric.LinkEnd{c.A, c.B} {
			if e.Switch == sw.Name {
				out[e.Interface] = true
			}
		}
	}
	if v := sw.State.VPC; v != nil {
		for _, l := range v.PeerLinks {
			name, _ := device.CanonicalInterface(l.Port)
			out[name] = true
		}
	}
	return out
}

// bundleOf maps member ports to their port-channel.
func bundleOf(sw *device.Switch) map[string]string {
	out := map[string]string{}
	for _, p := range sw.State.PortChannels {
		name, _ := device.CanonicalInterface(p.Name)
		for _, m := range p.Members {
			port, _ := device.CanonicalInterface(m.Port)
			out[port] = name
		}
	}
	return out
}

func newPort(sw *device.Switch, name string) *Port {
	p := &Port{Name: name}
	if sw.Config != nil {
		if i, ok := sw.Config.Interfaces[name]; ok {
			p.Description, p.VPC = i.Description, i.VPC
		}
	}
	return p
}

func (p *Port) add(m device.MACEntry, ips map[string][]string) {
	mac := string(m.MAC)
	if !slices.Contains(p.MACs, mac) {
		p.MACs = append(p.MACs, mac)
		p.IPs = append(p.IPs, ips[mac]...)
	}
	if v := int(m.VLAN); !slices.Contains(p.VLANs, v) {
		p.VLANs = append(p.VLANs, v)
	}
}

// localIPs maps MAC addresses to the IPs this switch learned locally.
func localIPs(sw *device.Switch) map[string][]string {
	out := map[string][]string{}
	for _, r := range sw.State.MACIPs {
		local := false
		for _, h := range r.NextHops {
			if strings.EqualFold(h, "local") {
				local = true
			}
		}
		if local && r.IP != "" {
			out[string(r.MAC)] = append(out[string(r.MAC)], r.IP)
		}
	}
	return out
}

func verdict(r Report) Verdict {
	switch {
	case len(r.Blockers) > 0:
		return VerdictBlocked
	case len(r.Missing) > 0:
		return VerdictUnknown
	case len(r.Risks) > 0:
		return VerdictRisky
	default:
		return VerdictReady
	}
}

// northSouth finds the VRF routes that leave the fabric through this
// border, how the other borders could carry each of them, and the OSPF
// adjacencies outside the underlay process.
func northSouth(rep *Report, r *fabric.Region, byName map[string]*device.Switch) {
	sw, ok := byName[rep.Switch]
	if !ok || !sw.Configured() {
		return
	}
	underlay := ""
	if r.Underlay != nil {
		underlay = r.Underlay.OSPFProcess
	}
	rep.Exits = exits(r, sw, byName, underlay, selfIDs(r, sw))
	judgeExits(rep)
	if sw.Has(device.DimOSPF) {
		for _, n := range sw.State.OSPF {
			if n.Process != underlay {
				rep.Peerings = append(rep.Peerings, fmt.Sprintf("OSPF %s %s %s（%s）", n.Process, n.RouterID, n.Interface, n.State))
			}
		}
	}
}

// selfIDs are the router IDs other switches see this one by.
func selfIDs(r *fabric.Region, sw *device.Switch) map[string]bool {
	out := map[string]bool{}
	if s, ok := r.Switch(sw.Name); ok && s.RouterID.IsValid() {
		out[s.RouterID.String()] = true
	}
	if sw.Config.BGP != nil && sw.Config.BGP.RouterID != "" {
		out[sw.Config.BGP.RouterID] = true
	}
	return out
}

// exits lists the border's VRF static routes and classifies, per route,
// the other borders that carry it.
func exits(r *fabric.Region, sw *device.Switch, byName map[string]*device.Switch, underlay string, self map[string]bool) []Exit {
	carries := map[string][]string{}
	for _, b := range r.ByRole(fabric.RoleBorder) {
		o, ok := byName[b.Name]
		if b.Name == sw.Name || !ok || !o.Configured() {
			continue
		}
		for vrf, v := range o.Config.VRFs {
			for _, rt := range v.Routes {
				k := vrf + "|" + rt.Prefix.String()
				carries[k] = append(carries[k], b.Name)
			}
		}
	}
	var out []Exit
	for _, vrf := range slices.Sorted(maps.Keys(sw.Config.VRFs)) {
		if vrf == "management" {
			continue
		}
		for _, rt := range sw.Config.VRFs[vrf].Routes {
			e := Exit{VRF: vrf, Prefix: rt.Prefix.String(), NextHop: rt.NextHop, NextHopVRF: rt.NextHopVRF}
			for _, name := range carries[vrf+"|"+e.Prefix] {
				switch wayOut(byName[name], underlay, self) {
				case exitOwn:
					e.Also = append(e.Also, name)
				case exitStandby:
					e.Standby = append(e.Standby, name)
				case exitThrough:
					e.Through = append(e.Through, name)
				}
			}
			out = append(out, e)
		}
	}
	return out
}

// judgeExits turns the exits into blockers, risks and steps.
func judgeExits(rep *Report) {
	var lone, through, standby []string
	for _, e := range rep.Exits {
		switch {
		case len(e.Also) > 0:
		case len(e.Standby) > 0:
			standby = union(standby, e.Standby)
		default:
			lone = append(lone, fmt.Sprintf("VRF %s %s → %s", e.VRF, e.Prefix, e.NextHop))
			through = union(through, e.Through)
		}
	}
	if len(standby) > 0 {
		rep.Risks = append(rep.Risks, fmt.Sprintf("南北向备份依赖 %s：其外部链路 up 且启用了 OSPF，但当前没有邻接。若对端是主备防火墙的备机，属正常设计，本机维护会触发防火墙切换；否则维护期间南北向中断", strings.Join(standby, "、")))
		rep.Before = append(rep.Before, "确认 "+strings.Join(standby, "、")+" 外部链路的对端（防火墙备机？）能在切换后建立 OSPF 邻接")
	}
	switch {
	case len(lone) > 0 && len(through) > 0:
		rep.Blockers = append(rep.Blockers, fmt.Sprintf("%d 条南北向出口路由没有有效备份：%s 虽有相同路由，但它在 fabric 外唯一的邻居就是本机，维护期间同样失去出口：%s",
			len(lone), strings.Join(through, "、"), strings.Join(lone, "；")))
		rep.Before = append(rep.Before, "先为 "+strings.Join(through, "、")+" 接入独立的外部链路（核心/防火墙），或安排在可中断南北向访问的窗口")
	case len(lone) > 0:
		rep.Blockers = append(rep.Blockers, fmt.Sprintf("%d 条南北向出口路由只在本机，维护期间这些 VRF 的外部访问中断：%s", len(lone), strings.Join(lone, "；")))
		rep.Before = append(rep.Before, "先在另一台 border 上配置相同的 VRF 出口路由，或安排在可中断外部访问的窗口")
	case len(rep.Exits) > 0 && len(standby) == 0:
		rep.Risks = append(rep.Risks, fmt.Sprintf("%d 条南北向出口路由维护期间由其他 border 承担", len(rep.Exits)))
	}
}

func union(list, add []string) []string {
	for _, s := range add {
		if !slices.Contains(list, s) {
			list = append(list, s)
		}
	}
	return list
}

type exit uint8

const (
	exitOwn exit = iota
	exitStandby
	exitThrough
)

// wayOut classifies how a border leaves the fabric without self: over a
// FULL OSPF neighbor of its own outside the underlay; over an external
// link of its own that is up and runs OSPF but has no neighbor (a standby
// firewall); or only through self. Without OSPF evidence the border gets
// the benefit of the doubt.
func wayOut(b *device.Switch, underlay string, self map[string]bool) exit {
	if b == nil || !b.Has(device.DimOSPF) {
		return exitOwn
	}
	toSelf := map[string]bool{}
	for _, n := range b.State.OSPF {
		port, _ := device.CanonicalInterface(n.Interface)
		switch {
		case n.Process == underlay:
		case self[n.RouterID]:
			toSelf[port] = true
		case n.Full:
			return exitOwn
		}
	}
	if b.Config == nil {
		return exitThrough
	}
	for name, i := range b.Config.Interfaces {
		if i.OSPF == nil || i.OSPF.Process == underlay || len(i.Addresses) == 0 || toSelf[name] || i.Admin == device.AdminDown {
			continue
		}
		if i.Kind != device.KindEthernet && i.Kind != device.KindPortChannel {
			continue
		}
		if st, ok := b.State.Port(name); ok && st.Up {
			return exitStandby
		}
	}
	return exitThrough
}
