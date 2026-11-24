package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gatework/netops/term"

	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/reach"
	"github.com/gatework/fabric/topology"
)

func newPath(e env) *cobra.Command {
	var ff fleetFlags
	var asJSON bool
	c := &cobra.Command{
		Use:   "path 源 目的",
		Short: "路径追踪：两个终端（MAC 或 IP）之间的 fabric 转发路径，双向逐跳验证并指出断点",
		Long: "定位两端所在 VTEP、端口、VLAN 与 VNI，判断二层桥接（同一 L2VNI）、三层路由（同一 VRF，经 anycast 网关与 L3VNI）或跨 VRF；\n" +
			"然后逐跳验证：接入端口与 VLAN 放行 → L2VNI/网关 SVI/L3VNI → 入口 VTEP 的 EVPN 路由与下一跳 → NVE 对端 → 两端上行与 ECMP → 目的端口，回程同样验证。\n" +
			"路径完整时退出码为 0。",
		Example: "  fabric path 10.150.93.71 10.150.99.193\n  fabric path e43d.1a1b.d556 10.248.8.10",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleet, err := ff.load(cmd, nil)
			if err != nil {
				return err
			}
			t, err := workflow.Trace(fleet, args[0], args[1])
			switch {
			case errors.Is(err, reach.ErrNotFound):
				return exit(ExitFindings, "%v：没有任何 VTEP 本地学到它（终端离线、静默，或不在 fabric 内）；可用 fabric locate 查看", err)
			case err != nil:
				return err
			}
			if asJSON {
				return writeJSON(e, t)
			}
			printTrace(e.stdout, t)
			if t.Verdict != topology.VerdictOK {
				return exit(ExitFindings, "")
			}
			return nil
		},
	}
	ff.bind(c, false)
	c.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	return c
}

func endLabel(x reach.End) string {
	s := x.Query
	if x.IP != "" && x.IP != x.Query {
		s += " " + x.IP
	}
	if string(x.MAC) != x.Query {
		s += "（" + string(x.MAC) + "）"
	}
	return s
}

func printTrace(w io.Writer, t reach.Trace) {
	a, b := t.From, t.To
	seg := fmt.Sprintf("VNI %d", a.VNI)
	if t.Mode == reach.ModeRouted {
		seg = fmt.Sprintf("L3VNI %d（VRF %s）", t.L3VNI, a.VRF)
	}
	core := "══ " + seg + " ══▶"
	if len(t.Spines) > 0 {
		core = fmt.Sprintf("══ %s ══▶ %s（ECMP %d）══▶", seg, strings.Join(t.Spines, " | "), len(t.Spines))
	}
	switch {
	case t.Mode == reach.ModeLocal || a.Switch == b.Switch:
		_ = term.Printf(w, "%s ─ %s ─ [%s] ─ %s ─ %s\n", endLabel(a), shortPort(a.Port), a.Switch, shortPort(b.Port), endLabel(b))
	default:
		_ = term.Printf(w, "%s ─ %s ─ [%s] %s [%s] ─ %s ─ %s\n", endLabel(a), shortPort(a.Port), a.Switch, core, b.Switch, shortPort(b.Port), endLabel(b))
	}
	_ = term.Printf(w, "模式：%s；源 VLAN %d / VNI %d / VRF %s，目的 VLAN %d / VNI %d / VRF %s\n\n", t.Mode,
		a.VLAN, a.VNI, term.Dash(a.VRF), b.VLAN, b.VNI, term.Dash(b.VRF))
	tbl := term.NewTable("", "方向", "检查", "结果")
	for _, h := range t.Hops {
		ev := h.Evidence
		switch {
		case h.Hint != "" && h.Verdict != topology.VerdictOK:
			ev += " → " + h.Hint
		case h.Hint != "":
			ev += " ⚠ " + h.Hint
		}
		tbl.Add(verdictMark(h.Verdict), h.Direction, h.Step, ev)
	}
	renderIndented(w, tbl, "  ")
	_ = term.Printf(w, "\n")
	switch {
	case t.Break != nil:
		_ = term.Printf(w, "结论：✗ 不通，断点在「%s %s」：%s\n", t.Break.Direction, t.Break.Step, t.Break.Evidence)
		if t.Break.Hint != "" {
			_ = term.Printf(w, "建议：%s\n", t.Break.Hint)
		}
	case t.Verdict == topology.VerdictUnknown:
		_ = term.Printf(w, "结论：? 已验证的各跳正常，但有证据未采集（见 ? 项），不能确认互通\n")
	default:
		_ = term.Printf(w, "结论：✓ 双向转发路径完整（%s）\n", t.Mode)
	}
	_ = term.Printf(w, "注意：路径验证基于采集时的控制面与转发表；MTU 只按配置判断（network-qos 设定的二层口 MTU 未采集），不包含 ACL 与防火墙策略（firemon 可补充）。\n")
}
