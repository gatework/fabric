package topology

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Layer is one layer a link is verified at, bottom up.
type Layer uint8

// Layers, in the order a link is verified: a link's fault is its lowest
// failing layer, because every layer above it fails as a consequence.
const (
	LayerPhysical Layer = iota + 1
	LayerNeighbor
	LayerConfig
	LayerRouting
)

// String returns the Chinese label.
func (l Layer) String() string {
	switch l {
	case LayerPhysical:
		return "物理"
	case LayerNeighbor:
		return "邻居"
	case LayerConfig:
		return "配置"
	case LayerRouting:
		return "路由"
	default:
		return "未知"
	}
}

// MarshalText encodes the label.
func (l Layer) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

// Verdict is the outcome of one layer.
type Verdict uint8

// Verdicts. Unknown means the evidence for the layer was not collected.
const (
	VerdictUnknown Verdict = iota
	VerdictOK
	VerdictFail
)

// String returns the Chinese label.
func (v Verdict) String() string {
	switch v {
	case VerdictOK:
		return "正常"
	case VerdictFail:
		return "异常"
	case VerdictUnknown:
		return "未知"
	default:
		return "未知"
	}
}

// MarshalText encodes the label.
func (v Verdict) MarshalText() ([]byte, error) { return []byte(v.String()), nil }

// Probe is the verdict on one layer of a link and the evidence for it.
type Probe struct {
	Layer    Layer   `json:"layer"`
	Verdict  Verdict `json:"verdict"`
	Evidence string  `json:"evidence,omitempty"`
}

// Diagnosis is the conclusion an operator acts on: the failing layer, its
// likely cause and the next step.
type Diagnosis struct {
	Fault Layer  `json:"fault"`
	Cause string `json:"cause"`
	Hint  string `json:"hint"`
}

// ports indexes "show interface brief" by canonical name.
type ports map[portKey]device.InterfaceStatus

// adjacencies indexes FULL-or-not OSPF neighbors by canonical interface.
type adjacencies map[portKey][]device.OSPFNeighbor

func indexPorts(sw *device.Switch, out ports) {
	if !sw.Has(device.DimInterfaces) {
		return
	}
	for _, p := range sw.State.Interfaces {
		name, _ := device.CanonicalInterface(p.Name)
		out[portKey{sw.Name, name}] = p
	}
}

func indexOSPF(sw *device.Switch, out adjacencies) {
	if !sw.Has(device.DimOSPF) {
		return
	}
	for _, n := range sw.State.OSPF {
		name, _ := device.CanonicalInterface(n.Interface)
		k := portKey{sw.Name, name}
		out[k] = append(out[k], n)
	}
}

// probe verifies the physical and routing layers of c and diagnoses it.
func (e *evidenceSet) probe(c *LinkCheck) {
	phys := e.physical(c.A, c.B)
	nbr := Probe{Layer: LayerNeighbor}
	switch c.State {
	case StateOK, StateUndeclared:
		nbr.Verdict = VerdictOK
	case StateMissing, StateMiscabled:
		nbr.Verdict, nbr.Evidence = VerdictFail, c.Detail
	case StateUnknown:
	}
	c.Ends = [2]End{e.end(c.A), e.end(c.B)}
	issues := append(endIssues(c.Ends[0], e.configured(c.A.Switch)), endIssues(c.Ends[1], e.configured(c.B.Switch))...)
	if len(issues) == 0 {
		issues = c.Mismatch
	}
	conf := Probe{Layer: LayerConfig}
	switch {
	case len(issues) > 0:
		conf.Verdict, conf.Evidence = VerdictFail, strings.Join(issues, "；")
	case e.configured(c.A.Switch) && e.configured(c.B.Switch):
		conf.Verdict = VerdictOK
	}
	c.Layers = []Probe{phys, nbr, conf, e.routing(c.A, c.B)}
	c.Diagnosis = diagnose(c)
}

type evidenceSet struct {
	byName map[string]*device.Switch
	ports  ports
	ospf   adjacencies
}

func (e *evidenceSet) configured(name string) bool {
	sw, ok := e.byName[name]
	return ok && sw.Configured()
}

// lacks reports whether the switch's configuration, observed completely,
// has no such interface.
func (e *evidenceSet) lacks(end fabric.LinkEnd) bool {
	sw, ok := e.byName[end.Switch]
	if !ok || !sw.Configured() {
		return false
	}
	_, has := sw.Config.Interfaces[end.Interface]
	return !has
}

func (e *evidenceSet) observed(name string, d evidence.Dimension) bool {
	sw, ok := e.byName[name]
	return ok && sw.Has(d)
}

func (e *evidenceSet) physical(a, b fabric.LinkEnd) Probe {
	p := Probe{Layer: LayerPhysical}
	var down []string
	known := 0
	for _, end := range []fabric.LinkEnd{a, b} {
		if !e.observed(end.Switch, device.DimInterfaces) {
			continue
		}
		st, ok := e.ports[portKey{end.Switch, end.Interface}]
		switch {
		case ok:
			known++
			if !st.Up {
				down = append(down, fmt.Sprintf("%s %s %s", end.Switch, end.Interface, portCondition(st)))
			}
		case e.lacks(end):
			// Neither the port table nor the configuration knows the port.
			known++
			down = append(down, fmt.Sprintf("%s %s 不存在", end.Switch, end.Interface))
		}
	}
	switch {
	case len(down) > 0:
		p.Verdict, p.Evidence = VerdictFail, strings.Join(down, "；")
	case known > 0:
		p.Verdict = VerdictOK
		if st, ok := e.ports[portKey{a.Switch, a.Interface}]; ok && st.Speed != "" {
			p.Evidence = "up " + st.Speed
		}
	}
	return p
}

func portCondition(st device.InterfaceStatus) string {
	s := st.Status
	if s == "" {
		s = "down"
	}
	if st.Reason != "" && st.Reason != "none" {
		s += "（" + st.Reason + "）"
	}
	return s
}

// routing reports whether OSPF is FULL on the link as seen from every
// end whose OSPF neighbors were collected: a one-sided adjacency is a
// fault too.
func (e *evidenceSet) routing(a, b fabric.LinkEnd) Probe {
	p := Probe{Layer: LayerRouting}
	var full, bad []string
	for _, end := range []fabric.LinkEnd{a, b} {
		if !e.observed(end.Switch, device.DimOSPF) {
			continue
		}
		if rid, state := adjacencyOn(e.ospf[portKey{end.Switch, end.Interface}]); rid != "" {
			full = append(full, end.Switch+" 看到 "+rid)
		} else {
			bad = append(bad, end.Switch+" "+end.Interface+" "+state)
		}
	}
	switch {
	case len(bad) > 0:
		p.Verdict, p.Evidence = VerdictFail, "OSPF 未在该互联上形成 FULL 邻接："+strings.Join(bad, "，")
	case len(full) > 0:
		p.Verdict, p.Evidence = VerdictOK, "OSPF FULL（"+strings.Join(full, "，")+"）"
	}
	return p
}

// adjacencyOn returns the router ID of a FULL neighbor, or the state of
// the best non-FULL one ("无邻居" without any).
func adjacencyOn(ns []device.OSPFNeighbor) (routerID, state string) {
	state = "无邻居"
	for _, n := range ns {
		if n.Full {
			return n.RouterID, ""
		}
		state = n.State
	}
	return "", state
}

// Healthy reports whether no layer of the link failed and its cabling is
// as declared.
func (c *LinkCheck) Healthy() bool {
	return c.Diagnosis == nil && (c.State == StateOK || c.State == StateUndeclared)
}

// Probe returns the link's verdict at one layer.
func (c *LinkCheck) Probe(l Layer) Probe {
	for _, p := range c.Layers {
		if p.Layer == l {
			return p
		}
	}
	return Probe{Layer: l}
}

func diagnose(c *LinkCheck) *Diagnosis {
	i := slices.IndexFunc(c.Layers, func(p Probe) bool { return p.Verdict == VerdictFail })
	if i < 0 {
		if c.State == StateUndeclared {
			return &Diagnosis{Fault: LayerNeighbor, Cause: "互联可用但意图未声明", Hint: "确认是否为新增上行；是则补入意图 links，否则拆除"}
		}
		return nil
	}
	p := c.Layers[i]
	d := &Diagnosis{Fault: p.Layer, Cause: p.Evidence}
	switch p.Layer {
	case LayerPhysical:
		d.Cause, d.Hint = physicalCause(p.Evidence)
	case LayerNeighbor:
		if c.State == StateMiscabled {
			d.Cause, d.Hint = "错接："+c.Detail, "按意图调整跳线，或确认现网正确后更新意图中的互联"
		} else {
			d.Cause, d.Hint = "端口 up 但 CDP 未发现对端", "检查端口是否关闭了 CDP（no cdp enable），或对端不是意图中的设备"
		}
	case LayerConfig:
		d.Cause, d.Hint = "两端配置不一致："+p.Evidence, "用 fabric plan 生成修正计划，经 fabric deploy 下发"
	case LayerRouting:
		d.Hint = "检查两端 OSPF 网络类型、认证、MTU 与区域是否一致；fabric switch show 查看该交换机的邻接"
	}
	return d
}

func physicalCause(evidence string) (cause, hint string) {
	lower := strings.ToLower(evidence)
	switch {
	case strings.Contains(lower, "administratively") || strings.Contains(lower, "disabled") && !strings.Contains(lower, "errdisabled"):
		return "端口被管理关闭：" + evidence, "确认是否为有意关闭；需要恢复时经变更流程执行 no shutdown"
	case strings.Contains(lower, "errdisabled") || strings.Contains(lower, "err-disabled"):
		return "端口 err-disabled：" + evidence, "查看 show interface status err-disabled 的原因，排除后 shutdown/no shutdown 恢复"
	case strings.Contains(lower, "xcvr") || strings.Contains(lower, "sfp"):
		return "光模块缺失或异常：" + evidence, "现场检查光模块是否插好、型号是否匹配"
	case strings.Contains(lower, "不存在"):
		return "意图中的端口在交换机上不存在：" + evidence, "核对意图中的端口编号与设备型号"
	case strings.Contains(lower, "link not connected"), strings.Contains(lower, "notconnect"):
		return "物理链路未连通：" + evidence, "现场检查跳线、光模块与配线架，确认两端端口在同一条线缆上；清除后执行 fabric collect 复核"
	default:
		return "端口 down：" + evidence, "现场检查线缆与光模块，查看 show interface 的错误计数"
	}
}
