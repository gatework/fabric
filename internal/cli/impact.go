package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/term"

	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/maintenance"
)

func newImpact(e env) *cobra.Command {
	var ff fleetFlags
	var verbose, asJSON bool
	c := &cobra.Command{
		Use:   "impact 交换机名",
		Short: "维护影响评估：交换机重启、升级或下线期间会断什么，能否现在维护",
		Long: "spine：哪些 VTEP 只剩本机一条健康上行（会失联）、哪些 VTEP 的 EVPN 会话只建在本机上、哪些会失去冗余。\n" +
			"leaf / border：本机学到的终端分为单归（会中断）与 vPC 双归（由对端接管），并检查 vPC 对端能否独自承载：上行、peer、keepalive、一致性与对应 vPC 是否 up；外部设备单独列出。\n" +
			"结论为 可以维护 / 有风险 / 不可维护，并给出维护前要做的事。只有 可以维护 时退出码为 0。",
		Example:           "  fabric impact SHA2-SPN01\n  fabric impact SHA2-LEF15 -v",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: switchArg(e),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleet, err := ff.load(cmd, []evidence.Dimension{device.DimConfig, device.DimCDP, device.DimInterfaces, device.DimOSPF,
				device.DimBGP, device.DimPortChannels, device.DimVPC, device.DimMACs, device.DimMACIPs})
			if err != nil {
				return err
			}
			rep, err := workflow.Maintenance(fleet, args[0])
			if err != nil {
				return exit(ExitError, "%v（可用 Tab 补全交换机名）", err)
			}
			if asJSON {
				err = writeJSON(e, rep)
			} else {
				printImpact(e.stdout, rep, verbose)
			}
			if err != nil {
				return err
			}
			if rep.Verdict != maintenance.VerdictReady {
				return exit(ExitFindings, "")
			}
			return nil
		},
	}
	ff.bind(c, true)
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "列出全部终端端口与 MAC")
	c.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	return c
}

func verdictIcon(v maintenance.Verdict) string {
	switch v {
	case maintenance.VerdictReady:
		return "✓"
	case maintenance.VerdictRisky:
		return "⚠"
	case maintenance.VerdictBlocked:
		return "✗"
	case maintenance.VerdictUnknown:
		return "?"
	default:
		return "?"
	}
}

func printImpact(w io.Writer, r maintenance.Report, verbose bool) {
	where := fmt.Sprintf("%s，区域 %s", r.Role, r.Region)
	if r.Peer != "" {
		where += "，vPC 对端 " + r.Peer
	}
	_ = term.Printf(w, "%s 维护影响评估（%s）：%s %s\n", r.Switch, where, verdictIcon(r.Verdict), r.Verdict)
	for _, s := range r.Missing {
		_ = term.Printf(w, "  ? 缺少证据：%s\n", s)
	}
	for _, s := range r.Blockers {
		_ = term.Printf(w, "  ✗ %s\n", s)
	}
	for _, s := range r.Risks {
		_ = term.Printf(w, "  ⚠ %s\n", s)
	}
	if len(r.Degraded) > 0 && verbose {
		_ = term.Printf(w, "  失去冗余的 VTEP：%s\n", strings.Join(r.Degraded, "、"))
	}
	printImpactPorts(w, "单归终端（维护期间中断）", r.SingleHomed, verbose)
	if len(r.DualHomed) > 0 {
		_, dual := r.Endpoints()
		if verbose {
			printImpactPorts(w, "双归终端（由 "+r.Peer+" 接管）", r.DualHomed, true)
		} else {
			_ = term.Printf(w, "  双归终端：%d 个 vPC 端口、%d 个 MAC 由 %s 接管（-v 列出）\n", len(r.DualHomed), dual, r.Peer)
		}
	}
	if len(r.Exits) > 0 {
		_ = term.Printf(w, "  南北向出口路由（VRF 静态路由）：\n")
		for _, x := range r.Exits {
			via := x.NextHop
			if x.NextHopVRF != "" {
				via += "（VRF " + x.NextHopVRF + "）"
			}
			also := "只在本机"
			switch {
			case len(x.Also) > 0:
				also = "同时在 " + strings.Join(x.Also, "、")
			case len(x.Standby) > 0:
				also = "⚠ " + strings.Join(x.Standby, "、") + " 也有此路由，外部链路 up 但当前无 OSPF 邻接（备用防火墙？）"
			case len(x.Through) > 0:
				also = "✗ " + strings.Join(x.Through, "、") + " 也有此路由，但其外部出口经过本机"
			}
			_ = term.Printf(w, "    VRF %s %s → %s  %s\n", x.VRF, x.Prefix, via, also)
		}
	}
	if len(r.Peerings) > 0 {
		_ = term.Printf(w, "  fabric 外的 OSPF 邻接：%s\n", strings.Join(r.Peerings, "；"))
	}
	if len(r.External) > 0 {
		_ = term.Printf(w, "  外部设备（CDP）：\n")
		for _, n := range r.External {
			_ = term.Printf(w, "    %s ── %s %s %s\n", shortPort(n.Port), n.Device, shortPort(n.RemotePort), n.Platform)
		}
	}
	if len(r.Before) > 0 {
		_ = term.Printf(w, "\n维护前：\n")
		for i, s := range r.Before {
			_ = term.Printf(w, "  %d. %s\n", i+1, s)
		}
	}
	_ = term.Printf(w, "\n下一步：fabric topology --switch %s · fabric switch show %s\n", r.Switch, r.Switch)
}

func printImpactPorts(w io.Writer, title string, ports []maintenance.Port, verbose bool) {
	if len(ports) == 0 {
		return
	}
	_ = term.Printf(w, "  %s：\n", title)
	t := term.NewTable("端口", "描述", "vPC", "VLAN", "MAC", "IP")
	for _, p := range ports {
		vpc := ""
		if p.VPC != 0 {
			vpc = fmt.Sprint(p.VPC)
		}
		vlans := make([]string, len(p.VLANs))
		for i, v := range p.VLANs {
			vlans[i] = fmt.Sprint(v)
		}
		macs := fmt.Sprint(len(p.MACs))
		if len(p.MACs) == 1 {
			macs = p.MACs[0]
		}
		t.Add(shortPort(p.Name), p.Description, vpc, clip(vlans, 4), macs, clip(p.IPs, 2))
	}
	limit := 10
	if verbose {
		limit = 0
	}
	var b strings.Builder
	omitted, _ := t.Render(&b, limit)
	for line := range strings.SplitSeq(strings.TrimRight(b.String(), "\n"), "\n") {
		_ = term.Printf(w, "    %s\n", line)
	}
	if omitted > 0 {
		_ = term.Printf(w, "    … 另有 %d 个端口，-v 列出全部\n", omitted)
	}
}
