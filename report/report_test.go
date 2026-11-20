package report_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric/adopt"
	"github.com/gatework/fabric/check"
	"github.com/gatework/fabric/report"
)

func TestCheckGroupsBySeverity(t *testing.T) {
	t.Parallel()
	sub := assess.NewSubject("region", "dc1").Child("switch", "LEF01")
	rep := check.Report{
		Fabrics: map[string]assess.Assessment{},
		Switches: map[string]assess.Assessment{"10.0.0.1": {
			Findings: []assess.Finding{
				{Rule: "fabric.a", Title: "低级", Severity: assess.SeverityLow, Subject: sub, Detail: "x"},
				{Rule: "fabric.b", Title: "严重项", Severity: assess.SeverityCritical, Subject: sub, Detail: "y"},
			},
			Rules: []assess.RuleOutcome{{Rule: "fabric.c", State: assess.RuleSkipped, Missing: []evidence.Dimension{"vpc"}}},
		}},
	}
	var buf bytes.Buffer
	if err := report.Check(&buf, rep, report.CheckOptions{}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Index(out, "严重项") > strings.Index(out, "低级") {
		t.Errorf("critical not first:\n%s", out)
	}
	for _, want := range []string{"LEF01：y", "未运行的规则", "fabric.c"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func ExampleConflicts() {
	_ = report.Conflicts(os.Stdout, []adopt.Conflict{{Region: "dc1", Subject: "networks[100]", Detail: "VNI 不一致", Chosen: "10100", Outliers: []string{"LEF03"}}})
	// Output:
	// 导入完成：1 处冲突需要确认（草稿采用多数值，规划会显示将要变更的内容）
	//
	// 区域 dc1：
	//   - networks[100]：VNI 不一致（采用 10100）；涉及 LEF03
}
