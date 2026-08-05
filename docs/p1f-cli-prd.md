# ensp-lab VRP CLI 仿真器 — CLI 命令广度扩展（P1-F）产品需求文档（简单 PRD）

> 文档类型：简单 PRD（默认类型，仅需求分析与文档，不含实现代码）
> 负责人：产品经理 许清楚（Xu）
> 范围：`internal/cli/`（CLI 仿真器），不牵动 sim 引擎核心（除非明确需要）
> 技术栈参考：Go 1.26.5 + Gin v1.12.0，单体前后端（`go:embed`），前端 `CliTerminal.tsx` 透传后端字符串（不改也能显示新命令回显）

---

## 0. 现状核查与范围更正（必读）

本 PRD 严格基于 `internal/cli/` 当前真实代码（2026-08 工作状态）撰写，而非预设前提。核查结论与原始任务 brief 的若干前提**不一致**，先在此更正，避免团队在已完成的工作上重复投入：

| # | 原始 brief 前提 | 代码真实状态 | 出处 |
|---|---|---|---|
| 0.1 | `display`/`dis` **未实现**，是 P0 最高优先级 | **已实现**，覆盖约 40 个子命令（`version`、`current-configuration`、`this`、`ip interface [brief]`、`ip routing-table`、`arp`、`nat`、`vlan`、`mac-address`、`interface [brief]`、`ospf`、`acl`、`m-lag`、`lldp`、`stp`、`vrrp`、`ipsec`、`snmp`、`syslog`、`ntp`、`ssh`、`vxlan`、`bgp`、`bfd`、`vrf`、`pbr`、`gre`、`qos`、`dot1x`、`radius`、`netflow`、`sysname`、`memory`、`cpu-usage`、`users`、`device`、`clock`、`temperature`、`startup`、`saved-configuration`、`history-command`、`eth-trunk`、`link-aggregation`、`port-vlan` 等） | `parser.go:2455` 起的 `case "display", "dis":` 大分支 |
| 0.2 | `bgp`、`rip` 等配置分支**不存在** | **已存在**（`bgp`/`peer` 进入 BGP 视图并建邻居；`rip` 启进程） | `parser.go:2106`、`parser.go:2141` |
| 0.3 | switch 末尾**无 default → 静默返回空串** | 末尾已有兜底 `return fmt.Sprintf("Error: unknown command '%s'", cmd.Command)`，未知命令**返回明确错误**而非空串 | `parser.go:3906` |
| 0.4 | `tracert`/`traceroute` 是**写死 2 跳的假路径** | **真实路径已在 API/handler 层完成**：`traceroute.go` 提供 `FormatEngineTraceroute`，`cli_handlers.go:66-71` 让 ping/tracert 走 `sim.Engine` 真实拓扑。仅 `parser.go:1139` 的兜底分支仍硬编码（非主路径，仅直连 `ExecuteCommandOn` 时命中） | `traceroute.go`、`internal/api/cli_handlers.go:66` |

**结论性更正 → P1-F 的真实 P0 目标**：

> 既然 `display` 家族与 `tracert` 真实化已落地，P1-F 的最高价值不再是"实现 display"，而是**消除能力矩阵（`capabilities.go`）与解析器分支（`parser.go`）之间的不一致**——即：凡是矩阵声明"支持"的命令，都必须有对应 parser 分支（至少返回有意义的仿真回显，而非 `unknown command`）；并补齐少量真实功能缺口（`undo` 系统视图、`display current-configuration` 的 VRP 风格化）。

### 0.5 真实缺口清单（capabilities 声明 but parser 无分支）

逐条比对 `capabilities.go` 与 `parser.go` 顶层 `case`，以下命令**声明支持却无 parser 分支**（能力校验通过后会落到 `parser.go:3906` 的 `unknown command` 错误，属"声明=支持"的不一致）：

| 命令 | 矩阵声明设备（capabilities.go） | 当前行为 | 性质 |
|---|---|---|---|
| `isis` | l3Devices（Router/L3Switch/Firewall/VTEP） | `Error: unknown command 'isis'` | 路由协议，学员常敲 |
| `quit-cli` | allDevices | `Error: unknown command 'quit-cli'` | 会话退出 |
| `vlanif` | l3SwitchOnly | `Error: unknown command 'vlanif'`（注：建 Vlanif 应走 `interface Vlanif <id>`，此声明疑似冗余） | 疑似冗余声明 |
| `port-security` | switchDevices | `Error: unknown command 'port-security'`（仅 `port security ...` 子命令存在） | 缺顶层分支 |
| `nslookup` | hostDevices（PC/Client/Server） | `Error: unknown command 'nslookup'` | 终端 DNS 查询 |
| `http` | serverDevices | `Error: unknown command 'http'` | 应用层服务 |
| `https` | serverDevices | `Error: unknown command 'https'` | 应用层服务 |
| `dns` | serverDevices | `Error: unknown command 'dns'`（注：终端侧 `ip dns` 已存在，此处是 Server 顶层命令） | 应用层服务 |
| `ftp` | serverDevices | `Error: unknown command 'ftp'` | 应用层服务 |

其他真实缺口：`undo` 仅支持接口视图（系统视图 `undo ospf/undo vlan/undo acl/...` 报 `Error: must be in interface view`，`parser.go:827`）；`display current-configuration` 目前是原始 `DeviceConfig` key-value 直排（`parser.go:2549`），非华为 VRP 配置快照风格（`display saved-configuration` 已用 `buildSavedConfigSnapshot` 的 VRP 风格，`parser.go:4032`）。

---

## 1. 产品目标

让 `capabilities.go` 能力矩阵中**声明的每一条命令**都能在对应设备上得到"已支持且有意义"的回显，从根本上消除"矩阵声明支持 → 终端却报 unknown command"的不一致；同时补齐 `undo` 系统视图与 `display current-configuration` 的 VRP 风格化，使学员在实验中敲出的 VRP 命令尽量都能得到合理反馈，提升实验教学真实感。

---

## 2. 用户故事

1. **作为网络实验学员**，我想在路由器上敲 `isis 1` 并进入 ISIS 视图，而不再看到 `Error: unknown command 'isis'`，以便练习 IS-IS 基础配置。
2. **作为终端实验学员**，我想在 PC 上敲 `nslookup www.example.com`，得到基于已配 DNS 的解析回显，而不是 unknown command。
3. **作为交换实验学员**，我想在接入交换机上敲 `port-security enable` 一键启用端口安全（顶层命令），而不是必须先 `interface` 进端口再 `port security`。
4. **作为学员**，我想在系统视图下 `undo ospf 1` / `undo vlan 10` 撤销配置并立即看到状态清除，而不是被提示"must be in interface view"。
5. **作为教师**，我希望 `display current-configuration` 输出与真实华为 VRP 一致的配置快照，便于对照讲义讲解，而不是一堆内部 key-value。

---

## 3. 需求池（按优先级 P0 / P1 / P2）

通用约束：纯增量、低风险；新增 parser 分支**不得破坏既有命令**；能力矩阵与 parser 分支保持一一对应（声明支持=有分支）。

### P0 — 一致性补齐（纯回显 / 最小状态，低风险，本期必做）

> 目标：让 §0.5 的 9 条"声明但无分支"命令在对应设备上返回**非 unknown command** 的有意义回显，与现有 `smtp`/`mpls`/`ppp` 的"启用即回显确认"模式一致。

| 命令 | 涉及设备类型 | 期望回显行为 | 依赖 CLIState 字段 | 验证方式 |
|---|---|---|---|---|
| `isis <process-id>` | Router/L3Switch/Firewall/VTEP | 进入 ISIS 视图（`Enter ISIS view, process <id>`），写 `state.DeviceConfig["isis:enabled"]="true"`、`isis:process-id`；本期先做最小启用（真实 network/引入放 P1） | 复用 `DeviceConfig`（新增 key，无需新 struct） | 对应设备敲 `isis 1` → 返回进入视图提示，不再 unknown |
| `quit-cli` | 全部 | 返回会话关闭提示（如 `Session closed.`）；语义等同退出 CLI | 无 | 任意设备敲 `quit-cli` → 返回提示而非 unknown |
| `port-security` | Switch/L3Switch/VTEP | 顶层分支：包一层现有 `port security` 子命令逻辑（enable/disable/max-mac-num/mac-address sticky），写 `DeviceConfig["interface:<cur>:port-security"]` | 复用 `port` 分支逻辑（`parser.go:672`） | 进接口后 `port-security enable` 或顶层 `port-security enable` 均生效 |
| `nslookup <host>` | PC/Client/Server | 复用 `state.HostDNS`：有 DNS 则返回模拟解析（`<host> can be resolved via <DNS>` + 一条模拟 A 记录）；无 DNS 返回"DNS server not configured"提示 | `HostDNS`（`state.go:47`） | PC 配 `ip dns` 后敲 `nslookup a.com` → 返回解析回显 |
| `vlanif <id>` | L3Switch/VTEP | 方案 A（推荐，低风险）：返回引导提示"Use 'interface Vlanif <id>' to create"；方案 B：直接调用 `interface Vlanif <id>` 逻辑。*待确认见 §5* | `Interfaces`（`state.go:79`） | 敲 `vlanif 10` → 返回引导或创建提示，不再 unknown |
| `http` / `https` | Server | `http enable` / `https enable` 写 `DeviceConfig["http:enabled"]` 并返回"HTTP(S) service enabled"，对齐 `smtp` 模式（`parser.go:2192`） | 复用 `DeviceConfig` | Server 上 `http enable` → 回显启用 |
| `dns` | Server | `dns enable` 写 `DeviceConfig["dns:enabled"]` 并返回确认（注意与终端 `ip dns` 区分，此为 Server 服务） | 复用 `DeviceConfig` | Server 上 `dns enable` → 回显启用 |
| `ftp` | Server | `ftp enable` 写 `DeviceConfig["ftp:enabled"]` 并返回确认 | 复用 `DeviceConfig` | Server 上 `ftp enable` → 回显启用 |

**P0 完成判据**：§0.5 全部 9 条命令在对应设备类型上敲入均返回"非 unknown command"的有意义回显；`capabilities.go` 与 `parser.go` 顶层 `case` 形成一一对应（无"声明支持却 unknown"）。

### P1 — 真实落地 & 功能补齐（本期建议做）

| 命令 / 能力 | 涉及设备 | 期望回显 / 行为 | 依赖字段 | 验证 |
|---|---|---|---|---|
| `isis` 真实配置 | Router/L3Switch/Firewall/VTEP | 支持 `isis <id>` → `network <type>`（level-1/level-2）→ `import-route`；新增 `ISISConfig` struct（`state.go`） | 新增 `ISIS *ISISConfig` | `isis 1` → `network level-2` → `display isis` 显示进程 |
| `display isis` | 同上 | 显示 ISIS 进程/网络类型/邻居数（参照 `display ospf` 风格，`parser.go:3183`） | `ISISConfig` | 配置后 `dis isis` 出表 |
| `undo` 系统视图扩展 | 全部（系统视图） | 支持 `undo ospf [<id>]`、`undo vlan <id>`、`undo acl <num/name>`、`undo stp`、`undo dhcp`、`undo bgp`、`undo ipv6`、`undo <feature>` 等；反向清理对应 state（置 disabled / 删 map 项）。仅在不支持的子命令上报 `undo '<x>' is not supported` | 各 feature 现有字段 | 系统视图 `undo ospf 1` → OSPF.Enabled=false 且 `dis ospf` 显示 Not configured |
| `display current-configuration` 风格化 | 全部 | 复用 `buildSavedConfigSnapshot()`（`parser.go:4032`）生成 VRP 风格配置快照，替换当前原始 key-value 直排（`parser.go:2549`）；与 `display saved-configuration` 视觉一致 | `SavedConfig` 生成逻辑 | `dis cur` 输出与华为 VRP 一致，非内部 key |
| `display diagnostic-information` | 全部 | 汇总输出：version + device + cpu-usage + memory + 关键协议状态（组合现有 helper） | 现有 display 子函数 | 单命令拿到设备"体检报告" |
| `display bgp peer` | Router/L3Switch/Firewall/VTEP | 在现有 `display bgp`（概要，`parser.go:3614`）基础上新增 `peer` 子命令，逐邻居显示 IP/RemoteAS/状态/EBGP | `BGP.Neighbors` | `dis bgp peer` 出邻居明细表 |
| `tracert` parser 兜底清理 | PC/Client/Server（及路由设备） | `parser.go:1139` 硬编码 2 跳分支改为：优先走 `sim.Engine` 真实路径（复用 `traceroute.go` 的 `FormatEngineTraceroute`）；仅在无引擎上下文时返回提示。*注：API 主路径已真实化，此为一致性清理* | `sim.TracerouteResult` | 直连 `ExecuteCommandOn` 也走真实路径，不再恒 2 跳 |

### P2 — 进阶 / 低优先（本期可选，或下期）

| 项 | 说明 |
|---|---|
| 命令 Tab 补全提示 | 前端 `CliTerminal.tsx` 改进：基于 `capabilities.go` 当前设备可用命令做前缀补全与 `?` 提示（后端可新增 `SuggestCommands(deviceType, prefix)` helper） |
| 历史命令上下键 | 复用 `state.History`（`state.go:82` / `FormatHistoryCommand`），前端绑定 ↑/↓ 回放 |
| `display ip vpn-instance` / `display ip vpn-target` | VRF 相关详细展示（依赖 `VRF` map，`state.go:67`） |
| EVPN/VXLAN 详细 display | `display vxlan vni` / `display evpn` 明细（现有 `display vxlan` 仅概要） |
| 终端 `tracert` 主机侧 | PC 侧 `tracert` 走本地路由表（`buildHostIPRoute`）模拟 |

---

## 4. UI / 交互设计稿（CLI 场景 = 回显格式规范）

CLI 无 GUI，此处规范"新增命令的输出样例"，并标注复用 / 需新写的格式化函数。整体对齐现有 `buildHostIfconfig`、`displayIPInterface`、`buildSavedConfigSnapshot` 的风格（等宽列、分隔线、华为术语）。

### 4.1 `nslookup`（终端，复用 `HostDNS`）
```
<host>?
Server:  <HostDNS>
Address:  <HostDNS>#53

Non-authoritative answer:
Name:    <host>
Address:  192.0.2.10
```
- 复用：`state.HostDNS`（`state.go:47`）；若无 DNS 则 `*** Can't find server name ... DNS server not configured.`
- 新写：一个小 `formatNslookup(host, dns)` helper（放入 `host.go`）。

### 4.2 `port-security`（顶层，复用 `port` 分支逻辑）
```
Port security enabled on <interface>
```
- 复用：`parser.go:672` 的 `port security` 子命令分支；顶层 `case "port-security"` 直接委托该逻辑（先确保 `CurrentView==ViewInterface`）。

### 4.3 Server 应用层 `http`/`https`/`dns`/`ftp`（对齐 `smtp`，`parser.go:2192`）
```
HTTP service enabled
```
- 复用：`smtp` 模式；写 `DeviceConfig["<proto>:enabled"]="true"`。

### 4.4 `isis`（最小启用，P0）+ 真实配置（P1）
P0 启用：
```
Enter ISIS view, process 1
```
P1 配置后 `display isis`：
```
ISIS Process 1
  Network Type: level-2
  State: Running
  Neighbors: 0
```
- 复用：`display ospf`（`parser.go:3183`）的列风格；P1 新增 `ISISConfig` struct。

### 4.5 `display current-configuration`（P1 风格化，复用快照）
```
sysname Core-R1
#
interface GigabitEthernet0/0/1
 ip address 192.168.1.1 255.255.255.0
 description Link to Core
#
...
!configuration saved at 2026-08-04 10:00:00
```
- 复用：`buildSavedConfigSnapshot()`（`parser.go:4032`）；将 `display current-configuration` 分支从原始 key-value 直排改为调用该快照生成器（运行配置即快照，无需先 save）。

### 4.6 `undo` 系统视图（P1）回显样例
```
<undo ospf 1>      -> OSPF process 1 removed
<undo vlan 10>     -> VLAN 10 removed
<undo acl 2000>    -> ACL 2000 removed
<undo xyz>         -> Error: undo 'xyz' is not supported
```
- 新写：在 `parser.go:823` 的 `undo` 分支内，对 `ViewSystem` 增加子命令分发（清理对应 feature state）。

---

## 5. 待确认问题（交主理人 / 用户拍板）

1. **`tracert` 真实路径是否纳入本期？** 现状：API 主路径已真实化（`traceroute.go` + `cli_handlers.go`），仅 `parser.go:1139` 兜底硬编码。建议本期**仅做兜底清理**（P1 末项），不重复造轮子；是否要将直连 `ExecuteCommandOn` 路径也强制接引擎，请拍板。
2. **`isis` 本期定位？** 建议 P0 先做"最小启用分支（回显 + 进视图）"，真实 `network`/`import-route` 放 P1。是否要一步到位做真实配置？
3. **`undo` 系统视图扩展范围？** 建议先覆盖高频：`ospf`/`vlan`/`acl`/`stp`/`dhcp`/`bgp`/`ipv6`；是否要覆盖全部 feature（含 `vxlan`/`m-lag`/`dfs-group` 等）？
4. **`vlanif` 声明如何处理？** 方案 A（推荐）：补一个"引导提示"分支；方案 B：直接当冗余声明从 `capabilities.go` 移除（因为建 Vlanif 应走 `interface Vlanif`）。请拍板。
5. **能力矩阵一致性约束是否本期强制执行？** 即：所有"声明支持"的命令都必须有 parser 分支（P0 已覆盖 §0.5 的 9 条）；反之，本期不打算实现的命令是否应从矩阵移除，避免误导？建议**采纳该约束**作为收口标准。
6. **`display current-configuration` 风格化是否必做？** 当前虽"能用"但输出是内部 key，教学体验差。建议 P1 必做（低风险，复用已有快照函数）。

---

## 6. 验收标准

### P0 完成定义（必须）
- [ ] §0.5 列出的 9 条"声明但无分支"命令（`isis`、`quit-cli`、`vlanif`、`port-security`、`nslookup`、`http`、`https`、`dns`、`ftp`）在各自矩阵声明的设备类型上敲入，**均返回非 `unknown command` 的有意义回显**。
- [ ] `capabilities.go` 顶层声明命令 与 `parser.go` 顶层 `case` **一一对应**（无"声明支持却 unknown"）。
- [ ] 未知命令仍返回 `Error: unknown command '<cmd>'`（`parser.go:3906`），**行为保持不变**。
- [ ] 既有命令（`display` 家族、`bgp`/`rip`、`tracert` 真实路径等）回显**无回归**（建议加 parser 分支单元测试覆盖新增命令）。

### P1 完成定义（建议）
- [ ] `isis` 真实配置 + `display isis` 可用。
- [ ] `undo` 在系统视图下可撤销 `ospf`/`vlan`/`acl`/`stp`/`dhcp`/`bgp`/`ipv6` 等并清理 state。
- [ ] `display current-configuration` 输出为华为 VRP 风格配置快照（复用 `buildSavedConfigSnapshot`）。
- [ ] `display diagnostic-information`、`display bgp peer` 可用。
- [ ] `tracert` parser 兜底分支接入真实引擎或返回合理提示。

### 非目标（本期不做）
- 不改动 sim 引擎核心（tracert 真实化已由既有 handler 完成）。
- 不新增前端组件（P2 的 Tab 补全 / 历史上下键为下期）。
- 不实现 MPLS LSP 转发、PPP 链路协商等深层协议仿真（本期 `mpls`/`ppp`/`pppoe` 仅维持现有"启用回显"语义）。
