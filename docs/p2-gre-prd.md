# ensp-lab P2 第七项：GRE 隧道（华为 VRP 实训课程 69）增量 PRD

> 文档类型：增量产品需求文档（PRD，**简单模式**，结构对齐 `docs/p2-dhcp-prd.md` / `docs/p2-lag-prd.md`）
> 关联：`docs/p2-dhcp-prd.md`（上一轮需求粒度 / AC 写法基准）、`docs/p2-dhcp-design.md`（§0 拍板汇总 + §7 共享知识，架构红线直接沿用）、`docs/reference/huawei-vrp-course.md:68`（课程 69）
> 代码基线：`internal/cli/parser.go` / `state.go` / `capabilities.go` / `dhcp_relay_eval.go` / `lag_eval.go`（**已逐条 grep 核查到 file:line，见文末证据索引**）
> 作者：产品经理 许清楚（Xu）
> 语言：中文
> 说明：本期**不做竞品/市场分析**（按主理人指示），仅输出产品目标 / 用户故事 / 需求池 / UI 设计稿 / 验收标准 / 待确认问题。

---

## 0. 项目信息

- **Language**：中文
- **Programming Language**：Go 1.26.5（本期工作集中在 `internal/cli`；前端 React/Vite 本期无变更）
- **Project Name**：`p2_gre_tunnel`
- **原始需求复述**：在 P2 已交付 NAT（38）、端口安全（49）、VRRP（60/61）、STP/RSTP/MSTP（55/56/57）、链路聚合 Eth-Trunk（63）、DHCP 中继（27）之后，为华为 eNSP VRP 仿真器落地 **GRE 隧道（课程 69）** 的增量实现：把 Tunnel 接口的 GRE 命令族（`interface Tunnel0/0/1` → `tunnel-protocol gre` → `source` → `destination` → `gre key` / `keepalive`）**从"自造非 VRP 命令 + 结构体死状态"重构为"接口视图真实命令 + `DeviceConfig` 单一事实源"**，新增 VRP 风格 `display interface Tunnel<x>` GRE 段与 `display gre tunnel` 只读展示，并对隧道运行态（Up/Down、keepalive 计数、封装报文数、对端可达性）施加**诚实占位**。

> **深度边界先验结论（务必先读 §6 待确认项）**：GRE 的真实价值在于**在公网之上封装私网报文**——本端把私网 IP 报文加 GRE 头 + 外层公网 IP 头（source/destination），送到对端后解封装。本工具是**单机 VRP CLI 仿真器，无真实封装/解封装引擎、无跨设备隧道协商、无 keepalive 定时器**。因此本期严格划界：
>
> - **配置面 100% 真实**：Tunnel 接口创建、`tunnel-protocol gre`、`source`、`destination`、`gre key`、`keepalive`、隧道口 `ip address` —— 必须真实落 `DeviceConfig` 键、真实可 `display`、真实可 `undo`、真实可 `save`→`reload` 复现。这部分**不允许打折**。
> - **运行面 100% 诚实占位**：「隧道 Protocol 状态 Up/Down、keepalive 收发计数、封装/解封装报文数、对端 destination 可达性、隧道 MTU 实测值」等 —— **一律显示 `-` 或诚实态并附注记，严禁编造数字、严禁随机数、严禁硬编码 `up`**。这是本项目核心价值观红线（对照 LAG 的 Partner 块、VRRP 的跨设备心跳、DHCP 中继的转发计数处置）。
>
> **重大基线发现（本期前置阻塞项，非另起炉灶）**：GRE 在代码基线中**并非完全缺失，而是以"错误形态"存在**，且缺陷比 DHCP 那轮更严重（DHCP 是 1 个死字段，GRE 是**一整条自造命令链路 + 一份跨包死代码**）。详见 §3「已有」表 P0-1 / P0-2 / P0-3 关联项：
> ① `parser.go:2263-2292` 存在一条**华为 VRP 根本不存在的自造命令** `gre <tunnel-name> <src-ip> <dst-ip> [key] [keepalive]`，且被硬守卫在**系统视图**——真机 GRE 是 **Tunnel 接口视图**命令族；
> ② 其结果写入 `state.GRE map[string]*GREConfig`（`state.go:72`/`:382`）——**结构体事实源、不入 `DeviceConfig`、不落盘**，`save`→`reload` 后全丢；
> ③ `display gre`（`parser.go:3517-3531`）是 **map 随机遍历**（输出顺序不确定，同 `display ip pool` 反面教材），且**无任何隧道状态与诚实注记**；
> ④ `internal/protocol/protocol.go:1370-1409` 另有一份 `EnableGRE`/`AddGRETunnel`/`GetGREStatus` **全仓零调用点的死代码**，其中 `GRETunnel{Status: "up"}`（`:1388`）是**硬编码编造隧道状态**——本期**不 import、不调用、不修改 `internal/protocol`**（架构红线），仅在 §7 记录为独立技术债。

---

## 1. 产品目标

在严守 P2「CLIState 层纯函数、单一事实源 `DeviceConfig`、诚实占位、零改动 `sim` 引擎、零新增第三方依赖、不 import `internal/protocol`」架构基线的前提下，把 GRE 从**"存在但形态错误"**纠正为一条学员可完整走通的实验链路：

1. **命令面对齐官方 VRP**：学员能按课程 69 的真实命令序列敲 `interface Tunnel0/0/1` → `ip address 10.0.0.1 30` → `tunnel-protocol gre` → `source 202.1.1.1` → `destination 202.2.2.2`，把隧道口配起来；命令形态、报错文案、`undo` 语义均对齐真机，肌肉记忆可平移到 eNSP / 真实设备。**废弃自造的系统视图 `gre <name> <src> <dst>` 命令**（真机无此命令，教错比不教更糟）。
2. **配置真实落地且持久**：把隧道配置从 `state.GRE` 结构体迁移到 `DeviceConfig["interface:<if>:tunnel-protocol"]` / `DeviceConfig["interface:<if>:gre-*"]` 单一事实源；`display interface Tunnel0/0/1`、`display gre tunnel`、`display current-configuration` 忠实复现，`save`→`reload` 后配置不丢（现状 100% 丢失）。
3. **展示忠实、边界诚实**：配置态字段（tunnel-protocol、source、destination、key、keepalive 开关与参数、隧道口 IP）**如实展示**；隧道 Protocol 状态、keepalive 收发计数、封装解封装报文数、对端可达性等仿真无法产出的运行态字段**一律 `-` / 诚实态 + `greSimNote()` 注记**，让学员清楚知道"哪些是我配对了、哪些是仿真给不了的"——绝不用假 `up` 换取观感。

---

## 2. 用户故事

1. **作为学习站点互联的网络学员（课程 69 主线）**：As a 学员，I want 在路由器上敲 `interface Tunnel0/0/1` → `tunnel-protocol gre` → `source 202.1.1.1` → `destination 202.2.2.2`，so that 我能用 `display interface Tunnel0/0/1` 核对隧道协议、源/目的地址是否与规划一致，验证自己的配置顺序和参数没记错。
2. **作为两端对配的学员**：As a 学员，I want 在 R1 配 `source 202.1.1.1 / destination 202.2.2.2`、在 R2 配 `source 202.2.2.2 / destination 202.1.1.1`，so that 我能通过两台设备各自的 `display gre tunnel` 对照检查"源目地址是否互为镜像"这一 GRE 最高频错点——同时我希望工具**老实告诉我它不做跨设备协商**，而不是伪造一个 `Protocol up` 让我误以为隧道真的通了。
3. **作为配置隧道识别关键字与保活的学员**：As a 学员，I want 敲 `gre key 1234` 和 `keepalive period 5 retry-times 3`，so that `display` 中 GRE key 与 keepalive 参数如实回显，我能对照课程理解"两端 key 必须一致"以及 keepalive 的周期/重试语义。
4. **作为关注持久化的学员**：As a 学员，I want `save` 后 `reload` 设备，so that 隧道配置（Tunnel 接口、tunnel-protocol、source、destination、key、keepalive、隧道口 IP）仍完整保留，`display current-configuration` 能复现整个 `interface Tunnel0/0/1` 块，而不必重配（**现状 `state.GRE` 不落盘，reload 后全丢**）。
5. **作为踩坑排障的学员**：As a 学员，I want 在没敲 `tunnel-protocol gre` 就配 `destination`、或在物理口 `GigabitEthernet0/0/1` 上敲 `tunnel-protocol gre`、或把 `source` 和 `destination` 配成同一个地址时，收到**明确、可读的错误提示**（而不是静默成功），so that 我立刻知道错在哪；同时我希望 `display` 里那些仿真给不了的 keepalive 计数**老老实实显示 `-`**，而不是给我一串假数字。

---

## 3. 需求池

> 共 **28 条**：P0 **14 条**、P1 **8 条**、P2 **6 条**（另列「已有」基线 8 条，属**重构对象 / 复用基线**，非新需求）。

### 已有（本期重构 / 复用，非另起炉灶）

| 标记 | 内容 | 位置 |
|---|---|---|
| [已有·**不合规·本期必改**] | **自造非 VRP 命令** `gre <tunnel-name> <src-ip> <dst-ip> [key] [keepalive]`：① 华为 VRP **根本不存在**该命令（真机是 Tunnel 接口视图命令族）；② 被 `if state.CurrentView != ViewSystem { return "Error: must be in system view" }` 硬守卫在**系统视图**；③ 参数为**位置参数**（`Args[0..4]`），非 VRP 具名子命令；④ 回显 `GRE tunnel %s created` 属自造欢快文案（对照 LAG 对 `Port added to Eth-Trunk 1` 的整改） | `parser.go:2263-2292` |
| [已有·**不合规·本期必改**] | `state.GRE map[string]*GREConfig{SourceIP, DestIP, Key, Keepalive}`：**结构体事实源**，不入 `DeviceConfig` → 不进 `SerializeToDeviceConfigData` → `save`→`reload` **配置 100% 丢失**；且无法表达 tunnel-protocol、keepalive period/retry、隧道口 IP 等真实字段 | `state.go:72`、`state.go:382-387`、`state.go:521` |
| [已有·**不合规·本期必改**] | `display gre`：① `for name, tunnel := range state.GRE` **map 随机遍历**，输出顺序不确定（与 `display ip pool` 同款缺陷，DHCP 那轮 AC7 已明令禁止复制）；② 输出 `Key: 0` / `Keepalive: false` 为**结构体零值**，未区分「未配置」与「配置为 0/false」；③ **无隧道状态字段、无诚实占位注记**；④ 命令形态非 VRP（真机为 `display interface Tunnel<x>` / `display gre tunnel`） | `parser.go:3517-3531` |
| [已有·**死代码·本期不动**] | `EnableGRE` / `DisableGRE` / `AddGRETunnel` / `GetGREStatus` + `GRETunnel{Status: "up"}`：**全仓零调用点**（`grep -rn "EnableGRE\|AddGRETunnel\|GetGREStatus" --include=*.go .` 仅命中定义处），且 `Status: "up"` 是**硬编码编造隧道状态**。**本期红线：不 import `internal/protocol`、不调用、不修改**；记入 §7 独立技术债，建议另开工单清理 | `internal/protocol/protocol.go:289-301`、`:1370-1409` |
| [已有·可复用] | `interface Tunnel0/0/1` / `interface Tunnel 0/0/1`（带空格）**已可创建**：`vlanifPrefixes` 含 `"Tunnel"`/`"Tun"`（`:325`），接口名正则含 `Tunnel|Tun`（`:352`），进入 `ViewInterface` 并写 `interface:<if>:status` | `parser.go:315-412` |
| [已有·**诚实性缺口·本期需处置**] | 接口创建时**无条件硬编码** `DeviceConfig["interface:<if>:status"] = "Up"` 且 `InterfaceConfig{Status:"Up", Protocol:"Up"}`。Tunnel 是**逻辑口**，真机其 Protocol 状态取决于 source/destination 配置完整性与对端可达性——无条件 `Up` 即编造（**与 LAG 聚合口曾踩的同类坑**，当时以 `syncLAGTrunkIfaceStatus` 实时派生修复，`parser.go:396-397`） | `parser.go:403-411` |
| [已有·可复用] | 能力矩阵已有 `"gre": l3Devices()`（Router / L3Switch / Firewall / VTEP）；`isCommandSupported` 按**首 token** 匹配，**未声明的命令默认放行** | `capabilities.go:61`、`:141-152`、`:174-181` |
| [已有·基线可复用] | 纯函数 + 诚实占位范式：`dhcpRelaySimNote()`（`dhcp_relay_eval.go:382-387`）、`lagSimNote()` / `vrrpSimNote()` / `stpSimNote()`（均读 `sim.EngineModeName()` 分 lite/full 两态）；键 helper 精确匹配范式 `dhcpSelectKeySuffix` / `dhcpRelayKeyInfix` / `dhcpRelayKeyPrefix`（`dhcp_relay_eval.go:76-82,151-164`）；IPv4 校验 `net.ParseIP + .To4()`（`parser.go:4539`）；接口视图 undo 的 **handled 模式**钩子（`parser.go:827-833`）；持久化接口块挂载点（`parser.go:5419-5438`）与独立输出通道范式（`parser.go:5440-5465`） | 见各处 |

### P0（本期核心 · 事实源迁移 + Tunnel 命令族 + display 忠实 + 诚实占位）

**A. 事实源与架构缺陷修复（前置阻塞项）**

- **[P0-1 废弃自造命令 `gre <name> <src> <dst>`，GRE 迁入 Tunnel 接口视图]**：官方 VRP 的 GRE 配置全部落在 **Tunnel 接口视图**。把 `parser.go:2263` `case "gre"` 的系统视图硬守卫改为**按视图分派**：`ViewInterface` 且当前接口为 Tunnel 口时处理 `gre key <n>` 等子命令；`ViewSystem` 下的旧自造命令**重写为报错引导**（`Error: Please configure GRE in the Tunnel interface view. Run 'interface Tunnel0/0/1' first.`）。**不做旧命令兼容**——真机无此命令，兼容一个错误行为没有教学价值；有 DHCP `dhcp select` 迁移与 STP 移除 `state.STP` 两个先例。
- **[P0-2 删除结构体事实源 `state.GRE` / `GREConfig`]**：删除 `state.go:72` 字段、`state.go:382-387` 类型、`state.go:521` 构造器初始化。全部隧道配置改走 `DeviceConfig` 键。**严禁在 `CLIState` 上新增任何 GRE / Tunnel 内嵌结构体**（架构铁律，对照 DHCP 中继 AC11 静态断言）。删除安全性：`state.GRE` 全仓仅 3 处引用（`parser.go:2284` 写、`:3519`/`:3521` 读，**均在本期重构范围内**），`internal/api` 与前端零引用，删字段后 `go build` 立即暴露遗漏。
- **[P0-3 GRE 键命名空间（精确匹配，防前缀碰撞）]**：键统一为 `interface:<if>:tunnel-protocol` 与 `interface:<if>:gre-<field>`，`<field>` ∈ `source` / `destination` / `key`（P1）/ `keepalive`（P1）/ `keepalive-period`（P1）/ `keepalive-retry`（P1）/ `checksum`（P2）。**最终键名以架构师设计为准**，本条仅做预对齐。
  > 🔴 **键匹配红线（比 DHCP 那轮更危险，务必写进设计）**：**严禁 `strings.Contains(k, "gre")` 模糊匹配**。既有键 `interface:Bridge-Aggregation<id>:lag:<field>`（`lag_eval.go:391-393`）中 `Ag-gre-gation` **本身就含 `gre` 子串**——模糊匹配会把 H3C 聚合口键**全部误判为 GRE 隧道**（幽灵隧道），且级联清理会**误删聚合配置**，比 DHCP 那轮误删 `dhcp-pool` 更严重。必须提供 `tunnelProtocolKey` / `greKey` / `greKeyPrefix` 精确 helper，口径同 `dhcp_relay_eval.go:76-82,151-164`。

**B. GRE 配置命令（对齐官方 VRP 课程 69）**

- **[P0-4 `tunnel-protocol gre`（Tunnel 接口视图）]**：写 `interface:<if>:tunnel-protocol` = `gre`；成功静默或 VRP 风格短回显；重复执行幂等（不报错、不产生重复键）。**仅允许在 Tunnel 接口执行**：物理口 / Vlanif / Eth-Trunk 上执行 → `Error: This command is only supported on Tunnel interfaces.`。非 `gre` 取值（`ipv4-ipv6` / `mpls` / `none`）本期**不实现** → `Error: unrecognized command`（范围见 §7）。
- **[P0-5 `source <ip-address>`（Tunnel 接口视图）]**：写 `interface:<if>:gre-source`，指定隧道源；单值，后配覆盖先配。IPv4 校验走 `net.ParseIP(x) != nil && x.To4() != nil`（范式对照 `parser.go:4539`），失败 → `Error: Invalid IP address <x>`。**是否同时支持接口名形态（`source GigabitEthernet0/0/0`，真机支持）见 §6 C3。**
- **[P0-6 `destination <ip-address>`（Tunnel 接口视图）]**：写 `interface:<if>:gre-destination`，指定隧道目的（对端公网地址）；单值，后配覆盖先配；同 P0-5 的 IPv4 校验。**仅接受 IPv4 地址，不接受接口名**（真机同此）。
- **[P0-7 前置条件守卫与拒错（关键教学点）]**：
  - 未 `tunnel-protocol gre` 就配 `source` / `destination` / `gre key` / `keepalive` → **明确报错且不写任何键**：`Error: Please run 'tunnel-protocol gre' on this interface first.`（口径与 DHCP 拍板 #1「未 `dhcp select relay` 配 `server-ip` 硬拒绝」完全一致，**不做隐式自动关联**）。
  - 非 Tunnel 接口执行 `tunnel-protocol` / `source` / `destination` / `keepalive` → `Error: This command is only supported on Tunnel interfaces.`
  - 非接口视图执行上述命令 → `Error: must be in interface view`。
  - 设备类型不支持（PC / Server / 二层 Switch）→ 复用 `l3Devices()` 拒绝（P0-14）。
  - 参数缺失 → VRP 风格 usage 提示（如 `Error: usage: source <ip-address>`）。
  - **`source` 与 `destination` 配成同一地址** → 处置见 §6 C5（PM 建议 `Error:` 硬拒绝）。
- **[P0-8 Tunnel 口 `ip address` 复用既有能力]**：`ip address <ip> <mask>` 在 Tunnel 口沿用**既有**接口 IP 逻辑（`parser.go:414+`），**本期零改动**；仅需测试覆盖 Tunnel 口场景，并在 `display` / `current-configuration` 中与 GRE 段一同复现。

**C. 展示与诚实占位**

- **[P0-9 `display interface Tunnel<x>` 新增 GRE 段 + 新增 `display gre tunnel`]**：① `display interface Tunnel0/0/1` 在既有接口块基础上追加 GRE 信息行（字段见 §4.2）；② 新增 `display gre tunnel`（VRP 风格汇总，**替代**自造的 `display gre`），输出全部已配 GRE 隧道汇总表（§4.3）。**输出必须确定性**（隧道口按接口名升序，**禁止复制 `parser.go:3521` 的 map 随机遍历**）。空态 → `Info: No GRE tunnel configured.`；指定 Tunnel 口未配 GRE → 明确提示而非空输出。**旧 `display gre` 的处置见 §6 C6。**
- **[P0-10 `greSimNote()` 诚实占位（CRITICAL 红线）]**：新增注记函数，口径严格对齐 `dhcpRelaySimNote()` / `lagSimNote()`（读 `sim.EngineModeName()`，lite / full 两态）：
  - lite → 「（GRE 隧道为配置态模拟（lite 引擎），无真实报文封装/解封装与对端协商，隧道状态与保活统计不可用）」
  - full → 「（GRE 隧道为配置态模拟，无真实封装转发引擎）」
  所有 `display interface Tunnel<x>` 的 GRE 段与 `display gre tunnel` 输出末尾必须附加。**输出中不得出现任何伪造的隧道运行态**（见 §4.2 占位标注表）。**特别地：严禁沿用 `internal/protocol/protocol.go:1388` 的 `Status: "up"` 硬编码。**
- **[P0-11 隧道口 Protocol 状态诚实化]**：修复「已有·诚实性缺口」——Tunnel 口 Protocol 状态**不得无条件硬编码 `Up`**。采用**本地可判定口径**（不臆造对端）：`tunnel-protocol=gre` **且** `gre-source` 与 `gre-destination` 均已配置 → 显示 `Up (config complete, peer not verified)`；缺任一项 → `Down (source/destination not configured)`；**永不基于对端可达性判定**（无 ICMP、无跨设备状态）。**最终文案与是否沿用 LAG 的实时派生范式见 §6 C4。**
- **[P0-12 `display current-configuration` 新增 Tunnel 块 + save→reload 贯通]**：新增 `buildSavedGREInterfaceConfig(state, iface)`，按 VRP 顺序输出 ` tunnel-protocol gre` / ` source <ip>` / ` destination <ip>` / ` gre key <n>` / ` keepalive period <p> retry-times <r>`（**缺省值不冗余输出**，对齐 `buildSavedLAGInterfaceConfig` / `buildSavedDHCPRelayInterfaceConfig` 口径）；挂入 `parser.go:5419-5438` 接口块；并新增**独立输出通道** `buildSavedGREConfig(state)`，为「有 GRE 键但 `state.Interfaces` 未重建」的 Tunnel 口补齐 `interface Tunnel<x>` 块（复用 `parser.go:5440-5465` 的 VRRP / LAG / DHCP 三份范式）。GRE 键随既有 `SerializeToDeviceConfigData` ↔ `LoadFromDeviceConfigData` 自动往返，**零新增持久化代码**。

**D. 纯函数与守卫**

- **[P0-13 新增 `internal/cli/gre_eval.go` 纯函数评估器]**：范式严格对照 `dhcp_relay_eval.go` / `lag_eval.go` —— **无副作用、不修改 `sim` 引擎、不 import `internal/protocol`、零新增第三方依赖、可单测**；仅读 `state.DeviceConfig` / `state.Interfaces` 派生只读视图。建议契约（**最终以架构师设计为准**）：
  - `tunnelProtocolKey(iface)` / `greKey(iface, field)` / `greKeyPrefix(iface)`：键构造 helper（P0-3 精确匹配）。
  - `collectGRETunnels(state) []string`：按**精确后缀** `:tunnel-protocol` 扫描且值为 `gre`，返回**接口名升序**去重列表。
  - `EvaluateGRE(state, iface) GREResult`：返回 `Interface / Protocol / Source / Destination / Key / Keepalive{Enabled,Period,Retry} / ConfigComplete / Stats`。`Stats` 各字段**类型为 string 且恒 `-`**（从类型层面杜绝日后填数字，对照 `RelayStats` 处置）。
  - `isTunnelInterface(name) bool`：Tunnel 口判定（**精确前缀** `Tunnel`，不得用 `Contains`）。
  - `greSimNote() string`：诚实占位注记（P0-10）。
- **[P0-14 能力矩阵与分支内守卫]**：`capabilities.go:61` 已有 `"gre": l3Devices()`，**本期保持零改动**。但新命令首 token 为 `tunnel-protocol` / `source` / `destination` / `keepalive`，**均未在矩阵中声明** → `isCommandSupported` **默认放行**（`capabilities.go:145-147`）。故设备守卫必须做在**分支内部**（复用 `l3Devices()`，`capabilities.go:174-181`，**严禁重定义**），口径完全对齐 DHCP 拍板 #5。`display gre tunnel` / `display interface Tunnel<x>` 为**只读命令、任意设备可读**，空态放行输出 `Info:`。

> ⚠️ **顶层 token 冲突核查（PM 已完成，架构师可直接采信并复验）**：`tunnel-protocol` / `source` / `destination` / `keepalive` 在 `parser.go` 顶层 `switch` 中**均无既有 case**，无冲突。`parser.go:946/952` 的 `case "source"/"destination"` 位于 **ACL rule 参数解析的内层 switch**；`parser.go:1295` 的 `case "keepalive"` 位于 **`m-lag` 子命令内层 switch**——两者与顶层分派不在同一层级，不受影响。

### P1（增强真实语义 · 建议默认纳入）

- **[P1-1 `gre key <key-value>`]**：写 `interface:<if>:gre-key`，隧道识别关键字；范围校验（PM 建议 `0`–`4294967295`，最终以 §6 C7 为准）；非法 → `Error: Invalid GRE key <x>`；`undo gre key` 清键。`display` 未配时显示 `-`（**不显示 `0`**——`0` 与「未配置」是不同语义，这正是现状 `parser.go:3525` `Key: %d` 的缺陷）。
- **[P1-2 `keepalive [period <p>] [retry-times <r>]`]**：写 `gre-keepalive` = `true` + `gre-keepalive-period` / `gre-keepalive-retry`；缺省 period `5` 秒 / retry `3` 次（PM 建议，最终以 §6 C7 为准）；范围校验（PM 建议 period 1–32767、retry 1–255）；`undo keepalive` 清全部 keepalive 键。**keepalive 仅为配置态，不做真实计时**（§6 C2），运行态收发计数恒 `-`。
- **[P1-3 多 Tunnel 口并存与隔离]**：同一设备多个 Tunnel 口（`Tunnel0/0/1` / `Tunnel0/0/2` …）各自独立配置、互不干扰；`display interface Tunnel<x>` 只反映该口配置；键前缀天然隔离，需测试覆盖。**数量上限见 §6 C1。**
- **[P1-4 `display gre tunnel` 汇总表]**：一屏纵览所有 GRE 隧道（接口名 / 协议 / Source / Destination / Key / Keepalive / 隧道口 IP / 状态占位列），列定义见 §4.3；接口按名称升序，输出确定性。
- **[P1-5 接口视图 `undo` 语义完整]**：`undo tunnel-protocol`（清 `tunnel-protocol` 键 + **级联清理**该口 `gre-` 精确前缀全部键，避免「协议已撤但 source/destination 还挂着」的幽灵配置——对照 DHCP 拍板 #3 的级联清理）、`undo source`、`undo destination`、`undo gre key`、`undo keepalive`。挂钩复用 `applyUndoDHCPInterface` 的 **handled 模式**（`parser.go:827-833`），未命中时交回既有分支，**零回归**。
- **[P1-6 `undo interface Tunnel<x>`（系统视图）]**：删除 Tunnel 逻辑口并清理 `interface:Tunnel<x>:*` 全部键。**现状 `applyUndoInterfaceTrunk` 对非聚合口名直接返回 `Error: undo interface '<x>' is not supported`**（`lag_cmd.go:744-747`），需扩展分派，**不得改坏既有 Eth-Trunk 分支**。
- **[P1-7 Tunnel 块一体复现]**：`interface Tunnel0/0/1` 块内同时输出 ` ip address ...` 与 GRE 配置行，顺序对齐 VRP（`ip address` → `tunnel-protocol` → `source` → `destination` → `gre key` → `keepalive`）。
- **[P1-8 brief 类 display 覆盖 Tunnel 口]**：Tunnel 口应出现在既有 `display interface brief` / `display ip interface brief` 输出中，Protocol 列采用 P0-11 的诚实口径（**不得显示无条件 `up`**）；需测试覆盖并断言无回归。

### P2（边界收敛 / 诚实边界 / 可选增强）

- **[P2-1 `gre checksum`]**：写 `interface:<if>:gre-checksum` = `true`，仅配置态与展示，不做真实校验和计算；`undo gre checksum` 清键。属课程 69 延伸项，可延后。
- **[P2-2 `source` 支持接口名形态]**：真机支持 `source GigabitEthernet0/0/0`（取该口主 IP 作隧道源）。若纳入，需明确「仿真是否推导该口 IP」——**PM 强烈建议：如实存接口名并原样展示，绝不推导 IP**（推导即臆造，与 DHCP 拍板 #4「`Source IP` 不推导接口主 IP」同源）。随 §6 C3 拍板。
- **[P2-3 文案语言与一致性]**：错误/提示统一英文 `Error:` / `Info:` 前缀（对照既有代码），诚实占位注记沿用**中文括注**（对照 `dhcpRelaySimNote()`）。本期**不引入 i18n 框架**，仅保证风格自洽、可 grep、可断言。
- **[P2-4 特殊 IPv4 地址边界]**：`0.0.0.0` / `255.255.255.255` / `224.0.0.0/4` / `127.0.0.0/8` 作 `source` / `destination` 是否拒绝？**PM 建议拒绝**（`Error: <x> is not a valid tunnel address.`），逻辑落在单个纯函数 `validGRETunnelIP` 内，增量小、可单测（对照 DHCP 设计 A4）。
- **[P2-5 GRE over IPv6 / IPv6 over GRE]**：**本期明确 out-of-scope**（见 §6 C8 与 §7）。
- **[P2-6 前端无变更]**：GRE 仅在 CLI 文本体现，**不新增 API 字段、不做拓扑图形化隧道指示**（与 NAT / 端口安全 / VRRP / STP / LAG / DHCP 中继一致）。

---

## 4. UI / 交互设计稿（CLI 回显与 display 输出，纯文本）

> 本节为 **display 输出的唯一权威源**（沿用 DHCP 那轮「display 渲染标签/列宽以 PRD §4 为准，设计不另定列宽」的团队约定）。工程师严格照样例实现，测试据此写子串断言。

### 4.1 配置命令序列回显（课程 69 主线操作流）

```
<R1> system-view
[R1] interface Tunnel0/0/1
[R1-Tunnel0/0/1] ip address 10.0.0.1 255.255.255.252
[R1-Tunnel0/0/1] tunnel-protocol gre
[R1-Tunnel0/0/1] source 202.1.1.1
[R1-Tunnel0/0/1] destination 202.2.2.2
[R1-Tunnel0/0/1] gre key 1234
[R1-Tunnel0/0/1] keepalive period 5 retry-times 3
[R1-Tunnel0/0/1] quit
```

> VRP 风格：配置成功**静默或规范短回显**，失败才 `Error:`。**不得出现 `GRE tunnel Tunnel0/0/1 created` 这类自造欢快文案**（现状 `parser.go:2290` 即此缺陷，对照 LAG 对 `Port added to Eth-Trunk 1` 的整改）。

**典型拒错回显（`Error:` 硬拒绝，且不写任何键）**：

```
[R1-Tunnel0/0/1] source 202.1.1.1
Error: Please run 'tunnel-protocol gre' on this interface first.      ← P0-7 第一条

[R1-GigabitEthernet0/0/1] tunnel-protocol gre
Error: This command is only supported on Tunnel interfaces.           ← P0-4 / P0-7 第二条

[R1-Tunnel0/0/1] destination 300.1.1.1
Error: Invalid IP address 300.1.1.1                                    ← P0-6

[R1] gre Tunnel0/0/1 202.1.1.1 202.2.2.2
Error: Please configure GRE in the Tunnel interface view. Run 'interface Tunnel0/0/1' first.
                                                                       ← P0-1（旧自造命令报错引导）

[R1-Tunnel0/0/1] destination 202.1.1.1                                 ← 与 source 相同
Error: The destination address cannot be the same as the source address.
                                                                       ← P0-7 末条（待 §6 C5 拍板）
```

### 4.2 `display interface Tunnel0/0/1`（单接口详情块 + GRE 段）

```
Tunnel0/0/1 current state : UP
Line protocol current state : DOWN (source/destination not configured)
Description: HUAWEI, AR Series, Tunnel0/0/1 Interface
Route Port,The Maximum Transmit Unit is 1476
Internet Address is 10.0.0.1/30
Encapsulation TUNNEL, loopback not set
Tunnel source 202.1.1.1, destination 202.2.2.2
Tunnel protocol/transport GRE/IP
GRE key         : 1234
Keepalive       : Enabled  (period 5, retry-times 3)
Checksumming of packets : Disabled
  --- Tunnel runtime statistics ---
  Keepalive sent          : -
  Keepalive received      : -
  Packets encapsulated    : -
  Packets decapsulated    : -
  Peer reachability       : -
（GRE 隧道为配置态模拟（lite 引擎），无真实报文封装/解封装与对端协商，隧道状态与保活统计不可用）
```

> 上例中 `Line protocol current state` 显示 `DOWN (...)` 是**示意 source/destination 缺配时的诚实态**；两者齐备时应为 `UP (config complete, peer not verified)`（P0-11，最终文案随 §6 C4 拍板）。

**字段真实性标注表**（架构师据此实现，测试据此断言）：

| 字段 | 数据来源 | 真实性 | 未配置时 |
|---|---|---|---|
| `Tunnel0/0/1 current state` | `interface:<if>:status`（管理态，复用 `shutdown`/`undo shutdown`） | **真实**（本地可判定） | `UP` |
| `Line protocol current state` | 由 `tunnel-protocol` + `gre-source` + `gre-destination` **本地派生** | **真实**（**本地可判定，附诚实限定语**） | `DOWN (source/destination not configured)` |
| `Internet Address` | `interface:<if>:ip`（既有键） | **真实**（配置态） | 该行不输出 |
| `Tunnel source` | `interface:<if>:gre-source` | **真实**（配置态） | `-` |
| `Tunnel destination` | `interface:<if>:gre-destination` | **真实**（配置态） | `-` |
| `Tunnel protocol/transport` | `interface:<if>:tunnel-protocol` | **真实**（配置态） | 该接口不列入 GRE 隧道 |
| `GRE key` | `interface:<if>:gre-key` | **真实**（配置态） | **`-`（不得显示 `0`）** |
| `Keepalive` | `gre-keepalive` + `gre-keepalive-period` + `gre-keepalive-retry` | **真实**（配置态） | `Disabled` |
| `Checksumming of packets` | `interface:<if>:gre-checksum`（P2-1） | **真实**（配置态） | `Disabled` |
| `The Maximum Transmit Unit` | 固定常量 `1476`（GRE 封装开销 24 字节的**教科书标称值**） | ⚠️ **标称值非实测**，需在 §6 C9 确认是否输出 | — |
| `Keepalive sent` | — | 🔴 **诚实占位 `-`** | `-` |
| `Keepalive received` | — | 🔴 **诚实占位 `-`** | `-` |
| `Packets encapsulated` | — | 🔴 **诚实占位 `-`** | `-` |
| `Packets decapsulated` | — | 🔴 **诚实占位 `-`** | `-` |
| `Peer reachability` | — | 🔴 **诚实占位 `-`**（无 ICMP / 无跨设备状态，**严禁** `Reachable` / `Up` / `Active`） | `-` |

> 🔴 = 仿真环境无真实数据源，**恒为 `-`，严禁编造数字、随机数或伪造对端响应**。`--- Tunnel runtime statistics ---` 分组是否保留（还是整块略去）见 §6 C9。

### 4.3 `display gre tunnel`（汇总表，替代自造的 `display gre`）

```
GRE tunnel information
---------------------------------------------------------------------------------------------
Interface       Protocol  Source           Destination      Key         Keepalive  State
---------------------------------------------------------------------------------------------
Tunnel0/0/1     GRE       202.1.1.1        202.2.2.2        1234        Enabled    Up*
Tunnel0/0/2     GRE       202.1.1.1        203.3.3.3        -           Disabled   Up*
Tunnel0/0/3     GRE       -                -                -          Disabled   Down
---------------------------------------------------------------------------------------------
Total: 3 GRE tunnel(s)
* State 仅由本端配置完整性派生，未与对端协商
（GRE 隧道为配置态模拟（lite 引擎），无真实报文封装/解封装与对端协商，隧道状态与保活统计不可用）
```

- 列含义：`Protocol` = `tunnel-protocol` 值（真实）；`Source` / `Destination` = 对应键（真实，未配 `-`）；`Key` = `gre-key`（真实，未配 `-`，**不显示 `0`**）；`Keepalive` = 开关（真实）；`State` = **P0-11 的本地派生态**，必须带 `*` 与脚注，**严禁裸 `Up` 让学员误以为隧道已通**。
- **接口按名称升序排序**（确定性，禁止 map 随机遍历）。
- 空态：
  ```
  Info: No GRE tunnel configured.
  ```

### 4.4 `display current-configuration` 中的 Tunnel 块（P0-12 / P1-7）

```
#
interface Tunnel0/0/1
 ip address 10.0.0.1 255.255.255.252
 tunnel-protocol gre
 source 202.1.1.1
 destination 202.2.2.2
 gre key 1234
 keepalive period 5 retry-times 3
#
```

> 输出顺序固定：`ip address` → `tunnel-protocol` → `source` → `destination` → `gre key` → `keepalive`。**缺省值不冗余输出**（未配 key / keepalive 时不输出对应行，对齐 VRP 惯例与 `buildSavedLAGInterfaceConfig` / `buildSavedDHCPRelayInterfaceConfig` 口径）。

### 4.5 前端

**本期无变更**。GRE 隧道仅在 CLI 终端文本体现（P2-6）。

---

## 5. 验收标准（AC1–AC12，每条可用自动化测试证明，非恒真断言）

- **AC1（接口视图分派 + 事实源写入）**：在 Router / L3Switch 上 `interface Tunnel0/0/1` → `tunnel-protocol gre` → `source 202.1.1.1` → `destination 202.2.2.2`，断言 `DeviceConfig["interface:Tunnel0/0/1:tunnel-protocol"] == "gre"`、`...:gre-source" == "202.1.1.1"`、`...:gre-destination" == "202.2.2.2"`。**反向断言：`state.GRE` 字段已不存在（或未被写入）**，证明 P0-2 结构体事实源已废弃（静态断言 `grep -n "GREConfig\|GRE " internal/cli/state.go` 无命中）。

- **AC2（save → reload 持久化贯通 · 现状 100% 丢失，本条是本期最大价值点）**：完成 AC1 配置并追加 `ip address` / `gre key 1234` / `keepalive period 5 retry-times 3` 后执行 `save`，经 `SerializeToDeviceConfigData` → `LoadFromDeviceConfigData` 往返，reload 后断言：① `DeviceConfig` 中 `interface:Tunnel0/0/1:tunnel-protocol` 与 `interface:Tunnel0/0/1:gre-*` 键集与 reload 前**逐键完全一致**；② `display interface Tunnel0/0/1` 完整复现 source / destination / key / keepalive；③ **`display current-configuration` 复现 §4.4 全部 6 行**。**同时补一条对照断言：改造前该场景 reload 后配置为空**（证明缺陷确被修复）。

- **AC3（旧自造命令与旧展示已下线，且无残留写入路径）**：① 系统视图执行 `gre Tunnel0/0/1 202.1.1.1 202.2.2.2` → 返回含 `Tunnel interface view` 的 `Error:`，**且断言 `DeviceConfig` 中无任何 `gre-` 键被写入**；② 静态断言 `grep -rn "state.GRE" internal/cli/` **零命中**；③ `display gre`（旧形态）按 §6 C6 拍板结果断言（PM 建议：等价于 `display gre tunnel`，断言输出含 `GRE tunnel information` 且**不含** map 随机遍历特征的旧字段 `Key: 0`）。

- **AC4（IPv4 合法性校验，P0-5 / P0-6）**：`source 300.1.1.1` / `source 10.1.1` / `source abc` / `source 10.1.1.1/24` / `destination 2001:db8::1`（IPv6）**全部**返回含 `Invalid IP address` 的 `Error:`，且断言对应键**未被写入或未被污染**；合法地址 `202.1.1.1` / `172.16.0.254` 全部成功。实现须使用 `net.ParseIP(x) != nil && x.To4() != nil`（对照 `parser.go:4539`）。

- **AC5（前置条件与视图/接口类型守卫，P0-7）**：逐条断言：① 未 `tunnel-protocol gre` 就 `source 202.1.1.1` → 含 `tunnel-protocol gre` 引导的 `Error:`，**且断言 `interface:Tunnel0/0/1:gre-source` 键未写入**（证明未静默成功）；② 在 `GigabitEthernet0/0/1` 上 `tunnel-protocol gre` → 含 `only supported on Tunnel interfaces`；③ 系统视图执行 `source 202.1.1.1` → 视图拒绝；④ `source`（缺参）→ 含 `usage:`；⑤ `destination` 与 `source` 同址 → 按 §6 C5 拍板断言。**每条断言具体子串，不得用「返回非空」这类恒真断言。**

- **AC6（`display interface Tunnel<x>` 忠实展示）**：配置 source / destination / key / keepalive 后**逐字段断言**：含 `Tunnel source 202.1.1.1, destination 202.2.2.2`、`Tunnel protocol/transport GRE/IP`、`GRE key` 行值为 `1234`、`Keepalive` 行含 `Enabled` 与 `period 5` 与 `retry-times 3`；**未配 key 时该行值为 `-` 而非 `0`**（P1-1 关键断言，直击现状 `parser.go:3525` 缺陷）；未配 GRE 的 Tunnel 口 → 明确提示而非空串；不存在的接口名 → 明确 `Error:`。

- **AC7（`display gre tunnel` 汇总 + 输出确定性）**：3 个 Tunnel 口各配 GRE 后输出 3 行数据行，各列取值正确；**接口按名称升序**；**同一状态连续调用 10 次输出字节级完全一致**（证明消除了 `parser.go:3521` 的 map 随机遍历，对照 DHCP AC7 / LAG AC5）；无 GRE 隧道时输出 `No GRE tunnel configured`。

- **AC8（诚实占位 · CRITICAL 红线）**：lite 引擎下所有 `display gre tunnel` 与 `display interface Tunnel<x>` 的 GRE 段输出**均含** `greSimNote()` 的「无真实报文封装/解封装与对端协商」注记；用**正则断言输出中不存在任何伪造运行态数字**——具体：`Keepalive sent` / `Keepalive received` / `Packets encapsulated` / `Packets decapsulated` / `Peer reachability` 五个字段的值**必须恒为 `-`**，断言其**不匹配** `\d+` 且**不匹配** `Reachable|Unreachable|Active`；汇总表 `State` 列**不得出现裸 `Up`**（必须带 `*` 或诚实限定语）。**该 AC 失败即视为违反项目核心价值观，不得以「观感更好」为由放行。**

- **AC9（隧道状态诚实派生，P0-11）**：① 仅 `tunnel-protocol gre`、未配 source/destination 时，`display interface Tunnel0/0/1` 的 `Line protocol current state` 断言含 `DOWN` 与 `source/destination not configured`；② 补齐 source + destination 后断言含 `UP` **且同时含**诚实限定语（如 `peer not verified`）——**断言不存在「裸 UP 无限定语」的输出**；③ 静态断言全仓 `internal/cli/` 无 `Status: "up"` / `"Protocol": "Up"` 之类针对 Tunnel 口的硬编码（**`internal/protocol/protocol.go:1388` 属包外死代码，本期不改，不纳入本断言范围**）。

- **AC10（undo 语义完整，P1-5 / P1-6）**：① `undo source` / `undo destination` / `undo gre key` / `undo keepalive` 后对应键**被清除而非留空串**（断言 `_, ok := DeviceConfig[key]; ok == false`）；② `undo tunnel-protocol` 后**级联清理**该口 `gre-` 精确前缀全部键，且 `display gre tunnel` 中该隧道消失；③ `undo interface Tunnel0/0/1` 后 `interface:Tunnel0/0/1:*` 全部键被清理，且**断言既有 `undo interface Eth-Trunk <id>` 行为逐字不变**（零回归）。

- **AC11（能力守卫，P0-14）**：
  - **AC11a（配置命令按设备类型守卫）**：PC / Server / 二层 Switch 上执行 `tunnel-protocol gre` / `source 202.1.1.1` / `destination 202.2.2.2` / `gre key 1` / `keepalive` 均**拒绝**（设备集 = `l3Devices()`，复用 `capabilities.go:174`，**不新增不重定义**）；Router / L3Switch / Firewall / VTEP 正常放行。
  - **AC11b（display 只读、任意设备可读）**：PC / Server 上执行 `display gre tunnel` **不得返回能力拒绝**，应放行并输出空态 `Info: No GRE tunnel configured.`；断言输出**不含** `is not supported on`。
  - **AC11c（零回归）**：断言 `capabilities.go` **零改动**（`"gre": l3Devices()` 保持原样）；断言既有 Eth-Trunk / Vlanif / 物理口的接口视图命令行为逐字不变。

- **AC12（纯函数无副作用 / 架构基线合规 + 键碰撞专项）**：
  - `EvaluateGRE` / `collectGRETunnels` / `isTunnelInterface` / `greSimNote` 单测证明——不修改 `sim` 引擎、不写 `state`、**不 import `internal/protocol`**、零新增第三方依赖、连续两次调用结果一致且**不改写任何 `DeviceConfig` 键**（调用前后对 `DeviceConfig` 做 deep-equal 断言）。
  - **键碰撞专项断言（本期最高危项，P0-3）**：构造同时存在 `interface:Bridge-Aggregation1:lag:mode`（含 `gre` 子串！）与 `interface:Tunnel0/0/1:gre-source` 的状态，断言 ① `collectGRETunnels` **只返回 `Tunnel0/0/1`**，不含 `Bridge-Aggregation1`；② 对 `Tunnel0/0/1` 执行 `undo tunnel-protocol` 的级联清理后，**`interface:Bridge-Aggregation1:lag:mode` 键完好无损**。
  - 静态断言 `state.go` 无任何 GRE / Tunnel 内嵌结构体（`grep -n "GREConfig\|Tunnel.*struct" internal/cli/state.go` 无命中，对照 DHCP AC11 口径）。

---

## 6. 待确认问题（交主理人 / 架构师拍板，按重要性排序）

> 沿用 DHCP 那轮的拍板模式：每项给候选方案 + PM 建议 + 影响面。拍板后由 PM 回填结论表，实现与验收一律以拍板为准。

- **C1（核心 · 决定 P0-1 / P0-2 的改造力度）：旧自造命令 `gre <name> <src> <dst>` 与 `state.GRE` 是"直接删"还是"兼容保留"？**
  - **(a) 直接删除 + 报错引导（PM 强烈建议）**：删 `state.GRE` / `GREConfig`，系统视图旧命令改为报错引导到 Tunnel 接口视图。理由——① 该命令**华为 VRP 根本不存在**，保留即持续教错；② 现状**不落盘**（`save`→`reload` 全丢），本就没有可保护的用户配置；③ 全仓仅 3 处引用且**零测试覆盖**（`grep -rn "gre " --include=*_test.go .` 无命中），破坏风险≈0；④ 有 DHCP 删 `state.DHCPSelectMode`、STP 移除 `state.STP`、LAG 直接改键名三个先例。
  - (b) 双轨兼容：旧命令保留写 `state.GRE`，新命令写 `DeviceConfig`。**PM 反对**——双写事实源，正是 LAG `:members` 与 STP `state.STP` 已被清理掉的坑。
  **PM 建议 (a)。请拍板，并确认是否允许删 `state.go:72` / `:382-387` / `:521` 三处。**

- **C2（核心 · 决定 P1-2 与 AC8 断言面）：keepalive 是否需要真实计时？**
  - **(a) 仅配置态，不做真实计时（PM 强烈建议）**：`keepalive period 5 retry-times 3` 只落键、只展示，收发计数恒 `-`。理由——真实计时需要引入 goroutine / timer / 跨设备状态机，**违反"纯函数评估器 + 零改动 sim 引擎"架构基线**，且单机仿真没有对端可回应 keepalive，计时器只会产出**必然失败的假状态**（比 `-` 更不诚实）。
  - (b) 起真实定时器并累加"已发送"计数。**PM 强烈反对**——"已发送"没有接收方，等同编造；且引入并发状态到本应无副作用的 CLIState 层。
  **PM 建议 (a)。请拍板，这直接决定 §4.2 `Keepalive sent/received` 是否恒 `-`。**

- **C3（决定 P0-5 / P2-2 与 AC4 断言面）：`source` 允许"接口名"还是"仅 IP"？**
  - (a) **仅 IP**（本期最小范围）：`source <ip-address>`，接口名形态报 usage。实现最简，AC 最干净。
  - **(b) IP + 接口名双形态，接口名如实存原样展示、绝不推导 IP（PM 建议）**：真机课程 69 常用 `source GigabitEthernet0/0/0`，学员会敲；如实存 `interface:<if>:gre-source = "GigabitEthernet0/0/0"` 并在 display 原样回显即可，**不去读该口 IP 做推导**（推导即臆造，与 DHCP 拍板 #4「`Source IP` 不推导接口主 IP」同源）。增量小（一个 `isTunnelSourceValid` 分支）。
  - (c) IP + 接口名，且推导接口主 IP 显示。**PM 强烈反对**（触犯诚实红线）。
  **PM 建议 (b)。请拍板；若选 (b)，请一并确认 display 中是否需要区分展示（如 `Tunnel source GigabitEthernet0/0/0`）。**

- **C4（决定 P0-11 与 AC9 断言写法）：Tunnel 口 Protocol 状态的诚实口径与文案？**
  - **(a) 本地派生 + 诚实限定语（PM 建议）**：配置完整 → `UP (config complete, peer not verified)`；缺配 → `DOWN (source/destination not configured)`。优点：既反映本端配置正确性（有教学价值），又明确"没验证对端"。
  - (b) 恒 `DOWN` + 注记（最保守）：绝无误读风险，但学员配对了也看不到正反馈，教学价值打折。
  - (c) 配置完整即裸 `UP`。**PM 强烈反对**（等同现状 `protocol.go:1388` 的 `Status:"up"` 编造）。
  **PM 建议 (a)。请拍板**，并确认：是否沿用 LAG 的 `syncLAGTrunkIfaceStatus` 实时派生范式（在配置命令中同步写 `interface:<if>:status`），还是**纯 display 期派生、不写状态键**（**PM 倾向后者**——不写键即无双写事实源，且 `status` 键语义是"管理态 shutdown"，与协议态混写会污染既有语义）。

- **C5（决定 P0-7 末条与 AC5 ⑤）：`source` 与 `destination` 配成同一地址如何处置？**
  - **(a) `Error:` 硬拒绝（PM 建议）**：`Error: The destination address cannot be the same as the source address.` 理由——自环隧道在真机无意义，且这是学员两端对配时的高频笔误，报错教学价值高。
  - (b) 允许 + `Info:` 软提示。
  - (c) 静默允许。**PM 反对**（放过一个必错配置）。
  **PM 建议 (a)。请拍板。**

- **C6（决定 P0-9 与 AC3 ③）：旧命令 `display gre` 如何处置？**
  - **(a) 等价于 `display gre tunnel`（PM 建议）**：学员肌肉记忆与既有用例都可能敲 `display gre`，直接重定向到新实现，零挫败感、零回归风险（display 是只读命令，重定向无副作用）。
  - (b) 报错引导到 `display gre tunnel`（严格对齐真机）。
  - (c) 保留旧 map 随机遍历实现。**PM 强烈反对**（AC7 确定性断言直接冲突）。
  **PM 建议 (a)。请拍板。**

- **C7（一批缺省值与规格数字，打包拍板 · 决定 P1-1 / P1-2 / P1-3）**：
  - `gre key` **取值范围**：PM 建议 `0`–`4294967295`（32 位无符号，对齐 RFC 2890 / VRP 常见规格）；未配时 display 显示 `-`（**不显示 `0`**）。
  - `keepalive` **缺省值**：PM 建议 period `5` 秒、retry-times `3` 次；范围 period `1`–`32767`、retry `1`–`255`。
  - 单设备 **Tunnel 口数量上限**：PM 建议**不设上限**（Tunnel 是逻辑口，`interface Tunnel<x>` 本就受接口名正则约束；DHCP 那轮的上限 8 是因为 server-ip 是同一键内的列表，语义不同）；若主理人希望设限，PM 建议 `64`。
  - `tunnel-protocol` **是否支持 `none`**（真机缺省值，表示未指定封装）：PM 建议**本期不实现**，`undo tunnel-protocol` 即等价回落。
  **请逐项拍板或整体采纳 PM 建议。**

- **C8（决定本期范围边界）：GRE over IPv6 / IPv6 over GRE 是否本期范围？**
  - **PM 建议：明确 out-of-scope**。理由——① 课程 69 摘要（`docs/reference/huawei-vrp-course.md:68`）仅覆盖「公网建隧道承载私网、站点互联」的 IPv4 场景，关键命令为 `interface Tunnel` / `tunnel-protocol gre`；② 仓库 IPv6 能力本身仍是 Roadmap 状态（同文件功能矩阵「IPv6 / OSPFv3 📋 Roadmap」）；③ 纳入会把 IPv4 校验纯函数扩成双栈，增量与风险不成比例。
  **请拍板确认 out-of-scope。**

- **C9（决定 §4.2 输出形态与 AC8 断言面）：`--- Tunnel runtime statistics ---` 分组保留占位还是整块略去？另：MTU 是否输出？**
  - **(a) 保留分组、5 个字段全填 `-` + 注记（PM 建议）**：与 DHCP 拍板 #4「保留 `Forwarding statistics` 分组、值全 `-`」**家族一致**；学员能看到"真机这里会有哪些指标"，具教学参考价值。
  - (b) 整块略去，只输出配置态字段 + 一行注记。输出最干净，绝无误读风险。
  - **附带确认**：`The Maximum Transmit Unit is 1476` 是否输出？该值是「1500 − GRE 封装开销 24」的**教科书标称值**，**并非实测**。PM 建议：**要么不输出该行，要么输出时加脚注标明为标称值**——直接裸写 `1476` 会让学员误以为是仿真实测结果，属灰色地带。
  **PM 建议 (a) + MTU 加脚注（或不输出）。请拍板，这直接决定 AC8 的断言写法。**

---

## 7. 不在本期范围

- 建设真实 GRE 封装 / 解封装引擎（外层 IP 头 + GRE 头构造、隧道口选路、分片与重组、MTU 实测）与跨设备隧道协商——**所有运行态指标一律诚实占位**；
- 真实 keepalive 定时器与状态机（C2 已明确，仅配置态）；
- GRE over IPv6 / IPv6 over GRE（C8，明确 out-of-scope）；
- `tunnel-protocol` 的非 GRE 取值（`ipv4-ipv6` / `ipv6-ipv4` / `mpls` / `none`）；
- GRE + IPsec 联动（`ipsec profile` 应用到 Tunnel 口）——既有 `case "ipsec"`（`parser.go:1652`）为独立自造实现，**本期不动、不联动**，建议另开工单按同款范式整改；
- 隧道路由（`ip route-static` 指向 Tunnel 口后的实际转发）与隧道内动态路由（OSPF over GRE）——本期仅配置面；
- **`internal/protocol/protocol.go:1370-1409` 的 GRE 死代码清理**（`EnableGRE` / `DisableGRE` / `AddGRETunnel` / `GetGREStatus` + `GRETunnel{Status:"up"}`）：全仓零调用点，且含硬编码编造状态。**本期红线是不 import / 不调用 / 不修改 `internal/protocol`**，故仅登记为独立技术债，**建议另开工单删除**，本期仅保证不使其恶化、不产生新引用；
- 前端图形化隧道链路指示 / 新增 API 字段（P2-6）；
- 重写 NAT / 端口安全 / VRRP / STP / LAG / DHCP 中继（仅 GRE 增量）。

---

## 附：关键 file:line 证据索引（供架构师直接定位，主理人可逐条 grep 验证）

**A. 本期重构对象（缺陷现状）**

- `internal/cli/parser.go:2263-2292` `case "gre"` —— **自造非 VRP 命令**入口；`:2264-2266` 系统视图硬守卫；`:2268-2270` 位置参数 `tunnelName/srcIP/destIP`；`:2275-2280` key 解析（`parseNum` 失败仅 warn 不报错）；`:2281-2283` keepalive 位置参数；`:2284-2289` 写 `state.GRE`；`:2290` 自造回显 `GRE tunnel %s created`。
- `internal/cli/state.go:72` `GRE map[string]*GREConfig` 字段声明；`:382-387` `GREConfig{SourceIP, DestIP, Key, Keepalive}` 类型；`:521` 构造器 `GRE: make(map[string]*GREConfig)`。**全仓 `state.GRE` 仅 3 处引用**（`parser.go:2284` 写、`:3519`/`:3521` 读），`internal/api` 与前端零引用 → 删除风险≈0（P0-2 依据）。
- `internal/cli/parser.go:3517-3531` `display gre` —— `:3521` `for name, tunnel := range state.GRE` **map 随机遍历**；`:3525` `Key: %d`（零值 `0` 与未配置不可区分）；`:3526` `Keepalive: %t`；**无状态字段、无 simNote**。
- `internal/protocol/protocol.go:289-296` `GRETunnel` 类型（含 `Status string`）；`:298-301` `GREState`；`:1370-1409` `EnableGRE` / `DisableGRE` / `AddGRETunnel` / `GetGREStatus`；`:1388` **`Status: "up"` 硬编码编造**。**全仓零调用点**（`grep -rn "EnableGRE\|DisableGRE\|AddGRETunnel\|GetGREStatus" --include=*.go .` 仅命中定义处）→ 死代码，**本期不 import / 不调用 / 不改**。
- `internal/cli/parser.go:403-411` 接口创建时**无条件** `DeviceConfig["interface:<if>:status"] = "Up"` + `InterfaceConfig{Status:"Up", Protocol:"Up"}` —— Tunnel 逻辑口的诚实性缺口（P0-11）。对照修复范式：`parser.go:396-397` `syncLAGTrunkIfaceStatus`（聚合口状态实时派生，注释明确「绝不硬编码 Up，那是编造」）。

**B. 本期复用基线（正面范式）**

- `internal/cli/parser.go:315-412` 接口创建与接口名解析：`:325` `vlanifPrefixes` 含 `"Tunnel"`/`"Tun"`（支持 `interface Tunnel 0/0/1` 带空格形态）；`:352` 接口名正则含 `Tunnel|Tun` → **`interface Tunnel0/0/1` 本就可用，本期零改动**。
- `internal/cli/dhcp_relay_eval.go:76-82` 键匹配常量（`dhcpSelectKeySuffix` 精确后缀 / `dhcpRelayKeyInfix` 精确中缀）；`:151-164` `dhcpSelectKey` / `dhcpRelayKey` / `dhcpRelayKeyPrefix` —— **GRE 键 helper 照此实现**（P0-3）。
- `internal/cli/dhcp_relay_eval.go:382-387` `dhcpRelaySimNote()`（lite/full 两态）—— `greSimNote()` 照此实现（P0-10）。同族：`lag_eval.go` `lagSimNote()`、`vrrp_eval.go` `vrrpSimNote()`、`stp_eval.go` `stpSimNote()`。
- `internal/cli/lag_eval.go:391-393` `lagBridgeTrunkKey` → `interface:Bridge-Aggregation%d:lag:%s` —— **🔴 键碰撞证据**：`Ag-gre-gation` 含 `gre` 子串，故 GRE 键扫描**严禁 `strings.Contains(k, "gre")`**（P0-3 红线、AC12 专项断言）。`lag_eval.go:178` `trunkFamilyPrefixes` 含 `"Bridge-Aggregation"`。
- `internal/cli/parser.go:827-833` 接口视图 undo 的 **handled 模式**钩子（`applyUndoLAGInterface` / `applyUndoDHCPInterface`，未命中交回既有分支）—— P1-5 照此挂载。
- `internal/cli/lag_cmd.go:736-747` `applyUndoInterfaceTrunk`：`:744-747` 对非聚合口名直接 `Error: undo interface '%s' is not supported` —— **P1-6 的直接阻塞点**（`undo interface Tunnel0/0/1` 当前不可用），扩展时**不得改坏 Eth-Trunk 分支**。
- `internal/cli/parser.go:5419-5438` `buildSavedConfigSnapshot` 接口块内挂载 VRRP / STP / LAG / DHCP 配置行的范式 —— P0-12 的 GRE 段照此挂载；`:5440-5465` 三份**独立输出通道**范式（VRRP `vrrpInterfaces` / `buildSavedLAGConfig` / `buildSavedDHCPRelayConfig`）—— `buildSavedGREConfig` 照此实现。
- `internal/cli/dhcp_relay_display.go:273-330` `buildSavedDHCPRelayInterfaceConfig` / `buildSavedDHCPRelayConfig` —— **只输出差异值**口径模板（P0-12「缺省值不冗余输出」）。
- `internal/cli/capabilities.go:61` `"gre": l3Devices()`（**本期零改动**）；`:141-152` `isCommandSupported` 按首 token 匹配、**未声明默认放行**（P0-14 必须分支内守卫的依据）；`:174-181` `l3Devices()` 定义（Router / L3Switch / Firewall / VTEP，**复用不重定义**）。
- IPv4 校验范式：`internal/cli/parser.go:4539` `if net.ParseIP(vip) == nil`（VRRP virtual-ip）。
- 顶层命令 token 冲突核查：`parser.go` 顶层 `switch` **无** `tunnel-protocol` / `source` / `destination` / `keepalive` 的 case；`:946`/`:952` 的 `source`/`destination` 属 ACL rule 参数内层 switch，`:1295` 的 `keepalive` 属 `m-lag` 子命令内层 switch —— **均不冲突**。
- 课程依据：`docs/reference/huawei-vrp-course.md:68` 第 69 讲「GRE原理与配置 / 公网建隧道承载私网、站点互联 / `interface Tunnel`、`tunnel-protocol gre`」；`:92` 功能矩阵「GRE｜`tunnel gre`｜📋 Roadmap｜视频 69」（本期交付后需同步更新为 ✅）。

---

## 文档状态

- 基线核查完成：GRE 自造命令（`parser.go:2263-2292`）、`state.GRE` / `GREConfig`（`state.go:72`/`:382`/`:521`）、`display gre`（`parser.go:3517-3531`）、protocol 层死代码（`protocol.go:1370-1409`）、Tunnel 接口创建能力（`parser.go:315-412`）、能力矩阵（`capabilities.go:61`）、持久化挂载点（`parser.go:5419-5465`）均已核实到 file:line。
- **核心结论**：GRE **并非"完全缺失"，而是"以错误形态存在"** —— 一条 VRP 根本不存在的自造系统视图命令 + 一份不落盘的结构体事实源 + 一个 map 随机遍历的 display + 一份含硬编码 `Status:"up"` 的跨包死代码。本期是**纠错型重构**，不是从零新建，改造力度高于 DHCP 那轮（DHCP 是 1 个死字段，GRE 是一整条链路）。
- **最高危技术点（务必写进设计）**：`Bridge-Aggregation` 键名中的 `Ag-gre-gation` **天然含 `gre` 子串**，任何 `strings.Contains(k, "gre")` 式扫描都会把 H3C 聚合口键误判为 GRE 隧道，且级联清理会**误删聚合配置**。必须全程精确后缀/中缀/前缀匹配，AC12 已设专项断言。
- 需求池 **28 条**（P0 14 / P1 8 / P2 6），验收标准 **AC1–AC12**（AC11 拆为 11a/11b/11c），其中 **AC8 为诚实占位红线断言**（5 个运行态字段恒 `-`、`State` 列不得裸 `Up`），**AC2 为本期最大价值断言**（现状 save→reload 配置 100% 丢失）。
- **§6 的 9 项待确认（C1–C9）待主理人 / 架构师拍板**，其中 C1（删旧命令与结构体）、C2（keepalive 不做真实计时）、C4（隧道状态诚实口径）为**阻塞项**，建议优先闭合。拍板后由 PM 回填结论表并同步修订受影响的 AC。
- 键命名（`interface:<if>:tunnel-protocol` / `interface:<if>:gre-<field>`）为 PM 预对齐建议，**最终以架构师设计文档为准**。

_Last updated: 2026-08-09 · 产品经理 许清楚（Xu）_
