# Changelog

## Unreleased

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
