# ensp-lab P2 第三项：VRRP（华为 VRP 实训课程 60/61）增量 PRD

> 文档类型：增量产品需求文档（PRD，简单模式，结构对齐 `docs/p2-portsec-prd.md`）
> 关联：`docs/p2-portsec-prd.md`（端口安全增量 PRD，风格/章节对齐基准）、`docs/p2-nat-prd.md`（NAT 增量 PRD）、`internal/cli/parser.go` / `state.go` / `capabilities.go` / `acl_eval.go` / `portsec_eval.go`（已核查代码基线）
> 作者：产品经理 许清楚（Xu）
> 语言：中文

---

## 0. 项目信息

- **Language**：中文
- **Programming Language**：Go 1.26.5（本期工作集中在 `internal/cli`；前端 React/Vite 本期无变更）
- **Project Name**：`p2_vrrp`
- **原始需求复述**：在 P2 已交付 NAT（课程 38）、端口安全（课程 49）之后，为华为 eNSP VRP 仿真器落地 **VRRP（虚拟路由冗余协议，课程 60/61）** 的增量实现：补齐接口视图下的 `vrrp vrid X virtual-ip ...` 命令族，使配置经 `DeviceConfig` 持久化，新增/升级 `display vrrp [brief|interface|vrid]` 忠实展示，并以**纯函数**实现主备选举/优先级比较/track 降优先级，同时按架构基线提供 **诚实占位**（lite 模式标注"非内核级真实 VRRP 故障切换"）。

> **深度边界先验结论（务必先读 §6 拍板项）**：VRRP 是 **L3 网关冗余** 特性，真实 Master/Backup 选举依赖设备间 VRRP 通告报文（心跳）。当前 sim 引擎**无真实 HA 心跳/故障切换**（`EngineModeName()` 仅用于决定诚实注记两态，见 `acl_eval.go:493-498`），因此"真实跨设备选举/故障切换"本期不可为，必须在纯函数选举结果之上叠加诚实占位注记。代码基线里已存在一个**不合规的 VRRP 残桩**（见 §3 已有表），本期必须按架构基线重写/扩展，而非另起炉灶。

---

## 1. 产品目标

在严守 P2「CLIState 层纯函数、单一事实源 `DeviceConfig`、诚实占位、零改动 `sim` 引擎」架构基线的前提下，把 VRRP 从代码里一个"仅写入 `state.VRRP` 结构化 map、不持久化、无状态机、无诚实占位"的残桩，升级为一条**可对学员实验产生可观测反馈**的 L3 网关冗余链路：让学员能按 VRP 课程 60/61 真实命令配置虚拟网关组（virtual-ip / priority / preempt / advertise / track / authentication），在 `display vrrp` 中忠实核对配置与本地角色，并借助纯函数选举直观理解"优先级高者胜、同优先级比 IP 大者胜、虚拟 IP 拥有者（priority=255）直接 Master"的选举规则——所有引擎无法真实模拟的行为一律以诚实占位注记标注，绝不编造状态或数字。

---

## 2. 用户故事

1. **作为路由实验学员**：As a 学员，I want 在 Router/L3Switch 的接口视图依次敲 `vrrp vrid 1 virtual-ip 192.168.1.254`、`vrrp vrid 1 priority 120`、`vrrp vrid 1 preempt-mode disable`，so that 该接口成为 VRRP 组 1 的网关冗余配置，能用 `display vrrp` 核对 virtual-ip / priority / 角色。
2. **作为关注持久化的学员**：As a 学员，I want `save` 后 `reload` 设备，so that VRRP 配置（含 virtual-ip / priority / preempt / advertise / track）仍保留，`display vrrp` 与 `display current-configuration` 复现，而不必重配（现有残桩因不写 `DeviceConfig` 会丢配置，见 `parser.go:1793-1838`）。
3. **作为理解选举机制的学员**：As a 学员，I want 看到纯函数选举给出的"谁当 Master"（按优先级、同优先级比 IP、虚拟 IP 拥有者 priority=255 直接 Master），so that 我能对照课程 60 的选举规则验证自己的配置，并明确知道这是"本地假设的静态选举结果、非跨设备真实通告"。
4. **作为关注链路可靠性的学员（P1）**：As a 学员，I want 配置 `vrrp vrid 1 track interface GigabitEthernet0/0/2 reduced 30`，so that 当被跟踪上行口故障时（按诚实占位，需显式触发/已有接口状态键），本设备优先级下降、主权谦让，我能在 `display vrrp` 看到降后的有效优先级。
5. **作为排障学员**：As a 学员，I want 用 `display vrrp brief` 一眼看到所有 VRRP 组的状态摘要，so that 快速定位哪组是 Master、虚拟 IP 是什么、角色是什么。

---

## 3. 需求池

### 已有（本期重构 / 扩展，非另起炉灶）

| 标记 | 内容 | 位置 |
|---|---|---|
| [已有·不合规] | 顶层 `vrrp` 分支：仅支持扁平 `vrrp <id> <vip> [priority X] [preempt disable] [delay X]`，写入 `state.VRRP[groupID]`（**非 `DeviceConfig`**），无虚拟 IP 同网段校验、无状态机、无诚实占位 | `parser.go:1793-1838` |
| [已有·不合规] | `state.VRRP map[int]*VRRPConfig` 与 `VRRPConfig{GroupID,VirtualIP,Priority,Preempt,Delay}`：缺 `AdvertiseTimer` / `TrackInterface` / `TrackReduced` / `AuthMode` / `AuthKey` / `State` 字段 | `state.go:58`、`state.go:276-282`、`state.go:517` |
| [已有·简化] | `display vrrp`：仅打印 Group / VirtualIP / Priority / Preempt / Delay，无状态字段、无 brief / interface / vrid 细化、无诚实占位 | `parser.go:3628-3647` |
| [已有·不合规] | `display current-configuration` 输出 `vrrp vrid %d ip %s`（用扁平 `ip` 而非 VRP 的 `virtual-ip`，与新命令格式不一致） | `parser.go:2689-2691` |
| [已有] | 能力矩阵：`"vrrp": l3Devices()`（Router / L3Switch / Firewall / VTEP），通用能力校验在 `ExecuteCommandOn` 的 `isCommandSupported` | `capabilities.go:57`、`parser.go:245` |
| [已有] | `?` 帮助已列出 `vrrp`；`tools.go` 有 `display` 子命令缩写 `vr→vrrp` 归一化（已支持 `display vrrp` 派发） | `parser.go:1247`、`tools.go:61` |
| [已有·基线] | 配置持久化机制：`SerializeToDeviceConfigData`（遍历 `DeviceConfig` 落盘，`parser.go:4618`）↔ `LoadFromDeviceConfigData`（回写 `DeviceConfig`，`parser.go:4649`）；因 VRRP 不写 `DeviceConfig`，**当前 reload 后丢失** | `parser.go:4618-4647`、`parser.go:4649` 起 |
| [已有·基线] | 纯函数评估器契约范式：`EvaluatePathACL` / `applyNAT`（`acl_eval.go`，无副作用、不写引擎、可单测）、`EvaluatePortSecurity`（`portsec_eval.go`）；诚实占位 `aclSimNote()` / `natSimNote()` / `portSecSimNote()`（lite / full 两态，读 `sim.EngineModeName()`） | `acl_eval.go:9-15,110-227,493-507`、`portsec_eval.go:7-10,179-244` |

### P0（本期核心 · 对齐端口安全「A + B-lite 同交」的力度建议）

- **[P0 重写] `vrrp vrid` 命令族（接口视图）**：将残桩改写为 VRP 真实子命令，全部经 `DeviceConfig["interface:<iface>:vrrp:<vrid>:<field>"]` 持久化（单一事实源），子命令至少含：
  - `vrrp vrid <1-255> virtual-ip <ip>`：写 `virtual-ip` 键；**做 virtual-ip 与接口 IP 同网段校验**（见 §6 #4），失败回显明确 `Error:`。
  - `vrrp vrid <1-255> priority <1-254>`：写 `priority` 键；默认 100（见 §6 #6）。
  - `vrrp vrid <1-255> preempt-mode disable`：写 `preempt` 键（`disable`→关闭抢占；默认开启）。
  - `vrrp vrid <1-255> timer advertise <1-255>`：写 `advertise` 键；默认 1s。
- **[P0 重写] 配置持久化贯通**：所有 VRRP 键随既有 `DeviceConfig` 往返自动落盘 / 回载（零新增持久化代码，复用 `SerializeToDeviceConfigData` / `LoadFromDeviceConfigData`）；reload 后 `display vrrp` 与 `display current-configuration` 复现；`display current-configuration` 的 `ip %s` 改为 VRP 合规的 `virtual-ip %s`。
- **[P0 新增] 纯函数主备选举**：新增 `vrrp_eval.go`，定义 `EvaluateVRRPRole(local *VRRPGroup, peers []*VRRPGroup) (role string, effectivePriority int)`（及 `CompareVRRPPriority` 含 tie-break）：只读 `DeviceConfig` 中 vrrp 键，按"优先级高者胜 / 同优先级比接口 IP 大者胜 / 虚拟 IP 拥有者（priority=255）直接 Master"规则计算本地角色；**无副作用、不写引擎、不 `import sim`、可单测**，与 `EvaluatePortSecurity` 同一契约。
- **[P0 升级] `display vrrp` 忠实展示**：展示每个已配 VRRP 组的 `interface / vrid / virtual-ip / priority / preempt / advertise / state(Master|Backup|Initialize) / 诚实占位注记`；新增 `display vrrp brief`（摘要表：interface / vrid / virtual-ip / priority / role）、`display vrrp interface <iface>`、`display vrrp vrid <id>`（对齐 VRP，见 §4 样例）。
- **[P0 新增] 诚实占位 `vrrpSimNote()`**：lite →「（VRRP 为模拟选举（lite 引擎），非内核级真实 VRRP 故障切换）」；full →「（VRRP 为模拟选举）」，口径与 `aclSimNote()` / `natSimNote()` / `portSecSimNote()` 完全一致（`acl_eval.go:493-507`、`portsec_eval.go:236-244`）。
- **[P0 新增] 拒错与守卫**：非接口视图执行 `vrrp` → `Error: must be in interface view`；非 `l3Devices()` 设备（如 Switch / PC）→ 能力拒绝（沿用 `parser.go:245`）；`vrid` 越界（非 1-255）、`priority` 越界（非 1-254）、`advertise` 越界（非 1-255）、`virtual-ip` 非法 / 不同网段 → 明确 `Error:`。

### P1（增强真实语义，建议默认纳入）

- **[P1 新增] `vrrp vrid X track interface <iface> [reduced <1-255>]`**：写 `track-interface` / `track-reduced` 键；纯函数 `EvaluateVRRPRole` 计算有效优先级 = 配置优先级 −（被跟踪接口 Down 时 `reduced`，缺省 10）；诚实占位：被跟踪接口 Down 状态需显式触发（如本仿真已有的接口 `shutdown` / 状态键），不臆造链路事件。
- **[P1 新增] `vrrp vrid X authentication-mode {simple|md5} <key>`**：写 `auth-mode` / `auth-key` 键（仅配置态持久化与展示，不做真实认证算法，诚实占位）。
- **[P1 新增] `undo vrrp vrid <id> [virtual-ip <ip>]`**：接口视图下按 vrid（可选 virtual-ip）删除对应 `DeviceConfig` vrrp 键；对齐 `undo` 既有分支（`parser.go:860-907`）。
- **[P1 升级] `display vrrp` 细化**：展示 track 状态（被跟踪接口、reduced 值、有效优先级）、认证模式（仅显示模式，不显示明文 key 或脱敏展示）。

### P2（增强 / 诚实边界）

- **[P2] 抢占延迟 `vrrp vrid X preempt timer delay <0-3600>`**：写 `preempt-delay` 键；展示用，因无真实切换时序，延迟语义仅配置态 + 诚实注记。
- **[P2] 拓扑多设备选举有限接线**：若主理人拍板（§6 #3）允许"接线拓扑 peers 做真实选举"，则在 `EvaluateVRRPRole` 增加 peers 入参（读取拓扑内同 vrid 其他设备的 vrrp 键），但**默认 out-of-scope**，本期仅本地静态选举 + 诚实注记。
- **[P2] 前端展示增强**：CLI 终端文本已足够；本期不新增 API 字段 / 图形化拓扑 Master / Backup 指示（与 NAT / 端口安全一致）。

---

## 4. UI / 展示设计稿（CLI 回显与 display vrrp 输出样例，纯文本表格）

- 配置回显（`interface GigabitEthernet0/0/1` 视图）：
  ```
  [R1-GigabitEthernet0/0/1] vrrp vrid 1 virtual-ip 192.168.1.254
  [R1-GigabitEthernet0/0/1] vrrp vrid 1 priority 120
  [R1-GigabitEthernet0/0/1] vrrp vrid 1 preempt-mode disable
  [R1-GigabitEthernet0/0/1] vrrp vrid 1 timer advertise 2
  ```
- `display vrrp` 输出样例（P0 起）：
  ```
  GigabitEthernet0/0/1 | Virtual Router 1
    State           : Master            (本地假设选举，非跨设备真实通告)①
    Virtual IP      : 192.168.1.254
    Priority        : 120
    Preempt         : Disabled
    Advertise Timer : 2 s
  （VRRP 为模拟选举（lite 引擎），非内核级真实 VRRP 故障切换）
  ```
  ① 角色呈现策略见 §6 #2 拍板。
- `display vrrp brief` 输出样例：
  ```
  VRID  Interface           Virtual IP      Priority  Role
  1     GE0/0/1             192.168.1.254   120       Master
  2     GE0/0/1             192.168.1.253   100       Backup
  ```
- `display vrrp interface GigabitEthernet0/0/1` / `display vrrp vrid 1`：单组详情（上表字段 + track / auth 字段，P1 起）。
- **前端（CLI 终端）：本期无变更**。VRRP 仅在 CLI 文本体现。

---

## 5. 验收标准（可测，每条可用自动化测试证明）

- **AC1（命令解析 + DeviceConfig 持久化）**：在 Router / L3Switch 接口视图执行 `vrrp vrid 1 virtual-ip 192.168.1.254` / `priority 120` / `preempt-mode disable` / `timer advertise 2` 均返回成功回显，对应 `DeviceConfig["interface:<iface>:vrrp:1:virtual-ip|priority|preempt|advertise"]` 键正确写入；非接口视图 → 含 `interface view` 拒绝；Switch / PC → 能力拒绝；`vrid 0` / `vrid 256` / `priority 255` / `advertise 0` / 非法 virtual-ip / 不同网段 → 明确 `Error:`。
- **AC2（关键子特性正确）**：`priority`（1-254，默认 100）、`preempt-mode`（默认开启、disable 可关）、`timer advertise`（默认 1s）、`track interface reduced`（P1，有效优先级计算正确）解析与持久化正确。
- **AC3（display 忠实展示）**：`display vrrp` / `display vrrp brief` / `display vrrp interface <iface>` / `display vrrp vrid <id>` 正确列出已配 VRRP 组的 virtual-ip / priority / preempt / advertise / 角色；列头与对齐符合 §4 样例；`display current-configuration` 输出合规 `vrrp vrid X virtual-ip Y`（非旧 `ip`）。
- **AC4（纯函数选举 / 优先级比较正确，含 tie-break）**：单测覆盖——优先级高者胜；同优先级比接口 IP 大者胜；虚拟 IP 拥有者（priority=255）直接 Master；tie-break 确定性；track 降优先级（P1）。
- **AC5（lite 诚实占位）**：lite 引擎下 `display vrrp` 输出带「非内核级真实 VRRP 故障切换」注记；角色标注"本地假设选举"等诚实文案，不臆造跨设备 Backup / Master 状态。
- **AC6（纯函数无副作用）**：`EvaluateVRRPRole` / `CompareVRRPPriority` 单测证明——不修改 `sim` 引擎、不 `import protocol`、零新依赖、连续两次调用结果一致且不改写无关 state；对照 `acl_eval.go:9-15`、`portsec_eval.go:7-10` 契约。

---

## 6. 待确认问题（交架构师 / 主理人拍板）

1. **交付力度（核心）**：P0 是否一次性交付「配置解析 + DeviceConfig 持久化 + display 忠实展示 + 纯函数选举 + 诚实占位」全套（参照端口安全拍板 #1：A + B-lite 同交）？**产品经理建议：是**，P0 即含纯函数选举与诚实占位（开销小、价值高）；track / auth / undo 归入 P1，抢占延迟 / 拓扑接线归 P2。请拍板 P0 是否包含纯函数选举与 honest placeholder，还是仅配置 + display、选举延后。
2. **主备角色呈现（仿真无跨设备心跳）**：本地角色如何呈现？
   - (a) 假设本设备即最高优先级 → 静态标 `Master`（带诚实注记"本地假设选举、非跨设备真实通告"）；
   - (b) 仅展示 `Initialize`，不臆造 Master / Backup。
   **产品经理建议：(a) + 诚实注记**——学员最关心"我这组会不会当 Master"，静态按优先级算 Master 并附注记，教学价值最高；但 (b) 更保守诚实，若团队坚持"不臆造任何角色"则采用 (b)。请拍板。
3. **跨设备真实选举**：是否接线拓扑 peers 做真实选举？还是明确 out-of-scope + 诚实注记？**产品经理建议：out-of-scope（明确不在本期）**，本期仅本地静态选举 + 诚实注记；未来若建设拓扑 peer 选举再接（见 P2）。请拍板。
4. **virtual-ip 与接口 IP 同网段校验**：要不要做？失败回显什么？**产品经理建议：做（P0）**，校验 virtual-ip 与接口 `ip address` 同网段，失败回显 `Error: virtual-ip x.x.x.x is not in the same subnet as interface IP`，并诚实说明"仅校验语法同网段，不验证引擎可达"。请拍板是否校验、回显文案。
5. **能力归属**：VRRP 命令开放给 Router 还是 L3Switch 还是两者？当前基线已是 `l3Devices()`（Router / L3Switch / Firewall / VTEP，`capabilities.go:57`）。**产品经理建议：保持 `l3Devices()`（最小改动，VRRP 本就是 L3 特性）**；若课程 60/61 仅用 Router，可在拍板时收窄为 Router + L3Switch + Firewall（VTEP 一般不跑 VRRP）。请拍板。
6. **默认值**：`priority 100` / `preempt 开启` / `advertise 1s` 是否如实实现？**产品经理建议：是**，全部对齐 VRP 原生默认（priority 默认 100、抢占默认开启、advertise 默认 1s），`VRRPConfig` 初始化与解析缺省均按此。请拍板。

---

## 7. 不在本期范围

- 建设真实 VRRP 通告 / 心跳引擎与跨设备故障切换（仅本地静态选举 + 诚实占位）；
- VRRP 与 MSTP / VXLAN 分布式网关（`distributed-gateway`）的联动；
- 真实认证算法（`authentication-mode` 仅配置态与展示，不做 md5 / simple 校验）；
- 抢占延迟的真实时序模拟（`preempt timer delay` 仅配置态 + 诚实注记）；
- 重写 NAT / 端口安全（仅 VRRP 增量）；前端图形化拓扑 Master / Backup 指示。

---

## 附：关键 file:line 证据索引（供架构师直接定位，主理人将逐条 grep 验证）

- `internal/cli/parser.go:1793-1838` 现有 `vrrp` 残桩：扁平 `vrrp <id> <vip> [priority] [preempt] [delay]`，仅写 `state.VRRP[groupID]`（**非 `DeviceConfig`**），无校验 / 无状态机 / 无诚实占位。
- `internal/cli/state.go:58` `VRRP map[int]*VRRPConfig`；`state.go:276-282` `VRRPConfig{GroupID,VirtualIP,Priority,Preempt,Delay}`（缺 AdvertiseTimer / Track / Auth / State）；`state.go:517` 构造器初始化 `VRRP: make(map[int]*VRRPConfig)`。
- `internal/cli/parser.go:3628-3647` 现有 `display vrrp`（仅 Group / VIP / Priority / Preempt / Delay，无状态 / 无 brief / 无诚实占位）。
- `internal/cli/parser.go:2689-2691` `display current-configuration` 输出 `vrrp vrid %d ip %s`（扁平 `ip`，与新 `virtual-ip` 格式不一致，需改）。
- `internal/cli/capabilities.go:57` `"vrrp": l3Devices()`（Router / L3Switch / Firewall / VTEP）；`capabilities.go:166-173` `l3Devices()` 定义。
- `internal/cli/parser.go:245` `ExecuteCommandOn` 内 `isCommandSupported` 通用能力校验；`parser.go:860-907` `undo` 既有分支（P1 `undo vrrp` 在此扩展）。
- `internal/cli/tools.go:61` `display` 子命令缩写 `vr→vrrp` 归一化（已支持 `display vrrp` 派发）。
- `internal/cli/parser.go:4618-4647` `SerializeToDeviceConfigData`（遍历 `DeviceConfig` 落盘）；`parser.go:4649` 起 `LoadFromDeviceConfigData`（回写 `DeviceConfig`）；因 VRRP 不写 `DeviceConfig`，当前 reload 后丢失——证明"单一事实源 = DeviceConfig"基线缺口。
- `internal/cli/acl_eval.go:9-15` 纯函数评估器契约注释（无副作用、不写 state、不碰 sim 引擎）；`acl_eval.go:110-140` `EvaluateDeviceACL` 纯函数范例；`acl_eval.go:493-498` `aclSimNote()`、`:502-507` `natSimNote()`（lite / full 两态、读 `sim.EngineModeName()`）。
- `internal/cli/portsec_eval.go:7-10` 纯函数契约；`:179-234` `EvaluatePortSecurity`（读 `DeviceConfig`、无副作用、返回 `Learned` / `Violation` 由调用方落地）；`:236-244` `portSecSimNote()`（口径基准）。
- `internal/cli/parser.go:1247` `?` 帮助已列 `vrrp`（命令可见性基线）。

---

## 文档状态

- 基线核查完成：VRRP 残桩（`parser.go:1793`）、`VRRPConfig`（`state.go:276`）、能力矩阵（`capabilities.go:57`）、display 派发（`parser.go:3628`）、持久化机制（`parser.go:4618/4649`）均已存在；**核心缺口**为"不写 DeviceConfig（不持久化）、命令格式非 VRP、无状态机 / 选举、无诚实占位"。
- 纯函数评估器与诚实占位范式有 `acl_eval.go` / `portsec_eval.go` 可直接对齐，零新依赖、零引擎改动。
- 6 项待确认已收敛，其中 #1（力度）、#2（角色呈现 (a)+注记）、#4（同网段校验）、#5（保持 l3Devices）、#6（如实默认）给出产品经理明确建议；#3（跨设备选举）建议 out-of-scope。

_Last updated: 2026-08-09 · 产品经理 许清楚（Xu）_
