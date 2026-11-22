package cli

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/store"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/adapter/snapshot"
	"github.com/gatework/fabric/baseline"
	"github.com/gatework/fabric/check"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/internal/bootstrap"
	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/report"
	"github.com/gatework/fabric/rollout"
	"github.com/gatework/fabric/vendors/nxos"
)

func writeJSON(e env, v any) error {
	enc := json.NewEncoder(e.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	return nil
}

func newParse(e env) *cobra.Command {
	var file, show string
	c := &cobra.Command{
		Use:   "parse",
		Short: "解析一份 running-config 或 show 命令输出，输出 JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			text := snapshot.Clean(string(body), show, "")
			if show == "" {
				cfg, err := nxos.ParseConfig(cmd.Context(), text)
				if err != nil {
					return err
				}
				return writeJSON(e, cfg)
			}
			for _, p := range nxos.Probes() {
				if p.Command == show {
					sw := &device.Switch{}
					nxos.Observe(cmd.Context(), sw, p.Dimension, text)
					return writeJSON(e, sw)
				}
			}
			return fmt.Errorf("不支持的命令 %q", show)
		},
	}
	c.Flags().StringVar(&file, "file", "", "输入文件")
	c.Flags().StringVar(&show, "show", "", "show 命令（省略表示 running-config）")
	_ = c.MarkFlagRequired("file")
	return c
}

func sshFlags(c *cobra.Command, s *bootstrap.SSH) {
	c.Flags().IntVar(&s.Port, "port", 22, "SSH 端口")
	c.Flags().StringVar(&s.KnownHosts, "known-hosts", "", "known_hosts 文件")
	c.Flags().StringVar(&s.HostKeyPolicy, "host-key-policy", "strict", "主机密钥策略：strict、accept_new 或 replace")
	c.Flags().DurationVar(&s.LoginTimeout, "login-timeout", 30*time.Second, "登录超时")
	c.Flags().DurationVar(&s.CommandTimeout, "command-timeout", 120*time.Second, "单条命令超时")
}

func newCollect(e env) *cobra.Command {
	var intentPath, output string
	var hosts []string
	var parallel int
	var ssh bootstrap.SSH
	c := &cobra.Command{
		Use:   "collect",
		Short: "采集交换机运行配置与运行状态到快照目录",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(hosts) > 0 && !cmd.Flags().Changed("intent") {
				// A configured intent names the default targets; explicit
				// hosts replace them instead of adding to them.
				intentPath = ""
			}
			targets, err := collectTargets(intentPath, hosts)
			if err != nil {
				return err
			}
			creds, err := bootstrap.CredentialsFromEnv(e.lookup)
			if err != nil {
				return err
			}
			dial, err := ssh.Collector(creds)
			if err != nil {
				return err
			}
			st, err := store.NewBatch(output, time.Now())
			if err != nil {
				return err
			}
			results := workflow.Collect(cmd.Context(), workflow.CollectRequest{
				Targets: targets, Dial: dial, Store: st, Parallel: parallel,
			})
			failed := 0
			for _, r := range results {
				if r.Error != "" {
					failed++
					_, _ = fmt.Fprintf(e.stdout, "  %s 失败：%s（已保存 %d 条命令）\n", r.Mgmt, r.Error, r.Commands)
				}
			}
			_, _ = fmt.Fprintf(e.stdout, "采集完成：%d 台成功，%d 台失败，快照目录 %s\n", len(results)-failed, failed, st.Root())
			if failed > 0 {
				return exit(ExitIncomplete, "")
			}
			return nil
		},
	}
	c.Flags().StringVar(&intentPath, "intent", "", "意图文件（采集其中全部交换机）")
	c.Flags().StringSliceVar(&hosts, "host", nil, "交换机管理地址，可重复")
	c.Flags().StringVarP(&output, "output", "o", "snapshots", "快照根目录")
	c.Flags().IntVarP(&parallel, "parallel", "p", 8, "并发数")
	sshFlags(c, &ssh)
	return c
}

func collectTargets(intentPath string, hosts []string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, h := range hosts {
		a, err := netip.ParseAddr(h)
		if err != nil {
			return nil, fmt.Errorf("管理地址 %q 无效", h)
		}
		out = append(out, a)
	}
	in, err := bootstrap.ReadIntent(intentPath)
	if err != nil {
		return nil, err
	}
	if in != nil {
		for _, r := range in.Regions {
			for _, s := range r.Switches {
				out = append(out, s.Mgmt.Addr)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("请用 --intent 或 --host 指定交换机")
	}
	return out, nil
}

type fleetFlags struct {
	snapshot string
	intent   string
	bits     int
}

func (f *fleetFlags) bind(c *cobra.Command, needIntent bool) {
	c.Flags().StringVar(&f.snapshot, "snapshot", "", "快照目录（每台交换机一个子目录）")
	c.Flags().StringVar(&f.intent, "intent", "", "意图文件")
	c.Flags().IntVar(&f.bits, "subnet-bits", 24, "无意图时按管理地址前缀长度划分区域")
	_ = c.MarkFlagRequired("snapshot")
	if needIntent {
		_ = c.MarkFlagRequired("intent")
	}
}

// load reads the snapshot; dims limits parsing (nil reads everything).
func (f *fleetFlags) load(cmd *cobra.Command, dims []evidence.Dimension) (*workflow.Fleet, error) {
	in, err := bootstrap.ReadIntent(f.intent)
	if err != nil {
		return nil, err
	}
	return workflow.LoadSnapshot(cmd.Context(), f.snapshot, in, workflow.LoadOptions{SubnetBits: f.bits, Dimensions: dims})
}

func newCheck(e env) *cobra.Command {
	var ff fleetFlags
	var basePath, prevPath, policyPath, region, only, minSeverity string
	var verbose, asJSON bool
	c := &cobra.Command{
		Use:   "check",
		Short: "检查配置关系、运行状态、一致性，以及与意图/基线的偏差",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fleet, err := ff.load(cmd, nil)
			if err != nil {
				return err
			}
			req := workflow.CheckRequest{Fleet: fleet, Region: region}
			if req.Baseline, err = bootstrap.ReadBaseline(basePath); err != nil {
				return err
			}
			if req.Policy, err = bootstrap.ReadPolicy(policyPath); err != nil {
				return err
			}
			if prevPath != "" {
				if req.Previous, err = workflow.LoadSnapshot(cmd.Context(), prevPath, fleet.Intent, workflow.LoadOptions{SubnetBits: ff.bits}); err != nil {
					return err
				}
			}
			if only != "" && region == "" {
				if sw, err := fleet.Switch(only); err == nil {
					req.Region = sw.Region
				}
			}
			rep, err := workflow.Check(cmd.Context(), req)
			if err != nil {
				return err
			}
			if rep, err = filterCheck(rep, fleet, only, minSeverity); err != nil {
				return err
			}
			if asJSON {
				err = writeJSON(e, rep)
			} else {
				err = report.Check(e.stdout, rep, report.CheckOptions{Verbose: verbose})
			}
			if err != nil {
				return err
			}
			switch {
			case rep.Incomplete():
				return exit(ExitIncomplete, "")
			case len(rep.Findings()) > 0:
				return exit(ExitFindings, "")
			default:
				return nil
			}
		},
	}
	ff.bind(c, false)
	c.Flags().StringVar(&basePath, "baseline", "", "基线文件")
	c.Flags().StringVar(&prevPath, "previous", "", "上一次采集的快照目录，用于发现运行状态退化")
	c.Flags().StringVar(&policyPath, "policy", "", "规则策略文件（启用/停用、级别、参数）")
	c.Flags().StringVar(&region, "region", "", "只检查一个区域")
	c.Flags().StringVar(&only, "switch", "", "只看与一台交换机有关的发现")
	c.Flags().StringVar(&minSeverity, "severity", "", "只看不低于该级别的发现：critical、high、medium、low、info")
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "列出全部发现")
	completeSwitches(e, c, "switch")
	completeRegions(e, c, "region")
	_ = c.RegisterFlagCompletionFunc("severity", cobra.FixedCompletions([]cobra.Completion{"critical", "high", "medium", "low", "info"}, cobra.ShellCompDirectiveNoFileComp))
	c.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	return c
}

func newBackup(e env) *cobra.Command {
	c := newCheck(e)
	c.Use = "backup"
	c.Short = "检查备份配置，默认读取 fabric.yml 的 backup_snapshot"
	c.Args = cobra.NoArgs
	return c
}

func writeOutput(e env, path string, body []byte) error {
	if path == "" || path == "-" {
		_, err := e.stdout.Write(body)
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

func newBaseline(e env) *cobra.Command {
	c := &cobra.Command{Use: "baseline", Short: "基线管理"}
	var ff fleetFlags
	var output string
	derive := &cobra.Command{
		Use:   "derive",
		Short: "按现网多数值生成基线草稿",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fleet, err := ff.load(cmd, workflow.ConfigOnly())
			if err != nil {
				return err
			}
			body, err := baseline.Encode(workflow.DeriveBaseline(fleet))
			if err != nil {
				return err
			}
			return writeOutput(e, output, body)
		},
	}
	ff.bind(derive, false)
	derive.Flags().StringVarP(&output, "output", "o", "-", "输出文件")
	c.AddCommand(derive)
	return c
}

func newImport(e env) *cobra.Command {
	var ff fleetFlags
	var output string
	c := &cobra.Command{
		Use:   "import",
		Short: "从现网反推意图草稿（存量 fabric 纳管）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ff.intent = ""
			fleet, err := ff.load(cmd, workflow.ConfigOnly())
			if err != nil {
				return err
			}
			in, conflicts := workflow.Import(fleet)
			body, err := fabric.EncodeIntent(in)
			if err != nil {
				return err
			}
			if err := writeOutput(e, output, body); err != nil {
				return err
			}
			out := e.stdout
			if output == "" || output == "-" {
				out = e.stderr
			}
			if err := report.Conflicts(out, conflicts); err != nil {
				return err
			}
			if verr := in.Validate(); verr != nil {
				return exit(ExitFindings, "草稿未通过校验，请先修正：%v", verr)
			}
			return nil
		},
	}
	ff.bind(c, false)
	c.Flags().StringVarP(&output, "output", "o", "-", "意图草稿输出文件")
	return c
}

func newPlan(e env) *cobra.Command {
	var ff fleetFlags
	var basePath, region, output string
	var switches []string
	var prune bool
	var strategy rollout.Strategy
	var rf recordFlags
	c := &cobra.Command{
		Use:   "plan",
		Short: "按意图（与基线）生成经过闭环验证的分批变更计划",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fleet, err := ff.load(cmd, workflow.ImpactDimensions())
			if err != nil {
				return err
			}
			base, err := bootstrap.ReadBaseline(basePath)
			if err != nil {
				return err
			}
			now := time.Now()
			rec, err := rf.record(e, now)
			if err != nil {
				return err
			}
			b, err := workflow.Plan(workflow.PlanRequest{
				Fleet: fleet, Baseline: base, Region: region, Switches: switches,
				Prune: prune, Strategy: strategy, Now: now, Change: rec,
			})
			if err != nil {
				return err
			}
			printBundle(e, b)
			if len(b.Items) == 0 {
				return nil
			}
			st, err := store.NewBatch(output, time.Now())
			if err != nil {
				return err
			}
			if err := workflow.WriteBundle(cmd.Context(), st, b); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(e.stdout, "\n计划 %s 已写入 %s\n下发：fabric deploy --plan %s --apply --confirm %s\n", b.ID, st.Root(), st.Root(), b.ID)
			return nil
		},
	}
	ff.bind(c, true)
	c.Flags().StringVar(&basePath, "baseline", "", "基线文件（同时纠正基线偏差）")
	c.Flags().StringVar(&region, "region", "", "只规划一个区域")
	c.Flags().StringSliceVar(&switches, "switch", nil, "只规划这些交换机，可重复")
	c.Flags().BoolVar(&prune, "prune", false, "删除意图中未声明的 fabric 对象（VNI 映射、L3VNI VRF、anycast SVI 等）")
	c.Flags().IntVar(&strategy.WaveSize, "wave-size", 4, "每批 leaf 数量上限")
	c.Flags().BoolVar(&strategy.NoCanary, "no-canary", false, "不单独先变更一台金丝雀 leaf")
	c.Flags().StringVarP(&output, "output", "o", "plans", "计划根目录")
	rf.bind(c)
	return c
}

func printBundle(e env, b *workflow.Bundle) {
	w := e.stdout
	_, _ = fmt.Fprintf(w, "规划结果：%d 台需要变更，%d 台已符合，%d 台拒绝\n", len(b.Items), b.Unchanged, len(b.Refused))
	if len(b.Items) > 0 {
		printGovernance(e, b)
	}
	for _, r := range b.Refused {
		_, _ = fmt.Fprintf(w, "  拒绝 %s：%s\n", r.Switch, r.Reason)
	}
	for _, wave := range b.Waves {
		_, _ = fmt.Fprintf(w, "\n第 %d 批（%s）\n", wave.Index, wave.Label)
		for _, m := range wave.Switches {
			it, _ := b.Item(m.Name)
			var parts []string
			for k, n := range it.Summary {
				parts = append(parts, fmt.Sprintf("%s×%d", k, n))
			}
			_, _ = fmt.Fprintf(w, "  %s [%s] %d 条命令：%s%s\n", m.Name, m.Role, len(it.Plan.Commands()), strings.Join(slices.Sorted(slices.Values(parts)), "，"), impactText(it))
		}
	}
}

// filterCheck keeps the findings about one switch and at or above a
// severity. Rule outcomes stay, so coverage is still reported.
func filterCheck(rep check.Report, fleet *workflow.Fleet, only, minSeverity string) (check.Report, error) {
	floor := assess.SeverityUnset
	if minSeverity != "" {
		s, err := assess.ParseSeverity(minSeverity)
		if err != nil {
			return rep, exit(ExitError, "--severity %q 无效，可选 critical、high、medium、low、info", minSeverity)
		}
		floor = s
	}
	key := ""
	if only != "" {
		sw, err := fleet.Switch(only)
		if err != nil {
			return rep, exit(ExitError, "%v（可用 Tab 补全交换机名）", err)
		}
		key = sw.Key()
	}
	if floor == assess.SeverityUnset && only == "" {
		return rep, nil
	}
	keep := func(name string, a assess.Assessment, switchLevel bool) assess.Assessment {
		var fs []assess.Finding
		for _, f := range a.Findings {
			if f.Severity < floor {
				continue
			}
			if only != "" && !(switchLevel && name == key) && !workflow.Names(f.Subject, only) {
				continue
			}
			fs = append(fs, f)
		}
		a.Findings = fs
		a.Counts = map[assess.Severity]int{}
		for _, f := range fs {
			a.Counts[f.Severity]++
		}
		return a
	}
	out := check.Report{Fabrics: map[string]assess.Assessment{}, Switches: map[string]assess.Assessment{}}
	for name, a := range rep.Fabrics {
		out.Fabrics[name] = keep(name, a, false)
	}
	for name, a := range rep.Switches {
		if only != "" && name != key {
			continue
		}
		out.Switches[name] = keep(name, a, true)
	}
	return out, nil
}
