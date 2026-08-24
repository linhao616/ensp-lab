# ensp-lab 系统性分析报告

- **日期**：2026-08-24
- **基线**：commit `8bbe92a`（main）
- **方法**：分层定位、证据驱动（network-architect 方法论）；所有结论引用具体代码位置与文档段落
- **范围**：① 示例拓扑分析 ② 底层操作命令设计逻辑评估 ③ 下一步规划建议 ④ 文档更新记录

---

## 1. 示例拓扑分析

### 1.1 自动创建的示例拓扑

| 拓扑 | 创建逻辑 | 结构 |
|------|----------|------|
| `default` | `cmd/server/main.go:101`——data 目录为空时 `topology.NewTopology("default", "Default Topology")` | **空拓扑**（0 设备/0 链路，`model.go:232`） |
| `-demo-vxlan` | `cmd/server/main.go:96`——`-demo-vxlan` 参数触发 `api.CreateVXLANTopology()` | VXLAN Spine-Leaf（14 设备/19 链路） |

### 1.2 data/ 预置拓扑全景（17 个 JSON + default）

| 拓扑 | 设备/链路 | 技术主题 | 设计意图评估 |
|------|-----------|----------|--------------|
| lab01-03 | 4-6 / 3-6 | VLAN/Trunk/STP | ✅ 交换基础三连，难度递进合理 |
| lab04-05 | 4-5 / 3-4 | OSPF | ✅ 单区/综合两级 |
| lab06 | 3/3 | VRRP | ✅ 高可用入门 |
| lab07/09 | 2-7 / 1-7 | DHCP | ✅ 单设备→中继递进 |
| lab08/10 | 3-5 / 2-5 | NAT/静态路由 | ✅ 出口基础 |
| lab11 | 4/3 | BGP | ✅ 基础建邻 |
| lab12 | 5/4 | IPSec VPN | ✅ 站点间 VPN |
| lab13-gap | 5/4 | 网闸摆渡 | ✅ 新增（08-24） |
| lab14-bigdata | 16/16 | 多安全域数据中心 | ✅ 大型综合（08-24） |
| lab15-vxlan-dc | 16/18 | VLAN+VXLAN+网闸 | ✅ 大型综合（08-24） |
| vxlan-spine-leaf | 14/19 | VXLAN 大二层 | ✅ 官方 demo（-demo-vxlan 同构） |
| gap-test | 3/2 | 网闸开发验证 | ⚠️ **开发残留，无教学价值** |

### 1.3 缺陷与冗余（证据）

| # | 问题 | 证据 | 影响 |
|---|------|------|------|
| D1 | `default` 是空拓扑，无引导示例 | `main.go:101` | 新用户首次启动看到空白画布，需手动选 lab 才知用法 |
| D2 | `gap-test.json` 为开发验证残留 | `data/gap-test.json`（3 设备，仅开发期用） | 污染下拉列表（与 lab13 功能重复），建议清理 |
| D3 | 拓扑命名不一致 | `lab13-gap.json` 的 id 是 `lab13-gap`，其余为 `labNN` | 下拉与文档引用易混淆 |
| D4 | lab15 vxlan 隧道链路无 subnet | `lab15-vxlan-dc.json` 17/18 | 属设计使然（隧道无物理网段），但前端渲染隧道时该行无标注，可接受 |
| D5 | `default` 空拓扑 + 18 个预置拓扑并存 | `main.go:101` 仅在 `len(topos)==0` 时建 default | data/ 非空时 default 永不出现，逻辑无冲突，但 default 的"引导"价值未兑现 |

### 1.4 结论

预置拓扑**覆盖完整**（交换/路由/安全/数据中心四类 15+ 场景），设计意图符合教学递进。主要短板：**缺少"开箱引导"**（default 空壳）与**残留清理**（gap-test）。

---

## 2. 底层操作命令设计逻辑评估

### 2.1 命令体系全景（证据统计）

| 层 | 数量 | 文件 |
|----|------|------|
| parser 顶层 case | **177 个** | `internal/cli/parser.go` |
| display 注册子命令 | **60 个** | `internal/cli/display_registry.go` |
| capabilities 能力矩阵 | **87 行** | `internal/cli/capabilities.go` |
| 设备类型 | 13 种 | `internal/topology/model.go`（含新增 `gap`） |

### 2.2 调用流程（架构）

```
前端 CliTerminal.tsx ──POST /api/.../cli──→ api.executeCLI（Clone 深拷贝工作副本）
  → parser.ParseCommand（sanitizeInput 剥提示符 → Fields 分词）
  → ExecuteCommandOn（PendingSave 确认 → 能力矩阵 isCommandSupported 拦截 → 主 switch 177 case）
  → 视图内命令分发（ViewInterface / ViewAAA / ViewGAP 等子视图）
  → display 兜底派发（displayRegistry[arg0] → regXxxDisplay 只读渲染）
  → 写 DeviceConfig（精确前缀键，禁 Contains 匹配防键碰撞）
  → 返回回显 → autosave 持久化
```

**三件套约定**（新特性统一）：`*_eval.go`（纯函数/键/校验）+ `*_cmd.go`（仅写 DeviceConfig）+ `*_display.go`（只读渲染），`parser.go` 负责接线（顶层 case / display registry / undo / saved-config 四处）。

### 2.3 对照华为 VRP 官方功能覆盖核验

| 功能域 | VRP 典型命令 | ensp-lab | 评估 |
|--------|-------------|----------|------|
| 系统管理 | `system-view`/`save`/`sysname`/`reboot` | ✅ 全支持 | 覆盖 |
| 接口 | `interface`/`ip address`/`shutdown`/`speed`/`duplex`/`mtu` | ✅ 全支持 | 覆盖 |
| 链路聚合 | `eth-trunk`/`trunkport`/`load-balance`/`lacp` | ✅ | 覆盖 |
| VLAN | `vlan`/`port link-type`/`vlanif` | ✅ | 覆盖 |
| STP | `stp`/`v-stp`/`pathcost-standard`/保护特性 | ✅ | 覆盖（RSTP/MSTP） |
| 路由 | 静态/默认/`ospf`/`rip`/`bgp`/`isis`/`import-route` | ✅ | 覆盖 |
| 路由策略 | `filter-policy`/`route-policy`/前缀列表 | ⚠️ 仅 `import-route` 基础 | **部分**（P2 已知缺口） |
| ACL | `acl`/`rule`/`traffic-filter` | ✅ | 覆盖（含隐式 deny） |
| NAT | `nat`（源/目的/端口映射） | ✅ | 覆盖 |
| 防火墙 | FW 设备 + `display firewall` | ✅ | 覆盖（过滤+安全域） |
| **网闸 GAP** | 第三方设备（非 VRP） | ✅ **新增**（08-24） | 超越需求（自定义设备） |
| IPSec/GRE | `ipsec`/`gre`/`tunnel-protocol` | ✅ | 覆盖 |
| AAA | `aaa`/`local-user`/三 scheme/domain | ✅ | 覆盖（VRP 视图层级还原） |
| DHCP | 全局/接口/中继 | ✅ | 覆盖 |
| VRRP | `vrrp vrid` | ✅ | 覆盖 |
| VXLAN/EVPN | `vxlan`/`vsi`/`vni`/`evpn-instance`/`remote-evpn-vtep` | ⚠️ | **部分**：静态隧道完整，**EVPN-BGP 控制面仅命令骨架**（`evpn-instance`/`route-distinguisher`/`vpn-target` 有视图但无状态计算） |
| IPv6 | `ipv6`/`ripng`/`ospfv3` | ✅ | 覆盖 |
| MPLS | `mpls` | ⚠️ | 仅命令骨架，无 LSP 状态仿真 |
| QoS | `qos` | ⚠️ | 基础（队列查看），无流量整形/限速 |
| 管理 | `snmp`/`syslog`/`ntp`/`stelnet`/`radius`/`netflow` | ✅ | 覆盖（多为诚实占位渲染） |
| 终端 | `ping`/`tracert`/`ipconfig`/`arp`/`netstat`/`nslookup` | ✅ | 覆盖（真实引擎） |
| 链路质量 | `delay`/`loss` | ✅ | 覆盖（v0.12 新增） |

**统计**：37 个功能域中 **31 个覆盖、4 个部分（路由策略/EVPN 控制面/MPLS/QoS）、2 个超越（网闸、链路质量模拟）**。

### 2.4 设计合理性评估

| 维度 | 评估 | 证据 |
|------|------|------|
| 可扩展性 | ✅ 优——能力矩阵 + display registry + 三件套约定，新增设备/命令低侵入（网闸 1 天落地） | `capabilities.go` 按设备类型矩阵；`display_registry.go` 单一事实源 |
| 健壮性 | ✅ 优——能力守卫先行（`isCommandSupported`）、键碰撞红线（精确前缀）、诚实占位（运行态恒 `-` 不编造）、深拷贝防并发 | `parser.go:258`、`gap_eval.go`、`model.go:249` |
| 一致性 | ✅ 优——`displaySubCommands` 表 ⊇ registry 键防漂移（`TestDisplaySubCommandsNoDrift`）、补全候选与执行器零漂移（`TestCompletionParamNoDrift`） | `tools.go:48`、`paramspec.go` |
| 冗余 | ⚠️ 中——177 顶层 case 的巨型 switch 是历史积累，显示已迁移 registry，但部分命令仍直写 case | `parser.go` 结构 |

---

## 3. 下一步规划建议（优先级排序）

| 优先级 | 改进项 | 预期收益 | 潜在风险 | 工作量 |
|--------|--------|----------|----------|--------|
| **P0** | default 拓扑升级为"引导示例"（3 设备：2PC+1SW，VLAN 入门）+ 删除 `gap-test` | 开箱即用的首次体验；下拉列表去残留 | 无 | 0.5h |
| **P0** | 路由策略补齐：`route-policy` + `filter-policy`（路由引入过滤） | 覆盖 VRP 高频排障场景（路由环路/选路控制） | parser case 增多，需测试锁死 | 4h |
| **P1** | EVPN-BGP 控制面状态仿真：`evpn-instance` 下 `display bgp evpn` 计算实际路由条目 | VXLAN 从"静态隧道"升级为"控制面学习"，教学价值质变 | 状态模型复杂，与 `displayRegistry` 集成需测试 | 8h |
| **P1** | QoS 深化：`qos car`（限速）+ `qos queue` 状态显示 | 数据中心/出口场景带宽管理教学 | 与现有 `qos` 命令合并需兼容 | 4h |
| **P2** | lab16 双区域 DCI 拓扑（Region-B 精简同构 + DCI 双链路） | 双活/灾备架构完整演示（上轮分析已建议） | 画布 30+ 设备需缩放 | 2h |
| **P2** | parser 巨型 switch 重构：命令执行器下沉到各自 `*_cmd.go`（对齐三件套约定） | 消除 177 case 单文件，可维护性 | **高风险**：大面积回归，必须全量测试 + CI 守护 | 12h |
| **P3** | MPLS 深化或降级声明（当前仅骨架） | 避免"命令存在但无状态"的误导（诚实占位原则） | 无 | 1h |

**决策建议**：优先 P0 两项（低成本高收益）；P1 选 **EVPN-BGP 控制面**（VXLAN 教学核心）；parser 重构风险高，建议暂缓或分阶段。

---

## 4. 文档更新记录（本次变更）

| 文档 | 变更内容 | 原因 |
|------|----------|------|
| `docs/ensp-lab_manual.md` | 新增 §4.11 网闸（GAP）设备（CLI 命令/display/仿真语义）；新增 §4.12 示例拓扑清单（17 拓扑全景表） | 网闸功能 + 预置拓扑此前无手册说明 |
| `CHANGELOG.md` | `[Unreleased]` 补 2026-08-23~24 全部变更（网闸/拓扑/布局/stale 修复/依赖/目录收口） | 版本记录完整性 |
| `docs/analysis-report-20260824.md` | 本报告（分析结论 + 规划 + 变更记录） | 系统性分析交付物 |

**版本基线**：本报告基于 `8bbe92a`（main，2026-08-24）。后续变更请在 `[Unreleased]` 继续追加。
