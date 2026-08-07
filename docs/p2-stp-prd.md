# ensp-lab P2 第四项：STP/RSTP/MSTP（华为 VRP 实训课程 55/56/57）增量 PRD

> 文档类型：增量产品需求文档（PRD，简单模式，结构对齐 `docs/p2-vrrp-prd.md` / `docs/p2-portsec-prd.md`）
> 关联：`docs/p2-vrrp-prd.md`（VRRP 增量 PRD，架构基线/纯函数/诚实占位范式基准）、`docs/p2-portsec-prd.md`（端口安全增量 PRD）、`docs/reference/huawei-vrp-course.md` 课程 55/56/57（验收基准）、`internal/cli/parser.go` / `state.go` / `capabilities.go` / `acl_eval.go` / `portsec_eval.go` / `vrrp_eval.go`（已核查代码基线）
> 官方权威源（命令正确性佐证）：
> - ZH：STP/RSTP 基本功能配置 — https://support.huawei.com/enterprise/zh/doc/EDOC1100367112/b9cc9782
> - EN：STP/RSTP/MSTP Configuration · Configuring Parameters to Adjust the Device Role, Port Role, and Port Status — https://support.huawei.com/enterprise/en/doc/EDOC1100466179/c9a6630d/EN-US_TASK_0000001743120048
> - RSTP 配置举例（S 系列）— https://support.huawei.cn/enterprise/en/doc/EDOC1100213107/40e7812e/example-for-configuring-rstp
> 作者：产品经理 许清楚（Xu）
> 语言：中文

---

## 0. 项目信息

- **Language**：中文
- **Programming Language**：Go 1.26.5（本期工作集中在 `internal/cli`；前端 React/Vite 本期无变更）
- **Project Name**：`p2_stp`
- **原始需求复述**：在 P2 已交付 NAT（课程 38）、端口安全（课程 49）、VRRP（课程 60/61）之后，为华为 eNSP VRP 仿真器落地 **STP/RSTP/MSTP（课程 55/56/57）** 的增量实现：把已有的 STP 残桩（仅写 `state.STP` 结构化字段、不经 `DeviceConfig` 持久化、无诚实占位）升级为「命令对齐官方 VRP、全部经 `DeviceConfig` 持久化、纯函数本地静态选举、诚实占位标注」的二层生成树链路；并补齐 `display stp [brief]` / `display stp interface <if>` / `display current-configuration` 的 STP 段。

> **深度边界先验结论（务必先读 §6 拍板项）**：STP/RSTP/MSTP 是 **L2 环路避免** 特性，真实根桥选举 / 端口角色判定 / 拓扑收敛依赖设备间 **BPDU 收发与交互式状态机**。当前 sim 引擎**无真实 BPDU 收发与拓扑计算**（与 VRRP 无 HA 心跳、端口安全无 L2 转发同源）。因此"真实跨设备选举收敛、端口状态机迁移、TCN 泛洪"本期不可为，必须在**纯函数本地静态计算**结果之上叠加诚实占位注记（`stpSimNote()`）。代码基线里已存在一个**不合规的 STP 残桩**与**一个不合规的 `display stp` 桩**（见 §3），本期必须按架构基线扩展/重写，而非另起炉灶。

---

## 1. 产品目标

在严守 P2「CLIState 层纯函数、单一事实源 `DeviceConfig`、诚实占位、零改动 `sim` 引擎」架构基线的前提下，把 STP 从代码里一个"仅写 `state.STP` 结构化字段、config 丢失、无诚实占位"的残桩 + 一个"读 struct 字段、无 brief/interface/region 保真、无诚实注记"的 `display stp` 桩，升级为一条**可对学员实验产生可观测反馈**的二层环路避免链路：

1. **配置真实性 + 持久化**：全部 STP 状态经 `DeviceConfig["stp:<field>"]`（系统级）与 `DeviceConfig["interface:<iface>:stp:<field>"]`（接口级）持久化，修掉 save→reload 丢配置缺陷（同源于旧 VRRP `state.VRRP` 缺陷，本次按 OSPF/BGP/ISIS 的 `LoadFromDeviceConfigData` 重建范式修复）；命令集对齐官方 VRP 课程 55/56/57。
2. **展示忠实性**：重写 `display stp [brief]` / 新增 `display stp interface <if>` / 在 `display current-configuration` 增加 STP 段，忠实呈现根桥 ID、本桥优先级、各端口角色/状态（本地静态计算）、MSTP 域配置；所有引擎无法真实模拟的行为一律诚实占位注记。
3. **（按拍板）选举真实性**：按选定力度，以纯函数 `EvaluateSTP` 本地静态计算根桥/端口角色（参照课程 55 四要素：桥 ID / 路径开销 / 发送者桥 ID / 端口 ID），让学员直观理解"优先级小者胜、同优先级比 MAC（桥 ID）小者胜、路径开销小者当根端口/指定端口"，并明确标注"本地假设选举、非真实 BPDU 选举"。

---

## 2. 用户故事

1. **作为交换实验学员**：As a 学员，I want 在系统视图依次敲 `stp mode rstp` / `stp priority 4096` / `stp root primary` / `stp pathcost-standard dot1t`，并在接口视图敲 `stp edged-port enable` / `stp cost 20000` / `stp port priority 16`，so that 设备按 VRP 课程 55/56 真实命令完成生成树参数配置，能用 `display stp` 核对模式/优先级/角色。
2. **作为关注持久化的学员**：As a 学员，I want `save` 后 `reload` 设备，so that STP 配置（含 mode / priority / root / pathcost-standard / edged-port / cost / port priority / region）仍保留，`display stp` 与 `display current-configuration` 复现，而不必重配（残桩因不写 `DeviceConfig` 会丢配置，见 `parser.go:4739`）。
3. **作为理解选举机制的学员**：As a 学员，I want 看到纯函数按"桥 ID 最小者当根桥、到根路径开销最小者当根端口、链路开销小者当指定端口"给出的**本地静态角色**，so that 我能对照课程 55 的四要素验证自己的配置，并明确知道这是"本地假设、非跨设备真实 BPDU 选举"。
4. **作为 MSTP 实验学员（P1）**：As a 学员，I want 配置 `stp region-configuration` → `region-name RG1` / `instance 1 vlan 2 to 10` / `revision-level 1` / `active region-configuration`，so that 我能用 `display stp region-configuration` 核对 MST 域与 VLAN-实例映射（课程 57）。
5. **作为排障学员**：As a 学员，I want 用 `display stp brief` 一眼看到所有端口的 `MSTID / Port / Role / State`，so that 快速定位哪个端口被阻塞（ALTE/BACK）、哪个是指定/根端口。

---

## 3. 需求池

### 已有（本期重构 / 扩展，非另起炉灶）

| 标记 | 内容 | 位置 |
|---|---|---|
| [已有·不合规] | 顶层 `stp` 分支（系统视图）：含 `enable/disable/mode/priority/v-stp/bridge-address/root primary\|secondary/edged-port(系统视图,**官方应为接口视图**)/bpdu-protection/root-protection/loop-protection/tc-protection interval/region-configuration/region-name/revision-level/instance vlan/active` 子命令；其中 `mode/priority/root/region-*`/`bridge-address` **仅写 `state.STP` 结构体字段，不经 `DeviceConfig` 持久化** | `parser.go:1483-1606` |
| [已有·不合规·桩] | `display stp` 桩：存在但**读 `state.STP` 结构体字段**（非 DeviceConfig），仅支持 `region-configuration` 子命令与极简默认输出（Mode/BridgePriority/VSTP/BridgeAddress/Ports），**无 `brief`、无 `interface <if>`、无角色/状态、无诚实占位** | `parser.go:3541-3594` |
| [已有·规整] | `edged-port`/`bpdu-protection`/`root-protection`/`loop-protection`/`tc-protection-interval` 已直接写裸 `DeviceConfig["stp:..."]`（口径与待修复字段不统一，须规整进统一键命名） | `parser.go:1541-1566` |
| [已有] | `undo stp` 已存在：仅 `state.STP.Enabled=false`（不清理 `DeviceConfig` 键）；`p1f_qa_test.go:228-231` 已有 `undo stp` → `r.STP.Enabled=false` 测试 | `parser.go:4649-4651` |
| [已有] | 能力矩阵：`"stp": switchDevices()`（Switch/L3Switch/VTEP）；系统视图守卫 `state.CurrentView != ViewSystem` 已存在 | `capabilities.go:70`、`parser.go:1484` |
| [已有·基线] | 配置持久化机制：`SerializeToDeviceConfigData`（仅遍历 `state.DeviceConfig` 落盘，`parser.go:4739` —— **凡不写 DeviceConfig 的配置全部丢失**）↔ `LoadFromDeviceConfigData`（`parser.go:4757` 起）；OSPF/BGP/ISIS 已用"循环写回 DeviceConfig + 重建结构化字段"范式修复同类缺陷（`parser.go:4820-4869`），本期 STP 直接复用该范式 | `parser.go:4739,4757,4820-4869` |
| [已有·基线] | 纯函数评估器契约范式 + 诚实占位：`aclSimNote()`/`natSimNote()`（`acl_eval.go:493-507`）、`portSecSimNote()`（`portsec_eval.go:236-244`）、`vrrpSimNote()`（`vrrp_eval.go:397-402`）；`EvaluatePortSecurity`/`EvaluateVRRP` 无副作用、不写引擎、不 `import protocol`、可单测 | `acl_eval.go:9-15,493-507`、`portsec_eval.go:7-10,236-244`、`vrrp_eval.go:13-25,394-402` |
| [已有] | `state.STP *STPConfig`（`Enabled/Mode/BridgePriority/Ports/VSTPEnabled/BridgeAddress/RegionName/RevisionLevel/VLANMapping/RegionActive`）、`STPPort{PortName/PortPriority/Cost}`；初始化 `Mode:"rstp", BridgePriority:32768` | `state.go:57,245-256,269-273,501-507` |

### P0（本期核心 · = 命令对齐官方 + 持久化贯通 + display 忠实 + 诚实占位）

> 每条命令标注 **[官方依据]**：ZZ=ZH 基本功能配置(EDOC1100367112/b9cc9782)；EE=EN Configuring Parameters…(EDOC1100466179/c9a6630d/EN-US_TASK_0000001743120048)；RS=RSTP 配置举例(EDOC1100213107/40e7812e)；★=WebSearch 二次核证的官方命令参考（见附·官方文档佐证小结）。

- **[P0-1 修复·持久化贯通] 全部 STP 状态改走 `DeviceConfig` 单一事实源**：系统级 `stp:<field>` + 接口级 `interface:<iface>:stp:<field>`；在 `LoadFromDeviceConfigData` 增加 STP 重建分支（对齐 OSPF/BGP/ISIS `parser.go:4820-4869`），reload 后由 `stp:*` 键重建 `state.STP`，彻底修掉 `stp mode`/`stp priority`/`stp root`/`region*`/`bridge-address` save→reload 丢配置缺陷。**[官方依据]** 架构基线（无专用官方命令，属缺陷修复）。
- **[P0-2 `stp mode {stp|rstp|mstp}`]** 补全并校验模式取值（现状 `state.STP.Mode=cmd.Args[1]` 无校验，可写入任意串）；写 `stp:mode`（默认 `rstp`，与 `state.go:503` 初始化一致）。**[官方依据]** ZZ「STP/RSTP 基本功能配置」；EE `stp mode { stp | rstp | mstp | vbst }`（本期仅 stp/rstp/mstp，vbst 见 P2-4）。
- **[P0-3 `stp priority <0-61440, step 4096>`]** 桥优先级（系统视图），写 `stp:priority`；默认 **32768**（VRP 原生默认，见 `state.go:504`），取值必须为 4096 的整数倍，越界报错。**[官方依据]** EE「Configuring Parameters…」：`stp [ instance instance-id ] priority priority`，默认 32768，须为 4096 倍数 ★（NetEngine AR doc 明示 default 32768）。
- **[P0-4 `stp root primary | secondary`]** 主/备根桥：`primary` → `stp:priority=0`，`secondary` → `stp:priority=4096`（写键，非仅写结构体）。**[官方依据]** EE/ZZ「配置生产树的根桥」：`stp root primary`（priority 0）/`stp root secondary`（priority 4096）★。
- **[P0-5 `undo stp root`]** 取消根桥/备份根桥角色，恢复 `stp:priority` 为默认 32768 并清理该键。**[官方依据]** EE「To change the priority of a device that has been configured as the root bridge … run the `undo stp [ instance instance-id ] root` command」★。
- **[P0-6 `stp pathcost-standard {dot1d-1998|dot1t|legacy}`]** 路径开销算法（系统视图），写 `stp:pathcost-standard`；默认 **dot1t（IEEE 802.1t）**。**[官方依据]** ZZ/EE「可选：配置路径开销计算方法」；★ 官方命令参考 `stp pathcost-standard { dot1d-1998 | dot1t | legacy }`（EDOC1000015892/adc7faa1），默认 dot1t。
- **[P0-7 `undo stp`（保留·修正）]** 禁用全局 STP：写 `stp:enabled=false` 并清理所有 `stp:*` 配置键（现状仅置 `state.STP.Enabled=false`，不清理键，须规整）；保留既有 `p1f_qa_test.go:228-231` 行为契约。**[官方依据]** ZZ「关闭 STP」。
- **[P0-8 `stp cost <n>`（接口视图）]** 端口路径开销，写 `interface:<iface>:stp:cost`；取值范围随 pathcost-standard 而定（dot1d-1998: 1–65535；dot1t: 1–200000000；legacy: 1–200000）。**[官方依据]** EE「Set a path cost value for the current port」：`stp [ process process-id ] [ instance instance-id ] cost cost`（接口视图）★。
- **[P0-9 `stp port priority <n>`（接口视图）]** 端口优先级，写 `interface:<iface>:stp:port-priority`；默认 **128**，范围 0–240（步进 16）。**[官方依据]** EE「Set the port priority」：`stp [ process process-id ] [ instance instance-id ] port priority priority`，默认 **128** ★。
- **[P0-10 `stp edged-port enable | disable`（迁移至接口视图）]** 把现有系统视图 `stp edged-port` **迁移到接口视图**（官方为接口视图命令），写 `interface:<iface>:stp:edged-port`；原系统视图 `stp:edged-port` 键废弃（见 §6 #1）。**[官方依据]** ZZ「配置端口为边缘端口」：`stp edged-port enable`（接口视图）★。
- **[P0-11 `display stp [brief]`（重写）]** 读 DeviceConfig 派生（非 struct 字段）；`display stp` 输出根桥 ID（本地静态假设：本桥优先级/MAC 组成的桥 ID）、本桥优先级、各端口角色/状态（纯函数 `EvaluateSTP` 本地静态计算）、MSTP 模式；`display stp brief` 输出 `MSTID / Port / Role / State` 摘要表（字段保真见 §4）。末尾附 `stpSimNote()` 诚实注记。**[官方依据]** ZZ/EE「查看生成树状态」；★ `display stp brief` 字段 `MSTID Port Role State`（故障排查官方样例）。
- **[P0-12 `display stp interface <iface>`（新增）]** 读 DeviceConfig，输出单端口：`Mode / CIST Bridge / CIST Root/ERPC / CIST RegRoot/IRPC / CIST RootPortId / BPDU-Protection / TC or TCN received / 端口 Role-State`（本地静态）+ `stpSimNote()` 注记。**[官方依据]** ZZ/EE「查看端口生成树状态」；★ `display stp interface` 字段集（故障排查官方样例含上述全部字段）。
- **[P0-13 `display current-configuration` 增加 STP 段]** 从 `stp:*` / `interface:<iface>:stp:*` 键渲染 `stp mode` / `stp priority` / `stp root` / `stp pathcost-standard` / `stp edged-port`（接口视图）/`stp region-configuration` 等，使 save→reload 后 `display current-configuration` 完整复现（修掉残桩丢配置，AC2）。**[官方依据]** VRP 原生 `display current-configuration` 含 STP 段（无专用官方章节，属展示保真）。
- **[P0-14 `stpSimNote()` 诚实占位]** lite →「（STP 为模拟生成树（lite 引擎），非内核级真实 BPDU 选举 / 无真实拓扑收敛）」；full →「（STP 为模拟生成树）」，口径与 `aclSimNote()`/`natSimNote()`/`portSecSimNote()`/`vrrpSimNote()` 完全一致。**[官方依据]** 架构基线（诚实占位范式）。
- **[P0-15 VRP 格式输出修正]** 现有返回 `"STP enabled (RSTP)"` / `"STP mode set to %s"` 等不规范文案改为 VRP 风格（配置成功静默或回显规范命令；`display` 输出字段对齐官方，无臆造计数）。**[官方依据]** VRP 行为（配置命令成功静默 / 仅报错）。
- **[P0-16 拒错与守卫]** 非系统视图执行 `stp` → `Error: must be in system view`；非 `switchDevices()`（Router/PC/Server 等）→ 能力拒绝（沿用 `capabilities.go:70`）；接口视图命令（`cost`/`port priority`/`edged-port`）在系统视图执行 → 拒绝并提示 `must be in interface view`；`priority` 非 4096 倍数 / `cost` 越界 / `port priority` 越界 / `pathcost-standard` 非法值 / `mode` 非法 → 明确 `Error:`。**[官方依据]** 架构基线 + VRP 参数约束。

### P1（保护 + MSTP region · 建议默认纳入）

- **[P1-1 `stp bpdu-protection`]** 规整进 `stp:bpdu-protection`（现状已写，补全 `undo stp bpdu-protection` 清理 + `state.STP` 结构化字段重建）；展示于 `display stp`/`display stp interface` 的 `BPDU-Protection` 字段。**[官方依据]** ZZ「配置 BPDU 保护」。
- **[P1-2 `stp root-protection`]** 规整进 `stp:root-protection`（同 P1-1 口径）。**[官方依据]** ZZ「配置根保护」。
- **[P1-3 `stp loop-protection`]** 规整进 `stp:loop-protection`（同 P1-1 口径）。**[官方依据]** ZZ「配置环路保护」。
- **[P1-4 `stp tc-protection [interval <s>]`]** 规整进 `stp:tc-protection` / `stp:tc-protection-interval`（现状 `stp:tc-protection-interval` 已写，补全 `undo` 与缺省 interval）；展示于 TC 防护相关字段。**[官方依据]** ZZ「配置 TC 保护」。
- **[P1-5 `stp region-configuration`（MSTP 完整实现）]** 进入 `[<sysname>-mst-region]` 视图，子命令 `region-name <name>`（`stp:region-name`）/ `revision-level <level>`（`stp:revision-level`，默认 0）/ `instance <id> vlan <vlan-list>`（`stp:instance:<id>:vlans`，支持 `2 to 10` 与 `10 20 30` 形态）/ `active region-configuration`（`stp:region-active=true` 激活）；全部经 DeviceConfig 持久化。`active` 前为预配置、不生效，须 `active` 才生效（官方语义）。**[官方依据]** ZZ/EE「配置 MST 域」；★ 官方 MSTP 示例（EDOC1000141427/fe03199b、EDOC1000089023/5e917476）：`region-name` / `instance 1 vlan 2 to 10` / `revision-level`（默认 0）/ `active region-configuration`。
- **[P1-6 `stp bridge-address <mac>`]** 规整进 `stp:bridge-address`（现状写结构体，须改写键 + 重建）。**[官方依据]** ZZ「配置桥 MAC 地址」。
- **[P1-7 `display stp region-configuration`（规整）]** 读 DeviceConfig 派生，输出 `Format selector / Region name / Revision level / Instance VLAN Mapped`（按实例分组），状态 Active/Inactive 取自 `stp:region-active`。**[官方依据]** ZZ/EE「查看 MST 域配置」；★ `display stp region-configuration` 输出样例（官方故障排查）。
- **[P1-8 `stp [instance <id>] root primary|secondary`（MSTP 每实例根桥）]** 可选增强：按实例设置主/备根桥，写 `stp:instance:<id>:root`（primary→0 / secondary→4096）。**[官方依据]** ZZ/EE「配置 MST 实例的根桥」★。

### P2（增强 / 诚实边界 · out-of-scope）

- **[P2-1 跨设备真实 BPDU 选举收敛]**：真实根桥选举依赖设备间 BPDU 收发，本期不做；仅本地静态假设（本桥即网内最小桥 ID）+ `stpSimNote()` 注记。
- **[P2-2 MSTP 实例级真实计算]**：实例级独立生成树真实计算本期不做；`display stp brief` 的多实例（MSTID>0）行仅展示配置态/本地静态假设，不臆造跨实例收敛。
- **[P2-3 TCN 泛洪]**：拓扑变化通知（TCN）真实泛洪本期不做；`display stp interface` 的 `TC or TCN received` 仅在诚实边界内展示（无真实计数，恒显示配置态/0 或诚实占位）。
- **[P2-4 `stp mode vbst`]**：VRP 支持 VBST（每 VLAN 生成树），课程 55/56/57 未覆盖，本期不实现；`stp mode` 校验仅放行 stp/rstp/mstp。

---

## 4. UI / 展示设计稿（CLI 回显与 display 输出样例，纯文本）

- 配置回显（系统视图 / 接口视图，VRP 风格静默成功）：
  ```
  [SW1] stp mode rstp
  [SW1] stp priority 4096
  [SW1] stp root primary
  [SW1] stp pathcost-standard dot1t
  [SW1] interface GigabitEthernet0/0/1
  [SW1-GigabitEthernet0/0/1] stp edged-port enable
  [SW1-GigabitEthernet0/0/1] stp cost 20000
  [SW1-GigabitEthernet0/0/1] stp port priority 16
  ```
- `display stp` 输出样例（P0-11 起，读 DeviceConfig + 本地静态选举 + 诚实注记）：
  ```
  -------[CIST Global Info]-------
  Mode                : RSTP
  CIST Bridge         : 32768.4c1f-cc12-3456
  Bridge Priority     : 4096
  Root Bridge         : 4096.4c1f-cc12-3456   (本地假设: 本桥桥 ID 最小, 非真实 BPDU 选举)①
  Root Path Cost      : 0
  -------[Port Role/State]-------
  GE0/0/1 : DESI  FORWARDING   (edged)
  GE0/0/2 : ROOT  FORWARDING
  GE0/0/3 : ALTE  DISCARDING    (本地静态阻塞, 非真实拓扑收敛)
  （STP 为模拟生成树（lite 引擎），非内核级真实 BPDU 选举 / 无真实拓扑收敛）
  ```
  ① 角色/状态呈现策略见 §6 #2。
- `display stp brief` 输出样例（字段对齐官方 `MSTID Port Role State`）：
  ```
  MSTID  Port            Role      State
  0      GE0/0/1         DESI      FORWARDING
  0      GE0/0/2         ROOT      FORWARDING
  0      GE0/0/3         ALTE      DISCARDING
  ```
- `display stp interface GigabitEthernet0/0/1` 输出样例（字段对齐官方 `display stp interface`）：
  ```
  CIST Global Information:
   Mode              : RSTP
   CIST Bridge       : 32768.4c1f-cc12-3456
   CIST Root/ERPC    : 4096.4c1f-cc12-3456 / 0   (本地假设, 非真实 BPDU)
   CIST RegRoot/IRPC : 32768.4c1f-cc12-3456 / 0
   CIST RootPortId   : 128.2
   BPDU-Protection   : Disabled
   TC or TCN received: 0
   Port Role/State   : DESI / FORWARDING   (edged)
  （STP 为模拟生成树（lite 引擎），非内核级真实 BPDU 选举 / 无真实拓扑收敛）
  ```
- `display stp region-configuration` 输出样例（P1-7）：
  ```
  Oper configuration
   Format selector    : 0
   Region name        : RG1
   Revision level     : 1
   Instance  VLAN Mapped
   0        1 to 4094
   1        2 to 10
   2        11 to 20
  ```
- **前端（CLI 终端）：本期无变更**。STP 仅在 CLI 文本体现；不新增 API 响应字段 / 图形化拓扑端口角色指示。

> **golden 输出待校验**：上述各 `display` 字段顺序/列宽/单位（如 ERPC/IRPC 命名、Hello/MaxAge/FwDly/MaxHop 是否展示）以官方 `display stp` / `display stp interface` 为基准，但**精确逐行 golden 输出待课程视频 55/56/57 逐帧核对后补 golden 测试**（与 VLAN/Hybrid `🟡 待校验` 同策略，见 `docs/reference/huawei-vrp-course.md`）。

---

## 5. 验收标准（可测，每条可用自动化测试证明）

- **AC1（命令解析 + VRP 规范 + 拒错）**：在 Switch/L3Switch 系统视图执行 `stp mode rstp` / `stp priority 4096` / `stp root primary` / `stp pathcost-standard dot1t` / `undo stp root` 返回成功回显，对应 `DeviceConfig["stp:mode|priority|pathcost-standard"]` 键正确写入；接口视图执行 `stp edged-port enable` / `stp cost 20000` / `stp port priority 16` 写 `interface:<iface>:stp:<field>`；非系统视图 → 含 `system view` 拒绝；Router/PC → 能力拒绝；`priority` 非 4096 倍数 / `cost` 越界（依 pathcost-standard）/ `port priority` 越界 / `pathcost-standard` 非法值 / `mode` 非法 → 明确 `Error:`；接口视图命令在系统视图执行 → `interface view` 拒绝。
- **AC2（关键子特性正确 + 持久化贯通）**：上述 STP 键随 `DeviceConfig` 往返落盘/回载（复用 `SerializeToDeviceConfigData`/`LoadFromDeviceConfigData`）；reload 后 `display stp` / `display current-configuration` 复现 mode/priority/root/pathcost-standard/edged-port/cost/port-priority/region；证明经 `DeviceConfig` 往返保留（单测比对 reload 前后 `DeviceConfig` 键集，验证 P0-1 修复生效，不再丢配置）。
- **AC3（display 忠实展示 + 诚实注记）**：`display stp` / `display stp brief`（`MSTID Port Role State`）/ `display stp interface <if>` 正确列出模式/桥优先级/根桥 ID/端口角色-状态；列头与对齐符合 §4 样例；`display current-configuration` 输出合规 STP 段（非旧 `ip` 式非 VRP 文案，P0-15）；lite 引擎下输出带 `stpSimNote()`「非内核级真实 BPDU 选举 / 无真实拓扑收敛」注记。
- **AC4（MSTP region 配置，P1）**：`stp region-configuration` → `region-name`/`revision-level`/`instance <id> vlan <...>`/`active region-configuration` 经 DeviceConfig 持久化；`display stp region-configuration` 正确展示 `Region name / Revision level / Instance VLAN Mapped`；`active` 前预配置不生效、激活后才生效（官方语义保真）。
- **AC5（纯函数本地静态选举 / 无副作用）**：`EvaluateSTP`（纯函数，读 DeviceConfig、无副作用、不写引擎、不 `import protocol`、可单测）单测覆盖——桥 ID 最小者当根桥（priority 小者胜、同优先级比 MAC 小者胜）、到根路径开销最小者当根端口、链路开销小者当指定端口、其余本地静态阻塞（ALTE/BACK）；连续两次调用结果一致且不改写无关 state；对照 `acl_eval.go:9-15`/`portsec_eval.go:7-10`/`vrrp_eval.go:13-25` 契约。
- **AC6（诚实占位 + 边界/能力拒绝）**：lite 引擎下 `display stp` / `display stp interface` / `display stp brief` 均含 `stpSimNote()` 诚实注记，不臆造跨设备 Backup/Master、不伪造 TC 计数；能力矩阵 `switchDevices()` 外的设备执行 `stp` 被拒绝；`undo stp` 清理全部 `stp:*` 键（`display stp` 复现 Disabled）。

---

## 6. 待确认问题（交架构师 / 主理人拍板）

1. **`edged-port` 视图归属（核心）**：官方 VRP 中 `stp edged-port enable` 是**接口视图**命令；现有残桩把它放在系统视图（`parser.go:1539-1547`，写全局 `stp:edged-port`）。本期是否**强制迁移到接口视图**（P0-10，写 `interface:<iface>:stp:edged-port`）？迁移会破坏现有系统视图用法——是否同时保留系统视图 `stp edged-port default`（将所有端口设为边缘，官方确有此形态）作为 P1？**产品经理建议**：P0 起 `edged-port` 仅接口视图（对齐官方、写接口键），系统视图 `stp edged-port default` 作为 P1 可选；旧全局 `stp:edged-port` 键在 P0-1 清理时一并废弃。请拍板迁移力度与是否保留 default。
2. **角色/状态呈现（仿真无真实 BPDU）**：`display stp` / `display stp interface` 的端口 Role/State 如何呈现？
   - (a) 假设本桥即网内最小桥 ID → 静态标各端口 Role（DESI/ROOT/ALTE/BACK）+ State（FORWARDING/DISCARDING），带诚实注记"本地假设选举、非真实 BPDU"；
   - (b) 仅展示配置态，不臆造 Role/State（如统一标 `----` / 不输出角色列）。
   **产品经理建议：(a) + 诚实注记**——学员最关心"我这台会不会被阻塞、哪个口是指定口"，静态按桥 ID/路径开销算 Role 并附注记，教学价值最高（与 VRRP 拍板 #2 (a) 同思路）；但 (b) 更保守诚实，若团队坚持"不臆造任何角色"则采用 (b)。请拍板。
3. **跨设备真实选举**：是否接线拓扑做真实 BPDU 选举？还是明确 out-of-scope + 诚实注记？**产品经理建议：out-of-scope（明确不在本期）**，本期仅本地静态选举 + 诚实注记（见 P2-1）。请拍板。
4. **`stp priority` 取值范围与步进**：VRP 为 0–61440、步进 4096（必须倍数）。本期是否严格按此校验（非倍数 → Error）？还是放宽？**产品经理建议：严格对齐 VRP（倍数校验）**。请拍板。
5. **`stp port priority` / `stp cost` 接口视图守卫与范围**：`cost` 范围依 pathcost-standard 三档（dot1d-1998 1–65535 / dot1t 1–200000000 / legacy 1–200000），`port priority` 0–240 步进 16（默认 128）。是否全部按官方范围校验？**产品经理建议：是**。请拍板。
6. **MSTP 实例级真实计算（P1/P2 边界）**：`display stp brief` 的 MSTID>0 行（多实例）本期是否仅展示配置态 + 本地静态假设（P2-2 out-of-scope 真实计算）？`stp [instance <id>] root`（P1-8）是否纳入本期？**产品经理建议**：P1-5 region 配置 + P1-7 `display stp region-configuration` 必做；P1-8 per-instance root 纳入 P1；MSTID>0 的真实计算明确 out-of-scope（P2-2）。请拍板 scope 是否含 P1-8。
7. **默认值对齐**：`mode` 默认 `rstp`、`priority` 默认 32768、`pathcost-standard` 默认 `dot1t`、`revision-level` 默认 0、`port priority` 默认 128 是否如实实现？**产品经理建议：是**，全部对齐 VRP 原生默认（`state.go:503-504` 已是 rstp/32768，须补其余默认）。请拍板。

---

## 7. 不在本期范围

- 建设真实 BPDU 收发 / 拓扑计算引擎与跨设备根桥选举收敛（仅本地静态选举 + 诚实注记，P2-1）；
- MSTP 实例级真实独立生成树计算（P2-2）；
- TCN 真实泛洪与拓扑变化通知（P2-3）；
- `stp mode vbst`（每 VLAN 生成树，课程未覆盖，P2-4）；
- RSTP Proposal/Agreement 快速迁移、端口状态机（Forwarding/Learning/Discarding）真实时序迁移；
- 重写 NAT / 端口安全 / VRRP（仅 STP 增量）；前端图形化拓扑端口角色/状态指示。

---

## 附：关键 file:line 证据索引 + 官方文档佐证小结

### 代码证据索引（供架构师直接定位，主理人将逐条 grep 验证）

- `internal/cli/state.go:57` `STP *STPConfig`；`:245-256` `STPConfig{Enabled,Mode,BridgePriority,Ports,VSTPEnabled,BridgeAddress,RegionName,RevisionLevel,VLANMapping,RegionActive}`；`:269-273` `STPPort{PortName,PortPriority,Cost}`；`:501-507` 构造器初始化 `Mode:"rstp", BridgePriority:32768`。
- `internal/cli/parser.go:1483-1606` 现有 `stp` 残桩：含 `enable/disable/mode/priority/v-stp/bridge-address/root primary|secondary/edged-port(系统视图)/bpdu-protection/root-protection/loop-protection/tc-protection interval/region-configuration/region-name/revision-level/instance vlan/active`；其中 `mode/priority/root/region*/bridge-address` **仅写 `state.STP` 字段，不经 DeviceConfig**（缺陷根因）。
- `internal/cli/parser.go:1541-1566` `edged-port`/`bpdu-protection`/`root-protection`/`loop-protection`/`tc-protection-interval` 已直接写裸 `DeviceConfig["stp:..."]`（口径不统一，须规整）。
- `internal/cli/parser.go:3541-3594` **现有 `display stp` 桩**（读 `state.STP` 字段、无 brief/interface/region 保真、无诚实占位——与任务简报"无 display stp"不符，已在此更正为 [已有·不合规·桩]）。
- `internal/cli/parser.go:4649-4651` `undo stp` → `state.STP.Enabled=false`（不清理 DeviceConfig 键，须规整）。
- `internal/cli/capabilities.go:70` `"stp": switchDevices()`（Switch/L3Switch/VTEP）；`switchDevices()` 定义 `:191-197`。
- `internal/cli/parser.go:4739` `SerializeToDeviceConfigData` 仅遍历 `state.DeviceConfig` 落盘（**凡不写 DeviceConfig 的配置全部丢失**——缺陷根因）；`:4757` `LoadFromDeviceConfigData` 起；`:4820-4869` OSPF/BGP/ISIS 已用"循环写回 DeviceConfig + 重建结构化字段"范式修复同类缺陷（**本期 STP 直接复用该范式**）。
- `internal/cli/acl_eval.go:493-507` `aclSimNote()`/`natSimNote()`（lite/full 两态、读 `sim.EngineModeName()`）；`portsec_eval.go:236-244` `portSecSimNote()`；`vrrp_eval.go:394-402` `vrrpSimNote()`（诚实占位口径基准）。
- `internal/cli/p1f_qa_test.go:228-231` 现有 `undo stp` 测试（P0-7 须保持行为契约）。

### 官方文档佐证小结（哪些命令已用官方文档确认）

| 命令 | 官方依据 | 核验状态 |
|---|---|---|
| `stp mode {stp\|rstp\|mstp}` | ZZ 基本功能配置；EE `stp mode` | ✅ 主源 |
| `stp [instance] priority`（默认 32768，4096 倍数） | EE Configuring Parameters…；★ NetEngine AR doc | ✅ 主源+核证 |
| `stp root primary`（priority 0）/ `secondary`（4096） | EE/ZZ 配置根桥；★ | ✅ 主源+核证 |
| `undo stp [instance] root` | EE「…run the `undo stp root` command」★ | ✅ 核证 |
| `stp pathcost-standard {dot1d-1998\|dot1t\|legacy}`（默认 dot1t） | ZZ/EE；★ EDOC1000015892/adc7faa1 | ✅ 主源+核证 |
| `stp [instance] cost`（接口视图，范围随标准） | EE「Set a path cost value…」★ | ✅ 主源+核证 |
| `stp [instance] port priority`（接口视图，默认 128） | EE「Set the port priority」★ | ✅ 主源+核证 |
| `stp edged-port enable\|disable`（接口视图） | ZZ 配置边缘端口；★ | ✅ 主源+核证 |
| `stp bpdu-protection` / `root-protection` / `loop-protection` / `tc-protection [interval]` | ZZ 配置 BPDU/根/环路/TC 保护 | ✅ 主源 |
| `stp region-configuration` → `region-name` / `revision-level`（默认 0）/ `instance <id> vlan <...>` / `active region-configuration` | ZZ/EE 配置 MST 域；★ EDOC1000141427/fe03199b、EDOC1000089023/5e917476 | ✅ 主源+核证 |
| `stp bridge-address <mac>` | ZZ 配置桥 MAC | ✅ 主源 |
| `display stp [brief]`（`MSTID Port Role State`） | ZZ/EE 查看生成树状态；★ 故障排查样例 | ✅ 主源+核证 |
| `display stp interface <if>`（Mode/CIST Bridge/Root/ERPC/RegRoot/IRPC/RootPortId/BPDU-Protection/TC） | ZZ/EE 查看端口状态；★ 故障排查样例 | ✅ 主源+核证 |
| `display stp region-configuration`（Region name/Revision/Instance VLAN Mapped） | ZZ/EE 查看 MST 域；★ 故障排查样例 | ✅ 主源+核证 |

> **核验说明**：三项主源 URL（EDOC1100367112/b9cc9782、EDOC1100466179/c9a6630d/EN-US_TASK_0000001743120048、EDOC1100213107/40e7812e）已经主理人核验可用，但因官网为 JS 渲染、WebFetch 仅能回显标题，本 PRD 对"非显而易见"的命令（`pathcost-standard` 取值与默认 dot1t、`priority` 默认 32768 与 4096 倍数、`cost`/`port priority` 接口视图与默认 128、`undo stp root`、`region-configuration` 子命令与默认 revision-level 0、`display stp brief` 字段）额外经 WebSearch 二次核证于华为官方命令参考/配置指南/故障排查文档（标记 ★），确保命令语法与默认值不凭记忆杜撰。**各 `display` 的精确逐行 golden 输出仍待课程视频 55/56/57 逐帧校验**（见 §4 标注）。

## 文档状态

- 基线核查完成：STP 残桩（`parser.go:1483`）、`STPConfig`（`state.go:245`）、能力矩阵（`capabilities.go:70`）、`undo stp`（`parser.go:4649`）、持久化机制（`parser.go:4739/4757`）、诚实占位范式（`acl_eval.go`/`portsec_eval.go`/`vrrp_eval.go`）均已存在；**核心缺口**为"不写 DeviceConfig（不持久化）、命令格式/校验非 VRP 保真、无本地静态选举/诚实占位、display 桩不保真"。
- 经代码核查**更正任务简报一处**：`display stp` 桩实际已存在（`parser.go:3541`），但为非合规桩（读 struct 字段、无 brief/interface/region 保真、无诚实注记），已按 [已有·不合规·桩] 记录，方案为"重写而非新建"。
- 纯函数评估器与诚实占位范式有 `acl_eval.go`/`portsec_eval.go`/`vrrp_eval.go` 可直接对齐，零新依赖、零引擎改动；持久化修复直接复用 OSPF/BGP/ISIS 的 `LoadFromDeviceConfigData` 重建范式。
- 7 项待确认已收敛，其中 #1（edged-port 接口视图迁移）、#2（角色呈现 (a)+注记）、#3（跨设备选举 out-of-scope）、#4（priority 倍数校验）、#5（cost/port-priority 官方范围）、#7（如实默认）给出产品经理明确建议。

_Last updated: 2026-08-09 · 产品经理 许清楚（Xu）_
