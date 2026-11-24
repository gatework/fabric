# Changelog

## Unreleased

### Added

- `fabric collect` 在全部维度采集完成后，额外按原文采集 `show bgp ipv4 unicast summary vrf all` 与 `show ip route summary vrf all`，为 border 南北向的 BGP 评估积累真实数据。这两条只保存不解析；设备不支持时跳过，不影响其他采集结果。尚未在真机上验证。

- `fabric impact` 对 border 增加南北向评估。列出本机 VRF 的出口路由（静态路由及下一跳），并逐条判断同区域其他 border 能否接替：
  - 有自己的 fabric 外 FULL 邻接：有效备份；
  - 有自己的外部链路且 up、启用了 OSPF，但当前无邻接：通常是主备防火墙的备机，判为有风险并提示核实；
  - 唯一的外部邻居就是本机：判为不可维护。

  同时列出 fabric 外的 OSPF 邻接。

- `fabric path` 增加 MTU 检查：底层互联最小 MTU 减去 VXLAN 封装的 50 字节，连标准 1500 字节报文都承载不了时判为断点；接入端口或网关 SVI 显式配置的 MTU 超过底层承载能力时给出警告（例如 SVI 与底层同为 9216 时，大于 9166 字节的报文会被丢弃）。network-qos 设定的二层口 MTU 未采集，不做判断。

- `fabric diff --from <旧快照>`：两次采集之间的变化（ADR 0015）。
  - 逐台交换机按配置块对比运行配置，ACL 条目顺序敏感；密码、community 等敏感内容已遮蔽，密钥变化单独标为“已更改”。
  - 两份快照都采集了运行状态时，再比较端口 up/down、OSPF FULL、BGP EVPN、NVE 对端的丢失与新增，以及本地终端的迁移、出现与消失。
  - 有丢失项的交换机排在最前；`--switch`、`-v` 显示明细。

- `fabric path <源> <目的>`：两个终端（MAC 或 IP）之间的路径追踪（ADR 0014）。
  - 判断转发模式：二层桥接（同一 L2VNI）、三层路由（同一 VRF，经 anycast 网关与 L3VNI）或跨 VRF。
  - 双向逐跳验证：接入端口与 VLAN 放行、L2VNI、网关 SVI、L3VNI、入口 VTEP 的 EVPN 路由及下一跳、NVE 对端、两端上行与 ECMP、目的端口。
  - 指出第一个断点并给出建议；只有路径完整时退出码为 0。

- `fabric impact <交换机>`：维护影响评估，回答该交换机重启、升级或下线期间会断什么（ADR 0013）。
  - spine：列出只剩本机一条健康上行的 VTEP（会失联）、EVPN 会话只建在本机上的 VTEP，以及会失去冗余的 VTEP。
  - leaf / border：本机学到的终端按端口归并，附描述、VLAN 和 IP，分为单归（会中断）与 vPC 双归（由对端接管）；同时检查 vPC 对端能否独自承载（上行冗余、peer、keepalive、一致性、对应 vPC 是否 up）；外部设备单独列出。
  - 结论为 可以维护 / 有风险 / 不可维护 / 无法判断，并给出维护前要做的事；只有 可以维护 时退出码为 0。

### Fixed

- `fabric port assign` 不再分配已接入的端口：端口链路 up 或学到 MAC 时拒绝分配，以免把接着未登记服务器的端口改掉。纳管已有服务器的端口要显式加 `--adopt`。新交换机上线时，spine 侧端口同样受此检查。
- `fabric port free` 原来把 up 的端口也算作可分配；现在分三组显示：可直接分配、已接入未纳管（up 或学到 MAC，需 `--adopt`）、有描述但未接入的预留口。端口按编号自然排序（1/2 排在 1/10 之前）。

### Changed

- `fabric capacity` 把以太网端口占用拆成 up 与预留（down 但有描述、地址或聚合配置），并给出区域的 up、预留、空闲合计；表格按中文字宽对齐。`fabric.switch_scale` 的端口发现同样注明拆分。

### Changed

- `fabric topology` 默认输出改为拓扑图：两台 spine 为左右母线，每台 VTEP 一行，连线标出两端端口与速率；粗线表示满速，细线表示低速，虚线加 ✗ 表示故障，并标出最低失败层。行尾注明原因、冗余等级与速率不对称，vPC 对用括号标出。`--switch` 以该交换机为根画出邻居树：fabric 互联（OSPF、BGP EVPN 状态）、vPC peer-link、fabric 内其他交换机、CDP 发现的外部设备与管理网。spine 的树会列出仅剩本机一条上行的 VTEP（本机维护前须先恢复）。故障详情改为 `--detail` 显示。`fabric switch show` 使用同一棵邻居树。
- CDP 邻居按用途分类，带本地 port-channel 与端口速率；每台 VTEP 到每台 spine 的 BGP EVPN 会话按 router-id 匹配。

### Added

- `fabric topology` 逐层验证每条 fabric 互联：物理（两端端口状态与原因）→ CDP 邻居 → 配置（三层路由口、未 shutdown、意图地址、两端 MTU/OSPF/子网一致）→ OSPF（每个已采集端都 FULL）。故障定位到最低失败层，给出两端 L2/L3、互联地址、状态、速率、MTU、原因、影响与处置建议。默认只列问题；`--matrix` 显示 leaf × spine 矩阵（含上行数与冗余等级），`--switch` 查看一台交换机的全部 fabric 端口，`--all` 列出全部互联（ADR 0012）。
- 上行冗余：每台 VTEP 的健康上行、可达与失去的 spine，等级为冗余正常 / 降级 / 单上行 / 孤立 / 未知；每台 spine 的健康下联；健康上行速率不一致（ECMP 不对称）告警；意图中有但快照中没有的交换机。
- `fabric switch show <名称>`（别名 `inspect`）：单台交换机全景，包括健康评分、上行或下联、互联明细、OSPF 与 BGP EVPN 会话（显示对端名称）、NVE 对端与 VNI、vPC、聚合，以及按标题归并的本机发现。
- `fabric check --switch`、`--severity`；`fabric status` 增加“主要问题”列，表格按中文字宽对齐。
- 每个视图末尾给出下一步命令；`--switch`、`--region`、`--severity` 与 `switch show` 支持 Tab 补全。

### Fixed

- 下发授权使用的设备角色写成了枚举的原始字节，授权策略中的 `role=leaf` 等范围永远匹配不到；现在使用角色名称。

### Added

- `fabric deploy` 与 `fabric rollback` 取跨工具设备租约（netops `lease`，按管理地址，存放在 `~/.local/share/gatework/leases`，可用 `$GATEWORK_LEASES_DIR` 覆盖）。另一工具（wireless、firemon）正在修改的交换机所在批次直接失败并报告占用者，后续批次停止。`--lease-ttl` 设定租约时长，默认 1 小时；进程异常退出后租约到期自动释放。
- `--authz <策略文件>`：按 netops `authz` 检查 `$USER` 的 `deploy`、`rollback` 权限。任何一台越权，整个计划都会被拒绝，不连接设备（报告 `denied`）。省略此参数时不做权限检查，行为与之前一致。
- 示例策略 `examples/authz.yml`。
- 共享运维内核更新至 netops v0.5.2。

## [0.3.1] - 2026-11-21

- 更新设备会话依赖至 net-connector v0.10.3，更新共享运维内核至 netops v0.4.0。

## [0.3.0] - 2026-11-20

### Added

- `fabric.yml` 可配置意图、基线、策略、快照及输出目录。凭据可保存在同目录、权限为 0600 的 `.env`。
- `fabric backup` 读取 `backup_snapshot` 指定的备份快照，可直接按区域检查或输出 JSON。

### Changed

- 采集、规划和执行记录默认写入 `~/.local/share/gatework/fabric/` 下的 `snapshots`、`plans` 和 `executions`。
- 配置了默认意图时，`collect --host` 只采集显式指定的主机。同时显式传入 `--intent` 可将意图中的交换机加入目标。

### Fixed

- `--help` 和 `help` 在配置无效或缺失时仍可显示帮助。

## [0.2.1] - 2026-11-20

- 将共享运维内核 netops 更新到 v0.3.0。

## [0.2.0] - 2026-11-20

- 公开编号池、设备模型和规划操作的区间类型现来自 `github.com/gatework/netops/set`。调用方同步更新 import；配置文件的区间编码保持一致。

## v0.1.0 - 2026-11-20

- 设备会话改由 net-connector v0.10.0 `connector` 提供（x/crypto/ssh 进程内实现，NX-OS 分页、检查点、回滚与保存由其 NX-OS 驱动负责）；`adapter/netconnector` 改写为基于 `connector.Dialer`，`vendors/nxos` 不再声明传输行为。
- 移除 go-netops 依赖：下发改用 netops `apply.Run` + `change.Plan.Precheck`，执行记录写入 netops `store/journal`，快照、计划与报告目录改用 netops `store.NewBatch`（批次命名不变）。
- `nxos.ParseConfig`/`Assemble`/`Observe` 接受 `context.Context`。

### 保障、治理、容量与性能

- `status` 健康评分、`locate` 终端定位、`topology` 布线审计、`capacity` 容量、`report deploy` 变更复盘。
- 新规则：互联缺失/错接/未声明/两端不一致、leaf–spine 全互联、重复 IP、资源池利用率、单机规模（共 42 条）。
- 计划影响分析与变更等级；变更记录（工单、申请人、原因、窗口、有效期）参与计划 ID；下发闸门；回滚后健康复核。
- 性能：并行与按需加载、去正则表解析；1000 台加载 2.18s→0.35s，检查 145ms→64ms；语料解析结果逐行一致。

### 日常业务与 underlay 纳管

- 业务命令：`network add/remove`、`tenant add`、`switch add/remove/day0`、`port assign/release/free`；自动分配 VLAN/VNI/loopback/互联子网/vPC 域/port-channel；写回意图并只规划本次变更。
- 意图支持 underlay（OSPF、互联、BGP 邻居设计、地址池）、vPC 对、互联链路、端口模板，以及单口 / LACP 聚合 / 跨 vPC 聚合接入。
- 按角色编译 day-0 状态并新增对应操作；`nxos.Day0` 生成出厂交换机完整配置，回读无差异。
- 存量导入推断 underlay：语料 4 个站点全部纳管、150 条互联，规划不改动任何 underlay 对象。
- 修复：意图 YAML 丢失全部地址/前缀字段（yaml.v3 的 omitempty 把 netip 值视为空），此前 `import` 生成的意图没有网关。

首个版本，取代 go-vxlan。

- 意图模型（区域、交换机、vPC、租户、网络、接入端口）与完整的一次性校验。
- NX-OS 解析：running-config 类型化模型；11 个 show 命令按表头解析，兼容 7.0(3)I7 到 10.6 的列宽与列序差异；未识别内容计入证据状态，不静默丢弃。
- 检查：34 条规则覆盖配置链、运行状态、vPC/VTEP、安全、跨 VTEP 一致性、意图与基线偏差、相对上次采集的退化。
- 存量纳管：从现网反推意图草稿，分歧以冲突列出。
- 基线：分层黄金配置、按多数值生成草稿、偏差检测与纠正计划。
- 规划：有序类型化操作、模拟收敛、地址重叠与 VNI 冲突安全检查、渲染与回读一致性；计划使用 go-netops 信封持久化，加载时重新渲染并校验摘要。
- 下发：分批（金丝雀、vPC 对拆批、border/spine 逐台），checkpoint、逐行写入、回读验证、健康门限，任一失败停止；显式原子回滚。
