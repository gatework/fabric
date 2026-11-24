package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gatework/netops/diff"
	"github.com/gatework/netops/term"

	"github.com/gatework/fabric/delta"
	"github.com/gatework/fabric/internal/bootstrap"
	"github.com/gatework/fabric/internal/workflow"
)

func newDiff(e env) *cobra.Command {
	var ff fleetFlags
	var from, only string
	var verbose, asJSON bool
	c := &cobra.Command{
		Use:   "diff",
		Short: "两次采集之间变了什么：逐台交换机的配置变更（按配置块）与运行状态变化（端口、OSPF、BGP EVPN、NVE、终端迁移）",
		Long: "比较 --from 指定的旧快照与 --snapshot 指定的新快照（默认 fabric.yml 中的 snapshot）。\n" +
			"配置按配置块对比（ACL 条目顺序敏感），密码、community 等敏感内容已遮蔽；运行状态只在两份快照都采集到时比较，否则显示未采集。\n" +
			"有变化的交换机按“丢失项多、变更行多”排序；--switch 或 -v 显示配置明细。有变化时退出码为 3。",
		Example: "  fabric diff --from ~/.local/share/gatework/fabric/snapshots/backup-2026-10-14\n  fabric diff --from <旧快照> --switch SHA2-BDR02",
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, err := bootstrap.ReadIntent(ff.intent)
			if err != nil {
				return err
			}
			rep, err := workflow.Diff(cmd.Context(), workflow.DiffRequest{From: from, To: ff.snapshot, Intent: in, SubnetBits: ff.bits, Switch: only})
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(e, rep)
			}
			if printDiff(e.stdout, rep, from, verbose || only != "") > 0 {
				return exit(ExitFindings, "")
			}
			return nil
		},
	}
	ff.bind(c, false)
	c.Flags().StringVar(&from, "from", "", "旧快照目录（对比基准）")
	c.Flags().StringVar(&only, "switch", "", "只看一台交换机，并显示配置明细")
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "显示每台交换机的配置明细")
	c.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	_ = c.MarkFlagRequired("from")
	completeSwitches(e, c, "switch")
	return c
}

func printDiff(w io.Writer, r delta.Report, from string, detail bool) int {
	changed := r.Changed()
	stateKnown, configChanged, stateChanged, presence := 0, 0, 0, 0
	for _, s := range r.Switches {
		if s.StateKnown {
			stateKnown++
		}
		if len(s.Sections) > 0 {
			configChanged++
		}
		if len(s.State) > 0 {
			stateChanged++
		}
		if s.Presence != "both" {
			presence++
		}
	}
	stateNote := fmt.Sprintf("运行状态变化 %d 台", stateChanged)
	if stateKnown == 0 {
		stateNote = "运行状态未比较（至少一份快照没有采集运行状态）"
	}
	_ = term.Printf(w, "对比 %s → %s：%d 台交换机，配置变化 %d 台，%s，增减设备 %d 台\n", r.From, r.To, len(r.Switches), configChanged, stateNote, presence)
	if len(changed) == 0 {
		_ = term.Printf(w, "两次采集之间没有变化。\n")
		return 0
	}
	t := term.NewTable("交换机", "区域", "配置变更", "变更的配置块", "状态变化")
	for _, s := range changed {
		cfg := fmt.Sprintf("+%d −%d", s.Added, s.Removed)
		switch {
		case s.Presence == "added":
			cfg = "新出现"
		case s.Presence == "removed":
			cfg = "已消失"
		case !s.ConfigKnown:
			cfg = "?"
		case len(s.Sections) == 0:
			cfg = "无"
		}
		st := "-"
		if s.StateKnown {
			st = stateSummary(s.State)
		}
		t.Add(s.Name, s.Region, cfg, clipText(s.Kinds(), 48), st)
	}
	renderIndented(w, t, "  ")
	if detail {
		for _, s := range changed {
			printSwitchDiff(w, s)
		}
	} else {
		_ = term.Printf(w, "\n下一步：fabric diff --from %s --switch <名称> 查看明细 · -v 列出全部\n", from)
	}
	return len(changed)
}

func stateSummary(cs []delta.StateChange) string {
	if len(cs) == 0 {
		return "无"
	}
	count := map[delta.Severity]int{}
	for _, c := range cs {
		count[c.Severity]++
	}
	var parts []string
	if n := count[delta.SeverityLoss]; n > 0 {
		parts = append(parts, fmt.Sprintf("丢失 %d", n))
	}
	if n := count[delta.SeverityGain]; n > 0 {
		parts = append(parts, fmt.Sprintf("新增 %d", n))
	}
	if n := count[delta.SeverityInfo]; n > 0 {
		parts = append(parts, fmt.Sprintf("变化 %d", n))
	}
	return strings.Join(parts, "，")
}

func clipText(s string, n int) string {
	if term.Width(s) <= n {
		return s
	}
	r := []rune(s)
	for term.Width(string(r))+1 > n {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func printSwitchDiff(w io.Writer, s delta.Switch) {
	_ = term.Printf(w, "\n%s（%s）\n", s.Name, term.Dash(s.Region))
	for _, c := range s.State {
		mark := map[delta.Severity]string{delta.SeverityLoss: "✗", delta.SeverityGain: "+", delta.SeverityInfo: "~"}[c.Severity]
		line := fmt.Sprintf("  %s [%s] %s", mark, c.Area, c.What)
		if c.Before != "" || c.After != "" {
			line += fmt.Sprintf("：%s → %s", term.Dash(c.Before), term.Dash(c.After))
		}
		_ = term.Printf(w, "%s\n", line)
	}
	for _, sec := range s.Sections {
		printSection(w, sec)
	}
}

func changeSign(c diff.Change) (sign, suffix string) {
	switch c.Kind {
	case diff.KindRemoved:
		return "−", ""
	case diff.KindChanged:
		return "~", "（敏感值已更改，内容已遮蔽）"
	case diff.KindAdded:
	}
	return "+", ""
}

// printSection prints one top-level block's changes: a lone top-level
// command on one line, otherwise the block header and its changes.
func printSection(w io.Writer, sec delta.Section) {
	if len(sec.Changes) == 1 && len(sec.Changes[0].Path) == 0 {
		c := sec.Changes[0]
		sign, suffix := changeSign(c)
		_ = term.Printf(w, "  %s %s%s\n", sign, c.Line, suffix)
		printBlock(w, c, sign, "      ")
		return
	}
	_ = term.Printf(w, "  %s\n", sec.Name)
	for _, c := range sec.Changes {
		sign, suffix := changeSign(c)
		indent := "    "
		if len(c.Path) > 1 {
			indent += strings.Repeat("  ", len(c.Path)-1) + strings.Join(c.Path[1:], " › ") + " › "
		}
		_ = term.Printf(w, "%s%s %s%s\n", indent, sign, c.Line, suffix)
		if len(c.Path) == 0 {
			printBlock(w, c, sign, "      ")
		}
	}
}

func printBlock(w io.Writer, c diff.Change, sign, indent string) {
	for _, b := range c.Block {
		if strings.TrimSpace(b) != strings.TrimSpace(c.Line) {
			_ = term.Printf(w, "%s%s %s\n", indent, sign, strings.TrimSpace(b))
		}
	}
}
