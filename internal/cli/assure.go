package cli

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/term"

	"github.com/gatework/fabric/capacity"
	"github.com/gatework/fabric/check"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/endpoint"
	"github.com/gatework/fabric/internal/bootstrap"
	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/report"
	"github.com/gatework/fabric/status"
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
			rep, findings, err := workflow.Status(cmd.Context(), workflow.CheckRequest{Fleet: fleet, Baseline: base, Region: region})
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(e, rep)
			}
			printStatus(e, rep, topIssues(findings), verbose)
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

// topIssues returns, per switch name, its most severe finding.
func topIssues(rep check.Report) map[string]string {
	best := map[string]assess.Finding{}
	for _, f := range rep.Findings() {
		for _, p := range f.Subject.Parts() {
			if p.Kind != "switch" {
				continue
			}
			if cur, ok := best[p.Key]; !ok || f.Severity > cur.Severity {
				best[p.Key] = f
			}
		}
	}
	out := map[string]string{}
	for name, f := range best {
		out[name] = "[" + report.SeverityName(f.Severity) + "] " + f.Title
	}
	return out
}

func printStatus(e env, rep status.Report, issues map[string]string, verbose bool) {
	w := e.stdout
	worst, worstScore := "", 101
	for _, r := range rep.Regions {
		_ = term.Printf(w, "区域 %s：%s（最低 %d，平均 %d）  正常 %d / 降级 %d / 故障 %d / 未知 %d\n", r.Name, r.Level, r.Worst, r.Mean,
			r.Levels["正常"], r.Levels["降级"], r.Levels["故障"], r.Levels["未知"])
		if len(r.Switches) == 0 {
			continue
		}
		t := term.NewTable("交换机", "角色", "评分", "等级", "NVE对端", "EVPN", "OSPF", "VNI", "vPC", "聚合", "发现(严/高/中/低)", "主要问题")
		for _, s := range r.Switches {
			t.Add(s.Name, s.Role.String(), fmt.Sprint(s.Score), s.Level.String(),
				ratio(s.NVEPeers), ratio(s.Sessions), ratio(s.OSPF), ratio(s.VNIs), ratio(s.VPC), ratio(s.Bundles),
				fmt.Sprintf("%d/%d/%d/%d", s.Findings["critical"], s.Findings["high"], s.Findings["medium"], s.Findings["low"]), issues[s.Name])
		}
		limit := 5
		if verbose {
			limit = 0
		}
		var b strings.Builder
		omitted, _ := t.Render(&b, limit)
		for line := range strings.SplitSeq(strings.TrimRight(b.String(), "\n"), "\n") {
			_, _ = io.WriteString(w, "  "+line+"\n")
		}
		if omitted > 0 {
			_ = term.Printf(w, "  … 另有 %d 台，使用 -v 查看全部（按评分从低到高）\n", omitted)
		}
		if s := r.Switches[0]; s.Level != status.LevelUnknown && s.Score < worstScore {
			worst, worstScore = s.Name, s.Score
		}
	}
	if worst != "" {
		_ = term.Printf(w, "\n下一步：fabric switch show %s（最低分）· fabric topology · fabric check --severity high\n", worst)
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
