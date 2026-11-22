package report

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/gatework/netops/assess"

	"github.com/gatework/fabric/adopt"
	"github.com/gatework/fabric/check"
)

// SeverityName returns the Chinese label of a severity.
func SeverityName(s assess.Severity) string {
	switch s {
	case assess.SeverityCritical:
		return "严重"
	case assess.SeverityHigh:
		return "高"
	case assess.SeverityMedium:
		return "中"
	case assess.SeverityLow:
		return "低"
	case assess.SeverityInfo:
		return "提示"
	case assess.SeverityUnset:
		return "未定"
	default:
		return s.String()
	}
}

// CheckOptions tune the check report.
type CheckOptions struct {
	// Verbose lists every finding; otherwise each rule shows at most
	// Limit findings.
	Verbose bool
	Limit   int
}

type ruleGroup struct {
	rule     assess.RuleID
	title    string
	severity assess.Severity
	findings []assess.Finding
}

// Check writes the findings grouped by rule, most severe first, followed
// by rules that could not run.
func Check(w io.Writer, rep check.Report, opt CheckOptions) error {
	if opt.Limit <= 0 {
		opt.Limit = 5
	}
	groups := map[assess.RuleID]*ruleGroup{}
	for _, f := range rep.Findings() {
		g, ok := groups[f.Rule]
		if !ok {
			g = &ruleGroup{rule: f.Rule, title: f.Title, severity: f.Severity}
			groups[f.Rule] = g
		}
		g.findings = append(g.findings, f)
	}
	list := slices.Collect(maps.Values(groups))
	slices.SortFunc(list, func(a, b *ruleGroup) int {
		return cmp.Or(cmp.Compare(b.severity, a.severity), cmp.Compare(a.rule, b.rule))
	})
	p := &printer{w: w}
	p.line("检查结果：%d 条发现，%d 条规则命中", len(rep.Findings()), len(list))
	for _, g := range list {
		p.line("")
		p.line("[%s] %s（%s）%d 条", SeverityName(g.severity), g.title, g.rule, len(g.findings))
		for i, f := range g.findings {
			if !opt.Verbose && i == opt.Limit {
				p.line("    … 另有 %d 条，使用 -v 查看全部", len(g.findings)-opt.Limit)
				break
			}
			p.line("  - %s", describe(f))
		}
	}
	skipped := skippedRules(rep)
	if len(skipped) > 0 {
		p.line("")
		p.line("未运行的规则（证据不完整，不能视为健康）：")
		for _, s := range skipped {
			p.line("  - %s", s)
		}
	}
	return p.err
}

// describe prefixes a finding with the switch it concerns unless the
// detail already names it.
func describe(f assess.Finding) string {
	for _, s := range slices.Backward(f.Subject.Parts()) {
		if s.Kind == "switch" && !strings.Contains(f.Detail, s.Key) {
			return s.Key + "：" + f.Detail
		}
	}
	return f.Detail
}

func skippedRules(rep check.Report) []string {
	seen := map[string]bool{}
	collect := func(a assess.Assessment) {
		for _, o := range a.Rules {
			if o.State == assess.RuleSkipped || o.State == assess.RuleFailed {
				var missing []string
				for _, d := range o.Missing {
					missing = append(missing, string(d))
				}
				seen[fmt.Sprintf("%s：%s %s", o.Rule, o.State, strings.Join(missing, ","))] = true
			}
		}
	}
	for _, a := range rep.Fabrics {
		collect(a)
	}
	for _, a := range rep.Switches {
		collect(a)
	}
	return slices.Sorted(maps.Keys(seen))
}

// Conflicts writes adoption conflicts grouped by region.
func Conflicts(w io.Writer, conflicts []adopt.Conflict) error {
	p := &printer{w: w}
	if len(conflicts) == 0 {
		p.line("导入完成：没有需要确认的冲突。")
		return p.err
	}
	p.line("导入完成：%d 处冲突需要确认（草稿采用多数值，规划会显示将要变更的内容）", len(conflicts))
	region := ""
	for _, c := range conflicts {
		if c.Region != region {
			region = c.Region
			p.line("")
			p.line("区域 %s：", region)
		}
		line := "  - " + c.Subject + "：" + c.Detail
		if c.Chosen != "" {
			line += "（采用 " + c.Chosen + "）"
		}
		if len(c.Outliers) > 0 {
			line += "；涉及 " + strings.Join(c.Outliers, ", ")
		}
		p.line("%s", line)
	}
	return p.err
}

// printer writes lines and keeps the first error.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) line(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format+"\n", args...)
}
