# ensp-lab P2 第五项：链路聚合 Eth-Trunk（华为 VRP 实训课程 63）增量 PRD

> 文档类型：增量产品需求文档（PRD，简单模式，结构对齐 `docs/p2-stp-prd.md` / `docs/p2-vrrp-prd.md`）
> 关联：`docs/p2-stp-prd.md`（STP 增量 PRD，纯函数/单一事实源/诚实占位范式基准）、`docs/p2-vrrp-prd.md`（VRRP 增量 PRD）、`docs/reference/huawei-vrp-course.md` 课程 63（验收 oracle）、`internal/cli/parser.go` / `state.go` / `capabilities.go` / `stp_eval.go` / `vrrp_eval.go`（已核查代码基线）
> 官方权威源（命令正确性佐证）：
> - ZH：S1700/S5700/S6700 配置指南-以太网交换（负载分担，缺省 `src-dst-ip`）— https://support.huawei.com/enterprise/zh/doc/EDOC1100366633/480e946
> - ZH：`interface eth-trunk` 命令参考（trunk-id 0~63、删除前须无成员）— https://support.huawei.com/hedex/pages/SC000063561730000990/01/SC000063561730000990/01/resources/ar/interface_eth-trunk.html
> - EN：Link Aggregation Commands（`mode`/`max active-linknumber`/`least active-linknumber`/`lacp timeout`）— https://support.huawei.com/enterprise/en/doc/EDOC1100320924/54c5f31b/link-aggregation-commands
> - EN：Creating an Eth-Trunk Interface and Configure a Link Aggregation Mode（`mode { manual load-balance | lacp-static | lacp-dynamic }`，缺省 manual load-balance）— https://support.huawei.com/enterprise/en/doc/EDOC1100458999/d542520f/creating-an-eth-trunk-interface-and-configure-a-link-aggregation-mode-for-it
> - EN：Configuring Link Aggregation in Static LACP Mode（`lacp priority` 缺省 32768、`lacp preempt delay` 缺省 30s、`max active-linknumber` 缺省 8、`least active-linknumber` 缺省 1）— https://support.huawei.com/enterprise/kr/doc/EDOC1000018101/cd7336e/configuring-link-aggregation-in-static-lacp-mode
> - ZH：S 系列交换机链路聚合的负载分担方式（V200，`load-balance` 取值集 + `Hash arithmetic` 映射）— https://support.huawei.cn/enterprise/zh/doc/EDOC1100092150
> 作者：产品经理 许清楚（Xu）
> 语言：中文

---

## 0. 项目信息

- **Language**：中文
- **Programming Language**：Go 1.26.5（本期工作集中在 `internal/cli`；前端 React/Vite 本期无变更）
- **Project Name**：`p2_lag`
- **原始需求复述**：在 P2 已交付 NAT（课程 38）、端口安全（课程 49）、VRRP（课程 60/61）、STP/RSTP/MSTP（课程 55/56/57）之后，为华为 eNSP VRP 仿真器落地 **链路聚合 Eth-Trunk（课程 63）** 的增量实现：把 `parser.go` 中已有的一组「只记键值、不做聚合行为判定、成员事实源双写、display 非 VRP 保真、无诚实占位、无能力守卫」的链路聚合残桩，升级为「命令对齐官方 VRP、全部经 `DeviceConfig` 单一事实源持久化、纯函数聚合状态评估（成员 up/down ↔ 聚合口 up/down 联动 + LACP 静态模式活动/备份端口本地选举）、`display eth-trunk` 忠实呈现、诚实占位标注」的二层链路捆绑链路，并补齐 `undo eth-trunk` / `undo interface Eth-Trunk` / `display current-configuration` 的 Eth-Trunk 段。

> **深度边界先验结论（务必先读 §6 拍板项）**：链路聚合的真实"活动接口选举"依赖设备两端交换 **LACPDU**（LACP 协议报文），真实"负载均衡"依赖数据面按哈希算法逐流分发、并产生**真实收发包计数**。当前 sim 引擎**无 LACPDU 收发、无 L2 数据面转发、无真实流量统计**（与 VRRP 无心跳、STP 无 BPDU 同源）。因此本期：
> - `display eth-trunk` 的 **Partner（对端）块**、`PortKey` / `PortState` 位图、真实 `Weight` 带宽权重、真实收发包/字节计数 —— **一律不可编造**，必须以诚实占位（"未接入真实数据源 / 需对端 LACPDU 协商"）呈现；
> - 活动/备份端口（Selected/Unselect）为**本地静态选举**（按官方选举因子的本地可判定子集），必须叠加 `lagSimNote()` 诚实注记；
> - 负载均衡算法**仅记录配置态并映射为 `Hash arithmetic` 展示串**，不做真实哈希分流（见 §6 #4）。
>
> 代码基线里已存在**一组不合规残桩**（配置侧 6 处 + display 侧 2 处，见 §3），本期必须按架构基线扩展/重写，而非另起炉灶。

---

## 1. 产品目标

在严守 P2「CLIState 层纯函数、单一事实源 `DeviceConfig`、诚实占位、零改动 `sim` 引擎、零新增第三方依赖」架构基线的前提下，把链路聚合从代码里"一组只记键值、无聚合行为、成员双写、display 非保真、无能力守卫"的残桩，升级为一条**可对学员实验产生可观测反馈**的二层链路捆绑链路：

1. **配置真实性 + 持久化**：命令集对齐官方 VRP 课程 63（`interface Eth-Trunk` / `mode manual load-balance | lacp-static` / `trunkport` / `eth-trunk <id>` / `load-balance` / `least|max active-linknumber` / `lacp priority|preempt|timeout`）；全部状态经 `DeviceConfig["interface:<iface>:lag:<field>"]`（聚合口级）、`DeviceConfig["interface:<member>:eth-trunk"]`（成员归属，**唯一成员事实源**）、`DeviceConfig["lacp:<field>"]`（系统级）持久化，**消除 `interface:<trunk>:members` 双写事实源**，并在 `display current-configuration` 新增 Eth-Trunk 段，根治 save→reload 丢配置。
2. **聚合行为真实性（本期核心增量）**：新增纯函数 `internal/cli/lag_eval.go`，实现"成员端口 up/down ↔ 聚合口 up/down 联动"（活动成员数 < `least active-linknumber` → 聚合口 Down；≥ → Up；无成员 → Down）与"LACP 静态模式活动/备份端口本地选举"（按端口 LACP 优先级小者优先、同优先级按接口名有序 tie-break，受 `max active-linknumber` 上限约束）。这是当前残桩**完全缺失**的部分——现状把聚合口状态硬编码为 `Up`（`parser.go:757`），属于编造状态。
3. **展示忠实性 + 诚实占位**：重写 `display eth-trunk [<id> [verbose]]`（手工/LACP 两套官方字段集）、新增 `display eth-trunk <id> load-balance` / `display trunkmembership eth-trunk <id>`；所有引擎无法真实产出的数据（Partner 块、PortKey/PortState 位图、真实流量统计、带宽权重）一律诚实占位，**绝不编造数字**；输出确定性（成员按接口名排序，杜绝现状 map 遍历导致的随机顺序）。
4. **能力矩阵收敛**：链路聚合命令仅在 `switchDevices()`（Switch/L3Switch/VTEP）可用，非交换机设备明确拒绝——现状 `eth-trunk`/`trunkport`/`mode`/`load-balance`/`link-aggregation` **均未进能力矩阵**（`isCommandSupported` 未声明默认放行，`capabilities.go:136-138`），PC/Router 也能配 Eth-Trunk，属能力越界。

---

## 2. 用户故事

1. **作为交换实验学员（手工模式，课程 63 主线）**：As a 学员，I want 在交换机上依次敲 `interface Eth-Trunk 1` → `mode manual load-balance` → `trunkport GigabitEthernet 0/0/1 to 0/0/3` → `load-balance src-dst-mac`，so that 三条物理链路被捆绑成一条逻辑链路，能用 `display eth-trunk 1` 核对 `WorkingMode / Hash arithmetic / Operate status / Number Of Up Port In Trunk` 与成员列表。
2. **作为成员口视角的学员**：As a 学员，I want 在物理接口视图敲 `eth-trunk 1` 把端口加入聚合组、敲 `undo eth-trunk` 退出聚合组，so that 我能理解"成员归属由成员口配置决定"，且 `display eth-trunk 1` 的成员列表随之增减。
3. **作为验证聚合状态联动的学员（本期核心增量）**：As a 学员，I want 对 Eth-Trunk 1 配置 `least active-linknumber 2`，然后把其中两个成员口 `shutdown`，so that 我能看到 `Operate status` 由 `up` 变为 `down`、`Number Of Up Port In Trunk` 从 3 变为 1，直观理解"活动接口数下限阈值"的作用；而不是像现状那样聚合口恒显示 Up。
4. **作为 LACP 模式学员**：As a 学员，I want 配置 `mode lacp-static` + `max active-linknumber 2` + 成员口 `lacp priority 100`，so that 我能看到本地静态选举出的 `Selected` / `Unselect` 端口（优先级小者被选中、超出上限者转备份），对照课程 63 理解 M:N 备份，并明确知道这是"本地假设选举、非真实 LACPDU 协商"。
5. **作为关注持久化的学员**：As a 学员，I want `save` 后 `reload` 设备，so that 聚合配置（Eth-Trunk 接口、mode、成员归属、load-balance、least/max active-linknumber、lacp 参数）仍保留，`display eth-trunk` 与 `display current-configuration` 完整复现，而不必重配。
6. **作为排障学员**：As a 学员，I want 用 `display eth-trunk` 一眼看到所有聚合组摘要、用 `display eth-trunk 1 verbose` 看单组详情，so that 快速定位哪个成员没 Up、聚合口为什么是 Down；并明确知道哪些字段是"仿真未接入真实数据源"。

---

## 3. 需求池

> 共 **40 条**：P0 **18 条**、P1 **13 条**、P2 **9 条**（另列「已有」基线 10 条，属重构对象非新需求）。

### 已有（本期重构 / 扩展，非另起炉灶）

| 标记 | 内容 | 位置 |
|---|---|---|
| [已有·不合规] | 物理接口视图 `eth-trunk <id>`：写 `interface:<iface>:eth-trunk`=`<id>`（**此键设计正确，保留为唯一成员事实源**），但同时把聚合口 `interface:<trunk>:status` **硬编码为 `Up`**（编造状态，违反诚实占位）、并**重复写** `interface:<trunk>:members` 逗号串（**双写事实源**）；无 trunk-id 范围校验、无"成员不得为 Eth-Trunk 本身"校验、无"一口只能属一个 trunk"校验、无 8 成员上限校验；回显 `Port added to Eth-Trunk 1` 非 VRP 风格 | `parser.go:743-781` |
| [已有·不合规] | Eth-Trunk 视图 `mode <x>`：仅取 `cmd.Args[0]`，**无取值校验**（任意串可写入），且官方 `mode manual load-balance` 为**两 token**，现状会丢掉 `load-balance` 只存 `manual`；键为 `interface:<trunk>:mode`（未命名空间化）；回显 `Aggregation mode set to %s` 非 VRP 风格 | `parser.go:783-792` |
| [已有·不合规] | Eth-Trunk 视图 `trunkport <type> <num> [to <type> <num>]`：写 `interface:<trunk>:members`（**双写事实源**，且 `display eth-trunk` 根本不读此键 → 经 `trunkport` 加的成员在 display 中不可见）；`to` 语法要求 `to <type> <num>`，**与官方 `trunkport GigabitEthernet 0/0/1 to 0/0/3`（类型只写一次）不符**；范围不展开为逐个成员；无任何合法性校验 | `parser.go:793-818` |
| [已有·不合规] | Eth-Trunk 视图 `load-balance <mode>`：`strings.Join(cmd.Args, " ")` 原样存入 `interface:<trunk>:load-balance`，**无取值枚举校验**；回显非 VRP 风格 | `parser.go:819-828` |
| [已有·H3C 变体] | `port link-aggregation group <id>`（物理口视图）：写 `interface:<iface>:eth-trunk`，但聚合口命名为 `Bridge-Aggregation<id>`，与同键的 Eth-Trunk 语义**共用一个键却映射两种聚合口名**（是 `display link-aggregation summary` 幽灵组缺陷的根因） | `parser.go:675-705` |
| [已有·H3C 变体] | `link-aggregation mode <dynamic\|static>`：写 `interface:<trunk>:mode`，无视图/取值校验 | `parser.go:829-840` |
| [已有·不合规·桩] | `display eth-trunk [id]`：**已存在**（与任务简报"缺失"不符，已更正）。缺陷：① 仅从 `interface:*:eth-trunk` 反查成员，**不读 `:members` 键** → `trunkport` 加的成员不可见；② `for trunkName, members := range trunkMap` **map 遍历，输出顺序随机**（golden 测试不可能稳定）；③ 字段集 `Eth-Trunk1 (Mode: x, Load-Balance: y) / Status / Member ports` **完全非官方格式**（官方为 `WorkingMode / Hash arithmetic / Least Active-linknumber / Max Bandwidth-affected-linknumber / Operate status / Number Of Up Port In Trunk / PortName Status Weight`）；④ 聚合口 `Status` 直读硬编码键，与成员状态**无联动**；⑤ 成员 status 空值默认 `"Up"`（编造）；⑥ **无诚实占位注记**；⑦ 仅守 `isHost/isCloudHub`，Router 可执行 | `parser.go:2579-2637` |
| [已有·不合规·桩] | `display link-aggregation summary`：第二个循环对**同一批 `:eth-trunk` 键**再生成一遍 `Bridge-Aggregation<id>` 条目 → 一台只配了 Eth-Trunk 的设备会**同时显示 `Eth-Trunk1` 和幽灵的 `Bridge-Aggregation1`**（编造数据，CRITICAL 违规）；同样 map 遍历顺序随机 | `parser.go:2639-2703` |
| [已有] | 接口名解析已支持 `Eth-Trunk`/`Eth-trunk`/`ET`/`Bridge-Aggregation`/`BAGG` 前缀与 `interface Eth-Trunk 1`（带空格）形态；`interface` 命令会创建 `state.Interfaces[name]` 且 `status` 缺省写 `Up`（对 Eth-Trunk 而言是**编造**，无成员的 Eth-Trunk 真机为 Down）；`tools.go:78-80` 有 `display` 缩写 `eth-trunk`/`et`/`eth` → `eth-trunk` | `parser.go:310-372`、`tools.go:78-80` |
| [已有·基线] | 持久化机制 `SerializeToDeviceConfigData`（`parser.go:5249`，仅遍历 `state.DeviceConfig` 落盘）↔ `LoadFromDeviceConfigData`（`parser.go:5280`，回写 `DeviceConfig` + 重建 ISIS/OSPF/BGP 结构化字段）；`buildSavedConfigSnapshot`（`parser.go:5463`）已有 STP 段（`buildSavedSTPConfig`/`buildSavedSTPInterfaceConfig`）与 VRRP 段（`buildSavedVRRPConfig` + "独立输出通道"补齐 reload 后 `state.Interfaces` 缺失的接口块），**但无 Eth-Trunk 段**；`LoadFromDeviceConfigData` **不重建 `state.Interfaces`** → reload 后 Eth-Trunk 逻辑口从 `state.Interfaces` 消失 | `parser.go:5249,5280,5463-5540` |
| [已有·基线] | 纯函数评估器 + 诚实占位范式：`aclSimNote()`/`natSimNote()`（`acl_eval.go:493-507`）、`portSecSimNote()`（`portsec_eval.go:236-244`）、`vrrpSimNote()`（`vrrp_eval.go:397-402`）、`stpSimNote()`（`stp_eval.go:290-296`）；`EvaluateSTP`/`EvaluateVRRP` 无副作用、不写引擎、不 import `internal/protocol`、可单测；`stp_eval.go` 已有 `sortedInterfaceNames`（确定性排序）可复用 | `acl_eval.go`、`portsec_eval.go`、`vrrp_eval.go`、`stp_eval.go:304-316` |
| [已有] | 能力矩阵：`"lacp": switchDevices()`（`capabilities.go:78`）**仅覆盖 `lacp` 顶层命令**；`eth-trunk`/`trunkport`/`mode`/`load-balance`/`link-aggregation` **均未声明** → `isCommandSupported` 默认放行（`capabilities.go:136-138`），PC/Router 可配聚合 | `capabilities.go:78,136-138,191-197` |
| [已有·正交] | `lacp m-lag priority\|system-id`（`parser.go:1489-1514`）属 M-LAG 跨设备聚合，与本期基础聚合正交，**本期不动**（仅需保证新增 `lacp priority` 子命令不与之冲突） | `parser.go:1489-1514` |
| [已有] | 无任何 Eth-Trunk / 链路聚合相关测试（`grep -i eth-trunk internal/cli/*_test.go` 为空）；`state.go` 无 LAG 结构体（**符合基线，本期也不得新增**） | `internal/cli/*_test.go`、`state.go` |

### P0（本期核心 · 命令对齐官方 + 单一事实源贯通 + 聚合行为判定 + display 忠实 + 诚实占位）

> 每条命令标注 **[官方依据]**：ZH1=S1700/S5700/S6700 配置指南-以太网交换(EDOC1100366633/480e946)；CMD=`interface eth-trunk` 命令参考(SC000063561730000990)；EN1=Link Aggregation Commands(EDOC1100320924/54c5f31b)；EN2=Creating an Eth-Trunk Interface…(EDOC1100458999/d542520f)；EN3=Configuring Link Aggregation in Static LACP Mode(EDOC1000018101/cd7336e)；ZH2=S 系列链路聚合负载分担方式(EDOC1100092150)。

**A. 键命名与单一事实源（架构缺陷修复）**

- **[P0-1 键命名空间统一 + 消除双写事实源]**：聚合口属性统一走 `interface:<Eth-TrunkN>:lag:<field>`（`mode` / `load-balance` / `least-active-linknumber` / `max-active-linknumber` / `preempt` / `preempt-delay` / `lacp-timeout`），与 STP 的 `interface:<iface>:stp:<field>` 命名一致；**成员归属唯一事实源** = `interface:<member>:eth-trunk` = `<trunk-id>`（保留既有键，键名不变以兼容历史配置）；系统级 LACP 走 `lacp:<field>`。**废弃并停止写入 `interface:<trunk>:members`**（`trunkport` 改为逐个展开成员、回写 `interface:<member>:eth-trunk`），彻底消除"成员在两处记录、display 只读其一"的双写缺陷。旧键 `interface:<trunk>:mode` / `:load-balance` / `:members` 的迁移策略见 §6 #1。**[官方依据]** 架构基线（无对应官方命令，属缺陷修复）。
- **[P0-2 `display current-configuration` 新增 Eth-Trunk 段]**：新增 `buildSavedLAGConfig(state)`（聚合口块：`interface Eth-TrunkN` / ` mode ...` / ` load-balance ...` / ` least active-linknumber N` / ` max active-linknumber N`）与 `buildSavedLAGInterfaceConfig(state, iface)`（成员口行：` eth-trunk N` / ` lacp priority N`），挂入 `buildSavedConfigSnapshot`（`parser.go:5463`），并**复用 VRRP 的"独立输出通道"范式**（`parser.go:5528-5540`）为 reload 后不在 `state.Interfaces` 中的 Eth-Trunk 逻辑口补齐 `interface Eth-TrunkN` 块。输出顺序按接口名排序，保证快照稳定。**[官方依据]** ZH1 配置文件样例（`interface Eth-Trunk1` / `load-balance src-dst-mac` / 成员口 `eth-trunk 1`）。
- **[P0-3 reload 后聚合口重建]**：`LoadFromDeviceConfigData`（`parser.go:5280`）增加 LAG 重建分支——扫描 `interface:*:eth-trunk` 与 `interface:Eth-Trunk*:lag:*` 键，把缺失的 Eth-Trunk 逻辑口回填 `state.Interfaces`（`Name` / `Status` 由 `EvaluateLAG` 派生，**不硬编码 Up**），对齐 OSPF/BGP/ISIS 重建范式，修掉 reload 后 Eth-Trunk 从 `display interface` / `display current-configuration` 消失的缺陷。**[官方依据]** 架构基线（缺陷修复）。

**B. 配置命令（对齐官方 VRP）**

- **[P0-4 `interface Eth-Trunk <0-63>`（系统视图）]**：创建/进入 Eth-Trunk 接口视图；`trunk-id` 严格校验 **0~63**，越界 → `Error: invalid Eth-Trunk ID (0-63)`；**新建时聚合口状态不得硬编码 `Up`**（无成员的 Eth-Trunk 真机为 Down，须由 `EvaluateLAG` 派生，见 P0-11）。提示符为 `[<sysname>-Eth-Trunk1]`（既有接口视图提示符已满足）。**[官方依据]** CMD `interface eth-trunk trunk-id`，trunk-id 整数 0~63。
- **[P0-5 `undo interface Eth-Trunk <id>`（系统视图）]**：删除 Eth-Trunk 接口，清理 `interface:Eth-TrunkN:*` 全部键与 `state.Interfaces` 条目；**存在成员时必须拒绝** → `Error: The Eth-Trunk interface has member ports, please delete them first`（官方硬约束：删除 Eth-Trunk 时其中不能有成员接口）。当前 `applyUndoSystemFeature`（`parser.go:5131`）无 `interface` 分支，需新增。**[官方依据]** CMD「删除 Eth-Trunk 时，Eth-Trunk 中不能有成员接口」。
- **[P0-6 `mode { manual load-balance | lacp-static }`（Eth-Trunk 视图）]**：**修复两 token 解析**（`manual load-balance` 必须整体识别，现状只存 `manual`）；写 `interface:<trunk>:lag:mode`，取值仅放行 `manual load-balance` / `lacp-static`（`lacp-dynamic` 见 P2-1）；缺省 **`manual load-balance`**；非法取值 → `Error: unrecognized command`。官方约束「LACP 模式改回手工模式前，Eth-Trunk 必须无成员接口」按 §6 #5 拍板决定是否强制。`undo mode` 恢复缺省。**[官方依据]** EN1/EN2：`mode { manual load-balance | lacp-static | lacp-dynamic }`，By default manual load balancing mode。
- **[P0-7 `trunkport <interface-type> <interface-number> [to <interface-number>]`（Eth-Trunk 视图）]**：**修正 `to` 语法**为官方形态（接口类型只写一次，如 `trunkport GigabitEthernet 0/0/1 to 0/0/3`）；把范围**展开为逐个成员**并逐个写 `interface:<member>:eth-trunk`（不再写 `:members` 串）；支持一条命令多个成员（`&<1-8>`）。校验见 P0-9。回显 VRP 风格（成功静默 / 仅报错）。**[官方依据]** EN2/课程 63 实操：`[S3-Eth-Trunk1] trunkport GigabitEthernet 0/0/1 to 0/0/3`。
- **[P0-8 `eth-trunk <id>` / `undo eth-trunk`（物理接口视图）]**：`eth-trunk <id>` 保留既有键写入语义但**移除聚合口 status 硬编码 Up 与 `:members` 双写**；**新增 `undo eth-trunk`**（当前接口视图 `undo` 分支无此项，`parser.go:866-900`）删除 `interface:<member>:eth-trunk` 与该口 `lacp:*` 键，使其退出聚合组。**[官方依据]** ZH1 配置文件样例成员口 `eth-trunk 1`；CMD `undo eth-trunk`。
- **[P0-9 成员加入合法性校验（官方硬约束）]**：`eth-trunk <id>` 与 `trunkport` 共用校验器，任一不满足即明确 `Error:`——① 目标 Eth-Trunk 必须已存在（未创建 → `Error: Eth-Trunk 1 does not exist`，见 §6 #2）；② 一个以太接口只能加入一个 Eth-Trunk（已属其他组 → `Error: The interface has been added to Eth-Trunk N`）；③ Eth-Trunk 不能作为另一个 Eth-Trunk 的成员（`Error: An Eth-Trunk interface cannot be a member of another Eth-Trunk`）；④ 单个 Eth-Trunk 成员数上限 **8**（超限 → `Error: The number of member interfaces exceeds the upper limit (8)`）；⑤ trunk-id 范围 0~63。**[官方依据]** ZH1/配置规则：一个 Eth-Trunk 最多 8 个成员口、一个以太接口只能加入一个 Eth-Trunk、Eth-Trunk 不能充当其他 Eth-Trunk 的成员口。
- **[P0-10 `load-balance { dst-ip | dst-mac | src-ip | src-mac | src-dst-ip | src-dst-mac }`（Eth-Trunk 视图）]**：**新增取值枚举校验**（现状任意串可写）；写 `interface:<trunk>:lag:load-balance`；缺省 **`src-dst-ip`**（现状 display 默认 `src-dst-mac` 是**错误缺省**，须改）；`undo load-balance` 恢复缺省。展示时映射为官方 `Hash arithmetic` 串：`src-dst-mac`→`According to SA-XOR-DA`、`src-dst-ip`→`According to SIP-XOR-DIP`、`src-mac`→`According to SA`、`dst-mac`→`According to DA`、`src-ip`→`According to SIP`、`dst-ip`→`According to DIP`。**[官方依据]** ZH1/ZH2：取值集 + 缺省 `src-dst-ip` + `Hash arithmetic` 字段映射（`load-balance src-dst-mac` → `SA-XOR-DA`）。

**C. 聚合行为判定（本期核心增量，纯函数）**

- **[P0-11 新增 `internal/cli/lag_eval.go` 纯函数评估器]**：范式严格对照 `stp_eval.go` / `vrrp_eval.go`——**无副作用、不修改 `sim` 引擎、不 import `internal/protocol`、零新第三方依赖、可单测**；仅读 `state.DeviceConfig` 派生只读视图，**不得在 `CLIState` 上新增 `state.LAG` 内嵌结构体**。建议契约：
  - `collectLAGTrunks(state) []int`：扫描 `interface:*:eth-trunk` 与 `interface:Eth-Trunk*:lag:*`，返回**升序**去重的 trunk-id 列表（确定性）。
  - `collectLAGMembers(state, trunkID) []LAGMember`：从 `interface:<member>:eth-trunk` 反查成员，按接口名**排序**（复用 `stp_eval.go:304` `sortedInterfaceNames` 口径），每个成员带 `Name / Up / LACPPriority / Selected`。
  - `EvaluateLAG(state, trunkID) LAGResult`：返回 `TrunkName / Mode / LoadBalance / HashArithmetic / LeastActive / MaxActive / OperateStatus / UpPortCount / Members[]`。
  - `SelectLACPActivePorts(members []LAGMember, maxActive int) []LAGMember`：LACP 静态模式活动端口本地选举（见 P0-13）。
  - `CompareLACPPort(a, b LAGMember) int`：选举 tie-break 比较器（可独立单测，对照 `CompareBridgeID` / `CompareVRRPPriority`）。
  - `lagSimNote() string`：诚实占位注记（见 P0-15）。
- **[P0-12 成员 up/down ↔ 聚合口 up/down 联动]**：`EvaluateLAG` 按下列规则派生 `Operate status`（**不再读硬编码的 `interface:<trunk>:status`**）——① 无成员 → `down`；② 活动成员数（`interface:<member>:status != "Down"` 且未被 LACP 选举排除）< `least active-linknumber` → `down`；③ 否则 → `up`。`Number Of Up Port In Trunk` = 活动成员数。成员 up/down 复用既有接口 `shutdown`/`undo shutdown` 写入的 `interface:<iface>:status` 键（对照 `stp_eval.go:175 isPortDown` / `vrrp_eval.go:462 isInterfaceDown`），**不臆造链路事件**。**[官方依据]** EN1「When the number of active interfaces falls below this threshold, the Eth-Trunk goes Down」。
- **[P0-13 LACP 静态模式活动/备份端口本地选举]**：`mode lacp-static` 时按官方选举因子的**本地可判定子集**计算 `Selected` / `Unselect`——排序键 =（成员端口 `lacp priority` 升序，缺省 32768）→（接口名字典序升序，作为 PortNo 的确定性替代）；取前 `max active-linknumber`（缺省 8）个为 `Selected`，其余为 `Unselect`；Down 的成员恒 `Unselect`。**官方真实选举还依赖两端 System Priority/System ID 决定 Actor 端与 Partner 的 PortNo/PortKey，本仿真无对端 → 本地恒假设本端为 Actor，并诚实注记**（见 §6 #3）。`mode manual load-balance` 时**不做 Selected/Unselect 选举**（手工模式所有 Up 成员均转发，官方 `display` 也只有 `PortName Status Weight` 三列）。**[官方依据]** EN3：系统优先级小者为 Actor、同优先级比 MAC 小者；接口 LACP 优先级缺省 32768；`max active-linknumber` 限制活动接口数，其余为 backup。
- **[P0-14 `least active-linknumber <n>` / `max active-linknumber <n>`（Eth-Trunk 视图）]**：分别写 `interface:<trunk>:lag:least-active-linknumber`（缺省 **1**）与 `:lag:max-active-linknumber`（缺省 **8**）；校验 `1 ≤ least ≤ max ≤ 8`（成员上限 8），互相矛盾 → 明确 `Error:`；`max active-linknumber` **仅 LACP 模式有效**，手工模式下执行给出官方语义提示（见 §6 #6）；`undo` 恢复缺省。**[官方依据]** EN1/EN3：least 缺省 1、max 缺省 8、max 仅 LACP 模式有效、须 max ≥ least。
- **[P0-15 `lagSimNote()` 诚实占位（CRITICAL）]**：lite →「（链路聚合为模拟聚合（lite 引擎），非内核级真实 LACPDU 协商 / 无真实流量哈希分担）」；full →「（链路聚合为模拟聚合）」，口径与 `aclSimNote()`/`natSimNote()`/`portSecSimNote()`/`vrrpSimNote()`/`stpSimNote()` 完全一致（读 `sim.EngineModeName()`）。所有 `display eth-trunk*` 输出末尾必须附加。**[官方依据]** 架构基线（项目核心价值观）。

**D. 展示与守卫**

- **[P0-16 `display eth-trunk [<trunk-id>] [verbose]`（重写）]**：读 `EvaluateLAG` 派生（非硬编码键），**按 trunk-id 升序、成员按接口名升序输出**（消除现状 map 随机序）；手工模式与 LACP 模式两套官方字段集（详见 §4）；无聚合组 → `Error: The Eth-Trunk does not exist` / 空列表提示；末尾附 `lagSimNote()`。**Partner 块、PortKey / PortState 位图、Weight 带宽权重一律诚实占位，不得编造**（见 §6 #3）。**[官方依据]** ZH1/EN2 `display eth-trunk [ trunk-id [ verbose ] ]` 及其官方输出样例。
- **[P0-17 能力矩阵收敛]**：`capabilities.go` 新增 `"eth-trunk": switchDevices()`、`"trunkport": switchDevices()`、`"load-balance": switchDevices()`；`"lacp"` 已有保持不变。**`mode` 不入顶层能力矩阵**（`mode` 是通用词，入表会误伤其他设备的其他 `mode` 命令），改为在 `case "mode"` 内部做设备类型守卫；`link-aggregation`（H3C 变体）按 §6 #7 拍板决定。非交换机执行 → 能力拒绝（沿用 `parser.go:245` `isCommandSupported`）。**[官方依据]** 架构基线（能力矩阵约束）。
- **[P0-18 拒错与视图守卫 + VRP 风格回显]**：Eth-Trunk 视图命令（`mode` / `trunkport` / `load-balance` / `least|max active-linknumber` / `lacp preempt|timeout`）在非 Eth-Trunk 接口视图执行 → `Error: ... only available in Eth-Trunk interface view`；`eth-trunk <id>` 在非物理接口视图执行 → 明确拒绝；全部配置命令回显改为 **VRP 风格（成功静默或规范回显，失败 `Error:`）**，去掉 `Port added to Eth-Trunk 1` / `Aggregation mode set to x` / `Load balance mode set to x` 等非 VRP 文案（对照 STP P0-15 同款整改）。**[官方依据]** VRP 行为（配置命令成功静默 / 仅报错）。

### P1（LACP 参数 + 辅助 display + H3C 变体规整 · 建议默认纳入）

- **[P1-1 `lacp priority <0-65535>`（成员物理接口视图）]**：写 `interface:<member>:lacp:priority`，缺省 **32768**，值越小优先级越高；作为 P0-13 本地选举的**主排序键**。须与既有 `lacp m-lag ...`（`parser.go:1489`）分派共存不冲突。**[官方依据]** EN3「接口 LACP 优先级缺省 32768」。
- **[P1-2 `lacp priority <0-65535>`（系统视图，系统 LACP 优先级）]**：写 `lacp:priority`，缺省 **32768**；仅配置态持久化 + `display eth-trunk` 的 `System Priority` 字段展示（真实 Actor 判定需对端 System ID 比较，本仿真无对端 → 诚实注记）。**[官方依据]** EN3「系统 LACP 优先级缺省 32768，小者为 Actor；同优先级比 MAC 小者」。
- **[P1-3 `lacp preempt enable` / `lacp preempt delay <0-180>`（Eth-Trunk 视图）]**：写 `interface:<trunk>:lag:preempt`（缺省 **disabled**）/ `:lag:preempt-delay`（缺省 **30** 秒）；仅配置态 + 展示（`Preempt Delay: Disabled` / `<n> s`），**无真实抢占时序模拟**，诚实注记。**[官方依据]** EN3「LACP 抢占缺省关闭；抢占延时缺省 30 秒」。
- **[P1-4 `lacp timeout { fast | slow }`（Eth-Trunk 视图）]**：写 `interface:<trunk>:lag:lacp-timeout`；仅配置态 + 展示（fast=1s 发包 / slow=30s 发包，接收超时缺省 90s），**不模拟真实超时**，诚实注记。**[官方依据]** EN1「fast → 对端每 1 秒发一个 LACPDU；slow → 每 30 秒；接收 LACPDU 超时缺省 90s」。
- **[P1-5 `display eth-trunk <id> load-balance`]**：输出 `Eth-TrunkN's load-balance information:` + `Load-balance Configuration: <SA-XOR-DA|SIP-XOR-DIP|...>`；`Load-balance options used per-protocol` 明细为真实数据面派生 → **诚实占位**（"未接入真实数据面，仅展示配置态"）。**[官方依据]** ZH2 官方输出样例。
- **[P1-6 `display trunkmembership eth-trunk <id>`]**：输出该 Eth-Trunk 的成员接口清单（成员名 + 状态 + 是否 Selected），按接口名排序 + `lagSimNote()`。**[官方依据]** ZH1「执行命令 `display trunkmembership eth-trunk trunk-id`，查看 Eth-Trunk 的成员接口信息」。
- **[P1-7 `display eth-trunk <id> interface <interface-type> <interface-number>`]**：单成员口在聚合中的状态详情。**[官方依据]** ZH1 `display eth-trunk [ trunk-id [ interface interface-type interface-number | verbose ] ]`。
- **[P1-8 `display interface Eth-Trunk<id>` 状态联动]**：既有通用 `display interface` 对 Eth-Trunk 的 `current state` 改为读 `EvaluateLAG` 派生状态，与 `display eth-trunk` 的 `Operate status` 保持一致，避免"两处 display 说法不一"。
- **[P1-9 `undo trunkport <interface-type> <interface-number> [to <interface-number>]`（Eth-Trunk 视图）]**：从聚合组移除成员（等价于对每个成员执行 `undo eth-trunk`）。**[官方依据]** VRP `undo trunkport`。
- **[P1-10 修复 `display link-aggregation summary` 幽灵 Bridge-Aggregation 缺陷（CRITICAL）]**：删除第二个把同批 `:eth-trunk` 键再映射成 `Bridge-Aggregation<id>` 的循环（`parser.go:2666-2691`）——该循环使一台只配 Eth-Trunk 的设备**凭空多出一个不存在的聚合组**，属编造数据。改为：仅当成员口确由 H3C 变体 `port link-aggregation group` 加入时才归为 `Bridge-Aggregation`（需 P1-11 的区分键），并按名称排序输出。
- **[P1-11 H3C 变体消歧]**：`port link-aggregation group <id>` 与 `link-aggregation mode <dynamic|static>` 增加区分键 `interface:<member>:agg-family` = `h3c`（缺省 `huawei`），使聚合口名（`Bridge-Aggregation<id>` vs `Eth-Trunk<id>`）可确定推导，消除同键双语义。是否保留 H3C 变体见 §6 #7。
- **[P1-12 `display eth-trunk` 无参数汇总视图]**：无 trunk-id 时按 trunk-id 升序输出所有聚合组的**完整块**（对齐官方，非自造摘要表），并在无任何聚合组时输出明确提示而非空串。
- **[P1-13 `?` 帮助与缩写补齐]**：Eth-Trunk 视图 `?` 列出 `mode` / `trunkport` / `load-balance` / `least active-linknumber` / `max active-linknumber` / `lacp`；`tools.go` 补 `display trunkmembership` 缩写归一化（`et`/`eth`/`eth-trunk` 已存在，`tools.go:78-80`）。

### P2（增强 / 诚实边界 · out-of-scope）

- **[P2-1 `mode lacp-dynamic`]**：官方存在但课程 63（手工模式 + LACP 静态）未覆盖，且动态 LACP 需真实协商；本期 `mode` 校验**不放行** `lacp-dynamic`。
- **[P2-2 真实 LACPDU 协商与 Partner 块]**：`display eth-trunk` 的 `Partner:` 段（PartnerPortName / SysPri / SystemID / PortPri / PortNo / PortKey / PortState）**完全依赖对端 LACPDU**，本期**不实现、不编造**，以诚实占位一行替代（见 §4）。
- **[P2-3 真实负载均衡哈希分流]**：`load-balance` 仅记录配置态 + 映射 `Hash arithmetic` 展示串，**不做真实哈希计算与逐流分发**（无数据面）；`display eth-trunk ... load-balance` 的 per-protocol 明细为诚实占位。
- **[P2-4 真实流量统计 / 带宽聚合]**：成员口与聚合口的 `input/output packets`、`bits/sec`、带宽叠加为真实数据面产物，**一律不编造**；`Weight` 列按官方缺省恒为 `1`（配置态，非实测带宽权重），并诚实标注。
- **[P2-5 跨设备两端聚合一致性校验]**：真机要求两端 mode 一致才 Up、两端 max active-linknumber 取小值等，需拓扑对端信息与协商；本期 out-of-scope，仅本地判定 + 诚实注记。
- **[P2-6 Eth-Trunk 子接口 / L3 模式切换]**：`interface eth-trunk <id>.<subnumber>`、`portswitch` / `undo portswitch`（二三层切换）本期不实现。
- **[P2-7 `load-balance enhanced profile` / `round-robin` / 弹性 HASH]**：增强负载分担模板与弹性 HASH 本期不实现，`load-balance` 仅放行六种基础取值。
- **[P2-8 `lacp stable-preferred` / `lacp mixed-rate link enable` 等 LACP 高级项]**：本期不实现。
- **[P2-9 前端展示]**：本期无前端变更；不新增 API 字段、不做拓扑图聚合链路合并渲染（与 NAT / 端口安全 / VRRP / STP 一致）。

---

## 4. UI / 展示设计稿（CLI 回显与 display 输出样例，纯文本）

- **配置回显（手工模式，课程 63 主线；VRP 风格静默成功）**：
  ```
  [SW1] interface Eth-Trunk 1
  [SW1-Eth-Trunk1] mode manual load-balance
  [SW1-Eth-Trunk1] trunkport GigabitEthernet 0/0/1 to 0/0/3
  [SW1-Eth-Trunk1] load-balance src-dst-mac
  [SW1-Eth-Trunk1] quit
  [SW1] interface GigabitEthernet0/0/4
  [SW1-GigabitEthernet0/0/4] eth-trunk 1
  ```
- **配置回显（LACP 静态模式）**：
  ```
  [SW1] interface Eth-Trunk 1
  [SW1-Eth-Trunk1] mode lacp-static
  [SW1-Eth-Trunk1] trunkport GigabitEthernet 0/0/1 to 0/0/3
  [SW1-Eth-Trunk1] max active-linknumber 2
  [SW1-Eth-Trunk1] least active-linknumber 1
  [SW1-Eth-Trunk1] quit
  [SW1] lacp priority 100
  [SW1] interface GigabitEthernet0/0/1
  [SW1-GigabitEthernet0/0/1] lacp priority 100
  ```
- **`display eth-trunk 1`（手工模式 · 字段对齐官方 NORMAL 输出）**：
  ```
  Eth-Trunk1's state information is:
  WorkingMode: NORMAL
  Hash arithmetic: According to SA-XOR-DA
  Least Active-linknumber: 1
  Max Bandwidth-affected-linknumber: 8
  Operate status: up
  Number Of Up Port In Trunk: 3
  --------------------------------------------------------------------------------
  PortName                        Status      Weight
  GigabitEthernet0/0/1            Up          1
  GigabitEthernet0/0/2            Up          1
  GigabitEthernet0/0/3            Up          1
  （Weight 为配置态缺省值 1，非实测带宽权重；未接入真实数据源）
  （链路聚合为模拟聚合（lite 引擎），非内核级真实 LACPDU 协商 / 无真实流量哈希分担）
  ```
- **`display eth-trunk 1`（LACP 静态模式 · 字段对齐官方 STATIC 输出）**：
  ```
  Eth-Trunk1's state information is:
  Local:
  LAG ID: 1                       WorkingMode: STATIC
  Preempt Delay: Disabled         Hash arithmetic: According to SIP-XOR-DIP
  System Priority: 100            System ID: <未接入真实数据源>
  Least Active-linknumber: 1      Max Active-linknumber: 2
  Operate status: up              Number Of Up Port In Trunk: 2
  --------------------------------------------------------------------------------
  ActorPortName                   Status    PortPri  Weight
  GigabitEthernet0/0/1            Selected  100      1
  GigabitEthernet0/0/2            Selected  100      1
  GigabitEthernet0/0/3            Unselect  32768    1
  （Selected/Unselect 为本地静态选举：按端口 LACP 优先级升序 + 接口名有序 tie-break，
    受 Max Active-linknumber 约束；非真实 LACPDU 协商结果）

  Partner:
  --------------------------------------------------------------------------------
  （对端 LACP 信息需真实 LACPDU 协商，仿真未接入真实数据源，不予展示）
  （链路聚合为模拟聚合（lite 引擎），非内核级真实 LACPDU 协商 / 无真实流量哈希分担）
  ```
  > **诚实占位说明**：官方 STATIC 输出的 `PortType` / `PortNo` / `PortKey` / `PortState`（8 位状态位图）与整个 `Partner:` 块均为真实 LACP 状态机产物，本期**一律不输出伪造值**——`PortType`/`PortNo`/`PortKey`/`PortState` 四列**整列略去**（而非填 0 或随机值），`System ID` 与 `Partner:` 块以显式占位文案标注。列集裁剪见 §6 #3。

- **`display eth-trunk 1`（聚合口 Down 场景，验证 P0-12 联动）**：
  ```
  Eth-Trunk1's state information is:
  WorkingMode: NORMAL
  Hash arithmetic: According to SIP-XOR-DIP
  Least Active-linknumber: 2
  Max Bandwidth-affected-linknumber: 8
  Operate status: down
  Number Of Up Port In Trunk: 1
  --------------------------------------------------------------------------------
  PortName                        Status      Weight
  GigabitEthernet0/0/1            Up          1
  GigabitEthernet0/0/2            Down        1
  GigabitEthernet0/0/3            Down        1
  （Operate status = down：活动接口数 1 < Least Active-linknumber 2）
  （链路聚合为模拟聚合（lite 引擎），非内核级真实 LACPDU 协商 / 无真实流量哈希分担）
  ```
- **`display eth-trunk 1 load-balance`（P1-5）**：
  ```
  Eth-Trunk1's load-balance information:
  Load-balance Configuration: SA-XOR-DA
  Load-balance options used per-protocol:
  （逐协议哈希因子明细需真实数据面支持，仿真未接入真实数据源）
  （链路聚合为模拟聚合（lite 引擎），非内核级真实 LACPDU 协商 / 无真实流量哈希分担）
  ```
- **`display trunkmembership eth-trunk 1`（P1-6）**：
  ```
  Trunk ID: 1
  Used status: VALID
  TYPE: ethernet
  Working Mode : NORMAL
  Number Of Ports in Trunk = 3
  Number Of Up Ports in Trunk = 2
  Operate status: up
  --------------------------------------------------------------------------------
  Interface GigabitEthernet0/0/1, valid, operate up,   weight=1
  Interface GigabitEthernet0/0/2, valid, operate up,   weight=1
  Interface GigabitEthernet0/0/3, valid, operate down, weight=1
  （链路聚合为模拟聚合（lite 引擎），非内核级真实 LACPDU 协商 / 无真实流量哈希分担）
  ```
- **`display current-configuration` 的 Eth-Trunk 段（P0-2，对齐官方配置文件样例）**：
  ```
  #
  interface Eth-Trunk1
   port link-type trunk
   port trunk allow-pass vlan 10 20
   mode manual load-balance
   load-balance src-dst-mac
   least active-linknumber 2
  #
  interface GigabitEthernet0/0/1
   eth-trunk 1
  #
  interface GigabitEthernet0/0/2
   eth-trunk 1
  #
  ```
- **典型错误回显**：
  ```
  [SW1-Eth-Trunk1] trunkport GigabitEthernet 0/0/9
  Error: The interface has been added to Eth-Trunk 2
  [SW1-Eth-Trunk1] load-balance src-dst-vlan
  Error: unrecognized command found at '^' position
  [SW1] interface Eth-Trunk 64
  Error: invalid Eth-Trunk ID (0-63)
  [SW1] undo interface Eth-Trunk 1
  Error: The Eth-Trunk interface has member ports, please delete them first
  [PC1] eth-trunk 1
  Error: Eth-Trunk is not supported on PC
  ```
- **前端（CLI 终端）：本期无变更**。链路聚合仅在 CLI 文本体现；不新增 API 响应字段、不做拓扑图聚合链路合并渲染。

> **golden 输出待校验**：上述各 `display` 字段顺序 / 列宽 / 大小写（如 `Operate status: up` 小写 vs `Up`、`WorkingMode` vs `Working Mode`、`Max Bandwidth-affected-linknumber`（手工）vs `Max Active-linknumber`（LACP））以官方文档样例为基准整理，但官方不同系列/版本存在写法差异，**精确逐行 golden 输出待课程视频 63 逐帧核对后补 golden 测试**（与 VLAN/Hybrid、STP 同策略，见 `docs/reference/huawei-vrp-course.md`）。

---

## 5. 验收标准（可测，每条可用自动化测试证明）

- **AC1（命令解析 + VRP 规范 + 单一事实源写入）**：在 Switch/L3Switch 上执行 `interface Eth-Trunk 1` → `mode manual load-balance` → `trunkport GigabitEthernet 0/0/1 to 0/0/3` → `load-balance src-dst-mac` → `least active-linknumber 2`，`DeviceConfig` 中正确出现 `interface:Eth-Trunk1:lag:mode`=`manual load-balance`、`:lag:load-balance`=`src-dst-mac`、`:lag:least-active-linknumber`=`2`，且 **`interface:GigabitEthernet0/0/1|0/0/2|0/0/3:eth-trunk` 三个键均为 `1`**（范围已展开）；断言 **`interface:Eth-Trunk1:members` 键不存在**（双写事实源已消除）；`mode manual load-balance` 两 token 完整解析（不得只存 `manual`）。
- **AC2（save→reload 持久化贯通）**：完成 AC1 配置后执行 `save`，经 `SerializeToDeviceConfigData` → `LoadFromDeviceConfigData` 往返，reload 后：① `DeviceConfig` 的 LAG 键集与 reload 前**完全一致**；② `display eth-trunk 1` 完整复现 mode / hash / least / 成员列表；③ **`display current-configuration` 能完整复现聚合配置**（含 `interface Eth-Trunk1` 块的 `mode` / `load-balance` / `least active-linknumber`，以及各成员口的 `eth-trunk 1` 行）；④ `state.Interfaces` 中 `Eth-Trunk1` 已重建（P0-3）。
- **AC3（成员 up/down ↔ 聚合口 up/down 联动，本期核心）**：Eth-Trunk1 含 3 个 Up 成员、`least active-linknumber 2` 时，`display eth-trunk 1` 的 `Operate status` = `up`、`Number Of Up Port In Trunk` = `3`；对其中 2 个成员执行 `shutdown` 后，`Operate status` 变为 `down`、`Number Of Up Port In Trunk` = `1`；对其中 1 个执行 `undo shutdown` 后回到 `up` / `2`；成员全部移除（`undo eth-trunk`）后 `Operate status` = `down`。**断言聚合口状态不再来自硬编码 `interface:Eth-Trunk1:status`=`Up`**。
- **AC4（LACP 静态模式活动/备份端口本地选举）**：`mode lacp-static` + 3 个成员 + `max active-linknumber 2`，成员 LACP 优先级分别为 100 / 100 / 32768 时，`display eth-trunk 1` 中优先级 100 的两口为 `Selected`、32768 的为 `Unselect`；把 `max active-linknumber` 改为 3 后三口全 `Selected`；同优先级场景下按接口名字典序确定性 tie-break（连续两次执行结果完全一致）；`Selected` 成员 `shutdown` 后转为 `Unselect` 且备份口顶上；**手工模式下不出现 Selected/Unselect 列**（字段集按模式切换）。
- **AC5（display 忠实展示 + 输出确定性）**：`display eth-trunk` / `display eth-trunk <id>` / `display eth-trunk <id> verbose` 字段与 §4 样例一致（手工 `WorkingMode: NORMAL` + `Max Bandwidth-affected-linknumber`；LACP `WorkingMode: STATIC` + `LAG ID` + `Max Active-linknumber`）；`Hash arithmetic` 按 `load-balance` 取值正确映射（`src-dst-mac`→`SA-XOR-DA`、缺省 `src-dst-ip`→`SIP-XOR-DIP`）；**同一状态连续调用 10 次输出字节级完全一致**（成员按接口名升序、多组按 trunk-id 升序，证明已消除 map 随机遍历）。
- **AC6（诚实占位 · CRITICAL）**：lite 引擎下所有 `display eth-trunk*` 输出均含 `lagSimNote()`「非内核级真实 LACPDU 协商 / 无真实流量哈希分担」注记；**输出中不存在任何伪造的 Partner 信息、PortKey/PortState 位图、真实收发包/字节/速率数字**（测试用正则断言输出不含 `PortKey` / `PortState` 数值列且 `Partner:` 段为占位文案）；`display eth-trunk 1 load-balance` 的 per-protocol 明细为占位文案；`Weight` 恒为配置态 `1` 且带说明。
- **AC7（合法性校验与拒错）**：① `interface Eth-Trunk 64` → `Error: invalid Eth-Trunk ID (0-63)`；② 已属 Eth-Trunk2 的口再 `eth-trunk 1` → `Error: The interface has been added to Eth-Trunk 2`；③ 在 Eth-Trunk 视图 `eth-trunk 2`（Eth-Trunk 作成员）→ 明确 `Error:`；④ 加入第 9 个成员 → `Error: ...exceeds the upper limit (8)`；⑤ `load-balance src-dst-vlan`（非枚举值）→ `Error:`；⑥ `mode lacp-dynamic` → 不放行（P2-1）；⑦ `least active-linknumber 5` 与 `max active-linknumber 3` 冲突 → `Error:`；⑧ `undo interface Eth-Trunk 1` 存在成员时 → 拒绝并提示先删成员；⑨ Eth-Trunk 视图命令在物理接口视图执行 / `eth-trunk <id>` 在系统视图执行 → 视图拒绝。
- **AC8（能力矩阵收敛）**：PC / Server / Router 上执行 `eth-trunk 1` / `trunkport ...` / `load-balance ...` / `display eth-trunk` 均返回能力拒绝（`... is not supported on <DeviceType>`）；Switch / L3Switch / VTEP 正常放行；**回归断言：新增能力矩阵条目未误伤其他设备的既有命令**（尤其 `mode` 未进顶层矩阵，Router 上其他 `mode` 类命令行为不变）。
- **AC9（纯函数无副作用 / 架构基线合规）**：`EvaluateLAG` / `SelectLACPActivePorts` / `CompareLACPPort` / `collectLAGTrunks` / `collectLAGMembers` 单测证明——不修改 `sim` 引擎、不写 `state`、**不 import `internal/protocol`**、零新增第三方依赖、连续两次调用结果一致且不改写任何 `DeviceConfig` 键；**静态断言 `CLIState` 未新增 `LAG` 内嵌结构体字段**（`grep -n "LAG" internal/cli/state.go` 仅命中既有 `ViewMLAG` / `MLAG`）；对照 `stp_eval.go:445 EvaluateSTP` / `vrrp_eval.go:259 EvaluateVRRP` 契约。
- **AC10（幽灵聚合组缺陷修复，P1-10）**：仅配置 Eth-Trunk（无任何 H3C `port link-aggregation group`）的设备执行 `display link-aggregation summary`，输出**只有 `Eth-Trunk1`，不得出现 `Bridge-Aggregation1`**；输出按组名排序稳定。
- **AC11（undo 语义完整）**：`undo eth-trunk`（成员口视图）后该口 `interface:<member>:eth-trunk` 键被清除且不再出现在 `display eth-trunk` 成员列表；`undo mode` / `undo load-balance` / `undo least active-linknumber` / `undo max active-linknumber` 恢复各自缺省并清键；`undo interface Eth-Trunk 1`（无成员时）清理 `interface:Eth-Trunk1:*` 全部键与 `state.Interfaces` 条目，`display eth-trunk 1` 报不存在。
- **AC12（缺省值对齐 VRP）**：未显式配置时 `mode`=`manual load-balance`（`WorkingMode: NORMAL`）、`load-balance`=`src-dst-ip`（`Hash arithmetic: According to SIP-XOR-DIP`，**非现状错误的 src-dst-mac**）、`least active-linknumber`=`1`、`max active-linknumber`=`8`、成员/系统 `lacp priority`=`32768`、`lacp preempt`=`disabled`、`lacp preempt delay`=`30`；`display eth-trunk` 与 `display current-configuration` 中缺省值**不冗余输出**（对齐 VRP 只输出差异值的惯例）。

---

## 6. 待确认问题（交架构师 / 主理人拍板，按重要性排序）

1. **LACP 静态模式活动端口选举依据哪些因子（核心 · 决定 P0-13 力度）**：官方真实选举链路为「① 比 System Priority（小者为 Actor），同值比 System MAC 小者 → ② Actor 端按端口 LACP 优先级（小者优先）→ ③ 同优先级比 PortNo（小者优先）→ ④ 取前 max active-linknumber 个为 Selected」。本仿真**无对端**，①（Actor 判定）不可为，③（PortNo）本仿真无真实端口编号。候选方案：
   - **(a) 本地可判定子集 + 诚实注记（PM 建议）**：恒假设本端为 Actor；排序键 = 成员口 `lacp priority` 升序 → 接口名字典序升序（PortNo 的确定性替代）；取前 `max active-linknumber` 为 `Selected`，Down 成员恒 `Unselect`。每次输出附「本地静态选举、非真实 LACPDU 协商」注记。
   - (b) 仅展示配置态，不给 Selected/Unselect（统一 `----`）。
   **PM 建议 (a)**——与 VRRP 拍板 #2(a)、STP 拍板 #2(a) 同思路，学员最关心"哪个口被选中当活动口"，教学价值最高且注记诚实；若团队坚持"不臆造任何选举结果"则退 (b)。**请拍板：采用 (a) 还是 (b)；若 (a)，tie-break 是否就用接口名字典序（而非引入伪造 PortNo）。**

2. **聚合口 up/down 的判定阈值与"活动成员"定义（核心 · 决定 P0-12 语义）**：拟定规则为「无成员 → down；活动成员数 < `least active-linknumber`（缺省 1）→ down；否则 up」。其中"活动成员"的定义有两种口径：
   - **(a) 手工模式 = 所有 `interface:<m>:status != Down` 的成员；LACP 模式 = `Selected` 且 Up 的成员（PM 建议）** —— 更贴近真机（LACP 下 Unselect 口不计入活动数）。
   - (b) 两种模式统一按"物理 Up"计数，不受 Selected 影响（实现更简单，但与真机 LACP 语义有偏差）。
   另需确认：成员 Down 的**唯一判定来源**是否就是既有 `interface:<iface>:status`（由 `shutdown` / `undo shutdown` 写入，对照 `stp_eval.go:175` / `vrrp_eval.go:462`）？**是否需要考虑"链路未接线"（拓扑侧无连线）也算 Down**？PM 建议：**仅以 `interface:<iface>:status` 为准，不引入拓扑连线判定**（避免跨模块耦合、也避免臆造链路事件）。**请拍板 (a)/(b) 与 Down 判定来源。**

3. **`display eth-trunk` LACP 模式的列集裁剪（核心 · 诚实占位边界）**：官方 STATIC 输出含 `ActorPortName / Status / PortType / PortPri / PortNo / PortKey / PortState / Weight` 八列 + 完整 `Partner:` 块。其中 `PortType`（真实端口速率）、`PortNo`（真实端口号）、`PortKey`、`PortState`（8 位 LACP 状态位图）、整个 Partner 块**均无法在本仿真真实产出**。候选方案：
   - **(a) 整列略去不可产出的列（PM 建议）**：只保留 `ActorPortName / Status / PortPri / Weight`，`Partner:` 块以一行占位文案替代，`System ID` 显式写 `<未接入真实数据源>`。优点：绝不出现伪造数字；缺点：与官方列集不完全一致，golden 对比需按仿真基线。
   - (b) 保留全部列但值填 `-` / `N/A`。优点：列集形似官方；缺点：`PortState` 这类位图填 `-` 观感割裂，且易被误读为"真的取到了空值"。
   - (c) 保留全部列并填算得出的近似值（**PM 强烈反对**，等同编造）。
   **PM 建议 (a)**。**请拍板列集裁剪方案，以及 `System ID` / `System Priority` 是否展示（System Priority 是本地可配置项，可如实展示；System ID 需设备 MAC —— 是否复用 `stp_eval.go:153 deriveMACFromName` 由设备名派生一个稳定假 MAC 并标注"本地派生、非真实网卡 MAC"？）。**

4. **负载均衡算法：真实哈希还是仅记录配置？**：候选——
   - **(a) 仅记录配置态 + 映射 `Hash arithmetic` 展示串（PM 建议，P2-3）**：不做真实哈希，因无 L2 数据面、无真实流量。
   - (b) 提供一个"模拟哈希计算器"命令（类似端口安全的 `simulate frame`），输入五元组后**计算**出该报文会走哪个成员口。这是有真实教学价值的**确定性纯函数**（不是编造数据，是可复现的算法演示），但属新增命令、增加工作量。
   **PM 建议：本期 (a)，(b) 记入 Roadmap**（若主理人认为教学价值足够，可作为 P1 追加，命令形如 `simulate eth-trunk 1 frame src-ip X dst-ip Y`，输出"按 SIP-XOR-DIP 算法命中成员口 GE0/0/2（算法演示，非真实转发）"）。**请拍板是否本期纳入 (b)。**

5. **`mode` 切换的成员约束是否强制**：官方规定「手工 → LACP 可保留成员；LACP → 手工必须先清空成员」（CE 系列另有"改模式前 Eth-Trunk 须无成员"的更严表述）。是否在仿真中强制该约束（LACP→手工且有成员时报错）？**PM 建议：强制**（真机会拒绝，学员踩到才学得到；错误文案 `Error: Please delete member interfaces before changing the working mode from LACP to manual`）。**请拍板强制 / 放行。**

6. **`max active-linknumber` 在手工模式下的行为**：官方明确「`max active-linknumber` 仅在 LACP 模式有效」，且手工模式的 display 字段名是 `Max Bandwidth-affected-linknumber`（语义不同）。候选——(a) 手工模式下执行 `max active-linknumber` 直接报错拒绝；(b) 允许配置但不生效、display 仍显示 `Max Bandwidth-affected-linknumber: 8`。**PM 建议 (b) + 提示信息**（真机通常允许配置只是不生效；报错会让学员困惑）。**请拍板。**

7. **H3C 变体命令（`port link-aggregation group` / `link-aggregation mode` / `Bridge-Aggregation`）是否同等对待**：本项目定位是**华为 VRP 仿真器**，课程 63 也是华为 Eth-Trunk。现状 H3C 变体与华为 Eth-Trunk **共用 `interface:<m>:eth-trunk` 键却映射两种聚合口名**，是 `display link-aggregation summary` 幽灵组缺陷（P1-10）的根因。候选——
   - **(a) 保留但降级 + 消歧（PM 建议）**：保留命令兼容（避免破坏既有用例），但增加 `interface:<m>:agg-family` 区分键（P1-11），H3C 变体**不享受**本期的聚合行为判定 / 官方 display 保真 / 能力守卫增强，仅保证不产生幽灵数据。
   - (b) 与华为 Eth-Trunk 同等对待（工作量近乎翻倍，收益低——课程与验收 oracle 都是华为）。
   - (c) 直接移除 H3C 变体（最干净，但可能破坏既有隐性用例；现状**无任何测试覆盖**，破坏风险其实很低）。
   **PM 建议 (a)**；若主理人偏好收敛代码面，(c) 也可接受。**请拍板 (a)/(b)/(c)。**

8. **键命名迁移策略（旧键兼容）**：P0-1 拟把 `interface:<trunk>:mode` / `:load-balance` 改名为 `interface:<trunk>:lag:mode` / `:lag:load-balance`，并废弃 `:members`。候选——(a) 直接改名，不做旧键兼容（现状无测试、无正式发布配置，风险低；**PM 建议**）；(b) `LoadFromDeviceConfigData` 增加一次性迁移逻辑（读到旧键则转写新键并删旧键）。**PM 建议 (a)**，理由：项目为本地单用户实验工具、无历史配置包袱、且 STP 重构时已有"直接移除 `state.STP`"的先例。**请拍板是否需要旧键迁移。**

9. **`trunkport` 范围展开的接口号推导规则**：官方 `trunkport GigabitEthernet 0/0/1 to 0/0/3` 中 `to` 后只给接口号。展开需推导 `0/0/1`→`0/0/2`→`0/0/3`（仅末段递增）。是否限制"仅末段可变、前段必须相同，否则报错"？**PM 建议：是**（前段不同 → `Error: invalid interface range`），并限制单次展开数量 ≤ 8（成员上限）。**请拍板。**

10. **`display eth-trunk` 无参数时的输出形态**：官方 `display eth-trunk`（无 id）输出所有 Eth-Trunk 的完整块。现状是自造的摘要式输出。**PM 建议：对齐官方，逐组输出完整块**（P1-12）；若主理人认为无参数时摘要表更利于排障教学，可保留一个自造摘要（但须明确标注为"仿真扩展"，避免学员误以为是真机输出）。**请拍板。**

---

## 7. 不在本期范围

- 建设真实 LACPDU 收发 / LACP 状态机与跨设备协商（仅本地静态选举 + 诚实注记，P2-2）；
- 真实负载均衡哈希分流与逐流转发（仅配置态 + `Hash arithmetic` 展示，P2-3）；
- 真实流量统计 / 带宽聚合 / 实测 Weight（一律诚实占位，P2-4）；
- 两端聚合一致性校验与协商失败诊断（P2-5）；
- `mode lacp-dynamic`（P2-1）、Eth-Trunk 子接口与 `portswitch` 二三层切换（P2-6）、`load-balance enhanced profile` / `round-robin` / 弹性 HASH（P2-7）、`lacp stable-preferred` 等高级项（P2-8）；
- M-LAG（`lacp m-lag ...`，`parser.go:1489`）相关能力（与本期基础聚合正交，本期不动）；
- 重写 NAT / 端口安全 / VRRP / STP（仅链路聚合增量）；前端图形化聚合链路合并渲染与 API 字段新增（P2-9）。

---

## 附：关键 file:line 证据索引 + 官方文档佐证小结

### 代码证据索引（供架构师直接定位，主理人将逐条 grep 验证）

- `internal/cli/parser.go:743-781` 物理接口 `eth-trunk <id>` 残桩：写 `interface:<iface>:eth-trunk`（**正确，保留**）；`:757` 把 `interface:<trunk>:status` **硬编码 `"Up"`**（编造状态，P0-11/P0-12 须移除）；`:764-780` 重复写 `interface:<trunk>:members` 逗号串（**双写事实源**，P0-1 须废弃）；无 trunk-id 范围 / 重复归属 / 8 成员上限 / Eth-Trunk 自嵌套校验（P0-9）。
- `internal/cli/parser.go:783-792` Eth-Trunk 视图 `mode`：`mode := cmd.Args[0]` **只取一个 token**（`mode manual load-balance` 丢 `load-balance`，P0-6 须修）；无取值枚举校验；键 `interface:<trunk>:mode`（P0-1 改 `:lag:mode`）。
- `internal/cli/parser.go:793-818` `trunkport`：`to` 语法要求 `to <type> <num>`（**与官方 `to <num>` 不符**，P0-7 须修）；写 `:members` 串且**不展开范围**；`display eth-trunk` 不读该键 → 经 `trunkport` 加的成员在 display 中完全不可见（P0-1/P0-7 修复）。
- `internal/cli/parser.go:819-828` `load-balance`：`strings.Join(cmd.Args," ")` 原样入库，**无枚举校验**（P0-10）。
- `internal/cli/parser.go:675-705` H3C `port link-aggregation group <id>`：与华为 Eth-Trunk **共用 `interface:<m>:eth-trunk` 键**但映射 `Bridge-Aggregation<id>`（同键双语义，P1-11 消歧）。
- `internal/cli/parser.go:829-840` H3C `link-aggregation mode <dynamic|static>`：写 `interface:<trunk>:mode`，无视图 / 取值校验。
- `internal/cli/parser.go:2579-2637` **现有 `display eth-trunk` 桩**（与任务简报"缺失 `display eth-trunk`"不符，已在 §3 更正为 [已有·不合规·桩]）：`:2592` `for k, v := range state.DeviceConfig` + `:2608` `for trunkName, members := range trunkMap` **双重 map 遍历 → 输出顺序随机**（P0-16 须排序）；`:2612-2621` 字段集非官方；`:2617` 聚合口 status 直读硬编码键（无联动，P0-12）；`:2628-2631` 成员 status 空值默认 `"Up"`（编造）；`:2580` 仅守 `isHost/isCloudHub`（P0-17 收敛能力）；全程无诚实占位（P0-15）。
- `internal/cli/parser.go:2639-2703` **`display link-aggregation summary` 幽灵组缺陷**：`:2666-2691` 第二个循环对**同一批 `interface:*:eth-trunk` 键**再生成 `Bridge-Aggregation<id>` 条目 → 只配 Eth-Trunk 的设备会凭空多出 `Bridge-Aggregation1`（**编造数据，违反诚实占位铁律**，P1-10 必修）。
- `internal/cli/parser.go:310-372` `interface` 命令：`:320` 前缀表含 `Eth-Trunk/Eth-trunk/ET/Bridge-Aggregation/BAGG`（支持 `interface Eth-Trunk 1` 带空格形态）；`:346` 正则同样覆盖；`:361-372` **新建接口 status 缺省写 `"Up"`**（对 Eth-Trunk 而言是编造，P0-4/P0-11 须让 Eth-Trunk 状态由 `EvaluateLAG` 派生）。
- `internal/cli/parser.go:861-900` 接口视图 `undo` 分支：仅 `vrrp` / `shutdown` / `ip address` / `description`，**无 `undo eth-trunk`**（P0-8 新增）。
- `internal/cli/parser.go:5131-5215` `applyUndoSystemFeature`：有 `ospf`/`vlan`/`acl`/`stp`/`dhcp`/`bgp`/`ipv6`/`isis`，**无 `interface`**（P0-5 `undo interface Eth-Trunk <id>` 需新增分支）。
- `internal/cli/parser.go:5249-5277` `SerializeToDeviceConfigData`（仅遍历 `state.DeviceConfig` 落盘 —— **凡不写 DeviceConfig 的配置全部丢失**）；`:5280-5430` `LoadFromDeviceConfigData`（回写 `DeviceConfig` + 重建 ISIS/OSPF/BGP 结构化字段；**不重建 `state.Interfaces`** → reload 后 Eth-Trunk 逻辑口消失，P0-3 须补）。
- `internal/cli/parser.go:5463-5566` `buildSavedConfigSnapshot`：`:5476-5479` STP 系统块（`buildSavedSTPConfig`）、`:5518-5523` 接口块内 VRRP/STP 行、`:5528-5540` **VRRP 独立输出通道**（为 reload 后 `state.Interfaces` 缺失的接口补 `interface` 块 —— **P0-2 直接复用此范式**）；**全文无 Eth-Trunk 段**。
- `internal/cli/capabilities.go:78` `"lacp": switchDevices()`（仅覆盖 `lacp` 顶层命令）；`:136-138` `isCommandSupported` 对**未声明命令默认放行** → `eth-trunk`/`trunkport`/`mode`/`load-balance`/`link-aggregation` **全部未声明，PC/Router 可配聚合**（P0-17 收敛）；`:191-197` `switchDevices()` = Switch/L3Switch/VTEP。
- `internal/cli/parser.go:245` `ExecuteCommandOn` 内 `isCommandSupported` 通用能力校验入口。
- `internal/cli/stp_eval.go:1-25` 纯函数评估器契约头注释（**新 `lag_eval.go` 的文件头范式模板**）；`:290-296` `stpSimNote()`（诚实占位口径基准）；`:304-316` `sortedInterfaceNames`（确定性排序，P0-16 直接复用）；`:175-189` `isPortDown`（成员 Down 判定口径，P0-12 复用）；`:260-278` `CompareBridgeID`（可单测比较器范式，`CompareLACPPort` 对照）；`:445` `EvaluateSTP`（主评估器签名范式）。
- `internal/cli/vrrp_eval.go:259` `EvaluateVRRP`、`:335` `CompareVRRPPriority`、`:397-402` `vrrpSimNote()`、`:462` `isInterfaceDown`（同上，第二套范式参照）。
- `internal/cli/acl_eval.go:493-507` `aclSimNote()` / `natSimNote()`（lite/full 两态、读 `sim.EngineModeName()`）；`internal/cli/portsec_eval.go:236-244` `portSecSimNote()`。
- `internal/cli/parser.go:1489-1514` `lacp m-lag priority|system-id`（M-LAG，与本期正交；P1-1/P1-2 新增 `lacp priority` 子命令时须与此分派共存不冲突）。
- `internal/cli/tools.go:78-80` `display` 子命令缩写 `eth-trunk`/`et`/`eth` → `eth-trunk`（已支持派发）。
- `internal/cli/state.go:131-138` `InterfaceConfig{Name,Status,Protocol,Description,IP,Mask}`；`:24` `ViewMLAG`、`:55` `MLAG *MLAGConfig` —— **`state.go` 中无任何 LAG/Eth-Trunk 结构体，本期也不得新增**（架构铁律 1，AC9 静态断言）。
- `internal/cli/*_test.go`：`grep -i "eth-trunk\|link-aggregation\|trunkport"` **零命中** —— 本期为全新测试面，无历史行为契约包袱（利好 §6 #7/#8 的激进选项）。

### 官方文档佐证小结（哪些命令 / 缺省值已用官方文档确认）

| 命令 / 缺省值 | 官方依据 | 核验状态 |
|---|---|---|
| `interface eth-trunk <trunk-id>`，trunk-id **0~63** | CMD `interface eth-trunk` 命令参考 | ✅ 主源 |
| `undo interface eth-trunk <id>`；**删除时不能有成员接口** | CMD 使用指南 | ✅ 主源 |
| `mode { manual load-balance \| lacp-static \| lacp-dynamic }`，**缺省 manual load-balance** | EN1 `mode` 命令 / EN2 配置流程 | ✅ 主源 ×2 |
| 改模式前的成员约束（LACP→手工须无成员） | EN1「Before changing the working mode… ensure the Eth-Trunk contains no member interface」 | ✅ 主源 |
| `trunkport <type> <num> [to <num>]`（类型只写一次） | EN2 / 课程 63 实操样例 `trunkport GigabitEthernet 0/0/1 to 0/0/3` | ✅ 主源 |
| `eth-trunk <id>`（成员口视图） | ZH1 配置文件样例（成员口下 `eth-trunk 1`） | ✅ 主源 |
| `load-balance { dst-ip \| dst-mac \| src-ip \| src-mac \| src-dst-ip \| src-dst-mac }`，**缺省 src-dst-ip** | ZH1「缺省情况下，Eth-Trunk 接口的负载分担模式为 src-dst-ip」；ZH2 同述 | ✅ 主源 ×2 |
| `Hash arithmetic` 映射（`src-dst-mac`→`SA-XOR-DA`、`src-dst-ip`→`SIP-XOR-DIP`） | ZH2 输出样例 + 课程 63 实操样例 | ✅ 主源 ×2 |
| `least active-linknumber <n>`，**缺省 1**；低于阈值 → Eth-Trunk Down | EN1 / EN3 | ✅ 主源 ×2 |
| `max active-linknumber <n>`，**缺省 8**；**仅 LACP 模式有效**；须 ≥ least | EN1 / EN3 | ✅ 主源 ×2 |
| `lacp priority`（系统级 & 接口级）**缺省 32768**，小者优先 | EN3 | ✅ 主源 |
| `lacp preempt enable`（**缺省 disabled**）/ `lacp preempt delay`（**缺省 30s**） | EN3 | ✅ 主源 |
| `lacp timeout { fast \| slow }`（fast 1s / slow 30s，接收超时缺省 90s） | EN1 | ✅ 主源 |
| 成员规则：一个 Eth-Trunk 最多 **8** 成员；一个以太口只能加入一个 Eth-Trunk；Eth-Trunk 不能作其他 Eth-Trunk 成员 | ZH 配置规则汇总（S 系列 Eth-Trunk 接口配置） | ✅ 核证 |
| `display eth-trunk [ trunk-id [ interface <if> \| verbose ] ]` | ZH1 检查配置结果章节 | ✅ 主源 |
| `display eth-trunk` **手工模式**字段集（`WorkingMode: NORMAL` / `Hash arithmetic` / `Least Active-linknumber` / `Max Bandwidth-affected-linknumber` / `Operate status` / `Number Of Up Port In Trunk` / `PortName Status Weight`） | 官方/课程 63 实操输出样例 | ✅ 核证 |
| `display eth-trunk` **LACP 模式**字段集（`LAG ID` / `WorkingMode: STATIC` / `Preempt Delay` / `System Priority` / `System ID` / `Max Active-linknumber` / `ActorPortName Status PortType PortPri PortNo PortKey PortState Weight` / `Partner:` 块） | 官方 LACP 输出样例 | ✅ 核证（**PortType/PortNo/PortKey/PortState/Partner 本期诚实占位，见 §6 #3**） |
| `display trunkmembership eth-trunk <id>` / `display eth-trunk <id> load-balance` | ZH1 检查配置结果 / ZH2 输出样例 | ✅ 主源 |

> **核验说明**：本 PRD 对**所有非显而易见**的命令语法与缺省值（`mode` 两 token 形态与缺省、`load-balance` 取值集与缺省 `src-dst-ip`、`Hash arithmetic` 映射串、`least/max active-linknumber` 缺省 1/8 与"max 仅 LACP 有效"、`lacp priority/preempt/timeout` 缺省、trunk-id 0~63、8 成员上限、删除前须无成员、`trunkport ... to <num>` 语法）均经华为官方命令参考 / 配置指南 / 故障排查文档核证，**不凭记忆杜撰**。**各 `display` 的精确逐行 golden 输出（列宽、大小写、`WorkingMode` vs `Working Mode`、`Operate status: up` vs `Up`）仍待课程视频 63 逐帧校验**（见 §4 标注）。

## 文档状态

- 基线核查完成：链路聚合配置侧 6 处残桩（`parser.go:675/743/783/793/819/829`）、display 侧 2 处残桩（`parser.go:2579/2639`）、接口名解析（`parser.go:320/346`）、能力矩阵（`capabilities.go:78/136`）、持久化机制（`parser.go:5249/5280/5463`）、纯函数与诚实占位范式（`stp_eval.go`/`vrrp_eval.go`/`acl_eval.go`/`portsec_eval.go`）均已核对。
- 经代码核查**更正任务简报三处**：① **`display eth-trunk` 已存在**（`parser.go:2579`，非"缺失"），但为非合规桩（map 随机序、字段非官方、状态无联动、成员事实源读错、无诚实占位），方案为"重写而非新建"；② `display link-aggregation summary` 存在**幽灵 `Bridge-Aggregation` 编造数据缺陷**（`parser.go:2666-2691`），属诚实占位铁律违规，已列 P1-10 必修；③ 除 `lacp` 外，`eth-trunk`/`trunkport`/`mode`/`load-balance`/`link-aggregation` **均未进能力矩阵**（默认放行），能力越界范围比简报描述更大。
- **核心缺口**（本期增量）：真实聚合行为判定（成员 up/down ↔ 聚合口状态联动、LACP 活动/备份端口选举）、成员事实源去双写、`undo eth-trunk` / `undo interface Eth-Trunk`、`display current-configuration` 的 Eth-Trunk 段、reload 后聚合口重建、`display` 官方保真与输出确定性、`lagSimNote()` 诚实占位、能力矩阵收敛。
- 架构基线全部可直接对齐：`lag_eval.go` 照抄 `stp_eval.go`/`vrrp_eval.go` 契约（纯函数、不 import `internal/protocol`、零新依赖、零引擎改动）；持久化复用既有 `DeviceConfig` roundtrip + `buildSavedConfigSnapshot` 的 STP/VRRP 双通道范式；**不得在 `CLIState` 新增 `state.LAG`**（AC9 静态断言）。
- 需求池 **40 条**（P0 18 / P1 13 / P2 9），验收标准 **12 条**（AC1–AC12），待确认 **10 项**，其中 #1（LACP 选举因子）、#2（聚合口 up/down 阈值与活动成员定义）、#3（LACP display 列集裁剪 / 诚实占位边界）为**必须先拍板才能开工**的三项，PM 均已给出明确建议。

_Last updated: 2026-08-07 · 产品经理 许清楚（Xu）_
