# P2 CLI 增强：Tab 补全 / 命令历史持久化 / EVPN display — PRD

> 目标版本：`v0.11.0`（候选）
> 路线：延续「纯函数仿真评估 + 诚实占位」；CLI 体验增强类，以**前端为主、后端 display 为辅**，风险可控。
> 依据：`frontend/src/components/CliTerminal.tsx` 现状核查 + 既有三件套（`*_eval/_cmd/_display.go`）架构。

---

## 1. 背景与目标

eNSP Lab 是华为 VRP CLI 实训仿真器，CLI 终端是用户每日最高频的交互面。当前终端已能收发命令、按视图（`user`/`system`/`interface`/`aaa`/`ripng`/`ospfv3`…）维护 `cliState`，并支持**内存态**命令历史回溯。但作为"实训"工具，仍有两块体感短板：

1. **无 Tab 补全**：用户必须完整手敲 VRP 长命令（如 `ipv6 address` / `ospfv3 1 area`），学习曲线陡、易拼错。
2. **历史不持久**：刷新页面即丢失，无法跨会话复用实验命令序列。
3. **EVPN 无展示**：BGP-EVPN 是数据中心互联主流技术，当前 `display` 体系未覆盖，实训内容有缺口。

目标：在不变更仿真内核的前提下，补齐上述三项，使 CLI 终端达到"可实训"的体验基线。

---

## 2. 现状核查（已具备 / 缺失）

通过核查 `frontend/src/components/CliTerminal.tsx`（行号见下）：

| 能力 | 状态 | 证据 |
|---|---|---|
| 命令历史（内存态） | ✅ 已实现 | `cmdHistory: string[]`(L39)、`MAX_HISTORY=200`(L45)、`ArrowUp/Down` 回溯(L266-283) |
| 历史按设备隔离 | ✅ 已实现 | 存于 `DeviceSession` 按 `devId` 分桶(L208-223) |
| 视图上下文跟踪 | ✅ 已实现 | `cliState: { view, sub }` 随命令推进(L47) |
| 浏览器原生自动补全 | 🚫 已关 | `autoComplete="off"`(L365) |
| **Tab 补全** | ❌ 缺失 | 无 `e.key === 'Tab'` 处理、无候选渲染 |
| **历史持久化** | ❌ 缺失 | 仅 React state，无 localStorage / 后端落盘，刷新即丢 |
| **EVPN display** | ❌ 缺失 | 后端无 `evpn` 相关 `*_display.go` |

**结论**：历史"补全持久化"是增强（已有骨架），Tab 补全与 EVPN display 是净新增。

---

## 3. 范围

### 3.1 In Scope
- **Tab 补全引擎**：视图感知的关键字/命令补全，覆盖本仿真器**已实现**的命令节点（单一事实源 = 解析器命令表，避免与 parser 漂移）。
- **命令历史持久化**：跨刷新保留，按设备隔离；保留现有 Up/Down 回溯；补齐去重与上限。
- **EVPN display（lite 诚实占位）**：`display bgp evpn`、`display evpn` 系列的结构化输出 + `evpnSimNote` 注记，不编造运行态数据。

### 3.2 Out of Scope
- 真实 BGP-EVPN 控制面/数据面仿真（属 gont+FRR full 引擎，独立排期）。
- 命令语法实时纠错（红色波浪线等）。
- 多行编辑 / 宏录制。
- 历史云端同步（单用户本地工具，不做）。

---

## 4. 需求与验收标准

### P0（必须，v0.11.0 必交付）
- **R1 Tab 补全触发**：在输入框任意位置按 `Tab`，基于「当前视图 + 已输入前缀 + 前文 token」给出候选关键字。
- **R2 唯一候选直补**：候选唯一时按 `Tab` 直接补全该 token（含尾随空格）。
- **R3 多候选展示**：多候选时渲染候选列表（去重、字典序），再次 `Tab` 循环高亮或需显式选择（二选一，定 P0 口径为"循环高亮 + Enter 确认"）。
- **R4 视图感知**：补全候选随 `cliState.view/sub` 变化（系统视图给 `interface`/`ipv6`/`aaa`…，接口视图给 `ip address`/`ipv6 address`/`shutdown`…）。
- **R5 补全不提交**：`Tab` 仅插入文本，绝不触发命令执行。
- **R6 历史持久化**：刷新页面后历史仍在；按 `deviceId` 隔离；上限 200、连续重复命令只记一次。
- **R7 历史回溯兼容**：现有 Up/Down 行为不变，且能回溯到持久化历史。

### P1（高优先）
- **R8 历史检索**：`Ctrl+R` 反向增量搜索（可选，若工期紧可降 P2）。
- **R9 补全上下文**：接口名补全（如 `interface Gi0/0/1` 的 `Gi0/0/1` 候选来自设备实际接口列表）。

### P2（增强）
- **R10 EVPN display 诚实占位**：见 §5.3。

### 验收（AC）
- **AC1**：在 `system-view` 后输入 `ipv` 按 `Tab` → 候选含 `ipv6`（及本仿真器其它 `ipv*` 命令），不出现未实现命令。
- **AC2**：候选唯一时 `Tab` 补全并追加空格；多候选时列表可见、循环高亮、Enter 落定。
- **AC3**：补全候选集随视图切换而变（系统视图 ≠ 接口视图 ≠ aaa 视图）。
- **AC4**：`Tab` 全程零命令提交（观察后端无新请求 / 日志无执行记录）。
- **AC5**：提交 5 条命令 → 刷新页面 → 历史仍在且按该设备隔离；第 6 条起若超 200 滚旧；连续两次相同命令只存 1 条。
- **AC6**：`display bgp evpn` / `display evpn` 在 lite 引擎下返回结构化头部 + `evpnSimNote`（"运行态恒 -"），无编造邻居/VNI/路由条目。
- **AC7**：补全与历史渲染经 `CliTerminal` 既有 `escapeHtml` 转义路径，无 XSS 回归。
- **AC8**：补全/历史的键匹配走精确 helper，**不**用 `strings.Contains` 模糊扫描，端口安全粘滞 MAC（`00e0-fc12-0aaa`）等不被误判/误删（单元测试锁死）。

---

## 5. 设计要点

### 5.1 Tab 补全引擎（主体）
- **单一事实源**：补全字典**由 parser 命令表生成**，不应手写第二份。建议新增 `internal/cli/completion.go` 暴露 `Complete(view, tokens) []string`，复用各 `*_cmd.go` 已注册的命令节点（与 dispatch 同源，零漂移）。
- **视图感知**：以 `cliState.view/sub` 作为根节点选择补全子树；接口视图额外注入该设备真实接口名（来自 `DeviceConfig`/`Topology`）。
- **前端交互**：`onKeyDown` 增加 `e.key === 'Tab'` 分支（已有关 `onKeyDown` L258）；候选列表用绝对定位浮层渲染，复用终端等宽样式；Enter 确认当前高亮项，`Esc` 关闭。
- **输入模型**：以「已输入 token 按空格切分」为上下文；补全当前最后一个不完整 token；补全后重写 `input` 值并更新光标。

### 5.2 命令历史持久化（增强既有）
- **存储**：每设备 `localStorage` 键 `ensp:cli-history:<topologyId>:<deviceId>`（单用户本地工具，无需后端落盘；若未来要随拓扑存档，可加 `cli:history:<device>` 进 DeviceConfig，本 PRD 默认 localStorage 方案，降低风险）。
- **加载**：组件挂载时从存储恢复进 `DeviceSession.cmdHistory`；写时 `JSON` 序列化 + 上限截断 + 去重。
- **不动**：现有 Up/Down 回溯逻辑（L266-283）与 `MAX_HISTORY` 常量保持，仅在其之上加持久层。

### 5.3 EVPN display（诚实占位）
- 新增 `internal/cli/evpn_display.go`（仅 display，无新配置命令，避免范围蔓延）。
- 支持 `display bgp evpn peer`、`display bgp evpn routing-table`、`display evpn vni` 等头部结构；正文行运行态字段（`Peer/Route/Prefix` 计数、VNI 列表）恒 `-` + `evpnSimNote()` 注记，明确"lite 引擎未实现 EVPN 控制面仿真"。
- 守卫：仅 `l3Devices()` 可读空态；与 `bgpSimNote`/`ipv6SimNote` 口径一致。

### 5.4 键碰撞红线（重申，最高危）
- `ip`/`ipv6`/`aaa`/`domain`/`gre` 等是合法十六进制串前缀；补全/历史的任何键匹配必须走**精确 helper**（如 `ipv6Key`/`aaaLocalUserKey`），**禁用 `strings.Contains` 模糊扫描**，避免误伤端口安全粘滞 MAC（`00e0-fc12-0aaa`）、`Bridge-Aggregation` 等。AC8 单元测试锁死。

---

## 6. 验收与测试
- **前端单测**（Vitest）：补全候选生成（视图感知、唯一/多候选、边界）、历史持久化（去重/上限/恢复）、键碰撞红线。
- **后端单测**（Go）：`Complete()` 视图感知正确性；EVPN display 占位输出结构。
- **浏览器 e2e**（Playwright，复用既有套路）：起服务 → 开 CLI → 敲 `ipv`+Tab 验证补全 → 提交命令 → 刷新 → 历史仍在 → `display bgp evpn` 验证占位。
- 独立 QA 两轮验收（PASS 判定 NoOne/零源码缺陷，对齐既往 SOP）。

---

## 7. 风险与开放问题
- **补全字典覆盖率**：依赖 parser 命令表完整导出；若 parser 命令注册分散，需先统一注册入口（可在本 PRD 前做小重构，单独 ticket）。
- **Tab vs 焦点**：终端输入在 canvas 场景下的焦点管理需验证（双击画布开 CLI 后焦点是否正确）。
- **EVPN 范围**：仅 display 占位，是否要同步补 `bgp evpn` 配置命令？本 PRD 定 P2 仅 display；配置命令留待 full 引擎或后续。
- **历史存储选型**：localStorage（默认）vs DeviceConfig 落盘，需用户拍板（影响是否随拓扑存档）。

---

## 8. 交付与版本
- 交付物：前端补全+持久化 + 后端 `evpn_display.go` + 手册 4.8.5「CLI 增强（Tab 补全 / 历史 / EVPN display）」+ 本 PRD/设计文档。
- 版本：`v0.11.0`。
- 流程：PM PRD（本文件）→ 架构设计（`p2-cli-enhancement-design.md`）→ 工程师三件套/前端实现 → QA 两轮。
