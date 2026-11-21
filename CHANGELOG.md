# Changelog

## Unreleased

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
