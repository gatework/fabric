package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/gatework/netops/term"

	"github.com/gatework/fabric/topology"
)

// Line glyphs: a healthy link at the fabric's full speed is thick, a
// slower one thin, a fault dashed. The label sits in the middle.
const lineWidth = 12

func pad(s string, w int) string { return s + strings.Repeat(" ", max(0, w-term.Width(s))) }

func padLeft(s string, w int) string { return strings.Repeat(" ", max(0, w-term.Width(s))) + s }

func center(label, fill string, w int) string {
	n := max(0, w-term.Width(label)-2)
	left := n / 2
	return strings.Repeat(fill, left) + " " + label + " " + strings.Repeat(fill, n-left)
}

// shortPort abbreviates Ethernet1/49 to Eth1/49 and port-channel10 to Po10.
func shortPort(p string) string {
	switch {
	case strings.HasPrefix(p, "Ethernet"):
		return "Eth" + strings.TrimPrefix(p, "Ethernet")
	case strings.HasPrefix(p, "port-channel"):
		return "Po" + strings.TrimPrefix(p, "port-channel")
	default:
		return p
	}
}

// bareSpeed drops the duplex marker of "100G(D)" and writes megabit
// speeds ("1000", "10000") in gigabits.
func bareSpeed(s string) string {
	s, _, _ = strings.Cut(s, "(")
	if n, err := strconv.Atoi(s); err == nil && n >= 1000 && n%1000 == 0 {
		return strconv.Itoa(n/1000) + "G"
	}
	return s
}

// linkGlyph draws one link. full is the region's fastest uplink speed.
func linkGlyph(c topology.LinkCheck, bgp topology.Session, full string) string {
	switch {
	case c.State == topology.StateMiscabled:
		return center("≠ 错接", "╌", lineWidth)
	case c.Diagnosis != nil && c.State != topology.StateUndeclared:
		return center("✗ "+c.Diagnosis.Fault.String(), "╌", lineWidth)
	case c.State == topology.StateUnknown:
		return center("?", "┄", lineWidth)
	}
	speed := bareSpeed(c.Ends[0].Speed)
	if speed == "" {
		speed = bareSpeed(c.Ends[1].Speed)
	}
	label := speed
	if bgp.Verdict == topology.VerdictFail {
		label = "⚠ BGP"
	}
	if c.State == topology.StateUndeclared {
		label = "+ " + speed
	}
	fill := "━"
	if speed != "" && full != "" && speed != full {
		fill = "─"
	}
	return center(label, fill, lineWidth)
}

// fullSpeed returns the most common speed of healthy fabric links.
func fullSpeed(a topology.Audit) string {
	count := map[string]int{}
	best := ""
	for _, c := range a.Links {
		if !c.Healthy() {
			continue
		}
		s := bareSpeed(c.Ends[0].Speed)
		count[s]++
		if s != "" && count[s] > count[best] {
			best = s
		}
	}
	return best
}

// between returns the links of a VTEP to a spine, the VTEP end first.
func between(a topology.Audit, vtep, spine string) []topology.LinkCheck {
	var out []topology.LinkCheck
	for _, c := range a.Links {
		switch {
		case c.A.Switch == vtep && c.B.Switch == spine:
			out = append(out, c)
		case c.B.Switch == vtep && c.A.Switch == spine:
			c.A, c.B = c.B, c.A
			c.Ends[0], c.Ends[1] = c.Ends[1], c.Ends[0]
			out = append(out, c)
		}
	}
	return out
}

func sessionTo(u topology.Uplinks, spine string) topology.Session {
	for _, s := range u.BGP {
		if s.Spine == spine {
			return s
		}
	}
	return topology.Session{Spine: spine}
}

// shortCause is the one-line reason drawn beside a broken link.
func shortCause(c topology.LinkCheck) string {
	d := c.Diagnosis
	switch d.Fault {
	case topology.LayerPhysical:
		var down []string
		reason := ""
		for _, e := range c.Ends {
			if e.Status != "" && !e.Up {
				down = append(down, shortPort(e.Interface))
				reason = e.Reason
			}
		}
		if len(down) == 2 && reason != "" {
			return "两端 down：" + reason
		}
		return d.Cause
	case topology.LayerNeighbor:
		if c.State == topology.StateMiscabled {
			return c.Detail
		}
		return "端口 up，CDP 不可见"
	case topology.LayerRouting:
		return "OSPF 未 FULL"
	case topology.LayerConfig:
		return c.Probe(topology.LayerConfig).Evidence
	default:
		return d.Cause
	}
}

const diagramPortW = 8

// sideWidth is the width of one side of a diagram row: spine port, line
// and VTEP port.
const sideWidth = diagramPortW + 1 + lineWidth + 1 + diagramPortW

// printRegionDiagram draws a two-spine region: spines as the left and
// right buses, every VTEP between them, each uplink with its ports and
// its state.
func printRegionDiagram(w io.Writer, a topology.Audit) {
	if len(a.Spines) != 2 {
		printRegionList(w, a)
		return
	}
	d := regionDiagram{a: a, left: a.Spines[0], right: a.Spines[1], full: fullSpeed(a), ups: map[string]topology.Uplinks{}, nameW: 10}
	for _, u := range a.Uplinks {
		d.ups[u.Switch] = u
	}
	for _, v := range a.VTEPs {
		d.nameW = max(d.nameW, term.Width(v))
	}
	bodyW := 2*sideWidth + 4 + d.nameW
	loads := map[string]topology.SpineLoad{}
	for _, s := range a.SpineLoads {
		loads[s.Switch] = s
	}
	head := fmt.Sprintf("%s（下联 %d/%d）", d.left, loads[d.left].Healthy, loads[d.left].Declared)
	tail := fmt.Sprintf("%s（下联 %d/%d）", d.right, loads[d.right].Healthy, loads[d.right].Declared)
	_ = term.Printf(w, "  %s%s\n", pad(head, bodyW+2-term.Width(tail)), tail)
	_ = term.Printf(w, "  ┃%s┃\n", strings.Repeat(" ", bodyW))
	for _, v := range a.VTEPs {
		_ = term.Printf(w, "%s\n", d.row(v))
	}
	_ = term.Printf(w, "  ┃%s┃\n", strings.Repeat(" ", bodyW))
	printLegend(w, d.full)
}

type regionDiagram struct {
	a           topology.Audit
	left, right string
	full        string
	ups         map[string]topology.Uplinks
	nameW       int
}

// side draws a VTEP's link to one spine; mirrored puts the spine on the
// right. It returns the drawing, the bus glyph and the fault note.
func (d regionDiagram) side(v, spine string, mirrored bool) (draw, bus, note string) {
	links := between(d.a, v, spine)
	if len(links) == 0 {
		return strings.Repeat(" ", sideWidth), "┃", ""
	}
	c := links[0]
	glyph := linkGlyph(c, sessionTo(d.ups[v], spine), d.full)
	if mirrored {
		draw = padLeft(shortPort(c.A.Interface), diagramPortW) + " " + glyph + " " + pad(shortPort(c.B.Interface), diagramPortW)
		bus = "┫"
	} else {
		draw = padLeft(shortPort(c.B.Interface), diagramPortW) + " " + glyph + " " + pad(shortPort(c.A.Interface), diagramPortW)
		bus = "┣"
	}
	if c.Diagnosis != nil {
		note = spine + "：" + shortCause(c)
	}
	return draw, bus, note
}

func (d regionDiagram) row(v string) string {
	u := d.ups[v]
	lc, lbus, lnote := d.side(v, d.left, false)
	rc, rbus, rnote := d.side(v, d.right, true)
	var notes []string
	for _, n := range []string{lnote, rnote} {
		if n != "" {
			notes = append(notes, n)
		}
	}
	switch {
	case u.Redundancy == topology.RedundancyUnknown && slices.Contains(d.a.Unobserved, v):
		notes = append(notes, "无快照")
	case u.Redundancy.AtRisk():
		notes = append(notes, u.Redundancy.String())
	}
	if u.Asymmetric() {
		notes = append(notes, "速率不对称 "+strings.Join(u.Speeds, "/"))
	}
	line := strings.TrimRight(fmt.Sprintf("  %s%s  %s  %s%s %s", lbus, lc, pad(v, d.nameW), rc, rbus, vpcMark(d.a, v)), " ")
	if len(notes) > 0 {
		line += "   ← " + strings.Join(notes, "；")
	}
	return line
}

func printLegend(w io.Writer, full string) {
	_ = term.Printf(w, "  图例：━ 正常（%s） ─ 正常但低于 %s  ╌ ✗ 故障（标出最低失败层）  ≠ 错接  + 未声明  ┄ ? 无证据  ⚠ BGP EVPN 会话异常  ┐┘ vPC 对\n", term.Dash(full), term.Dash(full))
}

// vpcMark brackets vPC pairs listed one after the other.
func vpcMark(a topology.Audit, v string) string {
	p, ok := a.PairOf(v)
	if !ok {
		return ""
	}
	first := p.Switches[0]
	if i, j := slices.Index(a.VTEPs, p.Switches[0]), slices.Index(a.VTEPs, p.Switches[1]); j >= 0 && (i < 0 || j < i) {
		first = p.Switches[1]
	}
	if v == first {
		return fmt.Sprintf("┐ vPC %d", p.Domain)
	}
	return "┘"
}

// printRegionList is the fallback for regions without exactly two
// spines: one line per VTEP with an entry per spine.
func printRegionList(w io.Writer, a topology.Audit) {
	full := fullSpeed(a)
	ups := map[string]topology.Uplinks{}
	for _, u := range a.Uplinks {
		ups[u.Switch] = u
	}
	for _, v := range a.VTEPs {
		var parts []string
		for _, sp := range a.Spines {
			for _, c := range between(a, v, sp) {
				parts = append(parts, shortPort(c.A.Interface)+" "+linkGlyph(c, sessionTo(ups[v], sp), full)+" "+sp+" "+shortPort(c.B.Interface))
			}
		}
		if len(parts) == 0 {
			parts = append(parts, "（意图未声明到 spine 的上行）")
		}
		_ = term.Printf(w, "  %s  %s\n", pad(v, 12), strings.Join(parts, "   "))
	}
	printLegend(w, full)
}

type branch struct {
	port, glyph, far, kind, note string
}

// peerLinkBranch draws the vPC peer-link of member i of the pair.
func peerLinkBranch(p topology.VPCPair, i int) (branch, bool) {
	pl := p.PeerLinks[i]
	if pl == nil {
		return branch{}, false
	}
	peer := p.Switches[1-i]
	label, fill := "peer-link", "═"
	switch {
	case pl.Virtual:
		label = "虚拟 peer-link"
	case !pl.Up || !pl.PeerOK:
		label, fill = "✗ peer-link", "╌"
	}
	var notes []string
	if len(pl.Members) > 0 {
		short := make([]string, len(pl.Members))
		for j, m := range pl.Members {
			short[j] = shortPort(m)
		}
		notes = append(notes, "成员 "+strings.Join(short, " "))
	}
	notes = append(notes, "对端 "+okWord(pl.PeerOK), "keepalive "+okWord(pl.KeepaliveOK))
	port := shortPort(pl.Port)
	if port == "" {
		port = "-"
	}
	return branch{port, center(label, fill, lineWidth), peer, fmt.Sprintf("vPC %d", p.Domain), strings.Join(notes, " · ")}, true
}

func okWord(ok bool) string {
	if ok {
		return "正常"
	}
	return "异常"
}

// printSwitchTree draws one switch and everything CDP and the intent
// connect it to: fabric links (also those CDP no longer sees), the vPC
// peer, the other fabric switches, external devices and the management
// segment.
func printSwitchTree(w io.Writer, a topology.Audit, sw string, verbose bool) {
	t := newSwitchTree(a, sw)
	t.links()
	t.peerLink()
	t.neighbors(verbose)
	_ = term.Printf(w, "  %s\n", t.head())
	t.render(w)
}

type switchTree struct {
	a        topology.Audit
	sw, full string
	own      topology.Uplinks
	ups      map[string]topology.Uplinks
	seen     map[string]bool
	branches []branch
}

func newSwitchTree(a topology.Audit, sw string) *switchTree {
	t := &switchTree{a: a, sw: sw, full: fullSpeed(a), ups: map[string]topology.Uplinks{}, seen: map[string]bool{}}
	for _, x := range a.Uplinks {
		t.ups[x.Switch] = x
		if x.Switch == sw {
			t.own = x
		}
	}
	return t
}

// session is the BGP EVPN session of a link, as its VTEP end reports it.
func (t *switchTree) session(far string) topology.Session {
	if t.own.Switch == t.sw {
		return sessionTo(t.own, far)
	}
	return sessionTo(t.ups[far], t.sw)
}

func (t *switchTree) links() {
	for _, c := range t.a.Links {
		if c.A.Switch != t.sw && c.B.Switch != t.sw {
			continue
		}
		if c.B.Switch == t.sw {
			c.A, c.B = c.B, c.A
			c.Ends[0], c.Ends[1] = c.Ends[1], c.Ends[0]
		}
		t.seen[c.A.Interface] = true
		kind := "fabric 互联"
		if c.State == topology.StateUndeclared {
			kind = "未声明互联"
		}
		t.branches = append(t.branches, branch{shortPort(c.A.Interface), linkGlyph(c, t.session(c.B.Switch), t.full),
			c.B.Switch + " " + shortPort(c.B.Interface), kind, t.linkNote(c)})
	}
}

func (t *switchTree) linkNote(c topology.LinkCheck) string {
	if c.Diagnosis != nil {
		return shortCause(c)
	}
	var parts []string
	if c.Probe(topology.LayerRouting).Verdict == topology.VerdictOK {
		parts = append(parts, "OSPF FULL")
	}
	if s := t.session(c.B.Switch); s.Verdict != topology.VerdictUnknown {
		parts = append(parts, strings.TrimSpace("BGP "+s.State+" "+s.Uptime))
	}
	return strings.Join(parts, " · ")
}

func (t *switchTree) peerLink() {
	p, ok := t.a.PairOf(t.sw)
	if !ok {
		return
	}
	i := 0
	if p.Switches[1] == t.sw {
		i = 1
	}
	b, ok := peerLinkBranch(p, i)
	if !ok {
		return
	}
	for _, n := range t.a.Neighbors {
		if n.Switch == t.sw && n.Kind == topology.NeighborPeerLink {
			t.seen[n.Port] = true
		}
	}
	t.branches = append(t.branches, b)
}

func (t *switchTree) neighbors(verbose bool) {
	mgmt := 0
	for _, n := range t.a.Neighbors {
		if n.Switch != t.sw || t.seen[n.Port] {
			continue
		}
		if n.Kind == topology.NeighborManagement && !verbose {
			mgmt++
			continue
		}
		fill, label := "─", bareSpeed(n.Speed)
		if !n.Up && n.Speed != "" {
			fill, label = "╌", "down"
		}
		note := n.Platform
		if n.Bundle != "" {
			note = strings.TrimSpace(shortPort(n.Bundle) + " 成员 · " + note)
		}
		t.branches = append(t.branches, branch{shortPort(n.Port), center(label, fill, lineWidth),
			n.Device + " " + shortPort(n.RemotePort), n.Kind.String(), note})
	}
	if mgmt > 0 {
		t.branches = append(t.branches, branch{"mgmt0", strings.Repeat(" ", lineWidth), fmt.Sprintf("%d 个 CDP 邻居", mgmt), "管理网", "-v 列出"})
	}
}

func (t *switchTree) head() string {
	head := t.sw
	if t.own.Declared > 0 {
		head += fmt.Sprintf("    上行：%s，健康 %d/%d", t.own.Redundancy, t.own.Healthy, t.own.Declared)
	}
	if p, ok := t.a.PairOf(t.sw); ok {
		peer := p.Switches[0]
		if peer == t.sw {
			peer = p.Switches[1]
		}
		head += fmt.Sprintf("    vPC %d 对端 %s", p.Domain, peer)
	}
	return head
}

func (t *switchTree) render(w io.Writer) {
	if len(t.branches) == 0 {
		_ = term.Printf(w, "  └── 没有 CDP 邻居，也没有声明的 fabric 互联\n")
		return
	}
	portW, farW, kindW, glyphW := 7, 10, 8, lineWidth
	for _, b := range t.branches {
		glyphW = max(glyphW, term.Width(b.glyph))
		portW = max(portW, term.Width(b.port))
		farW = max(farW, term.Width(b.far))
		kindW = max(kindW, term.Width(b.kind))
	}
	for i, b := range t.branches {
		elbow := "├──"
		if i == len(t.branches)-1 {
			elbow = "└──"
		}
		line := fmt.Sprintf("  %s %s %s %s  %s  %s", elbow, pad(b.port, portW), pad(b.glyph, glyphW), pad(b.far, farW), pad(b.kind, kindW), b.note)
		_ = term.Printf(w, "%s\n", strings.TrimRight(line, " "))
	}
}
