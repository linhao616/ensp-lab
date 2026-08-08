# ensp-lab P2 第六项：DHCP 中继 DHCP Relay（华为 VRP 实训课程 27）增量 PRD

> 文档类型：增量产品需求文档（PRD，**简单模式**，结构对齐 `docs/p2-lag-prd.md` / `docs/p2-stp-prd.md` / `docs/p2-vrrp-prd.md`）
> 关联：`docs/p2-lag-prd.md`（链路聚合增量 PRD，需求池 + AC 结构基准）、`internal/cli/parser.go` / `state.go` / `capabilities.go` / `lag_eval.go` / `vrrp_eval.go`（**已逐条核查代码基线，见文末 file:line 证据索引**）
> 作者：产品经理 许清楚（Xu）
> 语言：中文
> 说明：本期**不做竞品/市场分析**（按主理人指示），仅输出产品目标 / 用户故事 / 需求池 / 验收标准 / 展示设计稿 / 待确认问题。

---

## 0. 项目信息

- **Language**：中文
- **Programming Language**：Go 1.26.5（本期工作集中在 `internal/cli`；前端 React/Vite 本期无变更）
- **Project Name**：`p2_dhcp_relay`
- **原始需求复述**：在 P2 已交付 NAT（课程 38）、端口安全（课程 49）、VRRP（课程 60/61）、STP/RSTP/MSTP（课程 55/56/57）、链路聚合 Eth-Trunk（课程 63）之后，为华为 eNSP VRP 仿真器落地 **DHCP 中继（DHCP Relay，课程 27）** 的增量实现：在既有「DHCP 地址池能力」之上，补齐**完全缺失**的中继命令族（`dhcp select relay` / `dhcp relay server-ip` / `dhcp relay information enable|strategy` / `dhcp relay source-ip`），全部经 `DeviceConfig` 单一事实源持久化，新增 `display dhcp relay { interface <if> | all }` 忠实展示，并对所有运行态转发指标施加**诚实占位**。

> **深度边界先验结论（务必先读 §6 拍板项）**：DHCP 中继的真实价值在于**跨网段转发 DHCP 报文**——中继代理接收客户端广播的 DISCOVER，改写 `giaddr` 后单播给对端 DHCP 服务器，再把 OFFER/ACK 回传客户端。本工具是**单机 VRP CLI 仿真器，无真实 DHCP 服务器、无真实报文转发引擎、无 UDP 67/68 收发**。因此本期严格划界：
>
> - **配置面 100% 真实**：`select relay` 模式、`server-ip` 列表、option82 开关与策略、`source-ip` —— 必须真实落 `DeviceConfig` 键、真实可 `display`、真实可 `undo`、真实可 `save`→`reload` 复现。这部分**不允许打折**。
> - **运行面 100% 诚实占位**：「已转发 DISCOVER 数 / 收到 OFFER 数 / 收到 ACK 数 / 对端服务器可达性 / 客户端分配到的地址」等 —— **一律显示 `-` 并附诚实注记，严禁编造数字、严禁随机数、严禁伪造服务器响应**。这是本项目的核心价值观红线（对照 LAG 的 Partner 块处置、VRRP 的跨设备心跳处置）。
>
> 代码基线里 **DHCP 中继完全不存在**（`grep -in "relay" internal/cli/*.go` 无任何中继实现），但既有 DHCP 分支存在**两处必须一并整改的架构缺陷**（见 §3「已有」表 P0-1 / P0-2 关联项）：① `dhcp select` 被错误地限制在**系统视图**，而官方 VRP 该命令是**接口视图**命令；② `dhcp select` 的结果写入 `state.DHCPSelectMode`（全局单值），该字段**全仓库只写不读**（死状态）、**不持久化**、且**无法表达"每接口一种模式"**的真实语义。中继必须挂在接口上，所以这两处是本期的前置阻塞项，属重构对象而非另起炉灶。

---

## 1. 产品目标

在严守 P2「CLIState 层纯函数、单一事实源 `DeviceConfig`、诚实占位、零改动 `sim` 引擎、零新增第三方依赖」架构基线的前提下，把 DHCP 中继从**完全缺失**补齐为一条学员可完整走通的实验链路：

1. **命令面对齐官方 VRP**：学员能在接口视图按课程 27 的真实命令序列敲 `dhcp select relay` → `dhcp relay server-ip 10.1.1.1` → `dhcp relay information enable`，把三层接口配成中继代理；命令形态、报错文案、`undo` 语义均对齐真机，学员的肌肉记忆可平移到 eNSP / 真实设备。
2. **配置真实落地且持久**：修复既有 `dhcp select` 的「系统视图错位 + 全局死状态」缺陷，把接口 DHCP 模式与全部中继参数迁移到 `DeviceConfig["interface:<iface>:dhcp-select"]` / `DeviceConfig["interface:<iface>:dhcp-relay:*"]` 单一事实源；`display dhcp relay` 与 `display current-configuration` 忠实复现，`save`→`reload` 后配置不丢。
3. **展示忠实、边界诚实**：新增 `display dhcp relay { interface <if> | all }`，配置态字段（模式、server-ip 列表、option82、source-ip）**如实展示**；转发计数、服务器可达性、地址分配结果等仿真无法产出的运行态字段**一律 `-` + `dhcpRelaySimNote()` 注记**，让学员清楚知道"哪些是我配对了、哪些是仿真给不了的"——绝不用假数字换取观感。

---

## 2. 用户故事

1. **作为学习 DHCP 跨网段部署的网络学员（课程 27 主线）**：As a 学员，I want 在路由器接口视图依次敲 `dhcp select relay` 和 `dhcp relay server-ip 10.1.1.1`，so that 该接口成为 DHCP 中继代理，我能用 `display dhcp relay interface GigabitEthernet0/0/1` 核对中继模式与目标服务器地址，验证自己的配置顺序和参数没记错。
2. **作为配置双服务器冗余的学员**：As a 学员，I want 在同一接口上连敲两条 `dhcp relay server-ip 10.1.1.1` / `dhcp relay server-ip 10.1.1.2`，so that 我能看到 server-ip 列表按配置顺序累积为两条（而非后者覆盖前者），理解中继支持多服务器冗余；再用 `undo dhcp relay server-ip 10.1.1.1` 精确摘掉其中一个。
3. **作为理解 Option82 的学员**：As a 学员，I want 敲 `dhcp relay information enable` 并配 `dhcp relay information strategy replace`，so that `display dhcp relay` 中 Option82 一栏由 `Disabled` 变为 `Enabled`、策略显示 `replace`，我能对照课程理解中继信息选项的插入与处理策略。
4. **作为关注持久化的学员**：As a 学员，I want `save` 后 `reload` 设备，so that 中继配置（select relay、server-ip 列表、option82、source-ip）仍完整保留，`display dhcp relay` 与 `display current-configuration` 均能复现，而不必重配。
5. **作为踩坑排障的学员**：As a 学员，I want 在没敲 `dhcp enable` 或没敲 `dhcp select relay` 就配 `dhcp relay server-ip` 时收到**明确、可读的错误提示**（而不是静默成功或含糊报错），so that 我立刻知道少了哪一步；同时我希望 `display dhcp relay all` 里那些仿真给不了的转发计数**老老实实显示 `-`**，而不是给我一串假数字让我误以为报文真的转发了。

---

## 3. 需求池

> 共 **27 条**：P0 **12 条**、P1 **8 条**、P2 **7 条**（另列「已有」基线 8 条，属重构对象 / 复用基线，非新需求）。

### 已有（本期重构 / 复用，非另起炉灶）

| 标记 | 内容 | 位置 |
|---|---|---|
| [已有·可复用] | 系统视图 `dhcp enable` / `dhcp disable`：置 `state.DHCP.Enabled`，中继依赖此全局开关作为前置条件 | `parser.go:1562-1573` |
| [已有·可复用] | 系统视图 `dhcp pool <name>` 进入 `ViewDHCPPool` 视图（`network` / `gateway-list` / `dns-list` / `lease` / `excluded-ip-address`），提示符 `[<sys>-dhcp-pool-<name>]` | `parser.go:1477-1552`、`parser.go:5098-5099`、`state.go:27` |
| [已有·**不合规·本期必改**] | `dhcp select global\|interface`：① 被 `if state.CurrentView != ViewSystem { return "Error: must be in system view" }` 限制在**系统视图**，而官方 VRP `dhcp select` 是**接口视图**命令；② 结果写入 `state.DHCPSelectMode`（**全局单值**，无法表达每接口一种模式）；③ 该字段**全仓库只写不读**（`grep -rn "DHCPSelectMode"` 仅命中 `parser.go:1596` 写入与 `state.go:84` 声明），是**死状态**；④ **不写 `DeviceConfig` → 不持久化**，`save`→`reload` 后丢失 | `parser.go:1554-1556`、`parser.go:1588-1597`、`state.go:84` |
| [已有·缺口] | `DHCPConfig{Enabled, Pools}`：**无任何中继字段**（无 relay 模式、无 server-ip、无 option82、无 source-ip） | `state.go:140-143` |
| [已有·缺口] | **无 `display dhcp` 系列命令**（`display dhcp relay` / `display dhcp` 均不存在）；现仅有 `display ip pool [interface vlanif <id>]` 展示地址池，且其 `for name, pool := range state.DHCP.Pools` 为 **map 遍历、输出顺序随机** | `parser.go:2620-2690` |
| [已有·缺口] | `undo dhcp`（`applyUndoSystemFeature`）：**仅**置 `state.DHCP.Enabled = false`，无任何 relay 相关 undo 分支 | `parser.go:5031-5035` |
| [已有·缺口] | 持久化：`buildSavedConfigSnapshot` 仅输出一行 ` dhcp enable`（`state.DHCP.Enabled` 为真时），**地址池与 select 模式均不落盘**，中继自然更无 | `parser.go:4946-4965`、`parser.go:5337` |
| [已有·基线可复用] | 纯函数评估器 + 诚实占位范式：`aclSimNote()`/`natSimNote()`（`acl_eval.go:493-507`）、`portSecSimNote()`（`portsec_eval.go:239`）、`vrrpSimNote()`（`vrrp_eval.go:397`）、`stpSimNote()`（`stp_eval.go:290`）、`lagSimNote()`（`lag_eval.go:786`）；`net.ParseIP` 校验范例（`parser.go:4539` VRRP virtual-ip）；确定性排序 `sortedInterfaceNames`（`stp_eval.go:304`）；能力矩阵 `"dhcp": switchAndL3()`（`capabilities.go:64`，含 Router/L3Switch/Switch/Firewall/AC/AP/VTEP） | 见各处 |

### P0（本期核心 · 接口级事实源修复 + 中继命令族 + display 忠实 + 诚实占位）

**A. 事实源与架构缺陷修复（前置阻塞项）**

- **[P0-1 `dhcp select` 迁移到接口视图 + 接口级事实源]**：官方 VRP `dhcp select { global | interface | relay }` 是**接口视图**命令。本期把 `case "dhcp"` 的系统视图硬守卫改为**按视图分派**：`ViewSystem` 继续处理 `enable`/`disable`/`pool`；`ViewInterface` 新增处理 `select` 与 `relay` 子命令族。接口 DHCP 模式统一写 `DeviceConfig["interface:<iface>:dhcp-select"]` ∈ {`global`,`interface`,`relay`}，命名与 STP 的 `interface:<iface>:stp:<field>`、LAG 的 `interface:<iface>:lag:<field>` 一致。**废弃全局死字段 `state.DHCPSelectMode`**（`state.go:84`），迁移策略见 §6 #2。
- **[P0-2 中继配置键命名空间]**〔**2026-08-09 按设计文档定稿键名对齐**：原稿 `dhcp:select` / `dhcp:relay:<field>` 已统一为 `dhcp-select` / `dhcp-relay:<field>`，以设计文档 §7.1 键表为准〕：接口 DHCP 模式的**唯一事实源** = `DeviceConfig["interface:<iface>:dhcp-select"]`；中继参数全部走 `DeviceConfig["interface:<iface>:dhcp-relay:<field>"]`，`<field>` ∈ `server-ips`（**逗号分隔有序列表**，保序、去重）、`option82`（`true`）、`option82-strategy`（`drop|keep|replace`，P1）、`source-ip`（P1）。**不得落 `dhcp-relay:mode` 键**——模式已由 `dhcp-select` 承载，再落即双写事实源（LAG `:members` 双写同类坑），且与拍板 #3 的级联清理直接冲突。**不得在 `CLIState` / `DHCPConfig` 上新增内嵌 relay 结构体**（对照 LAG 的 AC9 静态断言：`state.go` 不新增 LAG 结构体）。

**B. 中继配置命令（对齐官方 VRP 课程 27）**

- **[P0-3 `dhcp select relay`（接口视图）]**：写 `interface:<iface>:dhcp-select` = `relay`；成功静默或 VRP 风格回显；重复执行幂等（不报错、不产生重复键）。`undo dhcp select relay` 清除该键（接口回落到无 DHCP 模式）。
- **[P0-4 `dhcp relay server-ip <ipv4>`（接口视图）· 单个与多个]**：追加写入 `interface:<iface>:dhcp-relay:server-ips` 列表尾部，**保序**（先配先列）、**去重**（重复地址不重复追加，返回幂等成功或明确提示）；同一接口支持配置多个服务器地址（上限见 P1-5）。
- **[P0-5 IPv4 合法性校验（`net.ParseIP`）]**：`server-ip` 参数必须通过 `net.ParseIP(x) != nil` **且** `.To4() != nil`（拒绝 IPv6），失败 → `Error: Invalid IP address <x>`。校验范式对照 `parser.go:4539`（VRRP virtual-ip）。特殊地址（`0.0.0.0` / 广播 / 组播 / 环回）的处置归 P2-4。
- **[P0-6 `dhcp relay information enable`（接口视图）]**：写 `interface:<iface>:dhcp-relay:option82` = `true`，开启 Option82 中继信息选项插入（**仅配置态与展示，不做真实报文插入**，附诚实注记）；`undo dhcp relay information enable` 清键，回落缺省 `Disabled`。
- **[P0-7 前置条件守卫与拒错（关键教学点）]**：
  - 全局未 `dhcp enable` 时执行任何 `dhcp select` / `dhcp relay ...` → **软提示不阻断、键照写**（**拍板 #6**）：在成功回显前附 `Info: DHCP is not enabled. Run 'dhcp enable' in system view to activate this configuration.`。〔**2026-08-09 修订**：原稿为 `Error:` 硬拒绝，已被拍板 #6 推翻，以本条为准。〕
  - **未 `dhcp select relay` 就配 `dhcp relay server-ip`** → **明确报错且不写任何键**（**拍板 #1 采纳 PM 建议 (a)**）：`Error: Please run 'dhcp select relay' on this interface first.`（不做隐式自动关联，避免学员跳过关键步骤而不自知）。
  - 接口已是 `select global` / `select interface` 时配 relay 参数 → 三态互斥**级联清理**（**拍板 #3 采纳 (a) 并将该项从 P2-1 上提至 P0**）：写入 `global`/`interface` 时删除该接口 `dhcp-relay:` 精确前缀的全部键，杜绝幽灵配置（注意勿误删 `dhcp-pool` 绑定键）。
  - 非接口视图执行 `dhcp select` / `dhcp relay ...` → `Error: must be in interface view`。
  - 参数缺失 → VRP 风格 usage 提示（如 `Error: usage: dhcp relay server-ip <ip-address>`）。

**C. 展示与诚实占位**

- **[P0-8 新增 `display dhcp relay { interface <interface-name> | all }`]**：新增 display 子命令分派（现状**完全不存在**）。`interface <if>` 输出单接口详情块；`all` 输出全部已配中继接口的汇总表。字段与列定义见 §4。**输出必须确定性**（接口按名称升序、server-ip 按配置序），杜绝 `display ip pool` 那样的 map 随机遍历。无任何中继接口时 → `Info: No DHCP relay interface configured.`；指定接口未配中继 → 明确提示而非空输出。
- **[P0-9 `dhcpRelaySimNote()` 诚实占位（CRITICAL 红线）]**：新增注记函数，口径严格对齐 `lagSimNote()` / `vrrpSimNote()` / `stpSimNote()`（读 `sim.EngineModeName()`，lite / full 两态）：
  - lite → 「（DHCP 中继为配置态模拟（lite 引擎），无真实 DHCP 报文转发与服务器交互，转发统计不可用）」
  - full → 「（DHCP 中继为配置态模拟，无真实报文转发引擎）」
  所有 `display dhcp relay*` 输出末尾必须附加。**输出中不得出现任何伪造的转发计数、服务器可达状态、租约分配结果**——此类字段一律 `-`（见 §4 占位列标注）。
- **[P0-10 `display current-configuration` 新增 relay 段 + save→reload 贯通]**：新增 `buildSavedDHCPRelayInterfaceConfig(state, iface)`，在接口块内按 VRP 顺序输出 ` dhcp select relay` / ` dhcp relay server-ip <ip>`（多行，每地址一行）/ ` dhcp relay information enable` / ` dhcp relay information strategy <x>` / ` dhcp relay source-ip <ip>`；挂入 `buildSavedConfigSnapshot`（`parser.go:5337`），复用 LAG/VRRP 的接口块挂载范式（`parser.go:5401-5409`）。中继键随既有 `SerializeToDeviceConfigData` ↔ `LoadFromDeviceConfigData` 自动往返，**零新增持久化代码**。

**D. 纯函数与守卫**

- **[P0-11 新增 `internal/cli/dhcp_relay_eval.go` 纯函数评估器]**：范式严格对照 `lag_eval.go` / `vrrp_eval.go` —— **无副作用、不修改 `sim` 引擎、不 import `internal/protocol`、零新增第三方依赖、可单测**；仅读 `state.DeviceConfig` 派生只读视图。建议契约：
  - `collectDHCPRelayInterfaces(state) []string`：扫描 `interface:*:dhcp-select` = `relay`，返回**按接口名升序**的去重列表（复用 `stp_eval.go:304` 排序口径）。
  - `parseRelayServerIPs(raw string) []string`：解析逗号分隔列表，保序去重、过滤空串。
  - `EvaluateDHCPRelay(state, iface) DHCPRelayResult`：返回 `Interface / Mode / ServerIPs[] / Option82Enabled / Option82Strategy / SourceIP / Status`。其中 `Status` 仅由**本地可判定条件**派生（见 §6 #4），不臆造服务器可达性。
  - `dhcpRelaySimNote() string`：诚实占位注记（P0-9）。
- **[P0-12 能力矩阵与视图守卫]**：`capabilities.go:64` 已有 `"dhcp": switchAndL3()`，覆盖 Router/L3Switch/Switch/Firewall/AC/AP/VTEP。中继是**三层特性**，二层 Switch 上配 relay 无意义，收窄粒度见 §6 #5（**PM 建议：`dhcp` 顶层保持 `switchAndL3()` 不动，仅在 `select relay` / `relay *` 子命令内部做三层设备守卫**，避免误伤既有二层设备的 `dhcp enable`/`dhcp pool` 用例）。PC / Server 执行 → 沿用 `parser.go:245` `isCommandSupported` 能力拒绝（**仅限 `select relay` / `relay *` 配置命令**——在 PC / Server / 二层 Switch 上拒绝；`display dhcp relay` 为只读命令、任意设备可读、空态放行）。

### P1（增强真实语义 · 建议默认纳入）

- **[P1-1 `dhcp relay source-ip <ipv4>`（接口视图）]**：写 `interface:<iface>:dhcp-relay:source-ip`，指定中继报文源地址（单值，后配覆盖先配）；同样走 P0-5 的 IPv4 校验；`undo dhcp relay source-ip` 清键；`display` 中如实展示，未配时显示 `-`（缺省行为在真机是取接口主 IP，仿真无转发故**不臆造推导值**，见 §6 #4）。
- **[P1-2 `dhcp relay information strategy { drop | keep | replace }`（接口视图）]**：写 `interface:<iface>:dhcp-relay:option82-strategy`；取值**严格枚举校验**，非法值 → `Error: unrecognized command`；**缺省 `replace`**（拍板 #6 已定）；**未 `information enable` 时配 strategy → 允许 + `Info:` 提示**（拍板 #6，避免顺序强耦合，不做硬拒绝）；`display` 中未配时**显示生效缺省值 `replace` 而非 `-`**（`-` 语义是"无数据源/不可产出"，缺省值是确定可知的事实），但 `current-configuration` **不输出缺省行**（VRP 只落差异值，对齐 `buildSavedLAGInterfaceConfig` 口径）。
- **[P1-3 多接口并存与隔离]**：同一设备上多个接口可各自独立配置中继（不同 server-ip 列表、不同 option82 开关），互不干扰；`display dhcp relay interface <if>` 只反映该接口配置；键前缀天然隔离，需测试覆盖。
- **[P1-4 `display dhcp relay all` 汇总表]**：一屏纵览所有中继接口（接口名 / 模式 / server-ip 数量与首地址 / Option82 / Source IP / 转发统计占位列），列定义见 §4；接口按名称升序，输出确定性。
- **[P1-5 server-ip 数量上限与超限拒错]**：单接口 server-ip 上限（**PM 建议 8**，对齐 VRP 常见规格，最终以 §6 #6 拍板为准）；超限 → `Error: The number of DHCP relay server IP addresses exceeds the upper limit (8).`
- **[P1-6 `undo dhcp relay server-ip <ip>` 精确删除单项]**：从列表中**精确摘除指定地址**并保持其余顺序不变；地址不存在 → `Error: The specified server IP address does not exist.`；删至空列表时清除该键（不留空串键）。另支持 `undo dhcp relay server-ip`（不带参数，清空全部）——语义歧义由 §6 #6 一并确认。
- **[P1-7 `display dhcp relay`（无参数）缺省行为]**：**PM 建议等价于 `all`**（学员最常敲的形态），避免"敲了没参数就报 usage"的挫败感；若主理人偏好严格对齐真机（要求必须带 `interface`/`all`），退化为 usage 提示。随 §6 #6 拍板。
- **[P1-8 Vlanif / 子接口支持]**：`Vlanif<id>` 等三层逻辑接口同样可配中继（L3Switch 典型组网：Vlanif 做中继、上联到集中式 DHCP 服务器）；接口名解析复用既有 `parser.go:310-372` 逻辑，需测试覆盖 `Vlanif10` 场景。

### P2（边界收敛 / 诚实边界 / 可选增强）

- **[P2-1 `select global` / `select interface` / `select relay` 三态互斥校验]**：同一接口三种模式**互斥**（真机一个接口只能一种 DHCP 服务模式）。候选：(a) 后配覆盖先配 + 清理旧模式残留的 relay 键；(b) 已配其他模式时明确报错要求先 `undo`。**PM 建议 (a) 覆盖 + 提示**（更贴近真机，学员切换模式不被卡住），并在切到非 relay 模式时**同步清理 `dhcp-relay:*` 全部键**避免残留幽灵配置。随 §6 #3 拍板。
- **[P2-2 系统视图旧 `dhcp select` 的兼容处置]**：P0-1 迁移后，系统视图下再敲 `dhcp select global|interface` 如何处理？候选：(a) 直接报错引导到接口视图（`Error: Please run 'dhcp select' in interface view.`）；(b) 保留旧行为兼容。**PM 建议 (a)**（真机就是接口视图命令，兼容一个错误行为没有教学价值；且现状该命令写入的是只写不读的死字段，破坏风险为零）。随 §6 #2 拍板。
- **[P2-3 提示文案语言与一致性]**：错误/提示文案统一采用与既有代码一致的**英文 `Error:` 前缀**风格（对照 LAG/VRRP/STP 的 `Error: ...`），诚实占位注记沿用**中文括注**（对照 `lagSimNote()` / `stpSimNote()`）。本期**不引入 i18n 框架**，仅保证文案风格自洽、可 grep、可断言。
- **[P2-4 特殊 IPv4 地址边界处置]**：`0.0.0.0` / `255.255.255.255` / 组播段 `224.0.0.0/4` / 环回段 `127.0.0.0/8` 作为 `server-ip` 或 `source-ip` 时是否拒绝？**PM 建议：拒绝并给明确文案**（`Error: <x> is not a valid DHCP server address.`），因真机也不接受；但属边界打磨，可延后。
- **[P2-5 `dhcp server group` + `dhcp relay server-select <group>`]**：部分 VRP 版本支持服务器组抽象（系统视图建组、接口引用）。**本期明确 out-of-scope**，命令面先只做直配 `server-ip`；如未来纳入，键设计为 `dhcp:server-group:<name>:server-ip`。
- **[P2-6 relay 与本机 DHCP 池的冲突提示]**：设备既配了 `dhcp pool` 又在接口上 `select relay` 时，给出信息性提示（`Info:` 级，不阻断）说明该接口的请求将被中继而非由本机池应答。属体验增强，非必须。
- **[P2-7 前端无变更]**：DHCP 中继仅在 CLI 文本体现，本期**不新增 API 字段、不做拓扑图形化中继链路指示**（与 NAT / 端口安全 / VRRP / STP / LAG 一致）。

---

## 4. UI / 交互设计稿（CLI 回显与 display 输出，纯文本）

### 4.1 配置命令序列回显（课程 27 主线操作流）

```
<R1> system-view
[R1] dhcp enable
[R1] interface GigabitEthernet0/0/1
[R1-GigabitEthernet0/0/1] ip address 192.168.10.254 255.255.255.0
[R1-GigabitEthernet0/0/1] dhcp select relay
[R1-GigabitEthernet0/0/1] dhcp relay server-ip 10.1.1.1
[R1-GigabitEthernet0/0/1] dhcp relay server-ip 10.1.1.2
[R1-GigabitEthernet0/0/1] dhcp relay information enable
[R1-GigabitEthernet0/0/1] dhcp relay information strategy replace
[R1-GigabitEthernet0/0/1] quit
```

> VRP 风格：配置成功**静默或规范短回显**，失败才 `Error:`。不得出现 `Relay server added OK!` 这类自造欢快文案（对照 LAG P0-18 对 `Port added to Eth-Trunk 1` 的整改）。

**典型拒错回显（硬拒绝，`Error:` 且不写任何键）**：

```
[R1-GigabitEthernet0/0/1] dhcp relay server-ip 10.1.1.1
Error: Please run 'dhcp select relay' on this interface first.        ← P0-7（拍板 #1，键不写入）

[R1-GigabitEthernet0/0/1] dhcp relay server-ip 300.1.1.1
Error: Invalid IP address 300.1.1.1                                    ← P0-5

[R1] dhcp select relay
Error: Please run 'dhcp select' in interface view.                     ← P2-2（拍板 #2）
```

**软提示回显（`Info:` 不阻断、键照写）**〔**2026-08-09 按拍板 #6 修订**，原稿此处为 `Error:` 硬拒绝，已作废〕：

```
<R1> system-view
[R1] interface GigabitEthernet0/0/1                    ← 注意：尚未敲过 dhcp enable
[R1-GigabitEthernet0/0/1] dhcp select relay
Info: DHCP is not enabled. Run 'dhcp enable' in system view to activate this configuration.
                                                        ← 配置已写入（dhcp-select=relay），仅未激活
[R1-GigabitEthernet0/0/1] quit
[R1] dhcp enable                                        ← 补敲后原配置直接生效，无需重配
```

### 4.2 `display dhcp relay interface GigabitEthernet0/0/1`（单接口详情块）

```
DHCP relay information of interface GigabitEthernet0/0/1
--------------------------------------------------------------
  Relay mode              : relay
  Server IP address(es)   : 10.1.1.1
                            10.1.1.2
  Option82 (information)  : Enabled
  Option82 strategy       : replace
  Source IP address       : -
  Interface status        : Up
  --- Forwarding statistics ---
  DHCP packets forwarded  : -
  DISCOVER forwarded      : -
  OFFER received          : -
  REQUEST forwarded       : -
  ACK received            : -
  Server reachability     : -
--------------------------------------------------------------
（DHCP 中继为配置态模拟（lite 引擎），无真实 DHCP 报文转发与服务器交互，转发统计不可用）
```

**列/字段真实性标注表**（架构师据此实现，测试据此断言）：

| 字段 | 数据来源 | 真实性 | 未配置时 |
|---|---|---|---|
| `Relay mode` | `interface:<if>:dhcp-select` | **真实**（配置态） | 不显示该接口 |
| `Server IP address(es)` | `interface:<if>:dhcp-relay:server-ips` | **真实**（配置态，保序） | `-` |
| `Option82 (information)` | `...:dhcp-relay:option82` | **真实**（配置态） | `Disabled` |
| `Option82 strategy` | `...:dhcp-relay:option82-strategy` | **真实**（配置态） | **`replace`（生效缺省值，非 `-`）**〔拍板 #6〕 |
| `Source IP address` | `...:dhcp-relay:source-ip` | **真实**（配置态） | `-`（**不推导接口主 IP**，见 §6 #4） |
| `Interface status` | `interface:<if>:status` | **真实**（本地可判定，复用 `shutdown`/`undo shutdown`） | `Up` |
| `DHCP packets forwarded` | — | 🔴 **诚实占位 `-`** | `-` |
| `DISCOVER forwarded` | — | 🔴 **诚实占位 `-`** | `-` |
| `OFFER received` | — | 🔴 **诚实占位 `-`** | `-` |
| `REQUEST forwarded` | — | 🔴 **诚实占位 `-`** | `-` |
| `ACK received` | — | 🔴 **诚实占位 `-`** | `-` |
| `Server reachability` | — | 🔴 **诚实占位 `-`**（无 ICMP/无转发引擎，**严禁**输出 `Reachable`/`Up`） | `-` |

> 🔴 = 仿真环境无真实数据源，**恒为 `-`，严禁编造数字、随机数或伪造服务器响应**。整个 `--- Forwarding statistics ---` 分组是否保留（还是整块略去）见 §6 #4 拍板。

### 4.3 `display dhcp relay all`（汇总表）

```
DHCP relay configuration summary
------------------------------------------------------------------------------------
Interface                 Mode    Servers  Primary Server   Option82  Source IP  Fwd
------------------------------------------------------------------------------------
GigabitEthernet0/0/1      relay   2        10.1.1.1         Enabled   -          -
GigabitEthernet0/0/2      relay   1        10.2.2.1         Disabled  10.2.2.254 -
Vlanif10                  relay   1        172.16.0.1       Enabled   -          -
------------------------------------------------------------------------------------
Total: 3 relay interface(s)
（DHCP 中继为配置态模拟（lite 引擎），无真实 DHCP 报文转发与服务器交互，转发统计不可用）
```

- 列含义：`Servers` = server-ip 条数（真实）；`Primary Server` = 列表**首个**地址（真实，保序取首）；`Fwd` = 转发报文总数，🔴 **恒 `-`**。
- **接口按名称升序排序**（确定性，禁止 map 随机遍历）。
- 空态：
  ```
  Info: No DHCP relay interface configured.
  ```

### 4.4 `display current-configuration` 中的中继段（P0-10）

```
#
interface GigabitEthernet0/0/1
 ip address 192.168.10.254 255.255.255.0
 dhcp select relay
 dhcp relay server-ip 10.1.1.1
 dhcp relay server-ip 10.1.1.2
 dhcp relay information enable
 dhcp relay information strategy replace
#
```

> 输出顺序固定：`select` → `server-ip`（按配置序，每地址独立一行）→ `information enable` → `information strategy` → `source-ip`。缺省值不冗余输出（对齐 VRP 惯例）。

### 4.5 前端

**本期无变更**。DHCP 中继仅在 CLI 终端文本体现。

---

## 5. 验收标准（AC1–AC12，每条可用自动化测试证明，非恒真断言）

- **AC1（接口视图分派 + 事实源写入）**：在 Router / L3Switch 上 `dhcp enable` → `interface GigabitEthernet0/0/1` → `dhcp select relay`，断言 `DeviceConfig["interface:GigabitEthernet0/0/1:dhcp-select"] == "relay"`；再执行 `dhcp relay server-ip 10.1.1.1` + `dhcp relay information enable`，断言 `DeviceConfig["interface:GigabitEthernet0/0/1:dhcp-relay:server-ips"] == "10.1.1.1"` 且 `...:dhcp-relay:option82" == "true"`。**反向断言：`state.DHCPSelectMode` 未被写入（或字段已删除）**，证明 P0-1 死状态已废弃。

- **AC2（save → reload 持久化贯通）**：完成 AC1 配置并追加第二个 server-ip 与 strategy 后执行 `save`，经 `SerializeToDeviceConfigData` → `LoadFromDeviceConfigData` 往返，reload 后断言：① `DeviceConfig` 中 `interface:*:dhcp-select` 与 `interface:*:dhcp-relay:*` 键集与 reload 前**逐键完全一致**（含 `server-ips` 列表的**顺序**）；② `display dhcp relay interface GigabitEthernet0/0/1` 完整复现两个 server-ip、Option82、strategy；③ **`display current-configuration` 复现 §4.4 全部 5 行**（`dhcp select relay` / 两行 `dhcp relay server-ip` / `information enable` / `information strategy replace`）。

- **AC3（多 server-ip 保序、去重、上限）**：依次配 `10.1.1.1` → `10.1.1.2` → `10.1.1.3`，断言列表为 `10.1.1.1,10.1.1.2,10.1.1.3`（**严格保序**，非 map/set 打乱）；重复配 `10.1.1.2` 后列表长度仍为 3 且顺序不变（去重）；连续配至第 9 个地址 → `Error: ...exceeds the upper limit (8)`（P1-5，上限值以 §6 #6 拍板为准）。

- **AC4（IPv4 合法性校验，P0-5）**：`dhcp relay server-ip 300.1.1.1` / `10.1.1` / `abc` / `10.1.1.1/24` / `2001:db8::1`（IPv6）**全部**返回含 `Invalid IP address` 的 `Error:`，且断言 `DeviceConfig["interface:<if>:dhcp-relay:server-ips"]` 键**未被写入或未被污染**（非法地址不得追加进列表、不得留下空串键）；合法地址 `10.1.1.1` / `172.16.0.254` / `192.168.1.1` 全部成功。校验实现须使用 `net.ParseIP(x) != nil && x.To4() != nil`（对照 `parser.go:4539`）。

- **AC5（前置条件守卫与拒错，P0-7）**：逐条断言：① **〔2026-08-09 按拍板 #6 修订〕** 未 `dhcp enable` 时 `dhcp select relay` → **软提示不阻断**：断言输出含 `Info:` 与 `DHCP is not enabled` **且** 断言 `interface:<if>:dhcp-select` 键**已写入**为 `relay`（证明未被阻断）；后续补敲 `dhcp enable` 后配置直接生效、无需重配。**原稿"返回 `Error:` 硬拒绝"的断言作废。** ② 已 `dhcp enable` 但未 `dhcp select relay` 时 `dhcp relay server-ip 10.1.1.1` → 含 `dhcp select relay` 引导文案的 `Error:`，**且断言 `dhcp-relay:server-ips` 键未写入**（证明未静默成功）；③ 系统视图执行 `dhcp select relay` → 含 `interface view`；④ 用户视图执行 `dhcp relay server-ip 10.1.1.1` → 视图拒绝；⑤ `dhcp relay server-ip`（缺参）→ 含 `usage:`。**每条断言具体子串，不得用"返回非空"这类恒真断言。**

- **AC6（`display dhcp relay interface <if>` 忠实展示）**：配置 2 个 server-ip + Option82 enable + strategy replace 后，输出**逐字段断言**：包含 `Relay mode` 行值为 `relay`、两行 server-ip 且**顺序为配置序**、`Option82` 行值为 `Enabled`、`Option82 strategy` 行值为 `replace`、`Source IP address` 行值为 `-`（未配 P1-1 时）；未配中继的接口 → 明确提示而非空串；不存在的接口名 → 明确 `Error:`。

- **AC7（`display dhcp relay all` 汇总 + 输出确定性）**：3 个接口（`GigabitEthernet0/0/1` / `GigabitEthernet0/0/2` / `Vlanif10`）各配中继后，`display dhcp relay all` 输出 3 行数据行，`Servers` 列计数正确、`Primary Server` 为各自列表首地址；**接口按名称升序**；**同一状态连续调用 10 次输出字节级完全一致**（证明消除了 map 随机遍历，对照 LAG AC5 与现状 `display ip pool` 的缺陷）；无中继接口时输出 `No DHCP relay interface configured`。

- **AC8（诚实占位 · CRITICAL 红线）**：lite 引擎下所有 `display dhcp relay*` 输出**均含** `dhcpRelaySimNote()` 的「无真实 DHCP 报文转发与服务器交互」注记；用**正则断言输出中不存在任何伪造运行态数字**——具体：`DHCP packets forwarded` / `DISCOVER forwarded` / `OFFER received` / `REQUEST forwarded` / `ACK received` / `Server reachability` 六个字段的值**必须恒为 `-`**，断言其**不匹配** `\d+` 且**不匹配** `Reachable|Unreachable|Up|Down|Active`；汇总表 `Fwd` 列同样恒 `-`。**该 AC 失败即视为违反项目核心价值观，不得以"观感更好"为由放行。**

- **AC9（undo 语义完整，P0-3 / P0-6 / P1-6）**：① `undo dhcp relay server-ip 10.1.1.1`（列表含 3 项）后列表变为 `10.1.1.2,10.1.1.3`，**其余顺序不变**；删除不存在的地址 → 含 `does not exist` 的 `Error:`；删至最后一项后该键**被清除而非留空串**（断言 `_, ok := DeviceConfig[key]; ok == false`）；② `undo dhcp relay information enable` 后 `display` 中 Option82 回落 `Disabled`；③ `undo dhcp select relay` 后该接口从 `display dhcp relay all` 列表中消失。

- **AC10（能力矩阵与设备守卫，P0-12）〔2026-08-09 按拍板 #5 修订：拆分为 10a/10b，配置命令守卫、display 只读放行〕**：
  - **AC10a（配置命令按设备类型守卫）**：PC / Server / 二层 Switch 上执行 `dhcp select relay` / `dhcp relay server-ip 10.1.1.1` / `dhcp relay information enable` 均**拒绝**（设备类型守卫，目标设备集 = `l3Devices()`：Router / L3Switch / Firewall / VTEP，复用 `capabilities.go:174`，**不新增不重定义**）；Router / L3Switch / Firewall 正常放行。
  - **AC10b（`display dhcp relay` 只读、任意设备可读）**：PC / Server 上执行 `display dhcp relay all` / `display dhcp relay`（无参，等价 `all`）**不得返回能力拒绝**，而应**正常放行**并输出空态 `Info: No DHCP relay interface configured.`；断言输出**不含** `is not supported on`。**原稿"PC / Server 上 display 返回能力拒绝"的断言作废。**
  - **AC10c（回归断言）**：本期改动未误伤二层 Switch 上既有的 `dhcp enable` / `dhcp disable` / `dhcp pool <name>` / `ViewDHCPPool` 全部子命令行为，**行为逐字不变**（对照 `parser.go:1477-1552` 既有用例）；断言 `capabilities.go` **零改动**（顶层 `"dhcp": switchAndL3()` 保持原样）。

- **AC11（纯函数无副作用 / 架构基线合规）**：`EvaluateDHCPRelay` / `collectDHCPRelayInterfaces` / `parseRelayServerIPs` / `dhcpRelaySimNote` 单测证明——不修改 `sim` 引擎、不写 `state`、**不 import `internal/protocol`**、零新增第三方依赖、连续两次调用结果一致且**不改写任何 `DeviceConfig` 键**（调用前后对 `DeviceConfig` 做 deep-equal 断言）；**静态断言 `state.go` 的 `DHCPConfig` 未新增 relay 内嵌结构体字段**（`grep -n "Relay" internal/cli/state.go` 无命中），对照 LAG AC9 口径。

- **AC12（多接口隔离 + Vlanif 支持，P1-3 / P1-8）**：`GigabitEthernet0/0/1` 配 `10.1.1.1` + Option82 enable，`Vlanif10` 配 `172.16.0.1` + Option82 未开；断言两接口的 `display dhcp relay interface <if>` **各自独立正确**、互不串键；`display dhcp relay all` 同时列出两者且 Option82 列分别为 `Enabled` / `Disabled`；对 `Vlanif10` 执行 `undo dhcp select relay` 后 `GigabitEthernet0/0/1` 配置**完全不受影响**。

---

## 6. 待确认问题（交架构师 / 主理人拍板，按重要性排序）

> ### ✅ 2026-08-09 更新：本节 6 项**已全部由主理人拍板闭合**，以下为定稿结论。原候选方案与 PM 论证保留作决策留痕，**实现与验收一律以「拍板结论」为准**。
>
> | # | 拍板结论 | 与 PM 建议 | 影响 |
> |---|---|---|---|
> | 1 | **(a) 明确报错拒绝且不写任何键** —— `Error: Please run 'dhcp select relay' on this interface first.` | ✅ 采纳 | P0-7 第二条、AC5 ② |
> | 2 | **(a) 直接迁接口视图**，**删除 `state.DHCPSelectMode` 字段**（`state.go` 仅删 1 行），系统视图报错引导 | ✅ 采纳 | P0-1、P2-2、AC1 反向断言 |
> | 3 | **(a) 后配覆盖 + 级联清理 `interface:<if>:dhcp-relay:` 精确前缀全部键**；**该项从 P2-1 上提至 P0** | ✅ 采纳（含上提） | P0-7 第三条 |
> | 4 | **(a) 保留 `Forwarding statistics` 分组、值全 `-` + 注记**；`Source IP` 未配**不推导接口主 IP，恒 `-`** | ✅ 采纳 | §4.2、AC8 |
> | 5 | **(a) 顶层 `"dhcp"` 保持 `switchAndL3()` 不动**（`capabilities.go` 零改动），仅在 `case "dhcp"` 内部对 `select relay` / `relay *` 做三层守卫（设备集 = `l3Devices()`，复用 `capabilities.go:174`）；**`display dhcp relay` 为只读、任意设备可读** | ✅ 采纳（display 只读为拍板补充） | P0-12、**AC10 拆分为 10a/10b/10c** |
> | 6 | strategy 缺省 `replace`（未配时 display 显示缺省值而非 `-`）；未 `information enable` 配 strategy → 允许 + `Info:`；**未 `dhcp enable` 配 relay → 软提示 `Info:` 不阻断、键照写**；server-ip 上限 **8**；`undo dhcp relay server-ip` 无参 = 清空全部；`display dhcp relay` 无参 = 等价 `all` | ⚠️ 部分推翻（"未 `dhcp enable` 硬拒绝"改为软提示） | P0-7 第一条、**AC5 ① 已改写**、P1-2/P1-5/P1-6/P1-7 |
>
> **两处原稿断言已作废**（架构师 §9.2 C1/C2 提出，本次同步改写）：① AC5 ① 的 `Error:` 硬拒绝 → 改为断言 `Info:` **且**键已写入；② AC10 的"PC/Server 上 display 返回能力拒绝" → 改为 display 放行输出空态。
> **另同步一处键名对齐**：全文 `dhcp:select` / `dhcp:relay:<field>` → `dhcp-select` / `dhcp-relay:<field>`（`server-ips` / `option82` / `option82-strategy` / `source-ip`），以设计文档 §7.1 键表为准。

1. **`dhcp relay server-ip` 与 `dhcp select relay` 的先后依赖（核心 · 直接决定 P0-7 与 AC5）**：学员未先敲 `dhcp select relay` 就配 `dhcp relay server-ip` 时如何处理？候选——
   - **(a) 明确报错拒绝（PM 建议）**：`Error: Please run 'dhcp select relay' on this interface first.`，且**不写入任何键**。理由：真机 VRP 就是这个依赖顺序，课程 27 的操作流也是「先 select 后 server-ip」；报错能让学员真正记住依赖关系，教学价值最高；实现最简单，状态机也最干净（不存在"配了 server-ip 但没生效"的中间态）。
   - (b) 自动关联：静默把该接口置为 `select relay` 再写 server-ip。理由：更"顺手"；但会让学员跳过关键概念，且与真机行为不符，属于用便利性换正确性。
   - (c) 允许写入但标注未生效（`display` 中 Mode 显示 `-` + 提示）。会引入"配置存在但模式缺失"的中间态，`display`/`current-configuration`/`undo` 三处都要额外处理，复杂度最高。
   **PM 建议 (a)**。**请拍板 (a)/(b)/(c) 与最终错误文案。**

2. **`dhcp select` 从系统视图迁移到接口视图的破坏性（核心 · 决定 P0-1 / P2-2 力度）**：现状 `dhcp select global|interface` 在**系统视图**（`parser.go:1554-1556` 硬守卫），写入的 `state.DHCPSelectMode` 经全仓 grep 确认**只写不读、不持久化**（`parser.go:1596` 唯一写点、`state.go:84` 唯一声明）——即**该功能当前完全没有实际效果**。候选——
   - **(a) 直接迁移到接口视图，系统视图下报错引导（PM 建议）**：删除 `state.DHCPSelectMode` 字段，`select` 全部走 `interface:<if>:dhcp-select` 键。理由：现状是死代码、无任何测试覆盖（`grep -i "dhcp select" internal/cli/*_test.go` 为空）、无历史配置包袱，破坏风险≈0；且这是唯一能承载 relay 的正确架构。有 LAG 重构「直接改键名不做旧键迁移」与 STP「直接移除 `state.STP`」的先例。
   - (b) 双视图兼容：系统视图保留旧行为，接口视图新增。会长期背一个错误语义。
   **PM 建议 (a)**，并同步删除 `state.go:84` 的 `DHCPSelectMode` 字段。**请拍板是否允许删字段、系统视图是否报错引导。**

3. **`select global` / `select interface` / `select relay` 的互斥粒度（决定 P2-1，可能需上提到 P0）**：同一接口配了 `select relay` 后再敲 `select global`，如何处理已有的 `dhcp-relay:*` 键？候选——
   - **(a) 后配覆盖 + 级联清理 relay 键（PM 建议）**：切到非 relay 模式时**一并删除**该接口所有 `dhcp-relay:*` 键。理由：避免"模式是 global 但 display 里还挂着 server-ip"的幽灵配置（这正是 LAG 的 `Bridge-Aggregation` 幽灵组缺陷同类问题，AC10 级别的坑）。
   - (b) 后配覆盖但保留 relay 键（切回 relay 时配置还在）。用户体验上"记住上次配置"友好，但会产生 `current-configuration` 里输出无效行的问题。
   - (c) 已配模式时拒绝切换，要求先 `undo`。
   **PM 建议 (a)**。另请一并确认：**互斥校验应归 P0 还是 P2**？PM 倾向**上提到 P0**（幽灵配置是数据正确性问题，不是打磨项），但按主理人原始分档要求暂列 P2，**请拍板是否上提**。

4. **`display dhcp relay` 的运行态字段：整块保留占位、还是整块略去（决定 §4.2 输出形态与 AC8 断言面）**：`Forwarding statistics` 分组的 6 个字段本仿真**全部无法产出**。候选——
   - **(a) 保留分组、全部值填 `-` + 注记（PM 建议）**：优点——学员能看到"真机这里会有哪些指标"，具备教学参考价值，且 `-` 配合注记语义明确；缺点——一整块 `-` 观感略空。
   - (b) 整块略去，只输出配置态字段 + 一行注记「（仿真环境无真实转发引擎，转发统计不可用）」：输出最干净，绝无被误读风险；但学员失去"真机有哪些指标"的认知。
   - (c) 填算得出的近似值（**PM 强烈反对**，等同编造，触犯项目红线）。
   **PM 建议 (a)**，与 LAG 拍板 #3 的处置思路保持家族一致（LAG 当时选了"整列略去不可产出的列"即 (a) 变体，若团队希望完全对齐 LAG 口径则应选 (b)）。**请拍板 (a)/(b)，这直接决定 AC8 的断言写法。** 附带确认：`Source IP address` 未配时是否推导为接口主 IP？**PM 建议不推导，恒 `-`**（真机缺省行为依赖转发时的出接口选路，本仿真无转发，推导即臆造）。

5. **能力矩阵收窄粒度（决定 P0-12 与 AC10）**：现状 `"dhcp": switchAndL3()`（`capabilities.go:64`）覆盖 Router / L3Switch / **Switch** / Firewall / AC / AP / VTEP。DHCP 中继是**三层特性**，二层 Switch 上配 relay 无实际意义。候选——
   - **(a) 顶层 `dhcp` 保持不变，仅在 `select relay` / `relay *` 子命令内部做三层设备守卫（PM 建议）**：理由——顶层收窄会**误伤**二层 Switch 上既有的 `dhcp enable` / `dhcp pool` 用例（现有代码路径 `parser.go:1477-1552`），风险不可控；子命令内守卫是最小改动、零回归。对照 LAG P0-17「`mode` 不入顶层能力矩阵，改在 `case "mode"` 内部守卫」的同款处置。
   - (b) 新增独立能力键（如 `"dhcp-relay": l3Devices()`）——需确认 `isCommandSupported` 是否按首 token 匹配（`dhcp` 是首 token，`relay` 是参数），若是则此方案不可行。
   **PM 建议 (a)**。**请拍板，并确认 `l3Devices()` 范围（Router / L3Switch / Firewall / VTEP）是否即为 relay 的目标设备集。**

6. **一批缺省值与规格数字（打包拍板，决定 P1-2 / P1-5 / P1-6 / P1-7）**：以下均为需要一个确定答案的小项，建议一次性拍板——
   - `dhcp relay information strategy` **缺省值**：PM 建议 `replace`（VRP 常见缺省）；未 `information enable` 时配 strategy 是**拒绝**还是**允许但标注未生效**（PM 建议：允许 + `Info:` 提示，避免顺序强耦合）。
   - 单接口 `server-ip` **数量上限**：PM 建议 **8**；超限文案见 P1-5。
   - `undo dhcp relay server-ip`（**不带参数**）语义：PM 建议**清空全部**并给出确认性回显；若主理人认为歧义太大，可改为必须带地址参数。
   - `display dhcp relay`（**无参数**）语义：PM 建议**等价于 `all`**（学员最常敲）；若要严格对齐真机则退化为 usage 提示。
   **请逐项拍板或整体采纳 PM 建议。**

---

## 7. 不在本期范围

- 建设真实 DHCP 报文转发引擎（UDP 67/68 收发、`giaddr` 改写、DISCOVER/OFFER/REQUEST/ACK 四步交互）与真实对端 DHCP 服务器——**所有运行态转发指标一律诚实占位**；
- 真实 Option82 报文字段插入 / 解析（`circuit-id` / `remote-id` 内容构造），本期 Option82 **仅配置态与展示**；
- `dhcp server group` 服务器组抽象与 `dhcp relay server-select <group>`（P2-5，明确 out-of-scope）；
- DHCP Snooping、DHCP 防攻击（`dhcp snooping enable` / `dhcp snooping check` 等，属课程其他章节）；
- DHCPv6 中继（`dhcpv6 relay ...`）；
- 既有 **DHCP 地址池自身**的持久化补齐与 `display ip pool` 的 map 随机遍历缺陷修复（已在 §3「已有」表记录，属独立技术债，**建议另开工单**，本期仅不使其恶化）；
- 前端图形化中继链路指示 / 新增 API 字段（P2-7）；
- 重写 NAT / 端口安全 / VRRP / STP / LAG（仅 DHCP 中继增量）。

---

## 附：关键 file:line 证据索引（供架构师直接定位，主理人可逐条 grep 验证）

- `internal/cli/parser.go:1475` `case "dhcp"` 分支入口；`:1477-1552` `ViewDHCPPool` 池视图子命令（`network` / `gateway-list` / `dns-list` / `lease` / `excluded-ip-address`）。
- `internal/cli/parser.go:1554-1556` **系统视图硬守卫** `if state.CurrentView != ViewSystem { return "Error: must be in system view" }` —— 这是 `dhcp select relay` 无法落在接口视图的**直接阻塞点**（P0-1 必改）。
- `internal/cli/parser.go:1562-1573` `dhcp enable` / `dhcp disable`（relay 的前置依赖，P0-7）；`:1574-1587` `dhcp pool <name>` 进入 `ViewDHCPPool`。
- `internal/cli/parser.go:1588-1597` `dhcp select global|interface` —— 仅接受两种取值（**无 `relay`**），写 `state.DHCPSelectMode`（`:1596`）。
- `internal/cli/state.go:84` `DHCPSelectMode string` 声明；**全仓 grep `DHCPSelectMode` 仅两处命中（`parser.go:1596` 写、`state.go:84` 声明），无任何读取点 → 确认为死状态**（P0-1 废弃依据）。
- `internal/cli/state.go:140-143` `DHCPConfig{Enabled bool; Pools map[string]*DHCPPool}` —— **无任何 relay 字段**；`:145-156` `DHCPPool` 结构；`:579-581` 构造器初始化；`:27` `ViewDHCPPool` 视图常量；`parser.go:5098-5099` 池视图提示符。
- `internal/cli/parser.go:2620-2690` `display ip pool [interface vlanif <id>]` —— 现有唯一 DHCP 相关 display；`:2643` / `:2662` / `:2679` 三处 `for ... range state.DHCP.Pools` **map 遍历，输出顺序随机**（本期新增 display 不得复制此缺陷，AC7 有确定性断言）。
- `internal/cli/parser.go:5031-5035` `applyUndoSystemFeature` 的 `case "dhcp"` —— 仅 `state.DHCP.Enabled = false`，**无 relay undo 分支**（P0-3 / P1-6 需扩展）。
- `internal/cli/parser.go:4946-4965` 启用状态快照补丁（`:4964-4965` 输出 ` dhcp enable`）；`parser.go:5337` `buildSavedConfigSnapshot`；`:5401-5409` 接口块内挂载 VRRP / STP / LAG 配置行的范式（**P0-10 的 DHCP relay 段照此挂载**）；`:5350` 系统级 STP 块挂载点。
- `internal/cli/capabilities.go:64` `"dhcp": switchAndL3()`；`capabilities.go:226` `switchAndL3()` 定义（Router / L3Switch / Switch / Firewall / AC / AP / VTEP）；`capabilities.go:136-138` `isCommandSupported` 未声明默认放行；`parser.go:245` 能力校验调用点。
- 诚实占位范式（`dhcpRelaySimNote()` 照此实现）：`acl_eval.go:493` `aclSimNote()`、`:502` `natSimNote()`、`portsec_eval.go:239` `portSecSimNote()`、`vrrp_eval.go:397` `vrrpSimNote()`、`stp_eval.go:290` `stpSimNote()`、`lag_eval.go:786` `lagSimNote()`（均读 `sim.EngineModeName()`，lite / full 两态）。
- 纯函数评估器契约参照：`lag_eval.go`（`EvaluateLAG` 系列）、`vrrp_eval.go:259` `EvaluateVRRP`、`stp_eval.go:445` `EvaluateSTP`、`stp_eval.go:304` `sortedInterfaceNames`（确定性排序，P0-8 / P0-11 复用）。
- IPv4 校验范式：`parser.go:4539` `if net.ParseIP(vip) == nil`（VRRP virtual-ip）；`vrrp_eval.go:356-357,415,432,447`、`acl_eval.go:368,402,454,462`。
- `internal/cli/parser.go:310-372` 接口名解析（支持 `Vlanif` 等逻辑接口，P1-8 复用）；`parser.go:287` `ViewDHCPPool` 视图判定。
- **缺口确认**：`grep -in "relay" internal/cli/*.go` 无任何 DHCP 中继实现；`grep -in "display dhcp" internal/cli/*.go` 无命中 → `display dhcp relay` 为**全新命令**；`internal/cli/tools.go` 无 `dhcp` display 缩写归一化条目（若需 `display dhcp r` 之类缩写，需在 `tools.go` 补）。

---

## 文档状态

- 基线核查完成：DHCP 池能力（`parser.go:1477-1597`）、`DHCPConfig`（`state.go:140`）、能力矩阵（`capabilities.go:64`）、`undo dhcp`（`parser.go:5031`）、持久化机制（`parser.go:5337`）均已核实到 file:line。
- **核心缺口**：DHCP 中继命令族 100% 缺失；`display dhcp relay` 不存在；`undo` 无 relay 分支；`current-configuration` 无 relay 段。
- **意外发现（本期前置阻塞项，已纳入 P0-1 / P2-2）**：既有 `dhcp select` 存在**双重架构缺陷**——错位在系统视图（官方为接口视图）+ 写入只写不读的全局死字段 `state.DHCPSelectMode`（不持久化、无法表达每接口模式）。中继必须挂接口，故此项必须先修。
- 需求池 **27 条**（P0 12 / P1 8 / P2 7），验收标准 **AC1–AC12**（AC10 拆为 10a/10b/10c），其中 **AC8 为诚实占位红线断言**（六个转发统计字段恒 `-`，正则断言不含数字与可达性词）。
- **§6 的 6 项待确认已于 2026-08-09 全部拍板闭合**，结论汇总见 §6 开头表格。其中 #1/#2/#3/#4/#5 采纳 PM 建议（#3 并将三态互斥从 P2-1 **上提至 P0**），#6 **部分推翻 PM 建议**——"未 `dhcp enable` 配 relay"由硬拒绝改为**软提示 `Info:` 不阻断、键照写**。
- **本次修订（2026-08-09，架构师 §9.2 C1/C2 反馈同步）**：① **AC5 ①** 由"断言 `Error:` 硬拒绝"改写为"断言 `Info:` 提示存在 **且** `dhcp-select` 键已写入"；② **AC10** 拆分为 10a（配置命令按 `l3Devices()` 守卫拒绝）/ 10b（`display dhcp relay` 只读放行、PC 上输出空态 `Info:`）/ 10c（二层 Switch 既有 DHCP 行为零回归 + `capabilities.go` 零改动）；③ 全文**键名对齐设计文档定稿**：`dhcp:select`→`dhcp-select`、`dhcp:relay:<field>`→`dhcp-relay:<field>`，字段名统一为 `server-ips` / `option82` / `option82-strategy` / `source-ip`，并明确**不得落 `dhcp-relay:mode` 键**（双写事实源）；④ P1-2 补齐拍板 #6 的 strategy 缺省展示口径（未配显示 `replace` 而非 `-`，`current-configuration` 不输出缺省行）。
- **键名对齐补漏（2026-08-09 第二轮，架构师复核发现）**：首轮批量替换只覆盖了完整形态键名，**遗漏 3 处缩写/通配形态**，已全部修正——① §4.2 真实性标注表 `Source IP address` 行的来源 `...:relay:source-ip` **漏 `dhcp-` 前缀** → 改为 `...:dhcp-relay:source-ip`（架构师指出：照此写测试会按错键断言，或导致 source-ip 永不命中）；② §1 产品目标的 `DeviceConfig["interface:<iface>:dhcp:*"]` 通配 → 拆为 `dhcp-select` / `dhcp-relay:*`；③ AC4 的"`DeviceConfig` 中 server-ip 键"模糊表述 → 明确为 `DeviceConfig["interface:<if>:dhcp-relay:server-ips"]` 并补"不得留下空串键"。**现全文键引用已 100% 收敛**（仅 3 处"原稿→新键"迁移留痕行保留旧写法，属刻意留痕）。
- **display 渲染标签归属（架构师 §7 绑定条款）**：设计文档明确「display 渲染标签/列宽以 **PRD §4.2/§4.3** 为准，设计不另定列宽」，且 `RelayStats` 结构体字段名已 1:1 对齐 PRD §4.2 的 VRP 显示标签（`DHCPPacketsForwarded`/`DiscoverForwarded`/`OfferReceived`/`RequestForwarded`/`AckReceived`/`ServerReachability`），渲染时直拼标签、杜绝二次翻译错配 AC8。**故 PRD §4.2/§4.3 为 display 输出的唯一权威源，工程师严格照样例实现。** `Interface status` 读既有 `interface:<if>:status` 键，**不归入 `RelayStats`**。
- 需求条目**无增减**（仍 27 条），本次仅文案与键名同步，PRD 与设计文档验收口径现已一致。

_Last updated: 2026-08-09 · 产品经理 许清楚（Xu）_
