# ensp-lab P2 第二项：端口安全（Port Security）真实准入（增量 PRD · 简单 PRD）

> 文档类型：增量产品需求文档（PRD，简单模式）
> 关联：`docs/p2-nat-prd.md`（NAT 增量 PRD，风格对齐）、`docs/p1f-cli-prd.md` §4.2（端口安全顶层分支已有）、`docs/reference/huawei-vrp-course.md` 课程 49（端口安全，验收 oracle）、`internal/cli/parser.go:4082` `applyPortSecurity`（已有解析）
> 作者：产品经理 许清楚（Xu）
> 语言：中文

---

## 0. 项目信息

- **Language**：中文
- **Programming Language**：Go 1.26.5（本期工作集中在 `internal/cli`；前端 React/Vite 本期无变更）
- **Project Name**：`p2_port_security`
- **原始需求复述**：在 P1-F 已落地的 `port-security` 解析基础上，把端口安全从「仅写 `DeviceConfig` 键的启用标记」升级为**可配置、可持久化、可忠实展示、并（按拍板力度）可真实触发违规动作**的 L2 接入控制特性，对齐课程 49 的 `port-security enable` / `max-mac-num` / `mac-address sticky` / `protect-action` / `aging-time` 与 `display port-security` / `display mac-address` 行为。

> **深度边界先验结论（务必先读 §6 拍板项）**：端口安全是 **L2 MAC 学习/准入** 特性，与 P1-C ACL / P2 NAT 的 **L3 路径评估（ping/tracert）** 本质不同。当前 sim 引擎以 L3 路径评估为主，**无 L2 帧转发/学习引擎**（`MACTable` 全代码库仅初始化、从未被填充，见 `state.go:602`）。因此本项"真实模拟"的成色完全取决于 §6 方案 A/B 的力度拍板。

---

## 1. 产品目标

在保持 P1-C/NAT「CLIState 层纯函数、诚实占位、不碰 sim 引擎」架构基线的前提下，让端口安全从 P1-F 的"启用标记"进化为一条**可对学员实验产生可观测反馈**的 L2 接入控制链路：

1. **配置真实性**：补齐 `protect-action` / `aging-time` / 粘滞 MAC 手动绑定等命令，使 `port-security` 命令集对齐 VRP 课程 49，且全部经 `DeviceConfig` 往返**持久化**（reload 后保留）。
2. **展示忠实性**：新增 `display port-security [interface ...]` 忠实呈现每端口的安全状态（启用/最大 MAC/保护动作/粘滞/老化/已学 MAC 数/违规计数）；`display mac-address` 的 `Type` 列应正确标注 `security` / `sticky` / `static` 等类型。
3. **（按拍板）准入真实性**：按选定力度，使违规（超出 `max-mac-num`、或来源 MAC 非粘滞/非授权）能按 `protect-action` 真实触发（protect=丢弃不告警；restrict=丢弃+告警标志；shutdown=端口 error-down 并在 `display` 体现），让学员直观理解端口安全的"过滤"语义。

---

## 2. 用户故事

1. **作为交换实验学员**：As a 学员，I want 在接入交换机端口依次敲 `port-security enable` / `max-mac-num 2` / `protect-action restrict` / `mac-address sticky`，so that 端口被限制为仅允许 2 个 MAC 且违规时记录日志，并能用 `display port-security` 核对配置。
2. **作为网络实验学员（关注持久化）**：As a 学员，I want `save` 后 `reload` 设备，so that 端口安全配置（含 protect-action / aging-time / sticky 标志）仍保留，`display port-security` 复现，而不必重配。
3. **作为安全/排障学员（仅方案 B-lite）**：As a 学员，I want 用诊断命令 `simulate frame <src-mac> [vlan]` 向某端口"注入"一帧，so that 当该 MAC 超出限制或非授权时，按 `protect-action` 看到丢弃/告警/端口 shutdown 的真实效果，并能在 `display port-security` / `display mac-address` 中观察到安全 MAC 与违规计数。

---

## 3. 需求池

### 已有（本期不重做，仅扩展）

| 标记 | 内容 | 位置 |
|---|---|---|
| [已有] | 顶层 `port-security` 分支（需接口视图守卫）、`port security` 子命令均委托 `applyPortSecurity` | `parser.go:2346` / `parser.go:735-738` |
| [已有] | `applyPortSecurity`：仅支持 `enable`/`disable`/`max-mac-num`/`mac-address sticky`（自动粘滞标志），写 `DeviceConfig` 键 | `parser.go:4082-4113` |
| [已有] | 能力矩阵：`port-security` 声明 `switchDevices()`（Switch/L3Switch/VTEP） | `capabilities.go:79` |
| [已有] | `display mac-address`：遍历 `state.MACTable` 输出 `MAC/VLAN/Interface/Type`；当前 `MACTable` 恒空（无学习） | `parser.go:3143-3152` |
| [已有] | `MACEntry{MAC,VLAN,Interface,Type}` 结构；`MACTable []*MACEntry` 仅初始化为空 | `state.go:82,125-130,602` |
| [已有] | 配置持久化机制：`SerializeToDeviceConfigData`（遍历 `DeviceConfig` 落盘，`parser.go:4384/4397`）↔ `LoadFromDeviceConfigData`（回写 `DeviceConfig`，`parser.go:4415`）；port-security 现有 3 键今日已持久化 |
| [已有] | 现有 port-security 测试：接口视图守卫、switch 支持、router 拒绝 | `p1f_test.go:45-71` / `p1f_qa_test.go:57-133` |

### P0（本期核心 · = 方案 A 最小可用集：命令解析 + 配置持久化 + display 忠实展示）

- **[P0 新增] 补齐 `port-security` 命令解析（接口视图）**：在 `applyPortSecurity` 现有 4 子命令基础上新增——
  - `port-security protect-action {protect | restrict | shutdown}`：写 `DeviceConfig["interface:<iface>:port-security-protect-action"]`；并校验取值合法（仅三选一，非法报错）。
  - `port-security aging-time <time>`：写 `DeviceConfig["interface:<iface>:port-security-aging-time"]`；校验 `<time>` 为合法正整数（范围与 VRP 对齐，建议 1–1440 分钟；见待确认 #4），非法报错。
  - `port-security mac-address sticky [<mac-address> vlan <vlan-id>]`：保留现有自动粘滞标志；新增**手动绑定**形态（指定 MAC+VLAN）写 `DeviceConfig["interface:<iface>:port-security-sticky-mac:<mac>"]`（多条可并列），用于"预绑定授权 MAC"。
- **[P0 新增] 配置持久化贯通**：上述所有 port-security 键（含新增 protect-action/aging-time/sticky-mac）经既有 `DeviceConfig` 往返自动落盘/回载（零新增持久化代码，复用 `SerializeToDeviceConfigData`/`LoadFromDeviceConfigData`）；reload 后 `display port-security` 与 `display current-configuration` 复现。
- **[P0 新增] `display port-security [interface ...]`**：新增 display 子命令，逐端口（或指定接口）忠实展示：启用状态（enable/disable）、`max-mac-num`、当前已学安全 MAC 数 / 上限、`protect-action`、粘滞是否开启、手动绑定 MAC 列表、`aging-time`、违规计数（violation count，方案 A 下恒为 0 或仅配置态）。输出风格对齐 `display mac-address` / `display vlan`（等宽列、华为术语）。
- **[P0 新增] `display mac-address` 的 `Type` 列忠实标注**：确保 `Type` 字段能渲染 `security` / `sticky` / `static` / `dynamic` 等真实 VRP 类型标签（方案 A 下 MACTable 仍可能为空，但渲染逻辑须正确；方案 B-lite 下由帧注入填充）。
- **[P0 新增] 拒错与守卫（对齐既有）**：非接口视图执行 `port-security` → `Error: must be in interface view`；非 `switchDevices()` 设备（如 Router/PC）→ 能力拒绝（沿用 `capabilities.go` 守卫，与 `p1f_test` 一致）；非法参数（max-mac-num 非数字/超 1–4096、protect-action 非法值、aging-time 非法、sticky MAC 格式错误）→ 明确 `Error: ...`。

### P1（方案 B-lite · 真实准入钩子，强烈建议默认纳入）

- **[P1 新增] 诊断命令 `simulate frame <src-mac> [vlan <vlan-id>]`（新增）**：在指定（当前）接口视图下"注入"一帧，作为端口安全准入判定的**唯一触发点**。理由见 §6 待确认 #2：端口安全是 L2 接入，不应强行塞进 L3 的 ping/tracert 路径评估；用独立诊断命令保持"纯函数 + 不碰 sim 引擎"基线，与 NAT 的 `ComputeL3PathNAT`/诊断钩子思路一致。
- **[P1 新增] 纯函数 `EvaluatePortSecurity(state *CLIState, iface, frame) (admit bool, violation *ViolationResult)`**：只读 `DeviceConfig` 中 port-security 键与 `state.MACTable`，判定——
  - 端口未 `enable` → admit=true（不介入）；
  - 来源 MAC 属手动绑定（sticky-mac）或已学安全/粘滞 MAC → admit=true；
  - 来源 MAC 超出 `max-mac-num` 且非授权 → 触发 `protect-action`：protect=丢弃不记录；restrict=丢弃 + 置告警标志（violation++）；shutdown=端口 error-down 置位（`DeviceConfig["interface:<iface>:port-security-error-down"]="true"`）+ violation++；
  - 合法新 MAC 且未达上限 → 准入；若 `sticky` 开启则把该 MAC 写入 `MACTable`（Type=`sticky`）；否则按 `aging-time` 视为 `security`/`dynamic` 类型。
  - **无副作用、不写引擎、可单测**（与 `applyNAT` 同一契约）。
- **[P1 新增] `display port-security` 体现运行态**：方案 A 基础上，新增展示"已学安全/粘滞 MAC 列表（取自 MACTable）"与"违规计数 / error-down 状态"，使 `simulate frame` 的效果可被观察。

### P2（诚实占位 / 标注，非功能）

- **[P2] 诚实占位注记**：方案 B-lite 下 `simulate frame` 输出追加「模拟帧，非内核级真实 L2 转发」注记，口径与 `aclSimNote()` / `natSimNote()` 一致（lite：「模拟帧注入（lite 引擎），非内核级真实 MAC 学习」/ full：「模拟帧注入」）。
- **[P2] 透传前端无变更**：端口安全仅在 CLI 文本与 `simulate frame` 结果语义层体现；不新增 API 响应字段、不扩展前端诊断面板（与 NAT PRD §4 一致）。

---

## 4. 行为 / UI 设计要点（CLI 输出如何体现端口安全）

- `display port-security` 输出样例（方案 A 起即支持配置态；方案 B-lite 起补运行态）：
  ```
  Port Security Configuration
  Interface          Status   Max MAC  Protect-Action  Sticky  Aging(min)  Violations
  GE0/0/1            enable   2        restrict         yes     15           0
  GE0/0/2            disable  -        -                no      -            -
  ```
  指定接口：`display port-security interface GigabitEthernet0/0/1` 额外给出"已学安全 MAC 列表"与"error-down 状态（方案 B-lite）"。
- `display mac-address` 的 `Type` 列：方案 B-lite 下出现 `sticky` / `security` 标注（如 `00e0-fc12-3456   10   GE0/0/1   sticky`）；方案 A 下 MACTable 仍空，仅验证渲染逻辑正确。
- `simulate frame`（仅方案 B-lite）：注入后回显判定，如 `Frame from 00e0-fc12-3456 on GE0/0/1: DROPPED (protect-action=restrict, violation logged)` 或 `ADMITTED (sticky MAC learned)` 或 `PORT ERROR-DOWN (protect-action=shutdown)` + 诚实占位注记。
- **前端（CLI 终端）：本期无变更**。端口安全仅在 CLI 文本体现；不新增 `diagnosticPing`/`diagnosticTraceroute` 响应字段。

---

## 5. 验收标准（可测，每条可用自动化测试证明）

- **AC1（命令接受与拒错）**：
  - 在 Switch 接口视图执行 `port-security enable` / `max-mac-num 2` / `protect-action restrict` / `aging-time 15` / `mac-address sticky 00e0-fc12-3456 vlan 10` 均返回成功回显，对应 `DeviceConfig` 键正确写入；
  - 非接口视图执行 → 含 `interface view` 的拒绝；
  - Router/PC 执行 → 能力拒绝（与 `p1f_test.go:128-133` 一致）；
  - `max-mac-num 0`（或 4097/非数字）、`protect-action foo`、`aging-time 0`（或非法）、`mac-address sticky` 非法 MAC 格式 → 明确 `Error:` 报错。
- **AC2（配置持久化）**：执行上述 port-security 配置后 `save`，重建/重载 CLIState（`NewCLIStateFromDeviceConfig` → `LoadFromDeviceConfigData`），`display port-security` 复现 enable/max-mac/protect-action/aging-time/sticky 标志；证明经 `DeviceConfig` 往返保留（可单测比对 reload 前后 `DeviceConfig` 键集）。
- **AC3（display 输出）**：
  - `display port-security` 列出所有接口的安全状态（启用/上限/保护动作/粘滞/老化/违规计数），列头与对齐符合 §4 样例；
  - `display port-security interface <iface>` 正确展示单端口详情；
  - `display mac-address` 的 `Type` 列对 `sticky`/`security`/`static`/`dynamic` 标签渲染正确（可用手工插入 `MACEntry` 的单元测试验证渲染，无需真实学习）。
- **AC4（违规动作模拟触发，仅方案 B-lite）**：
  - 端口 `enable` + `max-mac-num 1` + `protect-action protect`：注入第 2 个非授权 MAC（`simulate frame`）→ 帧被丢弃且无告警标志；
  - `protect-action restrict`：丢弃 + violation 计数 +1（告警标志置位，可在 `display port-security` 观察）；
  - `protect-action shutdown`：端口 error-down 置位（`display port-security` 显示 error-down），且后续帧在该端口被拒；
  - 合法/粘滞 MAC 注入 → 准入，且 sticky 开启时该 MAC 进入 `MACTable`（Type=`sticky`），`display mac-address` 可见。
- **AC5（纯函数 / 无副作用）**：`EvaluatePortSecurity` 单测覆盖——未启用→admit、授权/粘滞 MAC→admit、超 max-mac→按 protect-action 触发、纯函数无副作用（连续两次调用结果一致、不改写无关 state）；不 import `sim` 引擎、不引入新第三方依赖。
- **AC6（诚实占位，仅方案 B-lite）**：lite 引擎下 `simulate frame` 输出带「模拟帧注入（lite 引擎），非内核级真实 MAC 学习」注记。

---

## 6. 待确认问题（交架构师 / 主理人拍板）

1. **"真实模拟"力度拍板（核心）**：本项深度边界取哪一档？
   - **方案 A（轻量 / CLI 忠实，推荐作为 P0 默认）**：支持 `port-security` 全命令集解析、配置经 `DeviceConfig` 持久化、`display port-security` / `display mac-address` 忠实展示；**不接入实际帧转发仿真**（因当前无 L2 帧转发引擎）。风险最低，但"真实过滤"成色弱（仅配置态，无运行态违规触发）。
   - **方案 B（带仿真钩子）**：在方案 A 基础上增加帧注入→违规触发链路（`simulate frame` + 纯函数 `EvaluatePortSecurity` + `MACTable` 填充 + `protect-action` 执行）。价值更高，架构改动更大（新增诊断命令 + 安全 MAC 表语义 + error-down 状态）。
   - **产品经理建议**：**P0 = 方案 A（最小可用集，满足任务强制要求）；P1 = 方案 B-lite（上述"诊断命令钩子"形态，非侵入 L3 路径）作为默认纳入**，以兑现端口安全的"真实准入"教学价值，同时严守"纯函数 + 不碰 sim 引擎"基线。请拍板是否 P0 即含 B-lite，还是 B-lite 延至 P1。

2. **是否需要在 ping/tracert（L3 路径）某跳介入 MAC 校验？**
   - **产品经理建议：否**。端口安全是 L2 端口接入控制，ping/tracert 走 P1-C/NAT 的 L3 路径评估器，该评估器无 MAC/帧概念；在 L3 路径每跳合成帧做 MAC 校验既语义错位、又需把帧概念强加给纯 L3 评估器，破坏既有架构。正确做法是**用独立诊断命令 `simulate frame` 作为唯一触发点**（或未来若建设 L2 交换转发路径，再挂到该路径）。若团队坚持"自动联动 ping/tracert"，则需额外设计"L3 路径在交换机跳自动派生源 MAC 并校验"的桥接层——成本高、收益低，不建议。请拍板。

3. **粘滞 MAC 是否需持久化落盘？**
   - **产品经理建议：是（config 层面 P0，learned 层面随方案 B-lite）**。
   - `port-security mac-address sticky <mac> vlan <id>` 的**手动绑定**属配置，随 `DeviceConfig` 往返持久化（P0，零新增代码）；
   - 运行时**自动学习的粘滞 MAC**（`simulate frame` 触发、写入 `MACTable` Type=`sticky`）是否也落盘（写入 `saved_config` 快照并在 reload 后回填 `MACTable`）？建议方案 B-lite 下**持久化**（贴近真实 VRP 粘滞 MAC 重启保留语义），但需在 `doSave`/`buildSavedConfigSnapshot` 与 `LoadFromDeviceConfigData` 增加粘滞 MAC 回填分支。请拍板持久化范围（仅手动绑定 / 含自动学习）。

4. **`max-mac-num` 与 `aging-time` 的合法范围？**
   - `max-mac-num`：VRP 范围 1–4096，建议直接采用；0 视为非法（至少 1）。
   - `aging-time`：VRP 单位分钟，常见 1–1440；建议采用该范围，0 或超界非法。请确认是否沿用 VRP 原生范围，还是为简化放宽。

5. **`protect-action` 默认值与 `display` 缺省展示？**
   - VRP 默认 `protect-action` 为 `restrict`（部分版本 `protect`）。建议本仿真默认 `restrict` 并在 `display port-security` 未显式配置时如实显示 `restrict`（或 `default`）。请拍板默认值，以决定 `display` 缺省文案。

6. **`simulate frame` 命令的能力归属？**
   - 该诊断命令是否限定 `switchDevices()`（与 `port-security` 一致），还是作为通用诊断对所有设备开放（非交换机执行仅回显"not supported"）？建议限定 `switchDevices()`，与端口安全能力矩阵一致。请拍板。

---

## 7. 不在本期范围

- 建设完整 L2 帧转发 / MAC 自学习引擎（仅"模拟帧注入"，非真实交换转发）；
- 端口安全与 802.1X（`dot1x`）/ RADIUS 的联动（属 AAA 路线，课程 71，下期）；
- `protect-action` 的 SNMP Trap 真实上报（仅 CLI 告警标志，不做 Trap 协议）；
- 基于 MAC 划分 VLAN（`vlan mac-address`，课程 52）的联动；
- 重新实现 `applyPortSecurity` 既有 4 子命令、能力矩阵守卫、`display mac-address` 遍历（仅扩展，零重写）；
- 前端诊断面板新增端口安全字段 / 图形化拓扑上的端口 error-down 指示。

---

## 附：关键 file:line 证据索引（供架构师直接定位）

- `internal/cli/parser.go:2346-2354` 顶层 `port-security` 分支（接口视图守卫 + 委托 `applyPortSecurity`）；`:735-738` `port security` 子命令委托。
- `internal/cli/parser.go:4082-4113` `applyPortSecurity` 现有实现（仅 enable/disable/max-mac/sticky 三键）。
- `internal/cli/parser.go:3143-3152` `display mac-address`（遍历 `state.MACTable`，`Type` 列渲染）。
- `internal/cli/state.go:82,125-130` `MACTable []*MACEntry` / `MACEntry{MAC,VLAN,Interface,Type}`；`:602` 仅初始化为空（无学习逻辑，关键证据：当前无 L2 帧学习）。
- `internal/cli/capabilities.go:79` `port-security: switchDevices()`（Switch/L3Switch/VTEP）。
- `internal/cli/parser.go:4384,4397` `SerializeToDeviceConfigData`（遍历 `DeviceConfig` 落盘）；`:4415` `LoadFromDeviceConfigData`（回写 `DeviceConfig`）；port-security 现有 3 键今日已随该往返持久化。
- `internal/cli/parser.go:4543,4550` `doSave` / `buildSavedConfigSnapshot`（粘滞 MAC 持久化若纳入，需在此加分支）。
- `internal/cli/p1f_test.go:45-71` / `p1f_qa_test.go:57-133` 现有 port-security 测试（接口视图守卫、switch 支持、router 拒绝），P0 扩展须在其上追加 protect-action/aging-time/sticky-mac 用例。

## 文档状态

- 基线核查完成：port-security 解析（P1-F）、持久化机制、能力矩阵、display mac-address 均已存在；缺口为 protect-action/aging-time/粘滞手动绑定/display port-security/真实违规触发。
- 核心开放问题已收敛为 §6 的 6 项，其中 #1（力度 A/B）与 #2（不介入 L3 路径）给出产品经理明确建议默认值。
- 结构对齐 `docs/p2-nat-prd.md`（产品目标 / 用户故事 / 需求池 P0-P2 / AC1-AC6 / 待确认）。

_Last updated: 2026-08-09 · 产品经理 许清楚（Xu）_
