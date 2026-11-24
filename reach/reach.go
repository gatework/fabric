// Package reach traces the forwarding path between two endpoints of the
// fabric and verifies it hop by hop, in both directions: the access port,
// the VNI, the EVPN route the ingress VTEP holds for the destination and
// the VTEP it points to, the NVE peer, the underlay uplinks of both VTEPs,
// and the egress port.
//
// Two endpoints in the same VNI are bridged. Endpoints in different VNIs
// of one VRF are routed through the anycast gateway and the VRF's L3VNI.
// Different VRFs do not meet inside the fabric. A failing hop names the
// break; a hop whose evidence was not collected is unknown, never healthy.
//
// The package performs no I/O.
package reach

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/endpoint"
	"github.com/gatework/fabric/topology"
)

// Mode is how the fabric forwards between the two endpoints.
type Mode uint8

// Modes.
const (
	ModeUnknown Mode = iota
	// ModeLocal: both endpoints are on the same switch and VLAN.
	ModeLocal
	// ModeBridged: same L2VNI, carried as EVPN type-2 MAC routes.
	ModeBridged
	// ModeRouted: same VRF, routed by the anycast gateway over the L3VNI.
	ModeRouted
	// ModeSeparated: different VRFs; traffic must leave the fabric through
	// a border or a firewall.
	ModeSeparated
)

// String returns the Chinese label.
func (m Mode) String() string {
	switch m {
	case ModeLocal:
		return "本机交换"
	case ModeBridged:
		return "二层桥接"
	case ModeRouted:
		return "三层路由"
	case ModeSeparated:
		return "跨 VRF"
	case ModeUnknown:
		return "未知"
	default:
		return "未知"
	}
}

// MarshalText encodes the label.
func (m Mode) MarshalText() ([]byte, error) { return []byte(m.String()), nil }

// Hop is one verified step of the path.
type Hop struct {
	Direction string           `json:"direction"`
	Step      string           `json:"step"`
	Verdict   topology.Verdict `json:"verdict"`
	Evidence  string           `json:"evidence,omitempty"`
	Hint      string           `json:"hint,omitempty"`
}

// End is one endpoint as the fabric places it.
type End struct {
	Query  string      `json:"query"`
	Switch string      `json:"switch"`
	Port   string      `json:"port,omitempty"`
	VLAN   fabric.VLAN `json:"vlan"`
	VNI    fabric.VNI  `json:"vni"`
	MAC    fabric.MAC  `json:"mac"`
	IP     string      `json:"ip,omitempty"`
	VRF    string      `json:"vrf,omitempty"`
}

// Trace is the verified path between two endpoints.
type Trace struct {
	From  End        `json:"from"`
	To    End        `json:"to"`
	Mode  Mode       `json:"mode"`
	L3VNI fabric.VNI `json:"l3vni,omitempty"`
	// Spines are the spines both VTEPs reach over healthy uplinks: the
	// underlay ECMP set.
	Spines  []string         `json:"spines,omitempty"`
	Hops    []Hop            `json:"hops"`
	Verdict topology.Verdict `json:"verdict"`
	// Break is the first failing hop, if any.
	Break *Hop `json:"break,omitempty"`
}

// Fabric is what a trace reads: the endpoint index, the observed switches
// and the topology audits of their regions.
type Fabric struct {
	Index    *endpoint.Index
	Switches []*device.Switch
	Audits   []topology.Audit
}

// Trace verifies the path from one endpoint to the other, given each as a
// MAC or an IP address.
func (f Fabric) Trace(from, to string) (Trace, error) {
	a, err := f.end(from)
	if err != nil {
		return Trace{}, err
	}
	b, err := f.end(to)
	if err != nil {
		return Trace{}, err
	}
	t := Trace{From: a, To: b}
	sa, sb := f.switchOf(a.Switch), f.switchOf(b.Switch)
	a.VRF, b.VRF = vrfOf(sa, a.VLAN), vrfOf(sb, b.VLAN)
	t.From, t.To = a, b
	t.Mode = mode(a, b)
	if t.Mode == ModeRouted && sa != nil && sa.Config != nil {
		t.L3VNI = sa.Config.VRFs[t.From.VRF].VNI
	}
	h := &hops{}
	h.access("去程", sa, a)
	h.segment("去程", sa, a, t)
	if t.Mode == ModeSeparated {
		h.add("去程", "VRF", topology.VerdictFail, fmt.Sprintf("源在 VRF %s，目的在 VRF %s，fabric 内不互通", dash(t.From.VRF), dash(t.To.VRF)),
			"跨 VRF 流量须经 border 的 VRF 间路由泄露或防火墙；核对策略是否允许")
	}
	if t.Mode == ModeBridged || t.Mode == ModeRouted {
		h.overlay("去程", f, sa, a, b, t.Mode)
		h.overlay("回程", f, sb, b, a, t.Mode)
		t.Spines = h.underlay(f, a.Switch, b.Switch)
	}
	h.segment("目的", sb, b, t)
	h.access("目的", sb, b)
	t.Hops = h.list
	t.Verdict = topology.VerdictOK
	for i, x := range t.Hops {
		switch x.Verdict {
		case topology.VerdictFail:
			t.Verdict, t.Break = topology.VerdictFail, &t.Hops[i]
			return t, nil
		case topology.VerdictUnknown:
			t.Verdict = topology.VerdictUnknown
		case topology.VerdictOK:
		}
	}
	return t, nil
}

// ErrNotFound reports an endpoint no switch has learned.
var ErrNotFound = fmt.Errorf("reach: endpoint not learned")

func (f Fabric) end(query string) (End, error) {
	res := f.Index.Lookup(query)
	if len(res.Attached) == 0 {
		return End{}, fmt.Errorf("%w: %s", ErrNotFound, query)
	}
	l := res.Attached[0]
	e := End{Query: query, Switch: l.Switch, Port: l.Port, VLAN: l.VLAN, VNI: l.VNI, MAC: l.MAC, IP: l.IP}
	if e.IP == "" {
		for _, x := range append(slices.Clone(res.Attached), res.Remote...) {
			if x.MAC == l.MAC && x.IP != "" {
				e.IP = x.IP
				break
			}
		}
	}
	return e, nil
}

func (f Fabric) switchOf(name string) *device.Switch {
	for _, sw := range f.Switches {
		if sw.Name == name {
			return sw
		}
	}
	return nil
}

func vrfOf(sw *device.Switch, vlan fabric.VLAN) string {
	if sw == nil || sw.Config == nil {
		return ""
	}
	if i, ok := sw.Config.Interfaces[fmt.Sprintf("Vlan%d", vlan)]; ok {
		return i.VRF
	}
	return ""
}

func mode(a, b End) Mode {
	switch {
	case a.Switch == b.Switch && a.VLAN == b.VLAN:
		return ModeLocal
	case a.VNI != 0 && a.VNI == b.VNI:
		return ModeBridged
	case a.VRF == "" || b.VRF == "":
		return ModeUnknown
	case a.VRF == b.VRF:
		return ModeRouted
	default:
		return ModeSeparated
	}
}

type hops struct{ list []Hop }

func (h *hops) add(dir, step string, v topology.Verdict, evidence, hint string) {
	h.list = append(h.list, Hop{Direction: dir, Step: step, Verdict: v, Evidence: evidence, Hint: hint})
}

// access verifies the endpoint's port: up, and carrying its VLAN.
func (h *hops) access(dir string, sw *device.Switch, e End) {
	step := "接入 " + e.Switch + " " + e.Port
	switch {
	case sw == nil:
		h.add(dir, step, topology.VerdictUnknown, "快照中没有该交换机", "")
		return
	case !sw.Has(device.DimInterfaces):
		h.add(dir, step, topology.VerdictUnknown, "未采集接口状态", "")
		return
	}
	st, ok := sw.State.Port(e.Port)
	switch {
	case !ok:
		h.add(dir, step, topology.VerdictUnknown, "接口表中没有该端口", "")
	case !st.Up:
		h.add(dir, step, topology.VerdictFail, "端口 "+st.Status+" "+st.Reason, "检查终端网卡、线缆与端口状态")
	default:
		if sw.Config != nil {
			name, _ := device.CanonicalInterface(e.Port)
			if i, ok := sw.Config.Interfaces[name]; ok && i.Layer2 && !i.Carries(e.VLAN) {
				h.add(dir, step, topology.VerdictFail, fmt.Sprintf("端口 up，但未放行 VLAN %d", e.VLAN), "在端口的 allowed vlan 中加入该 VLAN（fabric port assign / plan）")
				return
			}
		}
		speed, _, _ := strings.Cut(st.Speed, "(")
		h.add(dir, step, topology.VerdictOK, fmt.Sprintf("端口 up %s，VLAN %d", speed, e.VLAN), "")
	}
}

// segment verifies the endpoint's L2VNI and, when routed, its gateway SVI
// and the VRF's L3VNI on its VTEP.
func (h *hops) segment(dir string, sw *device.Switch, e End, t Trace) {
	if t.Mode == ModeLocal || sw == nil {
		return
	}
	h.vni(dir, sw, e.VNI, "L2VNI")
	if t.Mode != ModeRouted {
		return
	}
	step := fmt.Sprintf("网关 %s Vlan%d", e.Switch, e.VLAN)
	if sw.Config == nil {
		h.add(dir, step, topology.VerdictUnknown, "未采集运行配置", "")
		return
	}
	i, ok := sw.Config.Interfaces[fmt.Sprintf("Vlan%d", e.VLAN)]
	switch {
	case !ok || len(i.Addresses) == 0:
		h.add(dir, step, topology.VerdictFail, "VTEP 上没有该 VLAN 的网关 SVI 地址", "为该网络配置 anycast 网关（fabric network add）")
	case i.Admin == device.AdminDown:
		h.add(dir, step, topology.VerdictFail, "网关 SVI 被 shutdown", "")
	default:
		h.add(dir, step, topology.VerdictOK, fmt.Sprintf("SVI %s，VRF %s", i.Addresses[0], dash(i.VRF)), "")
	}
	if t.From.Switch == t.To.Switch {
		// Routed between two SVIs of one VTEP: the L3VNI is not used.
		return
	}
	if vni := sw.Config.VRFs[e.VRF].VNI; vni != 0 {
		h.vni(dir, sw, vni, "L3VNI")
	} else {
		h.add(dir, "L3VNI "+e.Switch, topology.VerdictFail, "VRF "+dash(e.VRF)+" 未配置 L3VNI", "为 VRF 配置 vni 并在 nve 中 associate-vrf")
	}
}

func (h *hops) vni(dir string, sw *device.Switch, vni fabric.VNI, kind string) {
	step := fmt.Sprintf("%s %d %s", kind, vni, sw.Name)
	if !sw.Has(device.DimNVEVNIs) {
		h.add(dir, step, topology.VerdictUnknown, "未采集 show nve vni", "")
		return
	}
	for _, v := range sw.State.NVEVNIs {
		if v.VNI == vni {
			if v.Up {
				h.add(dir, step, topology.VerdictOK, "Up", "")
			} else {
				h.add(dir, step, topology.VerdictFail, "状态 "+v.State, "检查 NVE 成员、EVPN 实例与 VLAN 是否完整（fabric check --switch "+sw.Name+"）")
			}
			return
		}
	}
	h.add(dir, step, topology.VerdictFail, "VTEP 上没有该 VNI", "该网络未扩展到这台 VTEP（fabric network / port assign 会自动扩展）")
}

// overlay verifies that the ingress VTEP holds an EVPN route to the
// destination pointing at the destination's VTEP, and that the NVE peer
// to that VTEP is up.
func (h *hops) overlay(dir string, f Fabric, sw *device.Switch, src, dst End, m Mode) {
	if src.Switch == dst.Switch {
		h.add(dir, "本机转发 "+src.Switch, topology.VerdictOK, "两端在同一台 VTEP，经网关 SVI 本地路由", "")
		return
	}
	what := "MAC " + string(dst.MAC)
	if m == ModeRouted && dst.IP != "" {
		what = "IP " + dst.IP
	}
	step := fmt.Sprintf("EVPN %s 学到 %s", src.Switch, what)
	if sw == nil || !sw.Has(device.DimL2Routes) && !sw.Has(device.DimMACIPs) {
		h.add(dir, step, topology.VerdictUnknown, "未采集 l2route 表", "")
		return
	}
	var route *endpoint.Location
	for _, l := range f.Index.Lookup(string(dst.MAC)).Remote {
		if l.Switch == src.Switch && (m != ModeRouted || dst.IP == "" || l.IP == dst.IP || l.IP == "") {
			route = &l
			if l.IP == dst.IP {
				break
			}
		}
	}
	if route == nil {
		h.add(dir, step, topology.VerdictFail, src.Switch+" 没有到 "+what+" 的 EVPN 路由",
			"检查 "+dst.Switch+" 是否通告（本地是否学到）、两端到 spine 的 BGP EVPN 会话与 route-target")
		return
	}
	if !slices.Contains(route.Owners, dst.Switch) && !peerOwns(f, route.Owners, dst.Switch) {
		h.add(dir, step, topology.VerdictFail, fmt.Sprintf("下一跳 %s（%s），不是目的所在的 %s", route.VTEP, strings.Join(route.Owners, "/"), dst.Switch),
			"终端可能已迁移而旧路由未撤销，或存在重复 MAC（fabric check --severity high）")
		return
	}
	h.add(dir, step, topology.VerdictOK, fmt.Sprintf("下一跳 VTEP %s（%s）", route.VTEP, strings.Join(route.Owners, "/")), "")
	h.nvePeer(dir, sw, route.VTEP)
}

// peerOwns accepts an anycast VTEP owned by the destination's vPC pair.
func peerOwns(f Fabric, owners []string, sw string) bool {
	for _, a := range f.Audits {
		if p, ok := a.PairOf(sw); ok && (slices.Contains(owners, p.Switches[0]) || slices.Contains(owners, p.Switches[1])) {
			return true
		}
	}
	return false
}

func (h *hops) nvePeer(dir string, sw *device.Switch, vtep string) {
	step := "NVE 对端 " + sw.Name + " → " + vtep
	if !sw.Has(device.DimNVEPeers) {
		h.add(dir, step, topology.VerdictUnknown, "未采集 show nve peers", "")
		return
	}
	for _, p := range sw.State.NVEPeers {
		if p.Peer == vtep {
			if p.Up {
				h.add(dir, step, topology.VerdictOK, "Up "+p.Uptime, "")
			} else {
				h.add(dir, step, topology.VerdictFail, "状态 "+p.State, "检查两端 VTEP 地址的底层可达性（OSPF、loopback 通告）")
			}
			return
		}
	}
	h.add(dir, step, topology.VerdictFail, "没有到该 VTEP 的 NVE 对端", "检查底层路由是否能到达对端 VTEP 地址")
}

// underlay verifies both VTEPs keep healthy uplinks and returns the
// spines both reach: the ECMP set.
func (h *hops) underlay(f Fabric, a, b string) []string {
	if a == b {
		return nil
	}
	ups := map[string]topology.Uplinks{}
	for _, au := range f.Audits {
		for _, u := range au.Uplinks {
			ups[u.Switch] = u
		}
	}
	ua, oka := ups[a]
	ub, okb := ups[b]
	step := "底层 " + a + " ⇄ " + b
	switch {
	case !oka || !okb || ua.Redundancy == topology.RedundancyUnknown || ub.Redundancy == topology.RedundancyUnknown:
		h.add("底层", step, topology.VerdictUnknown, "缺少上行证据", "")
		return nil
	case ua.Healthy == 0 || ub.Healthy == 0:
		h.add("底层", step, topology.VerdictFail, fmt.Sprintf("%s 健康上行 %d/%d，%s 健康上行 %d/%d", a, ua.Healthy, ua.Declared, b, ub.Healthy, ub.Declared),
			"fabric topology --switch 查看失联的 VTEP")
		return nil
	}
	var both []string
	for _, s := range ua.Spines {
		if slices.Contains(ub.Spines, s) {
			both = append(both, s)
		}
	}
	if len(both) == 0 {
		h.add("底层", step, topology.VerdictFail, "两端没有共同可达的 spine", "恢复上行冗余")
		return nil
	}
	v, ev := topology.VerdictOK, fmt.Sprintf("经 %s（ECMP %d 条）；上行 %d/%d 与 %d/%d", strings.Join(both, "、"), len(both), ua.Healthy, ua.Declared, ub.Healthy, ub.Declared)
	hint := ""
	if ua.Redundancy.AtRisk() || ub.Redundancy.AtRisk() {
		hint = "路径可用，但有一端已失去上行冗余"
	}
	h.add("底层", step, v, ev, hint)
	return both
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
