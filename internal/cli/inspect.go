package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/term"

	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/internal/bootstrap"
	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/report"
	"github.com/gatework/fabric/topology"
)

func newSwitchShow(e env) *cobra.Command {
	var ff fleetFlags
	var basePath string
	var verbose, asJSON bool
	c := &cobra.Command{
		Use:               "show 交换机名",
		Aliases:           []string{"inspect"},
		Short:             "单台交换机全景：健康评分、上行与互联、控制面会话、vPC 与聚合、本机发现",
		Example:           "  fabric switch show SHA2-BDR02\n  fabric switch show SHA2-BDR02 --json",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: switchArg(e),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleet, err := ff.load(cmd, nil)
			if err != nil {
				return err
			}
			base, err := bootstrap.ReadBaseline(basePath)
			if err != nil {
				return err
			}
			d, err := workflow.Inspect(cmd.Context(), workflow.CheckRequest{Fleet: fleet, Baseline: base}, args[0])
			if err != nil {
				return exit(ExitError, "%v（可用 Tab 补全交换机名）", err)
			}
			if asJSON {
				return writeJSON(e, d)
			}
			printSwitchDetail(e.stdout, d, verbose)
			return nil
		},
	}
	ff.bind(c, false)
	c.Flags().StringVar(&basePath, "baseline", "", "基线文件")
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "列出全部发现与会话")
	c.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	return c
}

func printSwitchDetail(w io.Writer, d workflow.SwitchDetail, verbose bool) {
	sw, h := d.Switch, d.Health
	model := ""
	if sw.Model != "" {
		model = "  型号 " + sw.Model
	}
	_ = term.Printf(w, "%s  %s  区域 %s  管理 %s%s\n", sw.Name, sw.Role, term.Dash(sw.Region), sw.Mgmt, model)
	_ = term.Printf(w, "健康：%d %s   按意图期望：NVE对端 %s  EVPN会话 %s  OSPF %s  VNI %s  vPC %s  聚合 %s\n", h.Score, h.Level,
		ratio(h.NVEPeers), ratio(h.Sessions), ratio(h.OSPF), ratio(h.VNIs), ratio(h.VPC), ratio(h.Bundles))
	if len(h.Missing) > 0 {
		_ = term.Printf(w, "  ⚠ 证据不完整：%v，相关指标显示为未知\n", h.Missing)
	}
	limit := 8
	if verbose {
		limit = 0
	}
	printUplinks(w, d, verbose)
	printSessions(w, d, limit, verbose)
	printVPC(w, sw)
	printBundles(w, sw)
	printSwitchFindings(w, sw.Name, d.Findings, limit)
	next := []string{"fabric topology --switch " + sw.Name}
	if len(d.Findings) > 0 {
		next = append(next, "fabric check --switch "+sw.Name+" -v")
	}
	next = append(next, "fabric locate <MAC|IP>")
	_ = term.Printf(w, "\n下一步：%s\n", strings.Join(next, " · "))
}

func printUplinks(w io.Writer, d workflow.SwitchDetail, verbose bool) {
	audits := onlySwitch([]topology.Audit{d.Topology}, d.Switch.Name)
	if len(audits) == 0 && len(d.Topology.Neighbors) == 0 {
		return
	}
	a := d.Topology
	if len(audits) > 0 {
		a = audits[0]
	}
	_ = term.Printf(w, "\n邻居拓扑：\n")
	printSwitchTree(w, a, d.Switch.Name, verbose)
	if u := d.Uplinks; u != nil && u.Asymmetric() {
		_ = term.Printf(w, "  ⚠ 上行速率不一致：%s（ECMP 按条数均分流量，低速链路先拥塞）\n", strings.Join(u.Speeds, " / "))
	}
	if d.Downlinks != nil {
		_ = term.Printf(w, "  下联：健康 %d/%d\n", d.Downlinks.Healthy, d.Downlinks.Declared)
		printSpineRisk(w, d.Switch.Name, a.AtRisk())
	}
	if verbose && len(d.Links) > 0 {
		printLinks(w, topology.Audit{Links: d.Links}, d.Switch.Name)
	}
	for _, c := range d.Links {
		if c.Diagnosis != nil {
			_ = term.Printf(w, "  ✗ %s：%s；建议：%s\n", c.Name, c.Diagnosis.Cause, c.Diagnosis.Hint)
		}
	}
}

// compactRows is the table size above which healthy rows are left out
// unless -v asks for them.
const compactRows = 6

func peerName(d workflow.SwitchDetail, addr string) string {
	if n, ok := d.Peers[addr]; ok {
		return n + " " + addr
	}
	return addr
}

func printSessions(w io.Writer, d workflow.SwitchDetail, limit int, verbose bool) {
	if d.Switch.Has(device.DimOSPF) {
		printOSPF(w, d, verbose)
	}
	if d.Switch.Has(device.DimBGP) {
		printBGP(w, d, verbose)
	}
	printOverlay(w, d, limit)
}

func printOSPF(w io.Writer, d workflow.SwitchDetail, verbose bool) {
	st := d.Switch.State
	t := term.NewTable("OSPF 邻居", "状态", "接口", "地址", "进程")
	compact := !verbose && len(st.OSPF) > compactRows
	for _, n := range st.OSPF {
		if !compact || !n.Full {
			t.Add(peerName(d, n.RouterID), n.State, n.Interface, n.Address, n.Process)
		}
	}
	_ = term.Printf(w, "\nOSPF：%d 个邻居，FULL %d\n", len(st.OSPF), countTrue(st.OSPF, func(n device.OSPFNeighbor) bool { return n.Full }))
	renderCompact(w, t, compact)
}

func printBGP(w io.Writer, d workflow.SwitchDetail, verbose bool) {
	bgp := d.Switch.State.BGP
	t := term.NewTable("BGP EVPN 邻居", "AS", "状态", "时长", "前缀")
	compact := !verbose && len(bgp.Neighbors) > compactRows
	for _, n := range bgp.Neighbors {
		if compact && n.Established {
			continue
		}
		state := "Established"
		if !n.Established {
			state = "✗ " + term.Dash(n.State)
		}
		t.Add(peerName(d, n.Address), fmt.Sprint(n.AS), state, n.UpDown, fmt.Sprint(n.Prefixes))
	}
	_ = term.Printf(w, "\nBGP EVPN：router-id %s，AS %d，%d 个会话，建立 %d\n", term.Dash(bgp.RouterID), bgp.LocalAS,
		len(bgp.Neighbors), countTrue(bgp.Neighbors, func(n device.BGPNeighbor) bool { return n.Established }))
	renderCompact(w, t, compact)
}

func printOverlay(w io.Writer, d workflow.SwitchDetail, limit int) {
	st := d.Switch.State
	if d.Switch.Has(device.DimNVEPeers) && d.Switch.Role.VTEP() {
		var down []string
		for _, p := range st.NVEPeers {
			if !p.Up {
				down = append(down, peerName(d, p.Peer)+" "+p.State)
			}
		}
		_ = term.Printf(w, "\nNVE 对端：%d 个，up %d", len(st.NVEPeers), len(st.NVEPeers)-len(down))
		if len(down) > 0 {
			_ = term.Printf(w, "；down：%s", clip(down, limit))
		}
		_ = term.Printf(w, "\n")
	}
	if d.Switch.Has(device.DimNVEVNIs) && len(st.NVEVNIs) > 0 {
		var down []string
		for _, v := range st.NVEVNIs {
			if !v.Up {
				down = append(down, fmt.Sprintf("%d(%s %s)", v.VNI, v.Layer, v.State))
			}
		}
		_ = term.Printf(w, "VNI：%d 个，up %d", len(st.NVEVNIs), len(st.NVEVNIs)-len(down))
		if len(down) > 0 {
			_ = term.Printf(w, "；非 Up：%s", clip(down, limit))
		}
		_ = term.Printf(w, "\n")
	}
}

func printVPC(w io.Writer, sw *device.Switch) {
	v := sw.State.VPC
	if v == nil || !sw.Has(device.DimVPC) {
		return
	}
	ok := func(b bool) string {
		if b {
			return "✓"
		}
		return "✗"
	}
	_ = term.Printf(w, "\nvPC 域 %d（%s）：对端 %s %s  keepalive %s %s  一致性 全局 %s / per-vlan %s / type-2 %s\n", v.DomainID, term.Dash(v.Role),
		ok(v.PeerOK), v.PeerStatus, ok(v.KeepaliveOK), v.KeepaliveStatus, ok(v.ConsistencyOK), ok(v.PerVLANConsistent), ok(v.Type2Consistent))
	for _, l := range v.PeerLinks {
		_ = term.Printf(w, "  peer-link %s %s\n", l.Port, upDown(l.Up))
	}
	var bad []string
	for _, m := range v.Members {
		if !m.Up || !m.Consistent {
			s := fmt.Sprintf("vPC %d %s %s", m.ID, m.Port, upDown(m.Up))
			if m.Reason != "" && m.Reason != "success" {
				s += "（" + m.Reason + "）"
			}
			bad = append(bad, s)
		}
	}
	_ = term.Printf(w, "  成员 %d 个，异常 %d 个%s\n", len(v.Members), len(bad), listSuffix(bad))
}

func printBundles(w io.Writer, sw *device.Switch) {
	if !sw.Has(device.DimPortChannels) || len(sw.State.PortChannels) == 0 {
		return
	}
	var bad []string
	for _, p := range sw.State.PortChannels {
		var off []string
		for _, m := range p.Members {
			if !m.Bundled {
				off = append(off, m.Port+"("+m.Flag+")")
			}
		}
		if !p.Up || len(off) > 0 {
			s := p.Name + " " + upDown(p.Up)
			if len(off) > 0 {
				s += "，未捆绑成员 " + strings.Join(off, " ")
			}
			bad = append(bad, s)
		}
	}
	_ = term.Printf(w, "\n聚合：%d 个，异常 %d 个%s\n", len(sw.State.PortChannels), len(bad), listSuffix(bad))
}

func printSwitchFindings(w io.Writer, name string, fs []assess.Finding, limit int) {
	if len(fs) == 0 {
		_ = term.Printf(w, "\n发现：无\n")
		return
	}
	counts := map[assess.Severity]int{}
	for _, f := range fs {
		counts[f.Severity]++
	}
	_ = term.Printf(w, "\n发现 %d 条（严重 %d / 高 %d / 中 %d / 低 %d）：\n", len(fs), counts[assess.SeverityCritical], counts[assess.SeverityHigh],
		counts[assess.SeverityMedium], counts[assess.SeverityLow])
	type group struct {
		f     assess.Finding
		items []string
	}
	var groups []*group
	byTitle := map[string]*group{}
	for _, f := range fs {
		key := f.Severity.String() + "|" + f.Title
		g, ok := byTitle[key]
		if !ok || limit == 0 {
			g = &group{f: f}
			byTitle[key] = g
			groups = append(groups, g)
		}
		g.items = append(g.items, strings.TrimPrefix(f.Detail, name+" "))
	}
	shown := groups
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, g := range shown {
		text := g.f.Title
		switch {
		case len(g.items) == 1 && g.items[0] != "":
			text += "：" + g.items[0]
		case len(g.items) > 1:
			text += fmt.Sprintf("（%d 项）：%s", len(g.items), clipWith(g.items, 2, "；"))
		}
		_ = term.Printf(w, "  [%s] %s\n", report.SeverityName(g.f.Severity), text)
	}
	if len(shown) < len(groups) {
		_ = term.Printf(w, "  … 另有 %d 类，使用 -v 查看全部\n", len(groups)-len(shown))
	}
}

func countTrue[T any](xs []T, f func(T) bool) int {
	n := 0
	for _, x := range xs {
		if f(x) {
			n++
		}
	}
	return n
}

func upDown(up bool) string {
	if up {
		return "up"
	}
	return "down"
}

func clip(xs []string, limit int) string { return clipWith(xs, limit, "，") }

func clipWith(xs []string, limit int, sep string) string {
	if limit > 0 && len(xs) > limit {
		return strings.Join(xs[:limit], sep) + fmt.Sprintf(" … 另有 %d 个", len(xs)-limit)
	}
	return strings.Join(xs, sep)
}

func listSuffix(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return "：\n    " + strings.Join(xs, "\n    ")
}

// renderCompact renders a table; compact notes that healthy rows were
// left out.
func renderCompact(w io.Writer, t *term.Table, compact bool) {
	switch {
	case t.Len() > 0:
		renderIndented(w, t, "  ")
		if compact {
			_ = term.Printf(w, "  （正常项已省略，-v 显示全部）\n")
		}
	case compact:
		_ = term.Printf(w, "  全部正常，-v 显示明细\n")
	}
}
