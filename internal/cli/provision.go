package cli

import (
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/set"
	"github.com/gatework/netops/store"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/internal/bootstrap"
	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/plan"
	"github.com/gatework/fabric/provision"
	"github.com/gatework/fabric/vendors/nxos"
)

// changeFlags are shared by every intent-changing command.
type changeFlags struct {
	intent   string
	snapshot string
	output   string
	dryRun   bool
	record   recordFlags
}

func (f *changeFlags) bind(c *cobra.Command) {
	c.Flags().StringVar(&f.intent, "intent", "", "意图文件（就地更新，旧版本保存为 .bak）")
	c.Flags().StringVar(&f.snapshot, "snapshot", "", "快照目录：校验现网冲突，并为受影响的交换机生成计划")
	c.Flags().StringVarP(&f.output, "output", "o", "plans", "计划根目录")
	c.Flags().BoolVar(&f.dryRun, "dry-run", false, "只显示变更，不写意图与计划")
	_ = c.MarkFlagRequired("intent")
	f.record.bind(c)
}

func (f *changeFlags) run(cmd *cobra.Command, e env, req provision.Request) error {
	in, err := bootstrap.ReadIntent(f.intent)
	if err != nil {
		return err
	}
	now := time.Now()
	rec, err := f.record.record(e, now)
	if err != nil {
		return err
	}
	preq := workflow.ProvisionRequest{Intent: in, Request: req, Now: now, Change: rec}
	if f.snapshot != "" {
		if preq.Fleet, err = workflow.LoadSnapshot(cmd.Context(), f.snapshot, in, workflow.LoadOptions{
			Dimensions: append(workflow.ImpactDimensions(), device.DimInterfaces),
		}); err != nil {
			return err
		}
	}
	out, err := workflow.Provision(preq)
	if err != nil {
		return err
	}
	printProvisioned(e, out)
	if f.dryRun {
		_, _ = fmt.Fprintln(e.stdout, "\n预览模式：意图与计划均未写入。")
		return nil
	}
	if err := workflow.WriteIntent(f.intent, out.Intent); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.stdout, "\n意图已更新：%s（旧版本 %s.bak）\n", f.intent, f.intent)
	if out.Bundle == nil || len(out.Bundle.Items) == 0 {
		return nil
	}
	st, err := store.NewBatch(f.output, time.Now())
	if err != nil {
		return err
	}
	if err := workflow.WriteBundle(cmd.Context(), st, out.Bundle); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.stdout, "计划 %s 已写入 %s\n下发：fabric deploy --plan %s --apply --confirm %s\n", out.Bundle.ID, st.Root(), st.Root(), out.Bundle.ID)
	return nil
}

func printProvisioned(e env, p *workflow.Provisioned) {
	w := e.stdout
	_, _ = fmt.Fprintf(w, "区域 %s 的意图变更：\n", p.Result.Region)
	for _, c := range p.Result.Changes {
		_, _ = fmt.Fprintf(w, "  + %s\n", c.Detail)
	}
	if len(p.Result.Allocated) > 0 {
		_, _ = fmt.Fprintln(w, "自动分配：")
		for _, a := range p.Result.Allocated {
			_, _ = fmt.Fprintf(w, "  %s = %s\n", a.What, a.Value)
		}
	}
	_, _ = fmt.Fprintf(w, "受影响交换机：%s\n", strings.Join(p.Result.Affected, ", "))
	for _, name := range p.Unobserved {
		_, _ = fmt.Fprintf(w, "  %s 无采集数据：新设备用 fabric switch day0 --name %s 导出初始配置，上线后采集再规划\n", name, name)
	}
	if p.Bundle != nil {
		_, _ = fmt.Fprintln(w)
		printBundle(e, p.Bundle)
	}
}

func newNetwork(e env) *cobra.Command {
	c := &cobra.Command{Use: "network", Short: "分布式网关（网络）管理"}
	var f changeFlags
	var q provision.AddNetwork
	var vlan, vni uint
	var gateway string
	add := &cobra.Command{
		Use:   "add",
		Short: "新增分布式网关：VLAN + L2VNI + anycast 网关，默认下发到区域内全部 VTEP",
		RunE: func(cmd *cobra.Command, _ []string) error {
			q.VLAN, q.VNI = fabric.VLAN(vlan), fabric.VNI(vni)
			if gateway != "" {
				p, err := netip.ParsePrefix(gateway)
				if err != nil {
					return exit(ExitError, "网关 %q 无效，格式如 10.1.44.1/24", gateway)
				}
				q.Gateway = p
			}
			return f.run(cmd, e, q)
		},
	}
	f.bind(add)
	add.Flags().StringVar(&q.Region, "region", "", "区域")
	add.Flags().UintVar(&vlan, "vlan", 0, "VLAN（省略则自动分配）")
	add.Flags().UintVar(&vni, "vni", 0, "L2VNI（省略则按区域编号规则）")
	add.Flags().StringVar(&q.Name, "name", "", "网络名称（VLAN name）")
	add.Flags().StringVar(&q.Tenant, "tenant", "", "租户 VRF")
	add.Flags().StringVar(&gateway, "gateway", "", "anycast 网关地址/掩码；省略为纯二层网络")
	add.Flags().StringVar(&q.ACLIn, "acl-in", "", "网关入方向 ACL")
	add.Flags().StringSliceVar(&q.Switches, "switch", nil, "只下发到这些 VTEP（默认全部）")
	_ = add.MarkFlagRequired("region")

	var rf changeFlags
	var rq provision.RemoveNetwork
	var rvlan uint
	remove := &cobra.Command{
		Use:   "remove",
		Short: "下线网络（仍有端口承载或终端在线时拒绝）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			rq.VLAN = fabric.VLAN(rvlan)
			return rf.run(cmd, e, rq)
		},
	}
	rf.bind(remove)
	remove.Flags().StringVar(&rq.Region, "region", "", "区域")
	remove.Flags().UintVar(&rvlan, "vlan", 0, "VLAN")
	remove.Flags().BoolVar(&rq.Force, "force", false, "仍有在线终端时也下线")
	_ = remove.MarkFlagRequired("region")
	_ = remove.MarkFlagRequired("vlan")
	c.AddCommand(add, remove)
	return c
}

func newTenant(e env) *cobra.Command {
	c := &cobra.Command{Use: "tenant", Short: "租户 VRF 管理"}
	var f changeFlags
	var q provision.AddTenant
	var vni, vlan uint
	add := &cobra.Command{
		Use:   "add",
		Short: "新增租户 VRF（L3VNI 与核心 VLAN 可自动分配）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			q.L3VNI, q.VLAN = fabric.VNI(vni), fabric.VLAN(vlan)
			return f.run(cmd, e, q)
		},
	}
	f.bind(add)
	add.Flags().StringVar(&q.Region, "region", "", "区域")
	add.Flags().StringVar(&q.Name, "name", "", "租户名称")
	add.Flags().UintVar(&vni, "l3vni", 0, "L3VNI（省略则自动分配）")
	add.Flags().UintVar(&vlan, "vlan", 0, "核心 VLAN（省略则自动分配）")
	_ = add.MarkFlagRequired("region")
	_ = add.MarkFlagRequired("name")
	c.AddCommand(add)
	return c
}

// parseUplink reads "Eth1/49=SPN01:Eth1/3".
func parseUplink(s string) (provision.Uplink, error) {
	local, remote, ok := strings.Cut(s, "=")
	sw, port, ok2 := strings.Cut(remote, ":")
	if !ok || !ok2 || local == "" || sw == "" || port == "" {
		return provision.Uplink{}, exit(ExitError, "上联 %q 格式应为 本端口=对端交换机:对端口", s)
	}
	return provision.Uplink{Local: local, Remote: sw, RemotePort: port}, nil
}

// parseMembers reads repeated "SWITCH:PORT".
func parseMembers(list []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, m := range list {
		sw, port, ok := strings.Cut(m, ":")
		if !ok || sw == "" || port == "" {
			return nil, exit(ExitError, "成员口 %q 格式应为 交换机:端口", m)
		}
		out[sw] = append(out[sw], port)
	}
	return out, nil
}

func newSwitch(e env) *cobra.Command {
	c := &cobra.Command{Use: "switch", Short: "交换机详情、上线、下线与 day-0 配置"}
	c.AddCommand(newSwitchShow(e), newSwitchAdd(e), newSwitchRemove(e), newSwitchDay0(e))
	return c
}

func newSwitchAdd(e env) *cobra.Command {
	var f changeFlags
	var q provision.AddSwitch
	var mgmt, role, peerLink string
	var uplinks, peerPorts []string
	c := &cobra.Command{
		Use:   "add",
		Short: "新 leaf / border / spine 上线：分配 loopback、互联地址、vPC 域并规划对端",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			if q.Mgmt, err = netip.ParseAddr(mgmt); err != nil {
				return exit(ExitError, "管理地址 %q 无效", mgmt)
			}
			if q.Role, err = fabric.ParseRole(role); err != nil {
				return exit(ExitError, "角色应为 leaf、border 或 spine")
			}
			if err := q.PeerLink.UnmarshalText([]byte(peerLink)); err != nil {
				return exit(ExitError, "peer-link 应为 virtual 或 physical")
			}
			for _, u := range uplinks {
				up, err := parseUplink(u)
				if err != nil {
					return err
				}
				q.Uplinks = append(q.Uplinks, up)
			}
			if q.PeerLinkPorts, err = parseMembers(peerPorts); err != nil {
				return err
			}
			return f.run(cmd, e, q)
		},
	}
	f.bind(c)
	c.Flags().StringVar(&q.Region, "region", "", "区域")
	c.Flags().StringVar(&q.Name, "name", "", "交换机名称（即 hostname）")
	c.Flags().StringVar(&mgmt, "mgmt", "", "管理地址")
	c.Flags().StringVar(&role, "role", "leaf", "角色：leaf、border、spine")
	c.Flags().StringVar(&q.Model, "model", "", "型号")
	c.Flags().StringSliceVar(&uplinks, "uplink", nil, "互联：本端口=对端交换机:对端口，可重复")
	c.Flags().StringVar(&q.VPCPeer, "vpc-peer", "", "与这台已有交换机组成 vPC")
	c.Flags().StringVar(&peerLink, "peer-link", "virtual", "vPC peer-link：virtual 或 physical")
	c.Flags().StringSliceVar(&peerPorts, "peer-link-port", nil, "物理 peer-link 成员口：交换机:端口，可重复")
	_ = c.MarkFlagRequired("region")
	_ = c.MarkFlagRequired("name")
	_ = c.MarkFlagRequired("mgmt")
	return c
}

func newSwitchRemove(e env) *cobra.Command {
	var f changeFlags
	var q provision.RemoveSwitch
	c := &cobra.Command{
		Use:   "remove",
		Short: "交换机下线：释放对端互联口、解除 vPC、删除 spine 上的显式邻居",
		RunE:  func(cmd *cobra.Command, _ []string) error { return f.run(cmd, e, q) },
	}
	f.bind(c)
	c.Flags().StringVar(&q.Name, "name", "", "交换机名称")
	_ = c.MarkFlagRequired("name")
	return c
}

func newSwitchDay0(e env) *cobra.Command {
	var intentPath, name, output string
	c := &cobra.Command{
		Use:   "day0",
		Short: "导出新交换机的完整 day-0 配置（POAP 或 console 粘贴）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, err := bootstrap.ReadIntent(intentPath)
			if err != nil {
				return err
			}
			r, s, ok := in.Locate(name)
			if !ok {
				return exit(ExitError, "意图中没有交换机 %s", name)
			}
			if r.Underlay == nil {
				return exit(ExitError, "区域 %s 未纳管 underlay，无法生成 day-0 配置", r.Name)
			}
			text, err := nxos.Day0(plan.Compile(r, s))
			if err != nil {
				return err
			}
			return writeOutput(e, output, []byte(text))
		},
	}
	c.Flags().StringVar(&intentPath, "intent", "", "意图文件")
	c.Flags().StringVar(&name, "name", "", "交换机名称")
	c.Flags().StringVarP(&output, "output", "o", "-", "输出文件")
	_ = c.MarkFlagRequired("intent")
	_ = c.MarkFlagRequired("name")
	return c
}

func newPort(e env) *cobra.Command {
	c := &cobra.Command{Use: "port", Short: "接入端口分配、回收与空闲端口查询"}
	c.AddCommand(newPortAssign(e), newPortRelease(e), newPortFree(e))
	return c
}

func newPortAssign(e env) *cobra.Command {
	var f changeFlags
	var q provision.AssignPort
	var vpc, mode, vlans string
	var members []string
	var po uint
	c := &cobra.Command{
		Use:   "assign",
		Short: "分配接入端口：单口、单机 LACP 聚合或跨 vPC 聚合（所需网络自动扩展到该交换机）",
		Example: "  fabric port assign --intent i.yaml --switch LEF01 --port Ethernet1/10 --profile app-access\n" +
			"  fabric port assign --intent i.yaml --vpc LEF01 --member LEF01:Eth1/10 --member LEF02:Eth1/10 --vlans 100,300 --mode trunk",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := q.Mode.UnmarshalText([]byte(mode)); err != nil {
				return exit(ExitError, "模式应为 access 或 trunk")
			}
			nets, err := set.Parse[uint16](vlans)
			if err != nil {
				return exit(ExitError, "VLAN 列表 %q 无效", vlans)
			}
			for r := range nets.All() {
				for v := uint32(r.Min); v <= uint32(r.Max); v++ {
					q.Networks = append(q.Networks, fabric.VLAN(v))
				}
			}
			if q.Members, err = parseMembers(members); err != nil {
				return err
			}
			q.PortChannel = int(po)
			switch {
			case vpc != "":
				q.Kind, q.Switch = fabric.AttachVPC, vpc
			case len(q.Members) > 0:
				q.Kind = fabric.AttachPortChannel
			default:
				q.Kind = fabric.AttachPort
			}
			return f.run(cmd, e, q)
		},
	}
	f.bind(c)
	c.Flags().StringVar(&q.Switch, "switch", "", "交换机（单口或单机聚合）")
	c.Flags().StringVar(&q.Interface, "port", "", "端口（单口）")
	c.Flags().StringVar(&vpc, "vpc", "", "vPC 对中的任一交换机（跨 vPC 聚合）")
	c.Flags().StringSliceVar(&members, "member", nil, "聚合成员口：交换机:端口，可重复")
	c.Flags().UintVar(&po, "port-channel", 0, "port-channel / vPC 号（省略则自动分配）")
	c.Flags().StringVar(&q.Profile, "profile", "", "端口模板")
	c.Flags().StringVar(&mode, "mode", "access", "access 或 trunk（未用模板时）")
	c.Flags().StringVar(&vlans, "vlans", "", "承载的 VLAN，如 100,300-302（未用模板时）")
	c.Flags().IntVar(&q.MTU, "mtu", 0, "MTU")
	c.Flags().BoolVar(&q.Edge, "edge", false, "spanning-tree port type edge")
	c.Flags().StringVar(&q.Description, "description", "", "端口描述")
	c.Flags().BoolVar(&q.Adopt, "adopt", false, "纳管已接入的端口：允许端口 up 或已学到 MAC（默认拒绝，以免改掉在线服务器的端口）")
	return c
}

func newPortRelease(e env) *cobra.Command {
	var f changeFlags
	var q provision.ReleasePort
	c := &cobra.Command{
		Use:   "release",
		Short: "回收端口：从意图删除，端口恢复出厂并关闭（聚合按整体回收）",
		RunE:  func(cmd *cobra.Command, _ []string) error { return f.run(cmd, e, q) },
	}
	f.bind(c)
	c.Flags().StringVar(&q.Switch, "switch", "", "交换机")
	c.Flags().StringVar(&q.Interface, "port", "", "端口或 port-channel")
	_ = c.MarkFlagRequired("switch")
	_ = c.MarkFlagRequired("port")
	return c
}

func newPortFree(e env) *cobra.Command {
	var intentPath, snapshot, name string
	c := &cobra.Command{
		Use:   "free",
		Short: "列出可分配端口：空闲、已接入未纳管（up 或学到 MAC）、有描述的预留口分组显示",
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, err := bootstrap.ReadIntent(intentPath)
			if err != nil {
				return err
			}
			fleet, err := workflow.LoadSnapshot(cmd.Context(), snapshot, in, workflow.LoadOptions{
				Dimensions: []evidence.Dimension{device.DimConfig, device.DimInterfaces, device.DimMACs},
			})
			if err != nil {
				return err
			}
			free, err := provision.Free(in, provision.NewObservation(fleet.Switches), name)
			if err != nil {
				return err
			}
			printFreePorts(e.stdout, name, free)
			return nil
		},
	}
	c.Flags().StringVar(&intentPath, "intent", "", "意图文件")
	c.Flags().StringVar(&snapshot, "snapshot", "", "快照目录")
	c.Flags().StringVar(&name, "switch", "", "交换机")
	_ = c.MarkFlagRequired("intent")
	_ = c.MarkFlagRequired("snapshot")
	_ = c.MarkFlagRequired("switch")
	return c
}

func orDashCLI(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// printFreePorts groups the ports nothing configures: quiet ones can be
// assigned, connected ones need --adopt, described ones are probably
// reserved.
func printFreePorts(w io.Writer, name string, free []provision.FreePort) {
	var quiet, connected, reserved []string
	for _, p := range free {
		switch {
		case p.Status == "up" || p.MACs > 0:
			s := p.Interface + " " + orDashCLI(p.Status)
			if p.MACs > 0 {
				s += fmt.Sprintf("，%d 个 MAC", p.MACs)
			}
			connected = append(connected, s)
		case p.Status == "" || p.Status == "down" || strings.HasPrefix(p.Status, "notconn") || strings.HasPrefix(p.Status, "sfp"):
			if p.Described {
				reserved = append(reserved, p.Interface)
			} else {
				quiet = append(quiet, p.Interface)
			}
		default:
			reserved = append(reserved, p.Interface+" "+p.Status)
		}
	}
	_, _ = fmt.Fprintf(w, "%s：可直接分配 %d 个，已接入未纳管 %d 个，有描述的预留口 %d 个\n", name, len(quiet), len(connected), len(reserved))
	if len(quiet) > 0 {
		_, _ = fmt.Fprintf(w, "  可直接分配：%s\n", strings.Join(quiet, " "))
	}
	if len(connected) > 0 {
		_, _ = fmt.Fprintf(w, "  已接入未纳管（分配需 --adopt，先确认接的是谁：fabric locate）：\n    %s\n", strings.Join(connected, "\n    "))
	}
	if len(reserved) > 0 {
		_, _ = fmt.Fprintf(w, "  有描述但未接入（可能是预留口，分配前核对描述）：%s\n", strings.Join(reserved, " "))
	}
}
