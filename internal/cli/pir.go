package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/rollout"
)

func newReport(e env) *cobra.Command {
	c := &cobra.Command{Use: "report", Short: "报告"}
	var journal, format string
	deploy := &cobra.Command{
		Use:   "deploy",
		Short: "变更复盘（PIR）：结果、健康前后对比、耗时、回滚指引",
		RunE: func(_ *cobra.Command, _ []string) error {
			body, err := os.ReadFile(filepath.Join(journal, "deploy.json"))
			if err != nil {
				return err
			}
			var rep workflow.DeployReport
			if err := json.Unmarshal(body, &rep); err != nil {
				return exit(ExitError, "执行记录无法解析：%v", err)
			}
			md := format == "markdown"
			_, err = e.stdout.Write([]byte(renderPIR(rep, md)))
			return err
		},
	}
	deploy.Flags().StringVar(&journal, "journal", "", "执行记录目录（fabric deploy 输出的目录）")
	deploy.Flags().StringVar(&format, "format", "text", "text 或 markdown")
	_ = deploy.MarkFlagRequired("journal")
	c.AddCommand(deploy)
	return c
}

func healthDelta(before, after *rollout.Health) string {
	if before == nil || after == nil {
		return "-"
	}
	return fmt.Sprintf("NVE %d→%d / EVPN %d→%d / OSPF %d→%d", len(before.NVEPeers), len(after.NVEPeers),
		len(before.BGPSessions), len(after.BGPSessions), len(before.OSPF), len(after.OSPF))
}

func renderPIR(rep workflow.DeployReport, md bool) string {
	var b strings.Builder
	h := func(s string) {
		if md {
			fmt.Fprintf(&b, "## %s\n\n", s)
		} else {
			fmt.Fprintf(&b, "%s\n", s)
		}
	}
	line := func(format string, args ...any) {
		prefix := "  "
		if md {
			prefix = "- "
		}
		fmt.Fprintf(&b, prefix+format+"\n", args...)
	}
	if md {
		fmt.Fprintf(&b, "# 变更复盘：计划 %s\n\n", rep.Bundle)
	} else {
		fmt.Fprintf(&b, "变更复盘：计划 %s\n", rep.Bundle)
	}
	h("概况")
	line("工单：%s；申请人：%s；原因：%s", orDashCLI(rep.Change.Ticket), orDashCLI(rep.Change.Requester), orDashCLI(rep.Change.Reason))
	line("等级：%s；执行：%s — %s（%s）", rep.Risk.Class, rep.Started.Local().Format("2006-01-02 15:04:05"), rep.Ended.Local().Format("15:04:05"), rep.Ended.Sub(rep.Started).Round(1e9))
	counts := map[workflow.Outcome]int{}
	for _, r := range rep.Results {
		counts[r.Outcome]++
	}
	line("结果：已验证 %d，失败 %d，不确定 %d，健康退化 %d，配置已变化 %d，跳过 %d", counts[workflow.OutcomeVerified], counts[workflow.OutcomeFailed],
		counts[workflow.OutcomeUncertain], counts[workflow.OutcomeDegraded], counts[workflow.OutcomeDrifted], counts[workflow.OutcomeSkipped])
	if !rep.Decision.Allowed {
		line("变更闸门拒绝：%s", strings.Join(rep.Decision.Refusals, "；"))
	}
	for _, x := range rep.Decision.Bypassed {
		line("紧急变更绕过：%s（%s）", x, rep.Decision.Reason)
	}
	if md {
		b.WriteString("\n")
	}
	h("逐台记录")
	if md {
		b.WriteString("| 批次 | 交换机 | 结果 | 耗时 | 健康变化 | 说明 |\n|---|---|---|---|---|---|\n")
	}
	var rollbacks []string
	for _, r := range rep.Results {
		dur := "-"
		if !r.Started.IsZero() && !r.Ended.IsZero() {
			dur = r.Ended.Sub(r.Started).Round(1e8).String()
		}
		note := r.Detail
		if len(r.Lost) > 0 {
			note = strings.TrimPrefix(note+"；丢失 "+strings.Join(r.Lost, "、"), "；")
		}
		if md {
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s |\n", r.Wave, r.Switch, outcomeText(r.Outcome), dur, healthDelta(r.Before, r.After), note)
		} else {
			line("第 %d 批 %-16s %-10s %-8s %s %s", r.Wave, r.Switch, outcomeText(r.Outcome), dur, healthDelta(r.Before, r.After), note)
		}
		if r.Checkpoint != "" && r.Outcome != workflow.OutcomeVerified && r.Outcome != workflow.OutcomeSkipped {
			rollbacks = append(rollbacks, fmt.Sprintf("fabric rollback --switch %s --checkpoint %s", r.Mgmt, r.Checkpoint))
		}
	}
	if len(rollbacks) > 0 {
		if md {
			b.WriteString("\n")
		}
		h("回滚指引")
		for _, cmd := range rollbacks {
			line("%s", cmd)
		}
	}
	return b.String()
}
