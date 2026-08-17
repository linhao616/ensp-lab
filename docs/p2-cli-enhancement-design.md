# P2 CLI 增强：统一命令注册 / Tab 补全 / 历史分层 — 设计文档

> 目标版本：`v0.11.0`（候选）
> 路线：延续「纯函数仿真评估 + 诚实占位」；CLI 体验增强，以**后端统一注册表为核心**、前端消费其导出，单一事实源、零漂移。
> 依据：`frontend/src/components/CliTerminal.tsx` 现状 + `internal/cli/parser.go` `case "display","dis":` 分发 + `state.go`/`tools.go` 归一化 + 既有三件套架构。
> 配套 PRD：`docs/p2-cli-enhancement-prd.md`

---

## 0. 现状复核（纠正 PRD 两处误判）

### 0.1 历史持久化：两层都已存在，非"缺失"

| 层 | 机制 | 证据 | 状态 |
|---|---|---|---|
| localStorage（UI/历史，默认） | 前端把整个 `sessions`（含每设备 `cmdHistory`）按 topology 写入 `localStorage`，key=`ensp-lab-cli-sessions-<topo>` | `CliTerminal.tsx:49,64,76,208,221` | ✅ 已实现；去重 L220、上限 L221 已在 |
| DeviceConfig（拓扑/配置，用户触发 save） | 后端 `RecordHistory` 每次命令追加 `state.History`；`SerializeToDeviceConfigData` 将其随 `save` 序列化进拓扑 JSON；`LoadFromDeviceConfigData` 重载恢复 | `state.go:598-610`、`parser.go:5344,5376` | ✅ 已实现 |

**结论**：用户拍板的"分层存储"（localStorage 默认 + DeviceConfig 落盘）**架构已存在**。本版对历史的动作是**验证 + 命名清晰化 + 修复潜在不一致**，而非从零新建。PRD §2 把历史标为"缺失"属误判，此处更正。

### 0.2 `dis vxlan tunnel` / `dis arp`：已实现，但散落在巨型 switch

`internal/cli/parser.go` 的 `case "display","dis":`（L2571）是一个**超长 `switch arg0`**（L2594 起，跨近千行），每个 `case` 内联 builder 或调函数：
- `dis arp` → `case "arp":`（L2914）内联表格。
- `dis vxlan tunnel` → `case "vxlan":` + `arg1=="tunnel"` → `buildVXLANTunnelDisplay`（L189/L3579）。
- `dis ipv6 *` → `buildIPv6Display(state, args)`（已是正确的 `func(state,args)` 形态，`ipv6_display.go`）。

这正是最该统一的部分：**命令越多，switch 越长、越乱、越易漏设备守卫、且 Tab 补全无法复用**。

---

## 1. 设计目标

1. **统一注册机制**：把 `dis` 子命令分发从内联 `switch` 改为集中式注册表 `map[string]DisplayHandler`，每个 handler 自带二级子命令与设备守卫。新增 display 命令只改一处（注册 + 一个函数），不再动巨型 switch。
2. **Tab 补全单一事实源**：补全字典由注册表 + 视图感知关键字表生成（后端计算），前端不手写第二份，杜绝与 parser 漂移。
3. **历史分层自洽**：确认 localStorage / DeviceConfig 两层各自正确、命名清晰、跨刷新/跨重载均可恢复；去重与上限保持。

---

## 2. 统一命令注册机制（核心）

### 2.1 类型与注册表

新增 `internal/cli/display_registry.go`：

```go
package cli

// DisplayHandler 渲染一条 display 子命令。state=当前状态；cmd=完整命令；
// arg0=归一化子命令（注册 key）；arg1=归一化二级子命令（可能空）。只读，禁写 DeviceConfig 键。
// 实现采用 func(state *CLIState, cmd *Command, arg0, arg1 string) string
// （与 parser.go 分发一致，handler 直接携带 state/cmd/arg0/arg1，逻辑零改写）。
type DisplayHandler func(state *CLIState, cmd *Command, arg0, arg1 string) string

// displayRegistry 是 dis 子命令的集中注册表（单一事实源）。
// key = normalizeDisplaySubCmd 归一化后的子命令（如 "arp"/"vxlan"/"ipv6"）。
var displayRegistry = map[string]DisplayHandler{
    "this":                buildThisDisplay,        // 原 parser 内联
    "interface":           buildInterfaceDisplay,   // 原 dis int
    "ip":                  buildIPDisplay,          // dis ip interface brief / ip routing-table
    "arp":                 buildARPDisplay,         // 原 case "arp"
    "vxlan":               buildVXLANTunnelDisplay, // 原 buildVXLANTunnelDisplay（含 tunnel 二级）
    "bgp":                 buildBGPDisplay,         // 含 peer 二级
    "nat":                 buildNATDisplay,         // 含 server/address-group 二级
    "routing-table":       buildRoutingTableDisplay,
    "ipv6":                buildIPv6Display,       // 既有 ipv6_display.go
    "ospf":                buildOSPFDisplay,        // 含 peer 二级
    "vrrp":                buildVRRPDisplay,
    "stp":                 buildSTPDisplay,
    "acl":                 buildACLDisplay,
    "version":             buildVersionDisplay,
    "vlan":                buildVLANDisplay,
    "mac-address":         buildMACDisplay,
    "users":               buildUsersDisplay,
    "tcp":                 buildTCPDisplay,         // dis tcp status
    "lldp":                buildLLDPDisplay,
    "memory":              buildMemoryDisplay,
    "cpu-usage":           buildCPUDisplay,
    "current-configuration": buildCurrentConfigDisplay,
    "diagnostic-information": buildDiagnosticInfo,  // 既有
    "bfd":                 buildBFDDisplay,
    "vrf":                 buildVRFDisplay,
    "ndp":                 buildNDPDisplay,         // 新增（IPv6 邻居，诚实占位）
    "evpn":                buildEVPNDisplay,        // 新增（诚实占位）
}
```

### 2.2 分发入口（替换巨型 switch）

`parser.go` 的 `case "display","dis":` 块精简为：

```go
case "display", "dis":
    if len(cmd.Args) == 0 {
        return "Error: need args"
    }
    arg0 := strings.ToLower(cmd.Args[0])
    arg1 := ""
    if len(cmd.Args) > 1 {
        arg1 = strings.ToLower(cmd.Args[1])
    }
    arg0 = normalizeDisplaySubCmd(arg0)
    arg1 = normalizeDisplaySubCmd2(arg0, arg1)
    if h, ok := displayRegistry[arg0]; ok {
        return h(state, cmd, arg0, arg1)
    }
    return fmt.Sprintf("Error: unknown command 'dis %s'", arg0)
```

- **设备守卫内迁**：原 switch 中 `isSwitch && !isVTEP || ...` 这类守卫，搬进对应 handler 顶部（如 `buildRoutingTableDisplay` 开头保留原守卫）。`display` 类命令**默认任意设备可读**（AC11b），仅少数（routing-table/nat）有类型限制，handler 内自行判定。
- **二级子命令**：`vxlan`/`bgp`/`nat`/`ospf` 等 handler 内部对 `arg1` 做 `normalizeDisplaySubCmd2` 分流，逻辑与原 switch 完全一致。
- **逐函数迁移**：把原内联 `case` 体抽取为 `regXxxDisplay(state, cmd, arg0, arg1)` 纯函数（注册表键统一加 `reg` 前缀，**禁与既有 `buildXxxDisplay` 重名冲突**），**逐字保留**输出格式；迁移后单测锁死代表性输出（见 §7）。

### 2.3 与三件套 / 键纪律的关系

- 注册表 handler 即"展示层"，与既有 `*_display.go`（ipv6/gre/aaa/lag/dhcp_relay）同源；新命令仍走 `*_display.go` + 精确键命名空间。
- **键碰撞红线（重申）**：任何键匹配走精确 helper（如 `ipv6Key`/`aaaLocalUserKey`），**禁用 `strings.Contains` 模糊扫描**，不误伤端口安全粘滞 MAC（`00e0-fc12-0aaa`）/`Bridge-Aggregation`。单测 `TestDisplayKeyCollision` 锁死。

---

## 3. Tab 补全（消费注册表，后端计算）

### 3.1 后端补全器 `internal/cli/completion.go`

```go
// Complete 返回当前输入在给定视图下的补全候选（已去重、字典序）。
// tokens = 用户已输入、按空格切分的 token 序列（不含尾随空格产生的空串）。
// 最后一段 tokens[last] 为"待补全前缀"；其余为上下文。
func Complete(state *CLIState, tokens []string) []string
```

策略：
1. **`dis`/`display` 上下文**（tokens[0] ∈ {dis,display}）：候选 = `displayRegistry` 全部 key，按 `tokens[last]` 前缀过滤。→ 单一事实源，新增 display 命令自动进入补全。
2. **配置视图关键字表**（静态，按 `state.CurrentView` 分支）：列出该视图下合法下一 token（如 system 视图：`interface`/`ipv6`/`aaa`/`ripng`/`ospfv3`/`ip`/`ospf`/`bgp`/`vlan`/`stp`…，接口视图：`ip address`/`ipv6 address`/`shutdown`/`description`…）。**该表由 grep parser 已实现命令生成，并配单测 `TestCompletionNoDrift` 锁死**（表内每个 token 必须在 parser 中确实被处理，否则测试失败），杜绝沉默漂移。
3. **接口名补全**：当视图为 `interface` 且上一 token 期望接口名时，候选来自 `state.Interfaces` 的键（真实接口列表），按前缀过滤。

`Complete` 返回候选切片；唯一候选由前端直接补（R2），多候选由前端渲染浮层（R3）。

### 3.2 后端 API `internal/api/cli_handlers.go`

新增 `completeCLI`：
```
POST /api/topology/:id/device/:dev/cli/complete
body: { "view": "...", "sub": "...", "input": "ipv6 add" }
resp: { "candidates": ["address"] }
```
handler 取该设备 CLIState（与 `executeCLI` 同生命周期/锁），调 `cli.Complete(state, splitTokens(input))`。**仅计算，绝不执行命令**（AC4）。

### 3.3 前端 `CliTerminal.tsx`

- `onKeyDown` 增加 `e.key === 'Tab'` 分支：阻止默认、取当前 `input` 调 `/complete`、渲染候选浮层（绝对定位，复用等宽样式）、`Enter` 确认高亮项、`Esc` 关闭；唯一候选直接补并加尾随空格（R2）。
- 补全**只改 `input` 文本与光标**，零命令提交（R5/AC4）。
- 焦点：沿用现有 canvas 双击开 CLI 后的输入框焦点；浮层 `position: absolute` 于输入框上方。

---

## 4. 历史分层（验证 + 微调，非新建）

两层现状见 §0.1，本版动作：

1. **localStorage 层**：保留现有 `ensp-lab-cli-sessions-<topo>` 机制；将 key 语义在注释中明确为"UI 状态 + 命令历史（默认启用、跨刷新保留）"。确认去重（L220）与上限（L221=200）行为正确（AC5/AC6/AC7）。
2. **DeviceConfig 层**：确认 `RecordHistory`→`SerializeToDeviceConfigData`→`LoadFromDeviceConfigData` 链路在 `save`/重载时正确往返（AC：重载拓扑后 `dis history-command` 可见历史）。
3. **命名清晰化**：在 `display_registry.go` / `state.go` 注释中写明两层职责边界，避免后续维护者混淆。
4. 不引入云端同步（单用户本地工具）。

> 注：前端 `cmdHistory`（UI 用）与后端 `state.History`（DeviceConfig 落盘用）是**两份独立历史**，各有用途——前者为终端体验、后者为拓扑存档。两者共存、不冲突，正合"分层"本意。

---

## 5. 批量 display 命令（注册表驱动）

迁移既有 + 新增少量，全部经注册表：

| 命令 | 来源 | 动作 |
|---|---|---|
| `dis arp` | 既有 L2914 | 迁为 `buildARPDisplay` |
| `dis vxlan [tunnel]` | 既有 `buildVXLANTunnelDisplay` | 迁为 `buildVXLANTunnelDisplay`（保留 tunnel 二级） |
| `dis ipv6 *` | 既有 `ipv6_display.go` | 注册 `buildIPv6Display` |
| `dis bgp [peer]` / `dis nat [server|address-group]` / `dis ospf [peer]` / `dis routing-table` | 既有 | 迁为 handler |
| `dis this` / `dis int` / `dis ip int brief` / `dis version` / `dis vlan` / `dis mac-address` / `dis users` / `dis tcp status` / `dis lldp` / `dis memory` / `dis cpu-usage` / `dis current-configuration` / `dis diagnostic-information` / `dis bfd` / `dis vrf` | 既有内联 | 迁为 handler |
| `dis ndp` | **新增** | `buildNDPDisplay`：IPv6 邻居表诚实占位（运行态 `-`），读真实接口 IPv6 地址派生 |
| `dis evpn` / `dis bgp evpn [peer|routing-table|vni]` | **新增（P2）** | `buildEVPNDisplay`（含 `evpnSimNote()` 注记，运行态恒 `-`，不编造邻居/VNI/路由） |

新增两条走 `*_display.go` + 精确键（`evpn:` 命名空间），诚实占位路线；不新增配置命令（范围克制）。

**实现状态（v0.11.0）**：注册表迁移（§2）与 `dis ndp` / `dis evpn` / `dis bgp evpn` 均已完成并通过单测
（`TestDisplayRegistryDispatch` / `TestDisplayEVPNNDP` / `TestCompletionEVPNNDP`）。注册表实际函数名为
`regXxxDisplay`（统一 `reg` 前缀，避免与既有 `buildXxxDisplay` 重名）；`dis bgp evpn` 经 `regBgpDisplay`
的 `arg1=="evpn"` 分支委托 `buildEVPNBGPDisplay`。

---

## 6. 键碰撞红线（最高危，重申）

- 补全/历史/展示的任何键匹配走**精确 helper**（如 `ipv6Key`/`aaaLocalUserKey`/`interfaceKey`），**禁用 `strings.Contains` 模糊扫描**。
- 误伤对象：端口安全粘滞 MAC `00e0-fc12-0aaa`、接口名 `Bridge-Aggregation`、域名 `example.com` 中的 `aaa` 子串等。
- 单测 `TestDisplayKeyCollision` 锁死：含上述串的设备配置下，补全候选集与 display 输出不得异常/误删。

---

## 7. 验收映射（AC → 实现）

| AC | 实现点 |
|---|---|
| AC1 `ipv`+Tab→含 `ipv6` | `Complete` 前缀过滤注册表/关键字表，仅列已实现 |
| AC2 唯一直补 / 多候选浮层 | 前端 Tab 分支 |
| AC3 候选随视图变 | `Complete` 按 `state.CurrentView` 分支 |
| AC4 Tab 零提交 | `/complete` 仅计算；前端只改 input |
| AC5 刷新后历史在/去重/上限 | §0.1 两层 + 单测 `TestHistoryDedupCap` |
| AC6 `dis bgp evpn` 占位 | `buildEVPNDisplay` + `evpnSimNote` |
| AC7 escapeHtml 无 XSS 回归 | 复用 `CliTerminal.escapeHtml` 渲染候选/历史 |
| AC8 精确键匹配 | `TestDisplayKeyCollision` |

---

## 8. 文件改动清单

| 文件 | 改动 |
|---|---|
| `internal/cli/display_registry.go` | **新增**：注册表 + 所有 `buildXxxDisplay` handler（从 parser switch 抽取） |
| `internal/cli/parser.go` | `case "display","dis":` 替换为注册表查表；删除原巨型 switch 体（约 -900 行） |
| `internal/cli/completion.go` | **新增**：`Complete` + 视图感知关键字表 |
| `internal/cli/evpn_display.go` | **新增**：`buildEVPNDisplay` + `evpnSimNote` |
| `internal/cli/ndp_display.go` | **新增**：`buildNDPDisplay`（如需要） |
| `internal/api/cli_handlers.go` | 新增 `completeCLI` handler + 路由 |
| `frontend/src/components/CliTerminal.tsx` | `Tab` 键分支 + 候选浮层；历史分层注释 |
| `docs/ensp-lab_manual.md` | 新增 4.8.5「CLI 增强（统一注册 / Tab 补全 / 历史 / EVPN display）」 |
| `docs/p2-cli-enhancement-design.md` | 本文件 |

---

## 9. QA 计划

- **后端单测（Go）**：
  - `TestDisplayRegistryDispatch`：代表性 `dis` 命令经注册表输出与原 switch 字节级一致（迁移无回归）。
  - `TestCompletionViewAware`：`Complete` 在 system/interface/aaa 视图下候选集正确。
  - `TestCompletionNoDrift`：关键字表中每个 token 必须在 parser 中被实际处理（grep 锁死）。
  - `TestDisplayKeyCollision`：含 `00e0-fc12-0aaa` 等串的设备下，补全/展示无异常。
  - `TestHistoryDedupCap`：去重 + 200 上限。
- **前端单测（Vitest）**：候选生成、唯一/多候选、历史恢复。
- **浏览器 e2e（Playwright，复用套路）**：起服务 → 开 CLI → `ipv`+Tab 验证补全 → 提交命令 → 刷新 → 历史仍在 → `dis bgp evpn` 验证占位。
- 独立 QA 两轮验收（PASS 判定 NoOne/零源码缺陷）。

---

## 10. 风险

- **迁移回归**：巨型 switch 抽取为 handler 易漏设备守卫 → 靠 `TestDisplayRegistryDispatch` 锁死代表性输出 + 人工 diff。
- **补全漂移**：关键字表静默过时 → 靠 `TestCompletionNoDrift`。
- **Tab 焦点**：canvas 场景双击开 CLI 后焦点 → e2e 验证。
- **EVPN 范围**：仅 display 占位，不补 `bgp evpn` 配置命令（留待 full 引擎）。
