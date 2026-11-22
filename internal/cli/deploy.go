package cli

import (
	"bufio"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gatework/netops/authz"
	"github.com/gatework/netops/store"
	njournal "github.com/gatework/netops/store/journal"

	"github.com/gatework/fabric/internal/bootstrap"
	"github.com/gatework/fabric/internal/workflow"
)

func newDeploy(e env) *cobra.Command {
	var planDir, confirm, journal string
	var authzPath string
	var leaseTTL time.Duration
	var doApply, save bool
	var ssh bootstrap.SSH
	var gf gateFlags
	c := &cobra.Command{
		Use:   "deploy",
		Short: "按批次下发计划（默认只预览；--apply 才写入设备）",
		Long: "按计划中的批次下发。每台交换机：变更前健康快照 → 校验配置指纹 → 打 checkpoint → 逐行写入 → 回读验证 → 健康门限对比。" +
			"任何一台失败、不确定或健康退化都会停止后续批次；不会自动重试或回滚，请按报告中的 checkpoint 使用 fabric rollback。",
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := workflow.ReadBundle(planDir)
			if err != nil {
				return err
			}
			printBundle(e, b)
			if !doApply {
				_, _ = fmt.Fprintln(e.stdout, "\n预览模式：未连接设备。确认无误后加 --apply --confirm "+b.ID+" 执行。")
				return nil
			}
			if err := confirmed(e, b.ID, confirm); err != nil {
				return err
			}
			creds, err := bootstrap.CredentialsFromEnv(e.lookup)
			if err != nil {
				return err
			}
			dial, err := ssh.Dialer(creds)
			if err != nil {
				return err
			}
			st, err := store.NewBatch(journal, time.Now())
			if err != nil {
				return err
			}
			execs, err := njournal.Open(st.Root(), nil)
			if err != nil {
				return err
			}
			guard, err := newGuard(e, authzPath)
			if err != nil {
				return err
			}
			rep := workflow.Deploy(cmd.Context(), workflow.DeployRequest{
				Bundle: b, Dial: dial, Journal: execs, Save: save, Gate: gf.options(),
				Authorize: guard.authorize(authz.PermDeploy), Leases: guard.leases,
				Holder: guard.holder(b.ID), LeaseTTL: leaseTTL,
			})
			if _, err := st.WriteJSON(cmd.Context(), "deploy.json", rep); err != nil {
				return err
			}
			printDeploy(e, rep)
			_, _ = fmt.Fprintf(e.stdout, "执行记录：%s\n", st.Root())
			if !rep.OK() {
				return exit(ExitFindings, "下发未全部完成，请先处理上面的失败项")
			}
			return nil
		},
	}
	c.Flags().StringVar(&planDir, "plan", "", "计划目录")
	c.Flags().BoolVar(&doApply, "apply", false, "实际写入设备")
	c.Flags().StringVar(&confirm, "confirm", "", "计划 ID；非交互执行时必须提供")
	c.Flags().BoolVar(&save, "save", false, "验证通过后 copy running-config startup-config")
	c.Flags().StringVar(&journal, "journal", "executions", "执行记录根目录")
	c.Flags().StringVar(&authzPath, "authz", "", "权限策略文件（省略表示不做权限检查）")
	c.Flags().DurationVar(&leaseTTL, "lease-ttl", time.Hour, "设备租约时长；进程异常退出后到期自动释放")
	_ = c.MarkFlagRequired("plan")
	sshFlags(c, &ssh)
	gf.bind(c)
	return c
}

// confirmed requires the operator to name the plan: by flag, or by typing
// it on a terminal.
func confirmed(e env, id, flag string) error {
	if flag != "" {
		if flag != id {
			return exit(ExitError, "--confirm %s 与计划 ID %s 不一致", flag, id)
		}
		return nil
	}
	if !e.tty {
		return exit(ExitError, "非交互执行需要 --confirm %s", id)
	}
	_, _ = fmt.Fprintf(e.stdout, "\n将写入设备。输入计划 ID %s 确认：", id)
	line, _ := bufio.NewReader(e.stdin).ReadString('\n')
	if strings.TrimSpace(line) != id {
		return exit(ExitError, "未确认，已取消")
	}
	return nil
}

func outcomeText(o workflow.Outcome) string {
	switch o {
	case workflow.OutcomeVerified:
		return "已验证"
	case workflow.OutcomeSkipped:
		return "已跳过"
	case workflow.OutcomeDrifted:
		return "配置已变化，拒绝执行"
	case workflow.OutcomeFailed:
		return "失败"
	case workflow.OutcomeUncertain:
		return "结果不确定（禁止自动重试）"
	case workflow.OutcomeDegraded:
		return "已写入但健康退化"
	default:
		return string(o)
	}
}

func printDeploy(e env, rep workflow.DeployReport) {
	w := e.stdout
	if !rep.Decision.Allowed {
		_, _ = fmt.Fprintln(w, "\n变更闸门拒绝执行，未连接任何设备：")
		for _, r := range rep.Decision.Refusals {
			_, _ = fmt.Fprintf(w, "  - %s\n", r)
		}
		return
	}
	for _, b := range rep.Decision.Bypassed {
		_, _ = fmt.Fprintf(w, "\n⚠ 紧急变更，已绕过：%s（原因：%s）\n", b, rep.Decision.Reason)
	}
	_, _ = fmt.Fprintln(w, "\n下发结果：")
	for _, r := range rep.Results {
		line := fmt.Sprintf("  第 %d 批 %s：%s", r.Wave, r.Switch, outcomeText(r.Outcome))
		if r.Detail != "" {
			line += "（" + r.Detail + "）"
		}
		if len(r.Lost) > 0 {
			line += " 丢失：" + strings.Join(r.Lost, "、")
		}
		if r.Checkpoint != "" && r.Outcome != workflow.OutcomeVerified {
			line += " 回滚：fabric rollback --switch " + r.Mgmt + " --checkpoint " + r.Checkpoint
		}
		_, _ = fmt.Fprintln(w, line)
	}
	if rep.StoppedAt != 0 {
		_, _ = fmt.Fprintf(w, "第 %d 批出现问题，后续批次已停止。\n", rep.StoppedAt)
	}
}

func newRollback(e env) *cobra.Command {
	var mgmt, checkpoint, confirm, authzPath string
	var ssh bootstrap.SSH
	c := &cobra.Command{
		Use:   "rollback",
		Short: "将一台交换机原子回滚到指定 checkpoint",
		RunE: func(cmd *cobra.Command, _ []string) error {
			addr, err := netip.ParseAddr(mgmt)
			if err != nil {
				return exit(ExitError, "管理地址 %q 无效", mgmt)
			}
			if err := confirmed(e, checkpoint, confirm); err != nil {
				return err
			}
			creds, err := bootstrap.CredentialsFromEnv(e.lookup)
			if err != nil {
				return err
			}
			dial, err := ssh.Dialer(creds)
			if err != nil {
				return err
			}
			guard, err := newGuard(e, authzPath)
			if err != nil {
				return err
			}
			target := bootstrap.Switch(addr)
			if err := guard.authorize(authz.PermRollback)(target); err != nil {
				return exit(ExitError, "%s", err)
			}
			release, err := guard.hold(cmd.Context(), target, "rollback "+checkpoint)
			if err != nil {
				return exit(ExitError, "%s", err)
			}
			defer release()
			res, err := workflow.Rollback(cmd.Context(), dial(addr), checkpoint)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(e.stdout, "%s 已回滚到 %s，当前配置指纹 %s\n", mgmt, checkpoint, res.Fingerprint[:12])
			_, _ = fmt.Fprintf(e.stdout, "健康复核：NVE 对端 %d→%d，EVPN 会话 %d→%d，OSPF %d→%d\n",
				len(res.Before.NVEPeers), len(res.After.NVEPeers), len(res.Before.BGPSessions), len(res.After.BGPSessions), len(res.Before.OSPF), len(res.After.OSPF))
			for _, r := range res.Recovered {
				_, _ = fmt.Fprintf(e.stdout, "  恢复：%s\n", strings.ReplaceAll(r, " lost", ""))
			}
			if len(res.Lost) > 0 {
				return exit(ExitFindings, "回滚后仍有丢失：%s", strings.Join(res.Lost, "、"))
			}
			return nil
		},
	}
	c.Flags().StringVar(&mgmt, "switch", "", "交换机管理地址")
	c.Flags().StringVar(&checkpoint, "checkpoint", "", "checkpoint 名称")
	c.Flags().StringVar(&confirm, "confirm", "", "checkpoint 名称；非交互执行时必须提供")
	c.Flags().StringVar(&authzPath, "authz", "", "权限策略文件（省略表示不做权限检查）")
	_ = c.MarkFlagRequired("switch")
	_ = c.MarkFlagRequired("checkpoint")
	sshFlags(c, &ssh)
	return c
}
