package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/term"

	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/topology"
)

type topologyView struct {
	matrix  bool
	all     bool
	detail  bool
	verbose bool
	sw      string
}

func newTopology(e env) *cobra.Command {
	var ff fleetFlags
	var region string
	var view topologyView
	var asJSON bool
	c := &cobra.Command{
		Use:   "topology",
		Short: "fabric 拓扑：spine–VTEP 连线图与单机邻居树，逐层验证每条互联（物理/CDP/配置/OSPF）与上行冗余",
		Long: "画出每个区域的 spine–VTEP 拓扑：两台 spine 为左右母线，每台 VTEP 一行，连线标出两端端口、速率与状态，故障处断开并注明最低失败层与原因；vPC 对用括号标出。\n" +
			"--switch 以一台交换机为根画出全部邻居：fabric 互联、fabric 内其他交换机（如 border 对、vPC peer）、外部设备与管理网。\n" +
			"每条互联按 端口状态 → CDP 邻居 → 两端配置 → OSPF 邻接 逐层验证；--detail 列出每个故障的两端详情、原因、影响与处置建议，--all 列出全部互联的逐层结果。",
		Example: "  fabric topology\n  fabric topology --region sha2 --detail\n  fabric topology --switch SHA2-BDR02",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fleet, err := ff.load(cmd, []evidence.Dimension{device.DimConfig, device.DimCDP, device.DimInterfaces, device.DimOSPF, device.DimBGP, device.DimPortChannels, device.DimVPC})
			if err != nil {
				return err
			}
			if view.sw != "" && region == "" {
				if sw, err := fleet.Switch(view.sw); err == nil {
					region = sw.Region
				}
			}
			audits := workflow.Topology(fleet, region)
			if view.sw != "" {
				audits = onlySwitch(audits, view.sw)
				if len(audits) == 0 {
					return exit(ExitError, "意图中没有交换机 %s", view.sw)
				}
			}
			if asJSON {
				return writeJSON(e, audits)
			}
			if printTopology(e.stdout, audits, view) > 0 {
				return exit(ExitFindings, "")
			}
			return nil
		},
	}
	ff.bind(c, true)
	c.Flags().StringVar(&region, "region", "", "只看一个区域")
	c.Flags().StringVar(&view.sw, "switch", "", "以一台交换机为根画出它的全部邻居")
	c.Flags().BoolVar(&view.detail, "detail", false, "列出每个故障的两端详情、原因、影响与处置建议")
	c.Flags().BoolVar(&view.matrix, "matrix", false, "附 leaf × spine 符号矩阵")
	c.Flags().BoolVar(&view.all, "all", false, "列出全部互联及逐层结果")
	c.Flags().BoolVarP(&view.verbose, "verbose", "v", false, "邻居树中列出管理网邻居")
	c.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	completeSwitches(e, c, "switch")
	completeRegions(e, c, "region")
	return c
}

// onlySwitch keeps the links and uplinks of one switch.
func onlySwitch(audits []topology.Audit, name string) []topology.Audit {
	var out []topology.Audit
	for _, a := range audits {
		var links []topology.LinkCheck
		keep := map[string]bool{name: true}
		for _, c := range a.Links {
			if c.A.Switch == name || c.B.Switch == name {
				links = append(links, c)
				keep[c.A.Switch], keep[c.B.Switch] = true, true
			}
		}
		// The far ends' uplinks stay: a spine's tree shows each VTEP's
		// session to it and the VTEPs that lost redundancy through it.
		var ups []topology.Uplinks
		for _, u := range a.Uplinks {
			if keep[u.Switch] {
				ups = append(ups, u)
			}
		}
		if len(links) == 0 && len(ups) == 0 {
			continue
		}
		a.Links, a.Uplinks = links, ups
		out = append(out, a)
	}
	return out
}

func verdictMark(v topology.Verdict) string {
	switch v {
	case topology.VerdictOK:
		return "✓"
	case topology.VerdictFail:
		return "✗"
	case topology.VerdictUnknown:
		return "?"
	default:
		return "?"
	}
}

func layerLine(c topology.LinkCheck) string {
	parts := make([]string, 0, len(c.Layers))
	for _, p := range c.Layers {
		parts = append(parts, p.Layer.String()+" "+verdictMark(p.Verdict))
	}
	return strings.Join(parts, " │ ")
}

// printTopology writes the audits and returns the number of problems.
func printTopology(w io.Writer, audits []topology.Audit, view topologyView) int {
	problems := 0
	var next []string
	for _, a := range audits {
		faults, risk := a.Faults(), a.AtRisk()
		asym := a.Asymmetric()
		problems += len(faults) + len(risk) + len(a.Unobserved) + len(asym)
		if view.sw != "" {
			_ = term.Printf(w, "区域 %s\n", a.Region)
			printSwitchTree(w, a, view.sw, view.verbose)
		} else {
			printTopologySummary(w, a, len(faults), risk)
			printRegionDiagram(w, a)
		}
		if len(a.Unobserved) > 0 && view.sw == "" {
			_ = term.Printf(w, "  ⚠ 快照中没有 %d 台意图交换机，其互联无法验证：%s\n", len(a.Unobserved), strings.Join(a.Unobserved, "、"))
		}
		if view.detail || view.sw != "" {
			printTopologyDetail(w, a, view.sw, faults, risk, asym)
		}
		if view.all {
			printLinks(w, a, view.sw)
		}
		if view.matrix {
			printMatrix(w, a)
		}
		_ = term.Printf(w, "\n")
		for _, c := range faults {
			next = appendUnique(next, "fabric switch show "+vtepEnd(a, c))
		}
	}
	if problems == 0 {
		_ = term.Printf(w, "全部互联逐层验证通过，每台 VTEP 都保持完整的上行冗余。\n")
	}
	if len(next) > 0 && view.sw == "" {
		next = next[:min(len(next), 2)]
		if !view.detail {
			next = append(next, "fabric topology --detail（故障详情与处置建议）")
		}
		next = append(next, "fabric topology --switch <名称>（单机邻居树）")
		_ = term.Printf(w, "下一步：%s\n", strings.Join(next, " · "))
	}
	return problems
}

func printTopologyDetail(w io.Writer, a topology.Audit, sw string, faults []topology.LinkCheck, risk, asym []topology.Uplinks) {
	impact := impactOf(a)
	if len(faults) > 0 {
		_ = term.Printf(w, "  故障详情：\n")
	}
	for i, c := range faults {
		printFault(w, i+1, c, impact)
	}
	if len(asym) > 0 {
		_ = term.Printf(w, "  上行速率不一致（ECMP 按条数均分流量，低速链路先拥塞）：\n")
		for _, u := range asym {
			_ = term.Printf(w, "    %s：%s\n", u.Switch, strings.Join(u.Speeds, " / "))
		}
	}
	if slices.Contains(a.Spines, sw) {
		printSpineRisk(w, sw, risk)
		return
	}
	if len(risk) > 0 {
		_ = term.Printf(w, "  上行冗余不足：\n")
		for _, u := range risk {
			_ = term.Printf(w, "    %s %s：健康上行 %d/%d，可达 spine %s%s\n", u.Redundancy, u.Switch, u.Healthy, u.Declared,
				term.Dash(strings.Join(u.Spines, "、")), lostSuffix(u))
		}
	}
}

func printTopologySummary(w io.Writer, a topology.Audit, faults int, risk []topology.Uplinks) {
	counts := map[topology.Redundancy]int{}
	for _, u := range a.Uplinks {
		counts[u.Redundancy]++
	}
	at := counts[topology.RedundancyDegraded] + counts[topology.RedundancySinglePath] + counts[topology.RedundancyIsolated]
	_ = term.Printf(w, "区域 %s ── spine %d · VTEP %d · 互联 %d（故障 %d）· 冗余不足 %d · 上行速率不对称 %d\n",
		a.Region, len(a.Spines), len(a.Uplinks), len(a.Links), faults, at, len(a.Asymmetric()))
	_ = risk
}

func printFault(w io.Writer, n int, c topology.LinkCheck, impact map[string]string) {
	d := c.Diagnosis
	_ = term.Printf(w, "  %d. [%s] %s\n", n, d.Fault, c.Name)
	_ = term.Printf(w, "     两端：%s %s ↔ %s %s\n", c.Ends[0].Switch, c.Ends[0].Describe(), c.Ends[1].Switch, c.Ends[1].Describe())
	_ = term.Printf(w, "     原因：%s\n", d.Cause)
	for _, end := range []string{c.A.Switch, c.B.Switch} {
		if s, ok := impact[end]; ok {
			_ = term.Printf(w, "     影响：%s\n", s)
		}
	}
	_ = term.Printf(w, "     建议：%s\n", d.Hint)
	_ = term.Printf(w, "     逐层：%s\n", layerLine(c))
}

// impactOf describes, per VTEP at risk, what is left of its uplinks.
func impactOf(a topology.Audit) map[string]string {
	out := map[string]string{}
	for _, u := range a.Uplinks {
		if !u.Redundancy.AtRisk() {
			continue
		}
		s := fmt.Sprintf("%s %s，健康上行 %d/%d", u.Switch, u.Redundancy, u.Healthy, u.Declared)
		if len(u.Spines) > 0 {
			s += "，仅剩 " + strings.Join(u.Spines, "、")
		}
		if u.Redundancy == topology.RedundancySinglePath {
			s += "；再断一条将与 fabric 失联"
		}
		out[u.Switch] = s
	}
	return out
}

func lostSuffix(u topology.Uplinks) string {
	if len(u.Lost) == 0 {
		return ""
	}
	return "，失去 " + strings.Join(u.Lost, "、")
}

func vtepEnd(a topology.Audit, c topology.LinkCheck) string {
	for _, u := range a.Uplinks {
		if u.Switch == c.A.Switch || u.Switch == c.B.Switch {
			return u.Switch
		}
	}
	return c.A.Switch
}

func appendUnique(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

func printLinks(w io.Writer, a topology.Audit, local string) {
	t := term.NewTable("本端", "层", "地址", "状态", "速率", "MTU", "对端", "对端地址", "对端状态", "物理", "邻居", "配置", "路由", "说明")
	for _, c := range a.Links {
		x, y := c.Ends[0], c.Ends[1]
		if local != "" && y.Switch == local {
			x, y = y, x
		}
		note := ""
		if c.Diagnosis != nil {
			note = c.Diagnosis.Cause
		}
		t.Add(x.Switch+" "+x.Interface, x.Layer, x.Address, endState(x), x.Speed, mtu(x.MTU),
			y.Switch+" "+y.Interface, y.Address, endState(y),
			verdictMark(c.Probe(topology.LayerPhysical).Verdict), verdictMark(c.Probe(topology.LayerNeighbor).Verdict),
			verdictMark(c.Probe(topology.LayerConfig).Verdict), verdictMark(c.Probe(topology.LayerRouting).Verdict), note)
	}
	_ = term.Printf(w, "  互联明细：\n")
	renderIndented(w, t, "    ")
}

func endState(x topology.End) string {
	if x.Status == "" {
		return "?"
	}
	if !x.Up && x.Reason != "" {
		return x.Status + "（" + x.Reason + "）"
	}
	return x.Status
}

func mtu(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

func printMatrix(w io.Writer, a topology.Audit) {
	header := append([]string{"VTEP"}, a.Spines...)
	header = append(header, "上行", "冗余")
	t := term.NewTable(header...)
	ups := map[string]topology.Uplinks{}
	for _, u := range a.Uplinks {
		ups[u.Switch] = u
	}
	for _, v := range a.VTEPs {
		row := []string{v}
		for _, sp := range a.Spines {
			st, ok := a.Cells[v+"|"+sp]
			row = append(row, cell(st, ok))
		}
		u := ups[v]
		row = append(row, fmt.Sprintf("%d/%d", u.Healthy, u.Declared), u.Redundancy.String())
		t.Add(row...)
	}
	_ = term.Printf(w, "  leaf × spine 矩阵（✓ 正常  ✗ 缺失  ≠ 错接  + 未声明  ? 未知  · 无互联）：\n")
	renderIndented(w, t, "    ")
}

func renderIndented(w io.Writer, t *term.Table, indent string) {
	var b strings.Builder
	_, _ = t.Render(&b, 0)
	for line := range strings.SplitSeq(strings.TrimRight(b.String(), "\n"), "\n") {
		_, _ = io.WriteString(w, indent+line+"\n")
	}
}

func cell(s topology.State, ok bool) string {
	switch {
	case !ok:
		return "·"
	case s == topology.StateOK:
		return "✓"
	case s == topology.StateMissing:
		return "✗"
	case s == topology.StateMiscabled:
		return "≠"
	case s == topology.StateUndeclared:
		return "+"
	default:
		return "?"
	}
}

// printSpineRisk splits, for one spine, the VTEPs at risk into those that
// lost it and those that now depend on it alone: reloading the spine
// would cut the latter off.
func printSpineRisk(w io.Writer, spine string, risk []topology.Uplinks) {
	var lost, alone []string
	for _, u := range risk {
		switch {
		case slices.Contains(u.Lost, spine):
			lost = append(lost, u.Switch)
		case len(u.Spines) == 1 && u.Spines[0] == spine:
			alone = append(alone, u.Switch)
		}
	}
	if len(lost) > 0 {
		_ = term.Printf(w, "  失去到本机上行的 VTEP：%s\n", strings.Join(lost, "、"))
	}
	if len(alone) > 0 {
		_ = term.Printf(w, "  ⚠ 仅剩本机一条上行的 VTEP：%s——本机重启或维护前须先恢复它们的另一条上行\n", strings.Join(alone, "、"))
	}
}
