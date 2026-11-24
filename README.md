# fabric

多区域 Cisco Nexus VXLAN EVPN Fabric 控制器：采集与检查 fabric，按声明式意图与基线规划配置，分批次安全下发，并支持一键原子回滚。取代 go-vxlan（见 [ADR 0001](docs/adr/0001-replace-go-vxlan.md)）。

## 能做什么

| 能力 | 命令 | 说明 |
|---|---|---|
| 采集 | `fabric collect` | 每台交换机一个目录：running-config 与 11 个 show 命令的原始回显（与现有快照格式兼容） |
| 交换机详情 | `fabric switch show <名称>` | 健康评分、上行/下联与互联明细、OSPF 与 BGP EVPN 会话（按对端名称）、NVE、vPC、聚合、本机发现；长表只列异常，`-v` 显示全部 |
| 维护影响 | `fabric impact <名称>` | 重启/升级/下线前评估：spine 会让哪些 VTEP 失联或失去 EVPN 会话；leaf 的单归终端（端口、描述、IP）与由 vPC 对端接管的双归终端，对端能否承载；结论与维护前步骤 |
| 拓扑 | `fabric topology` | spine–VTEP 拓扑图：两端端口、速率、故障层与原因、冗余与速率对称、vPC 对；`--switch` 画单机邻居树（fabric 互联、vPC peer-link、外部设备、管理网）；`--detail` 列故障两端详情、原因与处置建议；每条互联按 物理 → CDP → 配置 → OSPF 逐层验证 |
| 检查 | `fabric check` | 42 条规则：VNI 配置链、运行状态、vPC/VTEP、安全、跨 VTEP 一致性、意图/基线偏差、相对上次采集的退化 |
| 存量纳管 | `fabric import` | 从现网反推意图草稿，分歧列为冲突，不猜测 |
| 基线 | `fabric baseline derive` | 按现网多数值生成黄金配置草稿（默认 → 角色 → 区域 → 区域内角色） |
| 规划 | `fabric plan` | 意图（+基线）→ 每台交换机有序操作 → 模拟收敛 + 安全检查 → NX-OS 命令与回滚脚本 → 分批 |
| 下发 | `fabric deploy` | 默认预览；`--apply --confirm <计划ID>` 才写入。checkpoint → 逐行写入 → 回读验证 → 健康门限；任一失败停止后续批次 |
| 回滚 | `fabric rollback` | `rollback running-config checkpoint <名> atomic` |
| 互斥与授权 | `--authz`、`--lease-ttl` | 下发与回滚前取跨工具设备租约（与 wireless、firemon 共享）；可选按角色和站点授权，见 `examples/authz.yml` |

所有视图先给结论再给明细，末尾给出下一步命令。`check` 支持 `--switch`、`--severity` 过滤；`status` 列出每台交换机的主要问题。交换机名、区域名与级别支持 Tab 补全（`fabric completion zsh|bash|fish`），补全读取 `--intent` 或 `fabric.yml` 中的意图。

## 日常业务（类似无线控制器的集中管理）

```sh
# 新增分布式网关：所有 VTEP 无差别下发 VLAN + L2VNI + anycast 网关，主机就近接入、本地转发
fabric network add --intent intent.yaml --snapshot snap/ --region dc1 --name db --tenant PROD --gateway 10.10.60.1/24
# 新 leaf 上线（自动分配 loopback、互联 /31、vPC 域），导出 day-0 配置
fabric switch add  --intent intent.yaml --snapshot snap/ --region dc1 --name LEF31 --mgmt 10.0.0.31 --role leaf \
                   --vpc-peer LEF30 --uplink Ethernet1/49=SPN01:Ethernet1/31 --uplink Ethernet1/50=SPN02:Ethernet1/31
fabric switch day0 --intent intent.yaml --name LEF31 -o LEF31.cfg
# 服务器双归 vPC 接入（po/vPC 号自动分配，所需网络自动扩展）
fabric port free   --intent intent.yaml --snapshot snap/ --switch LEF01
fabric port assign --intent intent.yaml --snapshot snap/ --vpc LEF01 --member LEF01:Eth1/10 --member LEF02:Eth1/10 \
                   --profile server-trunk --description DB-01
```

每条命令：校验（意图 + 现网）→ 自动分配 → 更新意图（保留 .bak）→ 只为这次变更生成计划（不夹带存量偏差）→ `fabric deploy` 分批下发。

## 保障与治理

```sh
fabric status   --snapshot snap/ --intent intent.yaml           # 全网健康评分（证据不全显示未知）
fabric locate   10.1.44.23 --snapshot snap/ --intent intent.yaml # 终端在哪台交换机哪个口
fabric topology --snapshot snap/ --intent intent.yaml           # 布线审计 + leaf×spine 矩阵
fabric capacity --snapshot snap/ --intent intent.yaml           # 资源池与单机规模
fabric plan ... --ticket CHG-123 --reason "新增 DB 网段" --window 22:00-06:00
fabric deploy --plan DIR --apply --confirm ID [--ack-risk] [--emergency --emergency-reason ...]
fabric report deploy --journal executions/<批次> --format markdown  # 变更复盘
```

计划自动标注影响（VLAN、租户、端口、vPC、underlay、受影响在线终端）与变更等级（标准/常规/重大）；重大变更逐台下发并需 `--ack-risk`；过期、窗口外、缺工单的计划在连接设备前即被拒绝。

## 快速开始

```sh
export NC_DEVICE_USERNAME=... NC_DEVICE_PASSWORD=...
fabric collect --host 198.51.100.11 --host 198.51.100.12 -o snapshots
fabric import  --snapshot snapshots/<批次> -o intent.yaml        # 审阅冲突，改区域名
fabric baseline derive --snapshot snapshots/<批次> --intent intent.yaml -o baseline.yaml
fabric check   --snapshot snapshots/<批次> --intent intent.yaml --baseline baseline.yaml
# 在 intent.yaml 中新增网络后：
fabric plan    --snapshot snapshots/<批次> --intent intent.yaml -o plans
fabric deploy  --plan plans/<计划目录>                          # 预览
fabric deploy  --plan plans/<计划目录> --apply --confirm <计划ID>
```

常用文件写进 `~/.gatework/fabric.yml` 后可省略对应参数：`intent`、`baseline`、`policy`、`snapshot`、`known_hosts` 是同名参数的默认值，采集快照、计划与执行记录默认写到 `~/.local/share/gatework/fabric/{snapshots,plans,executions}`；凭据放在同目录的 `.env`（0600）。配置按 `$FABRIC_CONFIG` → `./fabric.yml` → `~/.gatework/fabric.yml` 查找，示例见 [examples/fabric.yml](examples/fabric.yml)。

将备份快照目录配置为 `backup_snapshot` 后，运行 `fabric backup` 即可检查备份；`--json`、`--region` 等选项与 `fabric check` 相同。

意图示例见 [testdata/intent.yaml](testdata/intent.yaml)，整体设计见 [docs/design.md](docs/design.md)，术语见 [GLOSSARY.md](GLOSSARY.md)。

## 退出码

`0` 成功；`1` 证据不完整（部分规则未运行或采集失败）；`2` 错误；`3` 存在发现、意图草稿未通过校验或下发未全部完成；`130` 中断。

## 开发

遵循 [gatework Go 工程规范](https://github.com/gatework/go-standards/blob/main/STANDARD.md)，仓库特定约定见 [AGENTS.md](AGENTS.md)。

```sh
make check                                   # 格式、静态分析、架构边界、race 测试、fuzz、漏洞扫描
FABRIC_CORPUS=/path/to/snapshots make corpus # 在私有快照语料上跑闭环（只输出汇总）
```
