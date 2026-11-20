package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric/capacity"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/endpoint"
	"github.com/gatework/fabric/internal/bootstrap"
	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/status"
	"github.com/gatework/fabric/topology"
)

func newStatus(e env) *cobra.Command {
	var ff fleetFlags
	var basePath, region string
	var verbose, asJSON bool
	c := &cobra.Command{
		Use:   "status",
		Short: "全网健康总览：控制面指标、合规偏差与健康评分（证据不完整显示未知）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fleet, err := ff.load(cmd, nil)
			if err != nil {
				return err
			}
			base, err := bootstrap.ReadBaseline(basePath)
			if err != nil {
				return err
			}
			rep, _, err := workflow.Status(cmd.Context(), workflow.CheckRequest{Fleet: fleet, Baseline: base, Region: region})
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(e, rep)
			}
			printStatus(e, rep, verbose)
			return nil
		},
	}
	ff.bind(c, false)
	c.Flags().StringVar(&basePath, "baseline", "", "基线文件")
	c.Flags().StringVar(&region, "region", "", "只看一个区域")
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "列出每台交换机")
	c.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	return c
}

func ratio(r status.Ratio) string {
	switch {
	case !r.Known:
		return "?"
	case r.Total == 0:
		return "-"
	default:
		return fmt.Sprintf("%d/%d", r.Up, r.Total)
	}
}

func printStatus(e env, rep status.Report, verbose bool) {
	w := e.stdout
	for _, r := range rep.Regions {
		_, _ = fmt.Fprintf(w, "区域 %s：%s（最低 %d，平均 %d）  正常 %d / 降级 %d / 故障 %d / 未知 %d\n", r.Name, r.Level, r.Worst, r.Mean,
			r.Levels["正常"], r.Levels["降级"], r.Levels["故障"], r.Levels["未知"])
		limit := len(r.Switches)
		if !verbose {
			limit = min(limit, 5)
		}
		if limit == 0 {
			continue
		}
		_, _ = fmt.Fprintf(w, "  %-18s %-6s %4s %-4s %-8s %-7s %-6s %-8s %-4s %-6s %s\n", "交换机", "角色", "评分", "等级", "NVE对端", "EVPN", "OSPF", "VNI", "vPC", "聚合", "发现(严重/高/中/低)")
		for _, s := range r.Switches[:limit] {
			_, _ = fmt.Fprintf(w, "  %-18s %-6s %4d %-4s %-8s %-7s %-6s %-8s %-4s %-6s %d/%d/%d/%d\n", s.Name, s.Role, s.Score, s.Level,
				ratio(s.NVEPeers), ratio(s.Sessions), ratio(s.OSPF), ratio(s.VNIs), ratio(s.VPC), ratio(s.Bundles),
				s.Findings["critical"], s.Findings["high"], s.Findings["medium"], s.Findings["low"])
		}
		if limit < len(r.Switches) {
			_, _ = fmt.Fprintf(w, "  … 另有 %d 台，使用 -v 查看全部（按评分从低到高）\n", len(r.Switches)-limit)
		}
	}
}

func newLocate(e env) *cobra.Command {
	var ff fleetFlags
	var asJSON bool
	c := &cobra.Command{
		Use:   "locate MAC|IP",
		Short: "终端定位：MAC 或 IP 接在哪台交换机哪个端口，被哪些 VTEP 学到",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleet, err := ff.load(cmd, []evidence.Dimension{device.DimConfig, device.DimMACs, device.DimL2Routes, device.DimMACIPs})
			if err != nil {
				return err
			}
			res := workflow.Endpoints(fleet).Lookup(args[0])
			if asJSON {
				return writeJSON(e, res)
			}
			printLocate(e, res)
			if len(res.Attached)+len(res.Remote) == 0 {
				return exit(ExitFindings, "")
			}
			return nil
		},
	}
	ff.bind(c, false)
	c.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	return c
}

func printLocate(e env, res endpoint.Result) {
	w := e.stdout
	if len(res.Attached)+len(res.Remote) == 0 {
		_, _ = fmt.Fprintf(w, "未找到 %s\n", res.Query)
		return
	}
	_, _ = fmt.Fprintf(w, "%s：\n", res.Query)
	for _, l := range res.Attached {
		_, _ = fmt.Fprintf(w, "  接入  %s %s 端口 %s  VLAN %s / VNI %s  MAC %s %s（%s seq %d）\n", l.Region, l.Switch, orDashCLI(l.Port), l.VLAN, l.VNI, l.MAC, l.IP, l.Producer, l.Sequence)
	}
	remote := map[string]int{}
	for _, l := range res.Remote {
		remote[l.Region]++
	}
	for region, n := range remote {
		_, _ = fmt.Fprintf(w, "  远端  区域 %s 有 %d 台交换机经 EVPN 学到\n", region, n)
	}
	for _, a := range res.Anomalies {
		_, _ = fmt.Fprintf(w, "  ⚠ %s\n", a)
	}
}

func newTopology(e env) *cobra.Command {
	var ff fleetFlags
	var region string
	var asJSON bool
	c := &cobra.Command{
		Use:   "topology",
		Short: "布线审计：意图互联与 CDP 对照，输出 leaf × spine 矩阵",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fleet, err := ff.load(cmd, []evidence.Dimension{device.DimConfig, device.DimCDP})
			if err != nil {
				return err
			}
			audits := workflow.Topology(fleet, region)
			if asJSON {
				return writeJSON(e, audits)
			}
			bad := printTopology(e, audits)
			if bad > 0 {
				return exit(ExitFindings, "")
			}
			return nil
		},
	}
	ff.bind(c, true)
	c.Flags().StringVar(&region, "region", "", "只看一个区域")
	c.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	return c
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

func printTopology(e env, audits []topology.Audit) int {
	w := e.stdout
	bad := 0
	for _, a := range audits {
		_, _ = fmt.Fprintf(w, "区域 %s：%d 条互联（✓ 正常  ✗ 缺失  ≠ 错接  + 未声明  ? 未知  · 无互联）\n", a.Region, len(a.Links))
		_, _ = fmt.Fprintf(w, "  %-18s %s\n", "", strings.Join(a.Spines, "  "))
		for _, v := range a.VTEPs {
			var cells []string
			for _, sp := range a.Spines {
				st, ok := a.Cells[v+"|"+sp]
				cells = append(cells, fmt.Sprintf("%-*s", len(sp), cell(st, ok)))
			}
			_, _ = fmt.Fprintf(w, "  %-18s %s\n", v, strings.Join(cells, "  "))
		}
		for _, c := range a.Links {
			if c.State != topology.StateOK || len(c.Mismatch) > 0 {
				bad++
				detail := c.Detail
				if len(c.Mismatch) > 0 {
					detail = strings.TrimPrefix(detail+"；两端不一致："+strings.Join(c.Mismatch, "，"), "；")
				}
				_, _ = fmt.Fprintf(w, "  %s %s：%s\n", c.State, c.Name, detail)
			}
		}
	}
	return bad
}

func newCapacity(e env) *cobra.Command {
	var ff fleetFlags
	var region string
	var lim capacity.Limits
	var top int
	var asJSON bool
	c := &cobra.Command{
		Use:   "capacity",
		Short: "容量：区域资源池与单机规模（VLAN、端口、MAC 表、NVE 对端、聚合）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fleet, err := ff.load(cmd, []evidence.Dimension{device.DimConfig, device.DimInterfaces, device.DimMACs, device.DimNVEPeers})
			if err != nil {
				return err
			}
			regions := workflow.Capacity(fleet, region, lim)
			if asJSON {
				return writeJSON(e, regions)
			}
			printCapacity(e, regions, top)
			return nil
		},
	}
	ff.bind(c, false)
	d := capacity.DefaultLimits()
	c.Flags().StringVar(&region, "region", "", "只看一个区域")
	c.Flags().IntVar(&lim.VLANs, "max-vlans", d.VLANs, "单机 VLAN 上限")
	c.Flags().IntVar(&lim.MACs, "max-macs", d.MACs, "单机 MAC 表上限")
	c.Flags().IntVar(&lim.NVEPeers, "max-nve-peers", d.NVEPeers, "单机 NVE 对端上限")
	c.Flags().IntVar(&lim.PortChannels, "max-port-channels", d.PortChannels, "单机 port-channel 上限")
	c.Flags().IntVar(&top, "top", 5, "每区域列出利用率最高的交换机数")
	c.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	return c
}

func pct(u capacity.Usage) string {
	switch {
	case u.Used < 0:
		return "?"
	case u.Total <= 0:
		return fmt.Sprintf("%d", u.Used)
	default:
		return fmt.Sprintf("%d/%d %.0f%%", u.Used, u.Total, u.Percent())
	}
}

func peak(s capacity.Switch) float64 {
	best := 0.0
	for _, u := range s.All() {
		best = max(best, u.Percent())
	}
	return best
}

func printCapacity(e env, regions []capacity.Region, top int) {
	w := e.stdout
	for _, r := range regions {
		_, _ = fmt.Fprintf(w, "区域 %s\n", r.Name)
		for _, u := range r.Pools {
			_, _ = fmt.Fprintf(w, "  %-28s %s\n", u.Name, pct(u))
		}
		sw := slices.Clone(r.Switches)
		slices.SortFunc(sw, func(a, b capacity.Switch) int { return cmp.Compare(peak(b), peak(a)) })
		_, _ = fmt.Fprintf(w, "  利用率最高的交换机：\n  %-18s %-16s %-16s %-18s %-14s %s\n", "交换机", "VLAN", "以太网端口", "MAC 表", "NVE 对端", "port-channel")
		for _, s := range sw[:min(top, len(sw))] {
			_, _ = fmt.Fprintf(w, "  %-18s %-16s %-16s %-18s %-14s %s\n", s.Name, pct(s.VLANs), pct(s.Ports), pct(s.MACs), pct(s.NVEPeers), pct(s.PortChannels))
		}
	}
}
