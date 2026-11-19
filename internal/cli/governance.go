package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gatework/fabric/governance"
	"github.com/gatework/fabric/internal/workflow"
)

// recordFlags collect the change record of a plan.
type recordFlags struct {
	ticket, requester, reason, window, zone string
	ttl                                     time.Duration
}

func (f *recordFlags) bind(c *cobra.Command) {
	c.Flags().StringVar(&f.ticket, "ticket", "", "变更工单号")
	c.Flags().StringVar(&f.requester, "requester", "", "申请人（默认 $USER）")
	c.Flags().StringVar(&f.reason, "reason", "", "变更原因")
	c.Flags().StringVar(&f.window, "window", "", "维护窗口，如 22:00-06:00")
	c.Flags().StringVar(&f.zone, "tz", "Asia/Shanghai", "维护窗口时区")
	c.Flags().DurationVar(&f.ttl, "ttl", 24*time.Hour, "计划有效期（超过后拒绝下发，需重新规划）")
}

func (f *recordFlags) record(e env, now time.Time) (governance.Record, error) {
	rec := governance.Record{Ticket: f.ticket, Requester: f.requester, Reason: f.reason, CreatedAt: now.UTC()}
	if rec.Requester == "" {
		rec.Requester, _ = e.lookup("USER")
	}
	if f.ttl > 0 {
		rec.ExpiresAt = now.Add(f.ttl).UTC()
	}
	if f.window != "" {
		w, err := governance.ParseWindow(f.window, f.zone)
		if err != nil {
			return rec, exit(ExitError, "维护窗口无效：%v", err)
		}
		rec.Window = w
	}
	return rec, nil
}

// gateFlags collect the deploy-time declarations.
type gateFlags struct {
	ackRisk, emergency, requireTicket bool
	reason                            string
}

func (f *gateFlags) bind(c *cobra.Command) {
	c.Flags().BoolVar(&f.ackRisk, "ack-risk", false, "确认重大变更风险")
	c.Flags().BoolVar(&f.emergency, "emergency", false, "紧急变更：允许在维护窗口外执行（需 --emergency-reason）")
	c.Flags().StringVar(&f.reason, "emergency-reason", "", "紧急变更原因")
	c.Flags().BoolVar(&f.requireTicket, "require-ticket", false, "没有变更工单的计划拒绝执行")
}

func (f *gateFlags) options() governance.GateOptions {
	return governance.GateOptions{AckRisk: f.ackRisk, Emergency: f.emergency, Reason: f.reason, RequireTicket: f.requireTicket}
}

func printGovernance(e env, b *workflow.Bundle) {
	w := e.stdout
	r := b.Risk
	_, _ = fmt.Fprintf(w, "变更等级：%s（%d 台交换机，%d 个区域，影响在线终端约 %d 个）\n", r.Class, r.Switches, r.Regions, r.Endpoints)
	for _, reason := range r.Reasons {
		_, _ = fmt.Fprintf(w, "  · %s\n", reason)
	}
	c := b.Change
	var parts []string
	if c.Ticket != "" {
		parts = append(parts, "工单 "+c.Ticket)
	}
	if c.Requester != "" {
		parts = append(parts, "申请人 "+c.Requester)
	}
	if c.Window != nil {
		parts = append(parts, "窗口 "+c.Window.String())
	}
	if !c.ExpiresAt.IsZero() {
		parts = append(parts, "有效期至 "+c.ExpiresAt.Local().Format("2006-01-02 15:04"))
	}
	if len(parts) > 0 {
		_, _ = fmt.Fprintf(w, "变更记录：%s\n", strings.Join(parts, "，"))
	}
	if c.Reason != "" {
		_, _ = fmt.Fprintf(w, "变更原因：%s\n", c.Reason)
	}
}

func impactText(it *workflow.Item) string {
	im := it.Impact
	var parts []string
	if len(im.VLANs) > 0 {
		parts = append(parts, fmt.Sprintf("VLAN %d 个", len(im.VLANs)))
	}
	if len(im.Tenants) > 0 {
		parts = append(parts, "租户 "+strings.Join(im.Tenants, "/"))
	}
	if len(im.Ports) > 0 {
		parts = append(parts, fmt.Sprintf("端口 %d 个", len(im.Ports)))
	}
	if len(im.VPCs) > 0 {
		parts = append(parts, fmt.Sprintf("vPC %d 个", len(im.VPCs)))
	}
	if im.Underlay {
		parts = append(parts, "underlay")
	}
	if im.Endpoints > 0 {
		parts = append(parts, fmt.Sprintf("在线终端 %d", im.Endpoints))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  影响：" + strings.Join(parts, "，")
}
