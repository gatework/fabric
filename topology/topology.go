package topology

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// State is a link's audit verdict.
type State uint8

// Link states.
const (
	StateUnknown State = iota
	StateOK
	StateMissing
	StateMiscabled
	StateUndeclared
)

// String returns the Chinese label.
func (s State) String() string {
	switch s {
	case StateOK:
		return "正常"
	case StateMissing:
		return "缺失"
	case StateMiscabled:
		return "错接"
	case StateUndeclared:
		return "未声明"
	case StateUnknown:
		return "未知"
	default:
		return "未知"
	}
}

// MarshalText encodes the label.
func (s State) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// Adjacency is one CDP neighbor seen from a port.
type Adjacency struct {
	Switch     string `json:"switch"`
	Port       string `json:"port"`
	Neighbor   string `json:"neighbor"`
	RemotePort string `json:"remote_port"`
}

// LinkCheck is the verdict on one link.
type LinkCheck struct {
	A, B   fabric.LinkEnd `json:"-"`
	Name   string         `json:"link"`
	State  State          `json:"state"`
	Detail string         `json:"detail,omitempty"`
	// Mismatch lists attributes the two ends configure differently.
	Mismatch []string `json:"mismatch,omitempty"`
}

// Audit is one region's cabling verdict.
type Audit struct {
	Region string      `json:"region"`
	Links  []LinkCheck `json:"links"`
	// Unmeshed lists VTEP–spine pairs without any link.
	Unmeshed []string `json:"unmeshed,omitempty"`
	VTEPs    []string `json:"vteps"`
	Spines   []string `json:"spines"`
	// Cells holds, per "vtep|spine", the worst state of their links.
	Cells map[string]State `json:"cells"`
}

// NeighborName reduces a CDP device ID ("LEF01.example.net(FDO123)") to
// the switch name.
func NeighborName(id string) string {
	if i := strings.IndexByte(id, '('); i > 0 {
		id = id[:i]
	}
	if i := strings.IndexByte(id, '.'); i > 0 {
		id = id[:i]
	}
	return strings.TrimSpace(id)
}

type portKey struct{ sw, port string }

// Run audits a region.
func Run(r *fabric.Region, switches []*device.Switch) Audit {
	a := Audit{Region: r.Name, Cells: map[string]State{}}
	byName := map[string]*device.Switch{}
	cdp := map[portKey]Adjacency{}
	for _, sw := range switches {
		byName[sw.Name] = sw
		if !sw.Has(device.DimCDP) {
			continue
		}
		for _, n := range sw.State.Neighbors {
			remote, _ := device.CanonicalInterface(n.RemotePort)
			adj := Adjacency{Switch: sw.Name, Port: n.LocalPort, Neighbor: NeighborName(n.DeviceID), RemotePort: remote}
			cdp[portKey{sw.Name, n.LocalPort}] = adj
		}
	}
	declared := map[portKey]bool{}
	for _, l := range r.Links {
		declared[portKey{l.A.Switch, l.A.Interface}] = true
		declared[portKey{l.B.Switch, l.B.Interface}] = true
		a.Links = append(a.Links, check(l, byName, cdp))
	}
	if r.Underlay != nil {
		a.Links = append(a.Links, undeclared(r, cdp, declared)...)
	}
	a.matrix(r)
	return a
}

func linkName(x, y fabric.LinkEnd) string {
	return fmt.Sprintf("%s %s ↔ %s %s", x.Switch, x.Interface, y.Switch, y.Interface)
}

func check(l *fabric.Link, byName map[string]*device.Switch, cdp map[portKey]Adjacency) LinkCheck {
	c := LinkCheck{A: l.A, B: l.B, Name: linkName(l.A, l.B)}
	seenA, seenB := has(byName, l.A.Switch), has(byName, l.B.Switch)
	adjA, okA := cdp[portKey{l.A.Switch, l.A.Interface}]
	adjB, okB := cdp[portKey{l.B.Switch, l.B.Interface}]
	switch {
	case okA && (adjA.Neighbor != l.B.Switch || adjA.RemotePort != l.B.Interface):
		c.State, c.Detail = StateMiscabled, fmt.Sprintf("%s %s 实际连接 %s %s", l.A.Switch, l.A.Interface, adjA.Neighbor, adjA.RemotePort)
	case okB && (adjB.Neighbor != l.A.Switch || adjB.RemotePort != l.A.Interface):
		c.State, c.Detail = StateMiscabled, fmt.Sprintf("%s %s 实际连接 %s %s", l.B.Switch, l.B.Interface, adjB.Neighbor, adjB.RemotePort)
	case okA || okB:
		c.State = StateOK
	case seenA || seenB:
		c.State, c.Detail = StateMissing, "两端 CDP 均未看到对端"
	default:
		c.State = StateUnknown
	}
	c.Mismatch = mismatch(l, byName)
	return c
}

func has(byName map[string]*device.Switch, name string) bool {
	sw, ok := byName[name]
	return ok && sw.Has(device.DimCDP)
}

// mismatch compares the two ends' configuration.
func mismatch(l *fabric.Link, byName map[string]*device.Switch) []string {
	a, okA := byName[l.A.Switch]
	b, okB := byName[l.B.Switch]
	if !okA || !okB || a.Config == nil || b.Config == nil {
		return nil
	}
	ia, okA := a.Config.Interfaces[l.A.Interface]
	ib, okB := b.Config.Interfaces[l.B.Interface]
	if !okA || !okB {
		return []string{"接口缺失"}
	}
	var out []string
	if ia.MTU != ib.MTU {
		out = append(out, fmt.Sprintf("MTU %d/%d", ia.MTU, ib.MTU))
	}
	oa, ob := ospfOf(ia), ospfOf(ib)
	if oa != ob {
		out = append(out, fmt.Sprintf("OSPF %s/%s", oa, ob))
	}
	pa, _ := ia.Primary()
	pb, _ := ib.Primary()
	if !pa.IsValid() || !pb.IsValid() || pa.Masked() != pb.Masked() {
		out = append(out, fmt.Sprintf("子网 %s/%s", pa, pb))
	}
	return out
}

func ospfOf(i device.Interface) string {
	if i.OSPF == nil {
		return "-"
	}
	p2p := ""
	if i.OSPF.PointToPoint {
		p2p = " p2p"
	}
	return i.OSPF.Process + " area " + i.OSPF.Area + p2p
}

// undeclared reports VTEP–spine adjacencies on ports no link declares.
func undeclared(r *fabric.Region, cdp map[portKey]Adjacency, declared map[portKey]bool) []LinkCheck {
	var out []LinkCheck
	keys := make([]portKey, 0, len(cdp))
	for k := range cdp {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(x, y portKey) int { return cmp.Or(cmp.Compare(x.sw, y.sw), cmp.Compare(x.port, y.port)) })
	for _, k := range keys {
		adj := cdp[k]
		local, ok1 := r.Switch(k.sw)
		remote, ok2 := r.Switch(adj.Neighbor)
		if !ok1 || !ok2 || !local.Role.VTEP() || remote.Role != fabric.RoleSpine || declared[k] {
			continue
		}
		x := fabric.LinkEnd{Switch: k.sw, Interface: k.port}
		y := fabric.LinkEnd{Switch: adj.Neighbor, Interface: adj.RemotePort}
		out = append(out, LinkCheck{A: x, B: y, Name: linkName(x, y), State: StateUndeclared, Detail: "CDP 可见但意图未声明"})
	}
	return out
}

func (a *Audit) matrix(r *fabric.Region) {
	for _, s := range r.VTEPs() {
		a.VTEPs = append(a.VTEPs, s.Name)
	}
	for _, s := range r.ByRole(fabric.RoleSpine) {
		a.Spines = append(a.Spines, s.Name)
	}
	for _, c := range a.Links {
		v, sp := c.A.Switch, c.B.Switch
		if rs, ok := r.Switch(v); ok && rs.Role == fabric.RoleSpine {
			v, sp = sp, v
		}
		key := v + "|" + sp
		if cur, ok := a.Cells[key]; !ok || worse(c.State, cur) {
			a.Cells[key] = c.State
		}
	}
	for _, v := range a.VTEPs {
		for _, sp := range a.Spines {
			if _, ok := a.Cells[v+"|"+sp]; !ok {
				a.Unmeshed = append(a.Unmeshed, v+" ↔ "+sp)
			}
		}
	}
}

// worse ranks states for the matrix: problems over unknown over OK.
func worse(x, y State) bool {
	rank := func(s State) int {
		switch s {
		case StateOK:
			return 0
		case StateUnknown:
			return 1
		case StateUndeclared:
			return 2
		case StateMissing:
			return 3
		case StateMiscabled:
			return 4
		default:
			return 1
		}
	}
	return rank(x) > rank(y)
}
