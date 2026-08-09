# ensp-lab P2 第八项：AAA 本地认证（华为 VRP 实训课程 71）增量 PRD

> 文档类型：增量产品需求文档（PRD，**简单模式**，结构对齐 `docs/p2-gre-prd.md`）
> 关联：`docs/p2-gre-prd.md`（上一轮需求粒度 / AC 写法基准）、`docs/p2-gre-design.md`（架构红线直接沿用）、`docs/reference/huawei-vrp-course.md:69`（课程 71）
> 代码基线：`internal/cli/parser.go` / `state.go` / `capabilities.go` / `gre_eval.go` / `portsec_eval.go`（**已逐条 grep 核查到 file:line，见文末证据索引**）
> 作者：产品经理 许清楚（Xu）
> 语言：中文
> 说明：本期**不做竞品/市场分析**（按主理人指示），仅输出产品目标 / 用户故事 / 需求池 / UI 设计稿 / 验收标准 / 待确认问题。

---

## 0. 项目信息

- **Language**：中文
- **Programming Language**：Go 1.26.5（本期工作集中在 `internal/cli`；前端 React/Vite 本期无变更）
- **Project Name**：`p2_aaa_local_auth`
- **原始需求复述**：在 P2 已交付 NAT（38）、端口安全（49）、VRRP（60/61）、STP/RSTP/MSTP（55/56/57）、链路聚合（63）、DHCP 中继（27）、GRE 隧道（69，v0.8.0）之后，为华为 eNSP VRP 仿真器落地 **AAA 本地认证（课程 71）** 的增量实现：新建 **AAA 视图**及其认证方案 / 域子视图，把 `local-user` 命令族**从"系统视图 + 结构体死状态"重构为"AAA 视图真实命令 + `DeviceConfig` 单一事实源"**，补齐 `authentication-scheme` / `domain` 绑定链路，新增 VRP 风格 `display aaa` / `display local-user` / `display domain` 只读展示，并对认证运行态（认证成功/失败次数、在线会话、计费流量）施加**诚实占位**。

> **深度边界先验结论（务必先读 §6 待确认项）**：AAA 的真实价值在于**设备把"谁能登录、能干什么、用了多少"三件事从本地硬编码口令中解耦出来**——用户建在 local-user 库或 RADIUS 服务器，认证方式由 scheme 决定，scheme 由 domain 绑定，用户登录时按 `user@domain` 选域。本工具是**单机 VRP CLI 仿真器，无真实登录会话、无 RADIUS 协议栈、无计费采集**。因此本期严格划界：
>
> - **配置面 100% 真实**：AAA 视图、`local-user` 全属性（password / privilege level / service-type / state）、`authentication-scheme` + `authentication-mode`、`domain` + scheme 绑定 —— 必须真实落 `DeviceConfig` 键、真实可 `display`、真实可 `undo`、真实可 `save`→`reload` 复现。这部分**不允许打折**。
> - **运行面 100% 诚实占位**：「认证成功/失败次数、在线用户数与会话时长、计费上下行字节数与报文数、用户最后登录时间、当前接入类型」等 —— **一律显示 `-`，严禁编造数字、严禁随机数、严禁伪造 `Online`/`1 user(s)`**。这是本项目核心价值观红线（对照 GRE 的 keepalive 计数、LAG 的 Partner 块、DHCP 中继的转发计数处置）。
>
> **重大基线发现（本期前置阻塞项，非另起炉灶）**：AAA 在代码基线中**并非完全缺失，而是以"半截 + 错误形态"存在**，缺陷组合与 GRE 那轮高度同构（GRE 是"自造命令 + 结构体死状态"，AAA 是"错误视图 + 结构体死状态 + 死字段假 0 + 悬空引用"）。详见 §3「已有」表：
> ① **`aaa` 视图根本不存在**（全仓无 `case "aaa"`），但 `parser.go:1750` 的 VTY `authentication-mode aaa` **已经允许学员选 aaa 模式**——这是一条**指向空气的悬空引用**：工具让你声明"用 AAA 认证"，却没有任何地方能配 AAA；
> ② `local-user` 被硬守卫在**系统视图**（`parser.go:1784-1786`）——真机是 **AAA 视图**命令族；
> ③ 结果写入 `state.LocalUsers map[string]*LocalUser`（`state.go:65`/`:317`/`:498`）——**结构体事实源、不入 `DeviceConfig`、不落盘**（`SerializeToDeviceConfigData` 只拷 `state.DeviceConfig`，`parser.go:5163-5165`），`save`→`reload` 后**用户 100% 丢失**；
> ④ `LocalUser.PrivilegeLevel` 是**全仓只读不写的死字段**（`grep` 仅命中声明 `state.go:321` 与渲染 `parser.go:3427`）→ `display` 恒输出 `Privilege: 0`，属**结构体零值伪装成真实配置**；
> ⑤ `PasswordCipher` 与 `Password` **双写同一明文**（`parser.go:1803-1804`）——名为 cipher 实为明文，且 `display` 无脱敏；
> ⑥ 唯一露出点是 `display ssh` 里的 `Local Users` 段（`parser.go:3419-3428`），且是 **map 随机遍历**（输出顺序不确定，同 `display gre` / `display ip pool` 反面教材）；
> ⑦ `display aaa` / `display local-user` / `display domain` **全部不存在**；`undo local-user` **不存在**；`local-user` **不进 `display current-configuration`**。

---

## 1. 产品目标

在严守 P2「CLIState 层纯函数、单一事实源 `DeviceConfig`、诚实占位、零改动 `sim` 引擎、零新增第三方依赖、不 import `internal/protocol`」架构基线的前提下，把 AAA 从**"半截且形态错误"**纠正为一条学员可完整走通的实验链路：

1. **命令面对齐官方 VRP，补齐悬空引用**：学员能按课程 71 的真实命令序列敲 `aaa` → `local-user admin password cipher Huawei@123` → `local-user admin privilege level 15` → `local-user admin service-type telnet ssh` → `authentication-scheme sch1` → `authentication-mode local` → `domain huawei` → `authentication-scheme sch1`，把"用户 → 方案 → 域"三级链路配起来；命令形态、视图层级、报错文案、`undo` 语义均对齐真机，肌肉记忆可平移到 eNSP / 真实设备。**同时闭合 `user-interface vty` 下 `authentication-mode aaa` 的悬空引用**（现状选了 aaa 却无处可配）。
2. **配置真实落地且持久**：把用户库从 `state.LocalUsers` 结构体迁移到 `DeviceConfig["aaa:local-user:<name>:<field>"]` / `aaa:auth-scheme:*` / `aaa:domain:*` 单一事实源；`display aaa`、`display local-user`、`display domain`、`display current-configuration` 忠实复现，`save`→`reload` 后用户与方案不丢（现状 100% 丢失）。
3. **展示忠实、边界诚实、口令不裸奔**：配置态字段（用户名、privilege level、service-type、state、方案认证模式、域绑定）**如实展示**；认证成功/失败计数、在线会话、计费流量等仿真无法产出的运行态字段**一律 `-` + `aaaSimNote()` 注记**；口令在**所有** `display` 输出中**脱敏为 `****`**，且诚实声明"本仿真器不实现 VRP 不可逆加密算法，口令以明文存于本地配置文件"——绝不用伪造的 `%^%#...%^%#` 密文串换取观感。

---

## 2. 用户故事

1. **作为练习设备登录认证的网络学员（课程 71 主线）**：As a 学员，I want 敲 `aaa` 进入 AAA 视图，再 `local-user admin password cipher Huawei@123` / `local-user admin privilege level 15` / `local-user admin service-type telnet ssh`，so that 我能用 `display local-user` 核对用户名、权限级别、服务类型是否与规划一致，验证自己的命令顺序和参数没记错。
2. **作为理解"用户—方案—域"三级模型的学员**：As a 学员，I want 配 `authentication-scheme sch1` + `authentication-mode local`，再 `domain huawei` + `authentication-scheme sch1`，so that 我能通过 `display domain huawei` 看到该域实际绑定了哪个认证方案、该方案的认证模式是什么，把课程里抽象的三级关系落成能看见的配置。
3. **作为踩坑排障的学员**：As a 学员，I want 在**系统视图**直接敲 `local-user`、在域里绑定一个**不存在的 scheme**、把口令配成 `123`（太短）、给用户配一个非法 service-type 时，收到**明确、可读的错误提示**（而不是静默成功），so that 我立刻知道错在哪；同时我希望 `display` 里那些仿真给不了的认证计数**老老实实显示 `-`**，而不是给我一串假数字。
4. **作为关注持久化的学员**：As a 学员，I want `save` 后 `reload` 设备，so that 本地用户、认证方案、域绑定仍完整保留，`display current-configuration` 能复现整个 `aaa` 配置块，而不必重配（**现状 `state.LocalUsers` 不落盘，reload 后用户全丢**）。
5. **作为有安全意识的学员**：As a 学员，I want `display local-user` 与 `display current-configuration` 里的口令**显示为 `****` 而不是明文**，并且工具**老实告诉我**它没有实现真机的不可逆加密，so that 我既建立"真机口令不回显"的正确认知，又不会误以为这个仿真器的配置文件是安全的。
6. **作为把 VTY 与 AAA 串起来的学员**：As a 学员，I want 在 `user-interface vty 0 4` 下敲 `authentication-mode aaa` 之后，能在 `display aaa` 里看到 AAA 已被引用、且当前有哪些本地用户可用于登录，so that 我理解"VTY 选 aaa 模式"与"AAA 视图建用户"是同一条链路的两端——**同时我接受工具明说它不模拟真实登录握手**。

---

## 3. 需求池

> 共 **29 条**：P0 **15 条**、P1 **8 条**、P2 **6 条**（另列「已有」基线 8 条，属**重构对象 / 复用基线**，非新需求）。

### 已有（本期重构 / 复用，非另起炉灶）

| 标记 | 内容 | 位置 |
|---|---|---|
| [已有·**缺失·本期必补**] | **`aaa` 视图根本不存在**：全仓无 `case "aaa"`（`grep -n 'case "aaa"' internal/cli/*.go` 零命中），`ViewType` 枚举无 `ViewAAA`。真机 AAA 全部命令都在 `[R1-aaa]` 视图内 | `parser.go` 顶层 switch、`state.go:17-29` |
| [已有·**悬空引用·本期闭合**] | VTY 视图 `authentication-mode aaa` **已允许选中**（`authMode != "aaa" && ...` 校验放行，写 `state.VTY.AuthenticationMode`），但**全仓无任何地方能配 AAA** → 学员选了 aaa 模式后无处落地，属**指向空气的引用**。本期补齐 AAA 视图后需在 `display aaa` 中体现该引用关系 | `parser.go:1741-1753` |
| [已有·**不合规·本期必改**] | `local-user` **视图错误**：`if state.CurrentView != ViewSystem { return "Error: must be in system view" }` 硬守卫在**系统视图**——真机为 **AAA 视图**命令族；且参数为**弱解析循环**（`for i := 1..` + 内层 switch），仅支持 `password cipher` 与 `service-type` 两个子属性 | `parser.go:1783-1812` |
| [已有·**不合规·本期必改**] | `state.LocalUsers map[string]*LocalUser{Name, Password, PasswordCipher, ServiceType, PrivilegeLevel}`：**结构体事实源**，不入 `DeviceConfig` → `SerializeToDeviceConfigData` **只拷 `state.DeviceConfig`**（`parser.go:5163-5165`）→ `save`→`reload` **用户 100% 丢失** | `state.go:65`、`state.go:317-323`、`state.go:498` |
| [已有·**死字段假 0·本期必改**] | `LocalUser.PrivilegeLevel` **全仓只读不写**：`grep -rn "PrivilegeLevel" ` 在 local-user 路径上仅命中声明（`state.go:321`）与渲染（`parser.go:3427` `Privilege: %d`）——**没有任何命令能写它**。结果：`display` 恒输出 `Privilege: 0`，把**结构体零值伪装成真实配置**（与 GRE 那轮 `Key: 0` 同款缺陷，GRE PRD P1-1 已明令禁止复制） | `state.go:321`、`parser.go:3427` |
| [已有·**诚实/安全缺口·本期必改**] | `PasswordCipher` 与 `Password` **双写同一明文**（两行都赋 `cmd.Args[i+2]`）——名为 cipher 实为明文；且现状**无任何脱敏渲染**。本期须统一脱敏（P0-13） | `parser.go:1803-1804` |
| [已有·**不合规·本期必改**] | 本地用户唯一露出点是 `display ssh` 的 `Local Users` 段：① `for _, user := range state.LocalUsers` **map 随机遍历**，输出顺序不确定（与 `display gre` 同款缺陷，GRE AC7 已明令禁止复制）；② 无 state / password 字段；③ **无 `display local-user` / `display aaa` / `display domain`**；④ **`local-user` 不进 `display current-configuration`**（`grep -n "local-user" parser.go` 仅命中 `:1783`/`:1789`，`buildSavedConfigSnapshot` 零输出）；⑤ **无 `undo local-user`** | `parser.go:3419-3428`、`parser.go:5382+` |
| [已有·可复用] | 能力矩阵已有 `"local-user": l3Devices()`；`isCommandSupported` 按**首 token** 匹配，**未声明的命令默认放行**。子视图范式：`ViewDHCPPool` 进入（`parser.go:1622-1624`）/ 提示符（`parser.go:5143-5144`）/ `quit` 回退（`parser.go:285-296`）；`ViewMSTRegion` 嵌套守卫（`parser.go:4024`）。纯函数 + 诚实占位范式：`greSimNote()`（`gre_eval.go:583-588`）、`portSecSimNote()`（`portsec_eval.go:239-244`）；三件套文件范式 `gre_cmd.go` / `gre_display.go` / `gre_eval.go`；键 helper 精确匹配范式 `gre_eval.go` 常量段；接口视图 undo 的 **handled 模式**钩子（`parser.go:828+`）；持久化系统级块挂载点（`parser.go:5392-5395` STP 块） | 见各处 |

### P0（本期核心 · AAA 视图 + 事实源迁移 + 三级链路 + display 忠实 + 诚实占位）

**A. 视图与事实源（前置阻塞项）**

- **[P0-1 新建 AAA 视图 `ViewAAA` 及其子视图]**：系统视图执行 `aaa` → 进入 `ViewAAA`，提示符 `[<sysname>-aaa]`（挂 `parser.go:5126-5145` 的 `viewPrompt` switch）。AAA 视图内执行 `authentication-scheme <name>` → 进入方案子视图 `[<sysname>-aaa-authen-<name>]`；`domain <name>` → 进入域子视图 `[<sysname>-aaa-domain-<name>]`。🔴 **`quit` 层级必须正确**：方案/域子视图 `quit` → **回 `ViewAAA`**（**不是**直接回系统视图）；`ViewAAA` `quit` → 回 `ViewSystem`。**注意现状 `parser.go:285-296` 的 `quit` 是 if-else 链，末尾 `else` 一律回 `ViewSystem`**——新视图若不显式加分支，子视图会**越级弹回系统视图**（静默错误，**AC1 ③ 设专项断言**）。`return` 一律回 `ViewUser`（既有行为不变）。
- **[P0-2 删除结构体事实源 `state.LocalUsers` / `LocalUser`]**：删除 `state.go:65` 字段、`state.go:317-323` 类型、`state.go:498` 构造器初始化。全部 AAA 配置改走 `DeviceConfig` 键。**严禁在 `CLIState` 上新增任何 AAA / LocalUser / Domain / Scheme 内嵌结构体**（架构铁律，对照 GRE AC12 静态断言）。删除安全性：`state.LocalUsers` 全仓仅 **9 处**引用（`parser.go:1792/1793/1795/1796/1803/1804/1809` 写、`:3419/:3422` 读，**均在本期重构范围内**），`internal/api` / `internal/protocol` / 前端**零引用**，且**零测试覆盖**（`grep -rn "local-user\|LocalUser" --include=*_test.go .` 零命中）→ 删字段后 `go build` 立即暴露遗漏，破坏风险≈0。**`SSHUser` 结构体与 `state.SSH.Users` 是独立体系，本期不动**。
- **[P0-3 AAA 键命名空间（精确匹配，防前缀碰撞）]**：键统一为 `aaa:` 命名空间，建议形态：
  - 用户：`aaa:local-user:<name>:password` / `:privilege` / `:service-type` / `:state`
  - 认证方案：`aaa:authen-scheme:<name>:mode`
  - 授权方案（P1）：`aaa:author-scheme:<name>:mode`
  - 计费方案（P2）：`aaa:acct-scheme:<name>:mode`
  - 域：`aaa:domain:<name>:authen-scheme` / `:author-scheme` / `:acct-scheme` / `:state`
  **最终键名以架构师设计为准**，本条仅做预对齐。
  > 🔴 **键匹配红线（本期最高危，危险度高于 GRE 那轮）**：**严禁 `strings.Contains(k, "aaa")` 模糊匹配**。理由——**`aaa` 是合法的十六进制串**，而既有端口安全键 `interface:<if>:port-security-sticky-learned:<mac>` 的 MAC 段是十六进制：仓库现存测试数据即含 `00e0-fc12-0aaa`（`p2_portsec_qa_t07_test.go:275`），而 `aaaa-bbbb-cccc` 更是网络实验最常用的示教 MAC。模糊匹配会把**端口安全粘滞 MAC 键误判为 AAA 配置**（幽灵用户），且 `undo aaa` 级联清理会**误删端口安全配置**——比 GRE 那轮的 `Ag-gre-gation` 更容易踩中（GRE 是一个特定单词，`aaa` 是任意 MAC 都可能出现的十六进制片段）。同理**严禁 `strings.Contains(k, "domain")`**（`m-lag` 域相关键存在同名词）。必须提供 `aaaLocalUserKey` / `aaaSchemeKey` / `aaaDomainKey` / `aaaKeyPrefix` 精确 helper（**精确前缀 `aaa:` + 精确分段**），口径同 `gre_eval.go` 键常量段。

**B. 本地用户命令族（对齐官方 VRP 课程 71）**

- **[P0-4 `local-user <name> password cipher <password>`（AAA 视图）]**：写 `aaa:local-user:<name>:password`；用户名不存在则**隐式创建**（真机同此）。用户名约束：长度 1–64，**不得含 `@` 以外的分隔符歧义**（`user@domain` 形态见 §6 C4）。口令长度约束见 §6 C5（PM 建议 8–128）。🔴 **口令值在 `DeviceConfig` 中以明文存储**（本工具为本地单用户 JSON 存储，无加密基础设施），但**所有 `display` 输出一律脱敏为 `****`**（P0-13）。`password` 后**必须**跟 `cipher` 关键字；`password simple <pwd>`（真机已废弃的明文形态）→ `Error: unrecognized command`。
- **[P0-5 `local-user <name> privilege level <0-15>`（AAA 视图）]**：写 `aaa:local-user:<name>:privilege`；范围 0–15，越界 → `Error: Privilege level must be between 0 and 15.`。**直接修复「已有」表的死字段假 0 缺陷**：未配置时 `display` 显示 `-`（**不显示 `0`**——`0` 是合法的最低权限级别，与"未配置"是不同语义，这正是现状 `parser.go:3427` `Privilege: %d` 的缺陷，与 GRE `Key: 0` 同源）。
- **[P0-6 `local-user <name> service-type <type> [<type>...]`（AAA 视图）]**：写 `aaa:local-user:<name>:service-type`，**支持多值**（真机可 `service-type telnet ssh`）。合法取值集：`telnet` / `ssh` / `ftp` / `http` / `terminal` / `ppp`（课程 71 命令面）。非法取值 → `Error: Invalid service-type <x>. Available: telnet, ssh, ftp, http, terminal, ppp.`。存储与展示的多值顺序**必须确定性**（PM 建议：按上述固定枚举顺序规范化去重存储，而非按输入顺序，避免 `telnet ssh` 与 `ssh telnet` 产生两份不同配置——见 §6 C6）。
- **[P0-7 `local-user <name> state active|block`（AAA 视图）]**：写 `aaa:local-user:<name>:state`；缺省 `active`（**生效缺省，键不落盘**，对齐 GRE keepalive 缺省值口径）。非法取值 → `Error: usage: local-user <name> state { active | block }`。🔴 **`block` 仅为配置态标记**：本仿真器不做真实登录，故**不得声称"该用户已被拒绝登录 N 次"**，运行态计数恒 `-`。

**C. 认证方案与域（三级链路）**

- **[P0-8 `authentication-scheme <name>` + `authentication-mode <local|radius|none>`]**：AAA 视图 `authentication-scheme sch1` → 创建方案并进入方案子视图；子视图内 `authentication-mode local` 写 `aaa:authen-scheme:sch1:mode`。缺省 mode 为 `local`（真机 default 方案缺省即 local；**生效缺省，键不落盘**）。
  > 🔴 **顶层 token 冲突（本期最高危代码冲突，务必写进设计）**：`authentication-mode` 在 `parser.go:1741` **已存在顶层 `case`**，且**硬守卫在 `ViewVTY`**（`if state.CurrentView != ViewVTY { return "Error: must be in VTY user interface view" }`）。本期**必须把该 case 改为按视图分派**（`ViewVTY` → 既有 VTY 逻辑，**逐字不变**；`ViewAAA` 方案子视图 → AAA 逻辑；其它视图 → 合并报错），**严禁新增第二个 `case "authentication-mode"`**（Go 编译期即报 duplicate case，且语义双写）。注：`parser.go:4637` 的 `case "authentication-mode"` 位于 **VRRP 子命令内层 switch**，与顶层分派不在同一层级，**不受影响、不得改动**。
  > `authentication-mode radius` 的处置见 §6 C3（PM 建议：**接受为配置态并如实存储 + 诚实注记**，但**绝不联动**现存的自造 `radius` 命令与 `state.RADIUS`）。
- **[P0-9 `domain <name>` + `authentication-scheme <name>` 绑定]**：AAA 视图 `domain huawei` → 创建域并进入域子视图；子视图内 `authentication-scheme sch1` 写 `aaa:domain:huawei:authen-scheme`。域名约束：长度 1–64。
  > ⚠️ **同名 token 二义性（务必写进设计）**：`authentication-scheme` 在 **AAA 视图**是"创建方案并进视图"，在**域子视图**是"引用已有方案做绑定"——**同一 token、两种语义**，必须按 `CurrentView` 分派，且**域内引用不得隐式创建方案**（见 P0-10）。同理 `domain` 顶层 token 目前**未被占用**（`parser.go:1282` 的 `case "domain"` 位于 **m-lag 内层 switch**，不冲突），可安全新增。
- **[P0-10 引用完整性守卫（关键教学点）]**：域子视图绑定一个**不存在的**认证方案 → **明确报错且不写任何键**：`Error: The authentication scheme <name> does not exist.`（口径与 GRE 拍板「未 `tunnel-protocol gre` 配 `source` 硬拒绝」、DHCP 拍板 #1 完全一致，**不做隐式自动创建**）。**这是本期最高教学价值的守卫之一**——真机同样拒绝，学员必须建立"先建方案、后绑域"的顺序认知。

**D. 守卫、展示与诚实占位**

- **[P0-11 视图 / 设备 / 参数守卫矩阵]**：
  - **系统视图**执行 `local-user` / `authentication-scheme` / `domain` → `Error: Please configure it in the AAA view. Run 'aaa' first.`（**替换**现状 `parser.go:1784-1786` 的 `must be in system view`，该提示与真机完全相反，属**教错**）。
  - **AAA 视图**执行 `authentication-mode` → `Error: Please run 'authentication-scheme <name>' first.`（认证模式属方案子视图）。
  - 非 AAA 相关视图（接口视图等）执行上述命令 → 沿用/合并为明确视图报错，**不得静默吞掉**。
  - 设备类型不支持（PC / Server / 二层 Switch）→ 复用 `l3Devices()` 拒绝（P0-15）。
  - 参数缺失 → VRP 风格 usage 提示（如 `Error: usage: local-user <name> privilege level <0-15>`）。
  - **`local-user` 后跟未知子属性**（如 `local-user u1 foobar x`）→ `Error: unrecognized command`，**且不得创建该用户**（避免"打错字凭空产生幽灵用户"）。
- **[P0-12 新增 `display aaa` / `display local-user` / `display domain [<name>]`]**：VRP 风格只读展示，格式见 §4。**输出必须确定性**（用户 / 方案 / 域均按名称升序，**禁止复制 `parser.go:3422` 的 map 随机遍历**）。空态 → `Info: No local user configured.` / `Info: No domain configured.`。指定不存在的域 → `Error: The domain <name> does not exist.`。
- **[P0-13 口令脱敏（诚实 + 安全双红线）]**：**所有** `display` 输出（含 `display local-user`、`display aaa`、`display current-configuration`、`display saved-configuration`）中的口令**一律渲染为 `****`**。🔴 **严禁输出伪造的 VRP 密文串**（如 `%^%#xxxx%^%#`）——那是**编造数据**，与本项目"不伪造 `Status: up`"同一红线。同时须在 `display local-user` 输出附加诚实说明：本仿真器**未实现 VRP 不可逆加密算法**，口令以明文存于本地配置文件。**同步修复现状 `display ssh` 的 `Local Users` 段**（改读新事实源 + 确定性排序 + 脱敏）。
- **[P0-14 `aaaSimNote()` 诚实占位（CRITICAL 红线）]**：新增注记函数，口径严格对齐 `greSimNote()` / `portSecSimNote()`（读 `sim.EngineModeName()`，lite / full 两态）：
  - lite → 「（AAA 为配置态模拟（lite 引擎），无真实登录握手、无 RADIUS 协议交互与计费采集，认证统计与在线会话不可用）」
  - full → 「（AAA 为配置态模拟，无真实登录握手与计费采集）」
  所有 `display aaa` / `display local-user` / `display domain` 输出末尾必须附加。**输出中不得出现任何伪造的认证运行态**（见 §4.2 占位标注表）。
- **[P0-15 能力矩阵与分支内守卫]**：`capabilities.go:46` 已有 `"local-user": l3Devices()`，**本期保持零改动**。但新命令首 token 为 `aaa` / `authentication-scheme` / `authorization-scheme` / `accounting-scheme` / `domain`，**均未在矩阵中声明** → `isCommandSupported` **默认放行**（`capabilities.go:141-147`）。故设备守卫必须做在**分支内部**（复用 `l3Devices()`，`capabilities.go:173-181`，**严禁重定义**），口径完全对齐 GRE P0-14。`display aaa` / `display local-user` / `display domain` 为**只读命令、任意设备可读**，空态放行输出 `Info:`。
  > ⚠️ **`authentication-mode` 首 token 已被 `capabilities` 默认放行**，且既有 VTY 路径依赖此行为，改造时**不得**给它新增矩阵行（会连带影响 VTY 既有用例）。

**[P0-16 新增 `internal/cli/aaa_eval.go` 纯函数评估器]**：范式严格对照 `gre_eval.go` / `portsec_eval.go` —— **无副作用、不修改 `sim` 引擎、不 import `internal/protocol`、零新增第三方依赖、可单测**；仅读 `state.DeviceConfig` 派生只读视图。建议契约（**最终以架构师设计为准**）：
  - `aaaLocalUserKey(name, field)` / `aaaSchemeKey(kind, name, field)` / `aaaDomainKey(name, field)` / `aaaKeyPrefix()`：键构造 helper（P0-3 精确匹配）。
  - `collectAAALocalUsers(state) []string` / `collectAAASchemes(state, kind) []string` / `collectAAADomains(state) []string`：按**精确前缀 + 精确分段**扫描，返回**名称升序**去重列表。
  - `EvaluateAAA(state) AAAResult`：返回 `Users / AuthenSchemes / AuthorSchemes / AcctSchemes / Domains / VTYAuthMode / Stats`。`Stats` 各字段**类型为 string 且恒 `-`**（从类型层面杜绝日后填数字，对照 GRE `Stats` 与 DHCP `RelayStats` 处置）。
  - `maskAAAPassword(raw string) string`：恒返回 `****`（P0-13，独立纯函数便于断言）。
  - `aaaSimNote() string`：诚实占位注记（P0-14）。

> ⚠️ **顶层 token 冲突核查（PM 已完成，架构师请复验）**：`aaa` / `authentication-scheme` / `authorization-scheme` / `accounting-scheme` / `domain` 在 `parser.go` 顶层 `switch` 中**均无既有 case**（`grep -n 'case "<tok>"'` 逐个核过）→ 可安全新增。**唯一冲突是 `authentication-mode`**（`parser.go:1741` 顶层 + ViewVTY 守卫）→ 必须改为视图分派，见 P0-8 红框。`local-user`（`parser.go:1783`）为**本期重构对象**。`parser.go:1282` 的 `domain`、`parser.go:4637` 的 `authentication-mode` 均在**内层 switch**，不受影响。

### P1（增强真实语义 · 建议默认纳入）

- **[P1-1 `undo local-user <name>` 与属性级 undo]**：`undo local-user <name>` **级联清理**该用户 `aaa:local-user:<name>:` 精确前缀全部键；`undo local-user <name> privilege level` / `service-type` / `state` 清单个属性键（真机支持属性级 undo）。挂钩复用 `applyUndoGREInterface` 的 **handled 模式**（`parser.go:828+` 同款），未命中时交回既有分支，**零回归**。
- **[P1-2 `undo authentication-scheme <name>` / `undo domain <name>` + 引用完整性]**：删除方案/域并清理对应精确前缀键。🔴 **删除仍被域引用的方案** → 处置见 §6 C7（PM 建议：`Error:` 硬拒绝并提示引用者，避免产生悬空绑定——与 P0-10 的"先建后绑"教学点对称）。
- **[P1-3 `undo aaa`（系统视图）级联清理]**：清理 `aaa:` **精确前缀**全部键（用户 + 方案 + 域）。🔴 **本条是键碰撞红线的最高危触发点**：级联清理若用 `strings.Contains(k, "aaa")`，将**误删端口安全粘滞 MAC 键**（含 `0aaa` / `aaaa` 的 MAC）—— AC12 设专项断言。
- **[P1-4 `authorization-scheme <name>` + `authorization-mode <local|none>`]**：写 `aaa:author-scheme:<name>:mode`，与 P0-8 共用同一套方案子视图机制（增量极小）；域子视图可 `authorization-scheme <name>` 绑定，同样受 P0-10 引用完整性守卫约束。🔴 **纯配置态**：本仿真器不做真实命令级授权裁决，**不得声称"用户 X 被授权执行 Y"**。
- **[P1-5 `display current-configuration` 新增 `aaa` 块 + save→reload 贯通]**：新增 `buildSavedAAAConfig(state)`，按 VRP 顺序输出 `aaa` → `authentication-scheme <name>` + ` authentication-mode <mode>` → `local-user <name> password cipher ****` / ` privilege level` / ` service-type` / ` state block` → `domain <name>` + ` authentication-scheme <name>`（**缺省值不冗余输出**，对齐 `buildSavedGREInterfaceConfig` / `buildSavedSTPConfig` 口径）。挂入 `buildSavedConfigSnapshot` 的**系统级块区**（参照 `parser.go:5392-5395` 的 STP 块挂载点，位于接口块循环**之前**）。AAA 键随既有 `SerializeToDeviceConfigData` ↔ `LoadFromDeviceConfigData` 自动往返，**零新增持久化代码**。
  > ⚠️ **口令行的 save→reload 一致性**：由于 P0-13 脱敏，快照中输出 `password cipher ****`。因 `DeviceConfig` 才是事实源（明文键随 JSON 往返），reload 后渲染仍得 `****` → **字节级一致成立**。但须明确：**该文本快照不可回灌**（不是可执行配置），这与既有 STP / GRE 快照定位一致。
- **[P1-6 多用户 / 多方案 / 多域并存与隔离]**：同一设备多个用户、方案、域各自独立配置、互不干扰；键前缀天然隔离，需测试覆盖。数量上限见 §6 C8。
- **[P1-7 `display domain <name>` 详情视图]**：单域详情，展示该域绑定的认证/授权/计费方案名**及被绑方案的实际 mode**（跨对象解引用），以及域 state。方案不存在时（理论上被 P0-10 阻断）显示 `- (not found)` 而非崩溃。
- **[P1-8 VTY ↔ AAA 引用关系可见]**：`display aaa` 中体现 `user-interface vty` 当前 `authentication-mode` 取值（读既有 `state.VTY.AuthenticationMode`，**只读不写**），闭合「已有」表的悬空引用。🔴 **只展示引用关系，不模拟登录**：不得输出"当前在线用户""上次登录时间"等任何会话态。

### P2（边界收敛 / 诚实边界 / 可选增强）

- **[P2-1 `accounting-scheme <name>` + `accounting-mode <none|radius>`]**：写 `aaa:acct-scheme:<name>:mode`，机制与 P1-4 同构。**PM 建议降级到 P2**（而非随 P1-4 一起做），理由见 §6 C2：计费是 AAA 三要素中**唯一其全部价值都在运行态数据**（上下行字节、会话时长、计费记录）的能力——在配置态仿真器里，一个所有计数恒 `-` 的计费方案**教学收益最低、诚实占位负担最重**。若架构师评估增量确实微小（共用方案机制），可提升至 P1。
- **[P2-2 缺省域与缺省方案预置]**：真机开机即存在 `default` / `default_admin` 域与 `default` 认证方案。是否预置见 §6 C9。**PM 倾向不预置**（预置会让"空态 `Info:`"语义复杂化，且预置内容本身是一种"未经用户配置的既成事实"）。
- **[P2-3 `local-user <name> password irreversible-cipher <pwd>`]**：真机新版推荐形态。本期**不实现**（`Error: unrecognized command`），随 §6 C5 一并考虑。
- **[P2-4 用户名 `user@domain` 形态解析]**：真机登录名可带域后缀。本期仅在**用户名合法性校验**层面明确接受/拒绝（§6 C4），**不做登录期域解析**（无登录）。
- **[P2-5 文案语言与一致性]**：错误/提示统一英文 `Error:` / `Info:` 前缀（对照既有代码），诚实占位注记沿用**中文括注**（对照 `greSimNote()`）。本期**不引入 i18n 框架**，仅保证风格自洽、可 grep、可断言。
- **[P2-6 前端无变更]**：AAA 仅在 CLI 文本体现，**不新增 API 字段、不做用户管理 UI**（与 NAT / 端口安全 / VRRP / STP / LAG / DHCP 中继 / GRE 一致）。

---

## 4. UI / 交互设计稿（CLI 回显与 display 输出，纯文本）

> 本节为 **display 输出的唯一权威源**（沿用 GRE / DHCP 那轮「display 渲染标签/列宽以 PRD §4 为准，设计不另定列宽」的团队约定）。工程师严格照样例实现，测试据此写子串断言。

### 4.1 配置命令序列回显（课程 71 主线操作流）

```
<R1> system-view
[R1] aaa
[R1-aaa] local-user admin password cipher Huawei@123
[R1-aaa] local-user admin privilege level 15
[R1-aaa] local-user admin service-type telnet ssh
[R1-aaa] local-user guest password cipher Guest@2026
[R1-aaa] local-user guest privilege level 1
[R1-aaa] local-user guest state block
[R1-aaa] authentication-scheme sch1
[R1-aaa-authen-sch1] authentication-mode local
[R1-aaa-authen-sch1] quit
[R1-aaa] domain huawei
[R1-aaa-domain-huawei] authentication-scheme sch1
[R1-aaa-domain-huawei] quit
[R1-aaa] quit
[R1]
```

> VRP 风格：配置成功**静默或规范短回显**，失败才 `Error:`。**不得出现 `Local user admin created` 这类自造欢快文案**（现状 `parser.go:1811` 即此缺陷，对照 GRE 对 `GRE tunnel %s created`、LAG 对 `Port added to Eth-Trunk 1` 的整改）。
> 🔴 **注意 `quit` 层级**：`[R1-aaa-authen-sch1] quit` → 回 `[R1-aaa]`（**不是** `[R1]`）；`[R1-aaa] quit` → 回 `[R1]`（P0-1，AC12 专项断言）。

**典型拒错回显（`Error:` 硬拒绝，且不写任何键）**：

```
[R1] local-user admin password cipher Huawei@123
Error: Please configure it in the AAA view. Run 'aaa' first.      ← P0-11 第一条（替换现状"must be in system view"）

[R1-aaa] authentication-mode local
Error: Please run 'authentication-scheme <name>' first.            ← P0-11 第二条

[R1-aaa-domain-huawei] authentication-scheme nosuch
Error: The authentication scheme nosuch does not exist.            ← P0-10 引用完整性（最高教学价值）

[R1-aaa] local-user admin privilege level 16
Error: Privilege level must be between 0 and 15.                   ← P0-5

[R1-aaa] local-user admin service-type vnc
Error: Invalid service-type vnc. Available: telnet, ssh, ftp, http, terminal, ppp.
                                                                    ← P0-6

[R1-aaa] local-user admin password cipher 123
Error: The password length must be between 8 and 128.              ← P0-4（待 §6 C5 拍板）

[R1-aaa] local-user admin foobar x
Error: unrecognized command                                        ← P0-11 末条，且**不得创建 admin 用户**
```

### 4.2 `display local-user`

```
--------------------------------------------------------------------------------
  User-name                State    Privilege   Service-type          Password
--------------------------------------------------------------------------------
  admin                    Active   15          telnet ssh            ****
  guest                    Block    1           -                     ****
  operator                 Active   -           terminal              -
--------------------------------------------------------------------------------
  Total 3 user(s)

  --- Authentication runtime statistics ---
  Successful authentications : -
  Failed authentications     : -
  Online sessions            : -
  Last login time            : -
（口令未做不可逆加密：本仿真器未实现 VRP 密文算法，口令以明文存于本地配置文件，此处仅做展示脱敏）
（AAA 为配置态模拟（lite 引擎），无真实登录握手、无 RADIUS 协议交互与计费采集，认证统计与在线会话不可用）
```

**字段真实性标注表**（架构师据此实现，测试据此断言）：

| 字段 | 数据来源 | 真实性 | 未配置时 |
|---|---|---|---|
| `User-name` | `aaa:local-user:<name>:*` 键名解析 | **真实**（配置态） | 该用户不列出 |
| `State` | `aaa:local-user:<name>:state` | **真实**（配置态） | `Active`（生效缺省） |
| `Privilege` | `aaa:local-user:<name>:privilege` | **真实**（配置态） | **`-`（不得显示 `0`）** |
| `Service-type` | `aaa:local-user:<name>:service-type` | **真实**（配置态） | `-` |
| `Password` | `aaa:local-user:<name>:password` | **真实存在性**，值恒脱敏 | 已配 → `****`；未配 → `-` |
| `Successful authentications` | — | 🔴 **诚实占位 `-`** | `-` |
| `Failed authentications` | — | 🔴 **诚实占位 `-`** | `-` |
| `Online sessions` | — | 🔴 **诚实占位 `-`**（**严禁** `0 online` / `1 user(s) online`） | `-` |
| `Last login time` | — | 🔴 **诚实占位 `-`**（**严禁**输出 `time.Now()` 派生值） | `-` |

> 🔴 = 仿真环境无真实数据源，**恒为 `-`，严禁编造数字、随机数或伪造会话**。
> ⚠️ **`Password` 列的诚实边界**：显示 `****` 表示"已配置口令"，显示 `-` 表示"未配置口令"——这两者**必须可区分**（与 `Privilege` 的 `-` vs `0` 同源）。**严禁**输出任何形如 `%^%#...%^%#` 的伪造密文串。

### 4.3 `display aaa`

```
AAA configuration information
--------------------------------------------------------------------------------
Local user count            : 3
Authentication scheme count : 2
Authorization scheme count  : 0
Accounting scheme count     : 0
Domain count                : 1
VTY authentication-mode     : aaa        (user-interface vty, referenced)
--------------------------------------------------------------------------------
Authentication schemes:
  Name                     Mode
  ------------------------------------
  default                  local
  sch1                     local
--------------------------------------------------------------------------------
Domains:
  Name                     Authen-scheme   Author-scheme   Acct-scheme   State
  --------------------------------------------------------------------------
  huawei                   sch1            -               -             Active
--------------------------------------------------------------------------------
（AAA 为配置态模拟（lite 引擎），无真实登录握手、无 RADIUS 协议交互与计费采集，认证统计与在线会话不可用）
```

- `VTY authentication-mode` 行读既有 `state.VTY.AuthenticationMode`（**只读不写**，P1-8），闭合悬空引用。取值为 `aaa` 时附 `(user-interface vty, referenced)` 标注；非 `aaa` 时附 `(AAA not referenced by VTY)`。
- **方案 / 域均按名称升序排序**（确定性，禁止 map 随机遍历）。
- 空态：
  ```
  Info: No AAA configuration.
  （AAA 为配置态模拟（lite 引擎），...）
  ```

### 4.4 `display domain [<name>]`

```
  Domain-name              Authen-scheme   Author-scheme   Acct-scheme   State
  ----------------------------------------------------------------------------
  huawei                   sch1            -               -             Active
  ----------------------------------------------------------------------------
  Total 1 domain(s)
（AAA 为配置态模拟（lite 引擎），...）
```

指定域名时（P1-7 详情）：

```
Domain-name                 : huawei
State                       : Active
Authentication-scheme       : sch1  (mode: local)
Authorization-scheme        : -
Accounting-scheme           : -
  --- Domain runtime statistics ---
  Online users              : -
  Access accepts            : -
  Access rejects            : -
（AAA 为配置态模拟（lite 引擎），...）
```

- `Authentication-scheme` 行做**跨对象解引用**，附带被绑方案的实际 mode（P1-7）。
- 域不存在 → `Error: The domain <name> does not exist.`
- 空态 → `Info: No domain configured.`

### 4.5 `display current-configuration` 中的 AAA 块（P1-5）

```
#
aaa
 authentication-scheme default
 authentication-scheme sch1
  authentication-mode local
 local-user admin password cipher ****
 local-user admin privilege level 15
 local-user admin service-type telnet ssh
 local-user guest password cipher ****
 local-user guest privilege level 1
 local-user guest state block
 domain huawei
  authentication-scheme sch1
#
```

> 输出顺序固定：`authentication-scheme`（含其 `authentication-mode` 缩进子行）→ `local-user`（按用户名升序，每用户内按 `password` → `privilege level` → `service-type` → `state` 固定顺序）→ `domain`（含其绑定缩进子行）。**缺省值不冗余输出**（`state active` 不输出、缺省 `authentication-mode local` 不输出，对齐 VRP 惯例与 `buildSavedGREInterfaceConfig` / `buildSavedSTPConfig` 口径）。
> 🔴 口令行输出 `****`（P0-13）。该快照**不可回灌**，与既有 STP / GRE 快照定位一致。
> 挂载点：`buildSavedConfigSnapshot` 的**系统级块区**，参照 `parser.go:5392-5395` 的 STP 块（位于接口块循环**之前**）。

### 4.6 前端

**本期无变更**。AAA 仅在 CLI 终端文本体现（P2-6）。

---

## 5. 验收标准（AC1–AC13，每条可用自动化测试证明，非恒真断言）

- **AC1（AAA 视图与视图层级正确 · P0-1）**：① 系统视图 `aaa` → 断言 `state.CurrentView == ViewAAA` 且提示符**逐字**为 `[R1-aaa]`；② `authentication-scheme sch1` → 断言提示符为 `[R1-aaa-authen-sch1]`；③ 该子视图 `quit` → 断言提示符**回到 `[R1-aaa]`**（**不是** `[R1]`，直击 `parser.go:285-296` if-else 链末尾 `else` 越级弹回的隐患）；④ `domain huawei` → `[R1-aaa-domain-huawei]`，`quit` → `[R1-aaa]`；⑤ `[R1-aaa] quit` → `[R1]`；⑥ 任意 AAA 子视图 `return` → 断言回 `ViewUser`（既有行为不变）。

- **AC2（事实源写入 · P0-2/P0-3/P0-4/P0-5/P0-6/P0-7）**：在 Router / L3Switch 上走完 §4.1 主线，断言 `DeviceConfig["aaa:local-user:admin:password"] == "Huawei@123"`、`...:privilege" == "15"`、`...:service-type"` 含 `telnet` 与 `ssh`、`DeviceConfig["aaa:local-user:guest:state"] == "block"`、`DeviceConfig["aaa:authen-scheme:sch1:mode"] == "local"`、`DeviceConfig["aaa:domain:huawei:authen-scheme"] == "sch1"`。**反向断言：`state.LocalUsers` 字段已不存在**（静态断言 `grep -n "LocalUsers\|LocalUser struct" internal/cli/state.go` 无命中），证明 P0-2 结构体事实源已废弃。

- **AC3（save → reload 持久化贯通 · 现状 100% 丢失，本条是本期最大价值点）**：完成 AC2 配置后执行 `save`，经 `SerializeToDeviceConfigData` → `LoadFromDeviceConfigData`（或 `NewCLIStateFromDeviceConfig`）往返，reload 后断言：① `DeviceConfig` 中 `aaa:` 精确前缀键集与 reload 前**逐键完全一致**；② `display local-user` 完整复现 3 个用户及其 privilege / service-type / state；③ `display domain huawei` 复现绑定；④ **`display current-configuration` 复现 §4.5 全部 11 行**且**两次快照字节级一致**。**同时补一条对照断言：改造前该场景 reload 后用户列表为空**（证明缺陷确被修复）。

- **AC4（旧形态已下线，且无残留写入路径 · P0-2/P0-11）**：① **系统视图**执行 `local-user admin password cipher Huawei@123` → 返回含 `AAA view` 的 `Error:`（**断言不再返回 `must be in system view`**，该文案与真机相反属教错），**且断言 `DeviceConfig` 中无任何 `aaa:` 键被写入**；② 静态断言 `grep -rn "state.LocalUsers" internal/cli/` **零命中**；③ 断言不再出现自造回显 `Local user admin created`（`grep -rn "Local user .* created" internal/cli/` 零命中）。

- **AC5（`authentication-mode` 顶层 case 视图分派 · P0-8，本期最高危代码冲突）**：
  - **AC5a（AAA 路径）**：`aaa` → `authentication-scheme sch1` → `authentication-mode local` → 断言 `DeviceConfig["aaa:authen-scheme:sch1:mode"] == "local"`。
  - **AC5b（VTY 既有行为零回归）**：`user-interface vty 0 4` → `authentication-mode aaa` → 断言 `state.VTY.AuthenticationMode == "aaa"` 且**回显文案逐字不变**（`Authentication-mode set to aaa`）；`authentication-mode password` / `none` 同样逐字不变；VTY 下非法值仍返回原 usage 文案。
  - **AC5c（AAA 视图直接敲 `authentication-mode`）**：`[R1-aaa] authentication-mode local` → 断言返回含 `authentication-scheme` 引导的 `Error:`，**且不写任何 scheme 键**。
  - **AC5d（编译期唯一性）**：静态断言 `internal/cli/parser.go` 顶层 switch 中 `case "authentication-mode"` **有且仅有 1 处**（`grep -c` 结合缩进层级核验），证明未新增重复 case；且 `parser.go:4637` 的 VRRP 内层 case **逐字未改**。

- **AC6（引用完整性守卫 · P0-10 / P1-2，最高教学价值）**：① 域子视图绑定不存在的方案 `authentication-scheme nosuch` → 断言返回含 `does not exist` 的 `Error:`，**且断言 `aaa:domain:huawei:authen-scheme` 键未写入**（证明未静默成功、未隐式创建方案）；② 先建 `sch1` 再绑定 → 成功；③ 删除仍被引用的方案（`undo authentication-scheme sch1`）→ 按 §6 C7 拍板断言（PM 建议：含 `is referenced by domain` 的 `Error:` 且方案键保留）。

- **AC7（参数校验与守卫矩阵 · P0-4/P0-5/P0-6/P0-7/P0-11）**：逐条断言**具体子串**（**不得用「返回非空」这类恒真断言**）：① `privilege level 16` / `-1` / `abc` → 含 `between 0 and 15`，且键未写入；② `service-type vnc` → 含 `Invalid service-type`，且键未写入；③ `state enabled` → 含 `usage:` 与 `active`、`block`；④ `password cipher 123` → 按 §6 C5 拍板断言（PM 建议含 `length must be between 8 and 128`）；⑤ `local-user admin foobar x` → 含 `unrecognized command`，**且断言 `aaa:local-user:admin:*` 键一个都没有**（打错字不得凭空产生幽灵用户）；⑥ `local-user`（缺参）→ 含 `usage:`。

- **AC8（`display` 忠实展示 + 输出确定性 · P0-12）**：① 配 3 个用户后 `display local-user` 输出 3 行数据行，各列取值正确，**用户按名称升序**；② **未配 privilege 的用户该列为 `-` 而非 `0`**（P0-5 关键断言，直击现状 `parser.go:3427` 死字段假 0 缺陷）；③ 未配 service-type 的用户该列为 `-`；④ **同一状态连续调用 `display local-user` / `display aaa` / `display domain` 各 10 次，输出字节级完全一致**（证明消除了 `parser.go:3422` 的 map 随机遍历，对照 GRE AC7 / DHCP AC7）；⑤ 空态断言 `No local user configured` / `No domain configured`；⑥ `display domain nosuch` → 含 `does not exist` 的 `Error:`。

- **AC9（口令脱敏 · P0-13，诚实 + 安全双红线）**：① `display local-user` / `display aaa` / `display current-configuration` / `display saved-configuration` 输出中**均不含明文口令子串** `Huawei@123`（正则全量扫描断言）；② 已配口令的用户该列**恒为 `****`**；③ **未配口令的用户该列为 `-`**（与 `****` 可区分）；④ **断言输出中不存在任何形如 `%^%#` 的伪造密文标记**（正则 `%\^%#` 零命中）；⑤ `display local-user` 输出含"未实现 VRP 密文算法 / 明文存于本地配置文件"的诚实说明子串；⑥ 单测 `maskAAAPassword("anything")` 恒返回 `****`。

- **AC10（诚实占位 · CRITICAL 红线 · P0-14）**：lite 引擎下 `display aaa` / `display local-user` / `display domain` 输出**均含** `aaaSimNote()` 的「无真实登录握手、无 RADIUS 协议交互与计费采集」注记；用**正则断言输出中不存在任何伪造运行态数字**——具体：`Successful authentications` / `Failed authentications` / `Online sessions` / `Last login time` / `Online users` / `Access accepts` / `Access rejects` 七个字段的值**必须恒为 `-`**，断言其**不匹配** `\d+` 且**不匹配** `online|Online user|Never|\d{4}-\d{2}-\d{2}`（防 `time.Now()` 派生值）。**该 AC 失败即视为违反项目核心价值观，不得以「观感更好」为由放行。**

- **AC11（undo 语义完整 · P1-1/P1-2/P1-3）**：① `undo local-user admin privilege level` 后该键**被清除而非留空串**（断言 `_, ok := DeviceConfig[key]; ok == false`），且其余属性键完好；② `undo local-user admin` 后 `aaa:local-user:admin:` 精确前缀全部键被清理，且 `display local-user` 中该用户消失、**其他用户完好**；③ `undo domain huawei` 后域键清理；④ `undo aaa`（系统视图）后 `aaa:` 精确前缀全部键清理，`display aaa` 回到空态；⑤ **断言既有 `undo` 分支（接口视图 GRE / LAG / VRRP、系统视图各协议）行为逐字不变**（零回归）。

- **AC12（能力守卫 · P0-15）**：
  - **AC12a（配置命令按设备类型守卫）**：PC / Server / 二层 Switch 上执行 `aaa` / `local-user u1 password cipher Huawei@123` / `authentication-scheme s1` / `domain d1` 均**拒绝**（设备集 = `l3Devices()`，复用 `capabilities.go:173-181`，**不新增不重定义**）；Router / L3Switch / Firewall / VTEP 正常放行。
  - **AC12b（display 只读、任意设备可读）**：PC / Server 上执行 `display local-user` / `display aaa` / `display domain` **不得返回能力拒绝**，应放行并输出空态 `Info:`；断言输出**不含** `is not supported on`。
  - **AC12c（零回归）**：断言 `capabilities.go` **零改动**（`"local-user": l3Devices()` 保持原样，且**未新增 `authentication-mode` 矩阵行**）；断言既有 VTY / SSH / dot1x / radius 命令行为逐字不变。

- **AC13（纯函数无副作用 / 架构基线合规 + 键碰撞专项）**：
  - `EvaluateAAA` / `collectAAALocalUsers` / `collectAAASchemes` / `collectAAADomains` / `maskAAAPassword` / `aaaSimNote` 单测证明——不修改 `sim` 引擎、不写 `state`、**不 import `internal/protocol`**、零新增第三方依赖、连续两次调用结果一致且**不改写任何 `DeviceConfig` 键**（调用前后对 `DeviceConfig` 做 deep-equal 断言）。
  - 🔴 **键碰撞专项断言（本期最高危项，P0-3 / P1-3）**：构造同时存在
    `interface:GigabitEthernet0/0/1:port-security-sticky-learned:00e0-fc12-0aaa`（**含 `aaa` 子串！仓库实存测试数据 `p2_portsec_qa_t07_test.go:275`**）、
    `interface:GigabitEthernet0/0/2:port-security-sticky-learned:aaaa-bbbb-cccc`（**最常用示教 MAC**）、
    `interface:Bridge-Aggregation1:lag:mode`
    与 `aaa:local-user:admin:password` 的状态，断言 ① `collectAAALocalUsers` **只返回 `admin`**，不含任何 MAC 派生的幽灵用户；② 执行 `undo aaa` 级联清理后，**上述 3 个非 AAA 键全部完好无损**；③ 静态断言 `internal/cli/aaa_*.go` 中 **`strings.Contains(k, "aaa")` 与 `strings.Contains(k, "domain")` 零命中**（`grep` 断言）。
  - 静态断言 `state.go` 无任何 AAA / LocalUser / Domain / Scheme 内嵌结构体（`grep -n "LocalUser\|AAAConfig\|DomainConfig\|SchemeConfig" internal/cli/state.go` 无命中，对照 GRE AC12 口径）。

---

## 6. 约束与红线（项目铁律，实现与评审一律以本节为准）

> 本节为**不可协商项**。任何设计 / 实现 / QA 结论与本节冲突时，以本节为准。

1. 🔴 **诚实占位原则（最重要，信誉红线）**：本项目 Windows 侧跑的是 **lite 仿真引擎，不做真实数据平面**。凡是无法真实产生的运行时数据——**认证成功/失败次数、在线用户会话与时长、计费上下行字节数与报文数、用户最后登录时间、访问接受/拒绝计数**——**一律输出 `-`，严禁编造随机数字、严禁输出 `time.Now()` 派生的假时间、严禁伪造 `Online` / `0 online` / `Never` 之类状态词**，并在 `display` 输出末尾追加 `aaaSimNote()` 诚实提示（参考已有的 `greSimNote()` / `portSecSimNote()` 写法）。**衍生条款**：口令**严禁**输出伪造的 VRP 密文串（`%^%#...%^%#`），只能脱敏为 `****`。
2. **纯函数评估**：核心判定逻辑必须是**无副作用纯函数**（`internal/cli/aaa_eval.go`），不写 `state`、不碰 `sim` 引擎实例、不 import `internal/protocol`、零新增第三方依赖，便于单测。副作用集中在 `aaa_cmd.go` 单一入口。
3. **配置态单一事实源**：所有配置落在设备的 `DeviceConfig` 键值里，**不新增散落的 struct 字段**。本期须**删除** `state.LocalUsers` / `LocalUser`（GRE 那期就是因为早期用了 `state.GRE` 字段而做了纠正式重构；AAA 现状完全同构）。**严禁在 `CLIState` 上新增任何 AAA / Domain / Scheme 内嵌结构体**。
4. 🔴 **键碰撞红线**：读取配置键必须用**精确前缀 + 精确分段匹配**，**严禁 `strings.Contains(k, "aaa")`**——`aaa` 是合法十六进制串，会误伤端口安全粘滞 MAC 键（仓库实存 `00e0-fc12-0aaa`，且 `aaaa-bbbb-cccc` 是最常用示教 MAC），级联清理将**误删端口安全配置**。同理**严禁 `strings.Contains(k, "domain")`**。（历史教训：`strings.Contains(k,"gre")` 曾误伤 `Bridge-Aggregation`；本期风险**高于**那次。）
5. **save → reload 一致性**：配置 `save` 后重新加载，`display current-configuration` 必须**字节级还原**（口令行恒为脱敏后的 `****`，因 `DeviceConfig` 存明文、渲染恒脱敏，一致性成立）。
6. **零回归底线**：不得改坏既有 VTY `authentication-mode`、SSH / STelnet、dot1x、radius、端口安全、GRE / LAG / VRRP / STP / DHCP 中继任何行为；`capabilities.go` 本期**零改动**。

---

## 7. 明确的非目标（Out of Scope）

- **真实登录认证握手与会话管理**：不实现 telnet / SSH 登录时的用户名口令校验流程、不维护在线会话表、不做 `kill user-interface` 之类会话操作——**所有会话态指标一律诚实占位**；
- **真实 RADIUS 协议交互**：不实现 RADIUS 客户端、不发 Access-Request / 不解析 Access-Accept、不实现 `radius-server template` 命令族。`authentication-mode radius` 若被接受（§8 C3），**仅作配置态记录 + 诚实注记**，且**绝不联动**现存的自造 `radius <primary> <secret> <secondary>` 命令与 `state.RADIUS`（该命令为非 VRP 自造形态，属独立技术债，本期不动、不改、不联动，建议另开工单按 GRE 同款范式整改）；
- **真实计费采集**：不统计流量字节 / 报文数 / 会话时长、不产生计费记录、不做计费报文上报（C2 已明确，`accounting-scheme` 若纳入亦仅配置态）；
- **真实命令级授权裁决**：`authorization-scheme` 不影响任何命令能否执行、不做 privilege level 与命令等级的真实匹配（本仿真器所有命令对所有视图开放，权限模型不因 AAA 配置改变）；
- **SSH / Telnet 服务端实现**：不实现真实服务端监听、不实现 `stelnet server enable` 背后的协议栈（既有 `state.SSH` / `state.SSH.Users` 为独立体系，本期**不动、不合并**）；
- **802.1X 与端口接入认证联动**：既有 `dot1x`（`parser.go:2386`）为独立自造实现，本期**不联动、不整改**（`docs/p2-portsec-prd.md:150` 已登记为 AAA 路线延伸项，建议另开工单）；
- **口令不可逆加密算法**：不实现 VRP 的 irreversible-cipher / `%^%#` 密文格式（P2-3），口令以明文存于本地 JSON 配置文件，仅在展示层脱敏——**并在 `display` 中如实声明**；
- **`user@domain` 登录期域解析**：无登录流程，故不做（P2-4）；
- **AAA 相关 MIB / SNMP 上报、日志审计**；
- **前端图形化用户管理 UI / 新增 API 字段**（P2-6）；
- **重写 NAT / 端口安全 / VRRP / STP / LAG / DHCP 中继 / GRE**（仅 AAA 增量）。

---

## 8. 待确认问题（交主理人 / 架构师拍板，按重要性排序）

> 沿用 GRE / DHCP 那轮的拍板模式：每项给候选方案 + PM 建议 + 影响面。拍板后由 PM 回填结论表，实现与验收一律以拍板为准。

- **C1（核心 · 决定 P0-2 的改造力度）：旧 `local-user`（系统视图）与 `state.LocalUsers` 是"直接删"还是"兼容保留"？**
  - **(a) 直接删除 + 报错引导（PM 强烈建议）**：删 `state.LocalUsers` / `LocalUser`，系统视图旧命令改为报错引导到 AAA 视图。理由——① 该视图**与真机相反**（真机在 AAA 视图），保留即持续教错；② 现状**不落盘**（`save`→`reload` 全丢），本就没有可保护的用户配置；③ 全仓仅 9 处引用、`internal/api` 与前端零引用、**零测试覆盖**，破坏风险≈0；④ 有 GRE 删 `state.GRE`、DHCP 删 `state.DHCPSelectMode`、STP 移除 `state.STP` 三个先例。
  - (b) 双轨兼容：系统视图旧命令保留写 `state.LocalUsers`，AAA 视图写 `DeviceConfig`。**PM 反对**——双写事实源，正是 LAG `:members` 与 STP `state.STP` 已被清理掉的坑。
  **PM 建议 (a)。请拍板，并确认是否允许删 `state.go:65` / `:317-323` / `:498` 三处。**

- **C2（核心 · 决定 P1-4 / P2-1 与本期体量）：授权（authorization）与计费（accounting）本期做到什么程度？**
  - **(a) 认证 P0 + 授权 P1 + 计费 P2（PM 建议）**：三者机制同构（共用方案子视图），但**计费是 AAA 三要素中唯一其全部价值都在运行态数据**（字节数 / 时长 / 计费记录）的能力——在配置态仿真器里，一个所有计数恒 `-` 的计费方案**教学收益最低、诚实占位负担最重**。故计费降级 P2。
  - (b) 三者全 P0：AAA 语义最完整（"认证/授权/计费"课程标题得以完整覆盖），但 AC 面扩大约 40%。
  - (c) 仅认证，授权与计费全 out-of-scope：范围最小，但学员看不到 AAA 的完整三级模型。**PM 反对**。
  **PM 建议 (a)。请拍板；若架构师评估「计费方案共用方案机制、增量微小」，可将 P2-1 提升至 P1。**

- **C3（决定 P0-8 与 AC5 断言面）：`authentication-mode radius` 是否接受？**
  - **(a) 接受为配置态并如实存储 + 诚实注记（PM 建议）**：课程 71 明确讲"本地与域方式"并对比 local / radius，学员一定会敲。如实存 `aaa:authen-scheme:<n>:mode = "radius"` 并原样展示，同时 `aaaSimNote()` 声明"无 RADIUS 协议交互"。**接受配置 ≠ 伪造运行态**，与本项目诚实红线不冲突。
  - (b) 仅接受 `local` / `none`，`radius` → `Error: RADIUS authentication is not supported (no radius-server template configured).` 也很干净，但学员敲课程里的命令会被拒。
  **PM 建议 (a)。请拍板。无论选哪个，都请确认：绝不联动现存自造的 `radius` 命令与 `state.RADIUS`（§7 已列为 out-of-scope）。**

- **C4（决定 P0-4 与 AC7 断言面）：用户名合法性规则？**
  - PM 建议：长度 1–64；允许字母数字与 `_` `-` `.` `@`；**不允许空格与 `?`**。`@` 允许是因为真机 `user@domain` 是合法登录名形态（但本期**不做域解析**，仅原样存储，见 P2-4）。
  - 备选：本期禁止 `@`，把 `user@domain` 一并推到 P2。
  **PM 建议允许 `@` 但不解析。请拍板。**

- **C5（决定 P0-4 与 AC7 ④）：口令长度 / 复杂度约束？**
  - **(a) 仅长度 8–128，不校验复杂度（PM 建议）**：真机 VRP 默认要求复杂度（大小写 + 数字 + 特殊字符），但**各版本规则差异大**，强校验会让学员反复被拒、偏离课程主线；长度校验已能教到"口令不能是 `123`"这个点。
  - (b) 长度 8–128 + 复杂度（至少含两类字符）：更贴近真机，但增加挫败感与版本争议。
  - (c) 不校验：**PM 反对**（放过一个必错配置，且失去教学点）。
  **PM 建议 (a)。请拍板下限值（8 还是 6）与是否加复杂度。**

- **C6（决定 P0-6 与 AC8 断言面）：多值 `service-type` 的存储与展示顺序？**
  - **(a) 按固定枚举顺序规范化去重存储（PM 建议）**：`telnet ssh` 与 `ssh telnet` 归一为同一份配置。优点——`save`→`reload` 与 `display` 天然确定性（AC8 ④ 字节级一致断言直接成立），且避免"同一语义两份键值"。
  - (b) 按用户输入顺序原样存储：更"忠实回显"，但两次等价配置会产生不同快照，与 AC3 ④ 的字节级一致断言存在张力。
  - **附带确认**：`local-user u1 service-type ssh` 执行两次、或先 `telnet` 后 `ssh`，是**覆盖**还是**追加**？PM 建议**覆盖**（真机 `service-type` 一条命令即为全集声明）。
  **PM 建议 (a) + 覆盖语义。请拍板。**

- **C7（决定 P1-2 与 AC6 ③）：删除仍被域引用的方案如何处置？**
  - **(a) `Error:` 硬拒绝并提示引用者（PM 建议）**：`Error: The authentication scheme sch1 is referenced by domain huawei and cannot be deleted.` 与 P0-10「先建后绑」教学点**对称**，且杜绝悬空绑定。
  - (b) 允许删除 + 级联清空域绑定：实现简单，但学员会莫名其妙丢掉域配置。
  - (c) 允许删除、域绑定变悬空：**PM 强烈反对**（产生幽灵引用，`display domain` 需渲染 `- (not found)` 兜底）。
  **PM 建议 (a)。请拍板。**

- **C8（一批缺省值与规格数字，打包拍板 · 决定 P0-5 / P0-7 / P1-6）**：
  - `privilege level` **范围**：`0`–`15`（对齐真机与既有 `parser.go:1764` VTY `user privilege level` 校验，**复用同一常量**）；未配时 display 显示 `-`（**不显示 `0`**）。
  - `state` **缺省**：`active`（**生效缺省，键不落盘**，对齐 GRE keepalive 缺省口径）。
  - **数量上限**：本地用户 / 认证方案 / 域各自上限。PM 建议**不设上限**（键前缀天然隔离，无同键列表语义）；若主理人希望设限，PM 建议各 `64`。
  - `authentication-mode` **缺省**：`local`（**生效缺省，键不落盘**）。
  **请逐项拍板或整体采纳 PM 建议。**

- **C9（决定 P2-2 与空态 AC 断言）：是否预置真机的缺省域 `default` / `default_admin` 与缺省方案 `default`？**
  - **(a) 不预置（PM 倾向）**：空态即真空态，`Info: No local user configured.` / `Info: No domain configured.` 语义干净，AC 断言简单。缺点：与真机开机态有差异。
  - (b) 预置 `default` 认证方案（mode=local）+ `default` / `default_admin` 域：更贴近真机 `display domain` 开机输出。缺点——① 预置内容是"未经用户配置的既成事实"，`display current-configuration` 是否输出它需另行定义；② 空态 AC 全部要改写；③ `undo aaa` 后是否重建预置项又是一个分支。
  **PM 建议 (a)，但这是「贴近真机」与「实现简洁」的价值权衡，请主理人拍板。**

- **C10（决定 §4.2 输出形态与 AC10 断言面）：`--- Authentication runtime statistics ---` 分组保留占位还是整块略去？**
  - **(a) 保留分组、字段全填 `-` + 注记（PM 建议）**：与 GRE 拍板「保留 `Tunnel runtime statistics` 分组、值全 `-`」、DHCP 拍板 #4「保留 `Forwarding statistics` 分组」**家族一致**；学员能看到"真机这里会有哪些指标"，具教学参考价值。
  - (b) 整块略去，只输出配置态字段 + 一行注记。输出最干净，绝无误读风险。
  **PM 建议 (a)（保持家族一致）。请拍板，这直接决定 AC10 的断言写法。**

---

## 附：关键 file:line 证据索引（供架构师直接定位，主理人可逐条 grep 验证）

**A. 本期重构对象（缺陷现状）**

- `internal/cli/parser.go:1783-1812` `case "local-user"` —— **视图错误**入口；`:1784-1786` 系统视图硬守卫（`Error: must be in system view`，**与真机相反**）；`:1789` usage 文案仅覆盖 2 个子属性；`:1792-1796` 写 `state.LocalUsers`；`:1798-1811` 弱解析循环（`for i := 1..` + 内层 switch，仅支持 `password cipher` / `service-type`，**无 privilege level、无 state**）；`:1803-1804` **`PasswordCipher` 与 `Password` 双写同一明文**；`:1811` 自造回显 `Local user %s created`。
- `internal/cli/state.go:65` `LocalUsers map[string]*LocalUser` 字段声明；`:317-323` `LocalUser{Name, Password, PasswordCipher, ServiceType, PrivilegeLevel}` 类型；`:498` 构造器 `LocalUsers: make(map[string]*LocalUser)`。**全仓 `state.LocalUsers` 仅 9 处引用**（`parser.go:1792/1793/1795/1796/1803/1804/1809` 写、`:3419/:3422` 读），`internal/api` / `internal/protocol` / 前端**零引用**，`--include=*_test.go` **零命中** → 删除风险≈0（P0-2 依据）。
- `internal/cli/state.go:321` `PrivilegeLevel int` —— **全仓只读不写的死字段**：唯一读点 `parser.go:3427` `Privilege: %d`，**无任何写点** → `display` 恒输出 `Privilege: 0`，**结构体零值伪装成真实配置**（与 GRE `Key: 0` 同源，GRE PRD P1-1 已明令禁止复制）。
- `internal/cli/parser.go:3419-3428` `display ssh` 的 `Local Users` 段 —— `:3422` `for _, user := range state.LocalUsers` **map 随机遍历**（与 `display gre` 旧实现同款缺陷，GRE AC7 已明令禁止复制）；`:3427` 输出 `Privilege: %d`（死字段假 0）；**无 password 脱敏、无 state 字段、无 simNote**。本期须改读新事实源 + 确定性排序 + 脱敏（P0-13）。
- `internal/cli/parser.go:1741-1753` `case "authentication-mode"` —— **顶层 case，硬守卫 `ViewVTY`**；`:1750` 允许 `authMode == "aaa"` 但**全仓无 AAA 视图可配** → **悬空引用**。🔴 **本期最高危代码冲突点**：必须改为按视图分派（P0-8），**严禁新增第二个同名顶层 case**。
- **持久化缺口**：`internal/cli/parser.go:5150-5178` `SerializeToDeviceConfigData` **只拷贝 `state.DeviceConfig`**（`:5163-5165` `for k, v := range state.DeviceConfig`）→ `state.LocalUsers` **不落盘**，`save`→`reload` **用户 100% 丢失**（AC3 对照断言依据）。`buildSavedConfigSnapshot`（`:5382+`）**零 local-user 输出**（`grep -n "local-user" parser.go` 仅命中 `:1783`/`:1789`）。

**B. 本期复用基线（正面范式）**

- 🔴 **键碰撞证据（本期最高危，AC13 专项断言依据）**：`internal/cli/p2_portsec_qa_t07_test.go:275` 实存键 `interface:GigabitEthernet0/0/1:port-security-sticky-learned:00e0-fc12-0aaa` —— **MAC 十六进制段天然含 `aaa` 子串**，且 `aaaa-bbbb-cccc` 是最常用示教 MAC。故 AAA 键扫描**严禁 `strings.Contains(k, "aaa")`**（P0-3 红线）。对照历史教训：`internal/cli/lag_eval.go:391-393` `interface:Bridge-Aggregation%d:lag:%s` 中 `Ag·gre·gation` 含 `gre` 子串（GRE 那轮同款坑）。
- `internal/cli/gre_eval.go:583-588` `greSimNote()`（lite/full 两态，读 `sim.EngineModeName()`）—— `aaaSimNote()` 照此实现（P0-14）。同族：`portsec_eval.go:239-244` `portSecSimNote()`、`lag_eval.go` `lagSimNote()`、`dhcp_relay_eval.go:382-387` `dhcpRelaySimNote()`。
- `internal/cli/gre_eval.go:44-80` 规格常量段与**键片段常量段**（精确后缀 / 精确中缀 / 精确前缀，附 A1 红线注释）—— AAA 键 helper 照此实现（P0-3）。
- **三件套文件范式**：`gre_cmd.go`（副作用唯一入口 + 三态守卫顺序：视图 → 设备 → 前置条件，`:118-195`；`clearGREKeys` 级联清理 `:358`；`applyUndoGREInterface` handled 模式 `:388`）/ `gre_display.go`（`buildGREDisplay` `:52`、空态 `:67`、汇总 `:77`、`buildSavedGREInterfaceConfig` `:205`、独立通道 `buildSavedGREConfig` `:270`）/ `gre_eval.go`（纯函数）—— **`aaa_cmd.go` / `aaa_display.go` / `aaa_eval.go` 照此三分**（P0-16）。
- **子视图范式**：`ViewDHCPPool` 进入 `parser.go:1622-1624`（`state.CurrentView = ViewDHCPPool; state.CurrentSub = poolName`）；提示符 `parser.go:5143-5144`（`[%s-dhcp-pool-%s]`）；🔴 `quit` 回退 `parser.go:285-296`（**if-else 链，末尾 `else` 一律回 `ViewSystem`** —— AAA 嵌套子视图必须显式加分支，否则越级弹回，AC1 ③ 专项断言）；子视图内命令守卫 `parser.go:1507`（`if state.CurrentView == ViewDHCPPool`）与 `parser.go:4024`（`ViewMSTRegion`）。`ViewType` 枚举 `state.go:17-29`（新增 `ViewAAA` 等）。
- `internal/cli/parser.go:5392-5395` `buildSavedConfigSnapshot` **系统级块挂载点**（`buildSavedSTPConfig`，位于接口块循环**之前**）—— `buildSavedAAAConfig` 照此挂载（P1-5）。接口级块挂载范式见 `:5443-5465`。
- `internal/cli/capabilities.go:46` `"local-user": l3Devices()`（**本期零改动**）；`:141-152` `isCommandSupported` 按首 token 匹配、**未声明默认放行**（P0-15 必须分支内守卫的依据）；`:173-181` `l3Devices()` 定义（Router / L3Switch / Firewall / VTEP，**复用不重定义**）。
- `internal/cli/parser.go:1755-1768` VTY `user privilege level` 的 **0–15 校验范式**（`level < 0 || level > 15`）—— P0-5 复用同一口径与常量（C8）。
- 顶层命令 token 冲突核查：`parser.go` 顶层 `switch` **无** `aaa` / `authentication-scheme` / `authorization-scheme` / `accounting-scheme` / `domain` 的 case（逐个 `grep -n 'case "<tok>"'` 核过，零命中）；`:1282` 的 `domain` 属 **m-lag 内层 switch**、`:4637` 的 `authentication-mode` 属 **VRRP 内层 switch** —— **均不冲突、均不得改动**。**唯一冲突**：`:1741` 顶层 `authentication-mode`（P0-8）。
- **邻接技术债（本期不动）**：`parser.go:2424-2437` `case "radius"` 自造命令（真机为 `radius-server template`）+ `state.RADIUS`（`state.go:77`/`:423`/`:526`）；`parser.go:2386` `case "dot1x"`；`parser.go:1859` `case "ssh"` 与 `state.SSH.Users`（`SSHUser`，`state.go:302-309`）—— 均为独立体系，§7 已列 out-of-scope，建议另开工单按 GRE 同款范式整改。
- 课程依据：`docs/reference/huawei-vrp-course.md:69` 第 71 讲「AAA / 认证 · 授权 · 计费、本地与域方式 / `aaa`、`local-user`」；`:93` 功能矩阵「AAA｜`aaa` / `local-user`｜📋 Roadmap｜视频 71（安全相关）」（本期交付后需同步更新为 ✅）；`:104` 安全视角「次高优先：`71 AAA`」；`docs/p2-portsec-prd.md:150` 已登记「端口安全与 802.1X / RADIUS 联动属 AAA 路线，课程 71，下期」。

---

## 文档状态

- 基线核查完成：`local-user` 错误视图与弱解析（`parser.go:1783-1812`）、`state.LocalUsers` / `LocalUser`（`state.go:65`/`:317-323`/`:498`）、`PrivilegeLevel` 死字段（`state.go:321` ↔ `parser.go:3427`）、口令双写明文（`parser.go:1803-1804`）、`display ssh` 里的 map 随机遍历（`parser.go:3419-3428`）、VTY `authentication-mode aaa` 悬空引用（`parser.go:1741-1753`）、持久化缺口（`parser.go:5150-5178` / `:5382+`）、能力矩阵（`capabilities.go:46`）、子视图与快照挂载点（`parser.go:285-296` / `:1622-1624` / `:5126-5145` / `:5392-5395`）均已核实到 file:line。
- **核心结论**：AAA **并非"完全缺失"，而是"半截且形态错误"** —— 一条守卫在错误视图的半成品命令 + 一份不落盘的结构体事实源 + 一个只读不写恒显示 `0` 的死字段 + 一处名为 cipher 实为明文的双写 + 一段 map 随机遍历的展示 + 一条 VTY 指向空气的悬空引用。本期是**纠错型重构 + 补全**，与 GRE 那轮高度同构，但**多出"新建视图层级"与"三级引用链路"两块新增量**，改造力度略高于 GRE 那期。
- **最高危技术点（务必写进设计）**：① **`aaa` 是合法十六进制串**，任何 `strings.Contains(k, "aaa")` 式扫描都会把端口安全粘滞 MAC 键（仓库实存 `00e0-fc12-0aaa`，最常用示教 MAC `aaaa-bbbb-cccc`）误判为 AAA 配置，`undo aaa` 级联清理将**误删端口安全配置**——风险**高于** GRE 那轮的 `Ag-gre-gation`（那是一个特定单词，这是任意 MAC 都可能命中的十六进制片段）；② **`authentication-mode` 顶层 case 已存在且硬守卫 `ViewVTY`**，必须改为视图分派而非新增 case；③ **`quit` 的 if-else 链末尾 `else` 一律回 `ViewSystem`**，AAA 嵌套子视图不显式加分支即会越级弹回。AC13 / AC5 / AC1 已分别设专项断言。
- 需求池 **29 条**（P0 15 / P1 8 / P2 6），验收标准 **AC1–AC13**（AC5 拆为 5a–5d、AC12 拆为 12a–12c），其中 **AC10 为诚实占位红线断言**（7 个运行态字段恒 `-`）、**AC9 为口令脱敏双红线断言**（明文零泄漏 + 无伪造密文串）、**AC3 为本期最大价值断言**（现状 save→reload 用户 100% 丢失）、**AC13 为键碰撞专项断言**。
- **§8 的 10 项待确认（C1–C10）待主理人 / 架构师拍板**，其中 **C1**（删旧命令与结构体）、**C2**（授权/计费分档，直接决定本期体量）、**C3**（`authentication-mode radius` 是否接受）为**阻塞项**，建议优先闭合。拍板后由 PM 回填结论表并同步修订受影响的 AC。
- 键命名（`aaa:local-user:<name>:<field>` / `aaa:authen-scheme:<name>:mode` / `aaa:domain:<name>:<field>`）为 PM 预对齐建议，**最终以架构师设计文档为准**。

_Last updated: 2026-08-09 · 产品经理 许清楚（Xu）_
