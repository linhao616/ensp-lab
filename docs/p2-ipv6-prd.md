# ensp-lab P2 第九项：IPv6 基础与 IPv6 路由（华为 VRP 实训课程 43/44）增量 PRD

> 文档类型：增量产品需求文档（PRD，**简单模式**，结构对齐 `docs/p2-aaa-prd.md`）
> 关联：`docs/p2-aaa-prd.md`（上一轮需求粒度 / AC 写法基准）、`docs/p2-gre-prd.md`（第二参照）、`docs/reference/huawei-vrp-course.md:47-48`（课程 43/44）
> 代码基线：`internal/cli/parser.go` / `state.go` / `capabilities.go` / `tools.go` / `gre_eval.go` / `aaa_eval.go`（**已逐条 grep 核查到 file:line，见文末证据索引**）
> 作者：产品经理 许清楚（Xu）
> 语言：中文
> 说明：本期**不做竞品/市场分析**（按主理人指示），仅输出产品目标 / 用户故事 / 需求池 / UI 设计稿 / 验收标准 / 待确认问题。

---

## 0. 项目信息

- **Language**：中文
- **Programming Language**：Go 1.26.5（本期工作集中在 `internal/cli`；前端 React/Vite 本期无变更）
- **Project Name**：`p2_ipv6_basic_routing`
- **原始需求复述**：在 P2 已交付 NAT（38）、端口安全（49）、VRRP（60/61）、STP/RSTP/MSTP（55/56/57）、链路聚合（63）、DHCP 中继（27）、GRE 隧道（69，v0.8.0）、AAA（71，v0.9.0）之后，为华为 eNSP VRP 仿真器落地 **IPv6 基础（课程 43）与 IPv6 路由（课程 44）** 的增量实现：把 `ipv6` 命令族**从"系统视图一句话使能 + 接口视图无校验存串"的半截形态**升级为「**全局/接口使能真实命令 + `ipv6 address <addr>/<prefix>` 合法性与前缀校验 + 链路本地/EUI-64/地址类型纯函数 + `display ipv6 interface [brief]` 只读展示 + `ipv6 route-static` + `display ipv6 routing-table`**」的完整 CLI 静态能力，并对运行态（ND 邻居表、DAD、动态路由学习、RIPng/OSPFv3 会话状态）施加**诚实占位**。

> **深度边界先验结论（务必先读 §6 待确认项）**：IPv6 的真实价值在于**协议栈本身**——128 位地址结构与缩写规则、EUI-64 从 MAC 生成接口标识、链路本地 `fe80::/10`、静态/直连/动态三类路由在转发表中的呈现。本工具是**单机 VRP CLI 仿真器，无真实 IPv6 协议栈、无 ND 邻居发现、无 DAD 过程、无 RIPng/OSPFv3 状态机**。因此本期严格划界：
>
> - **静态面 100% 真实**：全局 `ipv6` 使能、接口 `ipv6 enable`、`ipv6 address <addr>/<prefix>`（含合法性校验与**规范化缩写存储**）、`ipv6 route-static <prefix> <nexthop>`（含前缀/地址纯函数计算）、`display ipv6 interface [brief]`、`display ipv6 routing-table` 中的**静态/直连条目**、EUI-64 / 压缩 / 展开 / 地址类型判定 —— 必须真实落 `DeviceConfig` 键、真实可 `display`、真实可 `undo`、真实可 `save`→`reload` 复现。这部分**不允许打折**。
> - **运行面 100% 诚实占位**：「链路本地地址（无真实 MAC 可推）、ND 邻居表、DAD 尝试次数、可达/重传定时器、动态路由学习条目、RIPng/OSPFv3 邻居与会话状态、接口 IPv6 协议状态（Protocol）」等 —— **一律显示 `-` 或明确占位文案并附 `ipv6SimNote()`，严禁编造数字、严禁随机数、严禁伪造 `fe80::xxxx` 假地址**。这是本项目核心价值观红线（对照 GRE keepalive、AAA 认证计数、DHCP 中继转发计数的处置）。
>
> **重大基线发现（本期前置事实，非另起炉灶）**：IPv6 在代码基线中**并非完全缺失，而是以"半截 + 错误形态"存在**（与 GRE/AAA 那两轮高度同构，但严重度略低——无结构体死状态、无跨包死代码，是"命令识别不完整 + 数据不校验 + 展示缺失"）：
> ① `case "ipv6"`（`parser.go:2120-2130`）**系统视图**分支**不校验参数**：`ipv6 任意串` 都写 `DeviceConfig["ipv6:enabled"]="true"` 并回显 `IPv6 enabled`（`ipv6 garbage` 也"成功"）——**命令面不严谨**；
> ② 同 case **接口视图**分支 `ipv6 address <x>` **零校验**地把原始串写入 `interface:<if>:ipv6-address`（`parser.go:2126`）——非法地址、无 `/prefix`、非规范化书写全部"成功"；
> ③ 接口视图**没有** `ipv6 enable` 命令（真机须先使能接口 IPv6 再配地址）；
> ④ **没有** `display ipv6 interface` / `display ipv6 interface brief` / `display ipv6 routing-table` / `ipv6 route-static`（`display` switch 无 `case "ipv6"`）；
> ⑤ **没有** `undo ipv6 address` / `undo ipv6 enable`（接口级）；系统级 `undo ipv6`（`parser.go:5142-5144`）只删全局键，接口键不在清理范围；
> ⑥ `ipv6:enabled` 已进 `formatProtocolBlocks`（`parser.go:5040-5042`）→ 已随 current-configuration 输出 ` ipv6 enable`，但**接口级 `ipv6 address` 不进快照**（`buildSavedConfigSnapshot` 接口块循环无 IPv6 行）；
> ⑦ 能力矩阵已有 `"ipv6": hostsAndL3()`（`capabilities.go:129`），`isCommandSupported` 按首 token 匹配、未声明默认放行（`capabilities.go:141-152`）——**新子命令必须分支内守卫**。
>
> **🔴 与课程内容的冲突提示（务必先读 §6 C7/C8）**：主理人转述课程 44 的 RIPng 命令为 `ipv6 router rip` / `ripng enable`。**`ipv6 router rip` 是 Cisco IOS 语法，华为 VRP 真机为 `ripng [<process-id>]`（系统视图进进程视图）+ 接口 `ripng <pid> enable`**；OSPFv3 接口使能真机形态为 `ospfv3 <pid> area <area-id>`（需 area 参数）。本期 RIPng/OSPFv3 仅做**命令识别 + 配置存取 + display 诚实占位**，不仿真协议，冲突影响面可控，但命令形态**必须由主理人/架构师对照课程视频拍板**（§6 C7/C8），PRD 默认按华为真机形态起草。

---

## 1. 产品目标

在严守 P2「CLIState 层纯函数、单一事实源 `DeviceConfig`、诚实占位、零改动 `sim` 引擎、零新增第三方依赖、不 import `internal/protocol`」架构基线的前提下，把 IPv6 从**"半截且不校验"**升级为一条学员可完整走通的静态配置 + 展示链路：

1. **命令面对齐官方 VRP，补全校验与视图语义**：学员能按课程 43 的真实命令序列敲 `ipv6`（全局使能）→ `interface GigabitEthernet0/0/0` → `ipv6 enable` → `ipv6 address 2001:db8::1/64`，再按课程 44 敲 `ipv6 route-static 2001:db8:2::/64 2001:db8:1::2`；非法地址、非法前缀长度、非规范书写**必须被明确拒绝**（现状全部静默成功），命令形态、报错文案、`undo` 语义对齐真机，肌肉记忆可平移到 eNSP / 真实设备。**同时修复系统视图 `ipv6 任意串` 也"成功"的缺陷**（命令面不严谨）。
2. **地址/前缀纯函数成为可单测核心**：IPv6 地址合法性校验、RFC 5952 规范化压缩、全展开、前缀长度校验、链路本地 `fe80::/10` 判定、地址类型（单播/组播/任播/回环/未指定）分类、EUI-64（MAC → 接口标识）——全部实现为 `ipv6_eval.go` 的**无副作用纯函数**，既是产品核心价值（课程 43 的教学重点），也是架构师可放心单测的地基。
3. **配置真实落地且持久**：所有 IPv6 配置落在 `DeviceConfig` 键（`ipv6:` 全局命名空间 + `interface:<if>:ipv6-` 接口命名空间 + `ipv6:route-static:` 静态路由命名空间），`display ipv6 interface [brief]` / `display ipv6 routing-table` / `display current-configuration` 忠实复现，`save`→`reload` 后配置不丢（现状接口地址与静态路由完全不落快照）。
4. **展示忠实、边界诚实**：配置态字段（全局使能、接口使能、已配地址/前缀、静态路由、直连路由、EUI-64/类型判定结果）**如实展示**；链路本地地址、ND/DAD、动态路由学习、RIPng/OSPFv3 会话等仿真无法产出的运行态字段**一律 `-` + `ipv6SimNote()` 注记**，绝不用伪造 `fe80::` 假地址或假邻居数换取观感。

---

## 2. 用户故事

1. **作为学习 IPv6 地址结构的网络学员（课程 43 主线）**：As a 学员，I want 在路由器上敲 `ipv6` → 接口视图 `ipv6 enable` → `ipv6 address 2001:db8::1/64`，so that 我能用 `display ipv6 interface GigabitEthernet0/0/0` 核对地址是否按 RFC 5952 规范化显示、前缀是否正确，验证自己对 128 位地址与缩写规则的理解。
2. **作为练习 IPv6 地址纯函数的学员**：As a 学员，I want 输入 `2001:0db8:0000:0000:0000:0000:0000:0001` 这类全展开写法时被正确压缩为 `2001:db8::1`，输入非法地址（如 `2001:db8::gg`、`2001:db8::1/129`）时收到**明确、可读的错误提示**（而不是现状的静默成功），so that 我立刻知道地址规则错在哪，建立 IPv6 书写肌肉记忆。
3. **作为理解链路本地与地址类型的学员**：As a 学员，I want 通过 `display ipv6 interface` 看到链路本地字段**诚实显示 `-`**（因为本仿真器没有真实 MAC 可推导、也不伪造），并可通过纯函数验证 `fe80::1/10` 属于链路本地、`ff02::1` 属于组播、`2001:db8::1` 属于全球单播，so that 我既掌握地址分类，又不会被假的 `fe80::` 误导以为真机也这么生成。
4. **作为配置 IPv6 静态路由的学员（课程 44 主线）**：As a 学员，I want 敲 `ipv6 route-static 2001:db8:2::/64 2001:db8:1::2`，so that 我能用 `display ipv6 routing-table` 看到这条静态路由真实出现在转发表（Protocol=Static），同时看到由接口地址推导出的直连路由（Protocol=Direct），而动态学习条目区域**老实显示不可用**，不会误以为 RIPng/OSPFv3 真的在跑。
5. **作为关注持久化的学员**：As a 学员，I want `save` 后 `reload` 设备，so that 全局使能、接口地址、静态路由仍完整保留，`display current-configuration` 能复现整个 IPv6 配置块，而不必重配（**现状接口 `ipv6 address` 与 `ipv6 route-static` 完全不进快照**）。
6. **作为踩坑排障的学员**：As a 学员，I want 在 PC / Server / 二层 Switch 上敲 `ipv6 address` / `ipv6 route-static` 时被设备类型守卫**明确拒绝**（而不是静默写入），在系统视图敲 `ipv6 address` 时收到"请在接口视图配置"的引导，so that 我清楚每条命令的视图与设备适用范围——同时我接受工具明说它**不模拟 ND 邻居发现与动态路由协议**。

---

## 3. 需求池

> 共 **27 条**：P0 **14 条**、P1 **8 条**、P2 **5 条**（另列「已有」基线 7 条，属**重构对象 / 复用基线**，非新需求）。

### 已有（本期重构 / 复用，非另起炉灶）

| 标记 | 内容 | 位置 |
|---|---|---|
| [已有·**命令面不严谨·本期必改**] | `case "ipv6"` **系统视图分支不校验参数**：`ipv6 任意串` 都写 `DeviceConfig["ipv6:enabled"]="true"` 并回显 `IPv6 enabled`。真机全局使能命令就是裸 `ipv6`（系统视图），无子参数；`ipv6 <anything>` 必须被拒绝 | `parser.go:2121-2123` |
| [已有·**零校验存串·本期必改**] | 接口视图 `ipv6 address <x>` **零校验**写入 `interface:<if>:ipv6-address=<原始串>`：非法地址、无 `/prefix`、非规范化书写全部"成功"，且**不要求先 `ipv6 enable`** | `parser.go:2124-2128` |
| [已有·**缺失·本期必补**] | 接口视图**无 `ipv6 enable`**（真机接口 IPv6 使能命令）；`ipv6` 接口分支只处理 `address` 一个子命令 | `parser.go:2124-2128` |
| [已有·**缺失·本期必补**] | **无 `display ipv6 interface` / `display ipv6 interface brief` / `display ipv6 routing-table`**：`display` switch（`parser.go:2490+`）无 `case "ipv6"`，输入 `display ipv6 ...` 落入默认报错 | `parser.go:2490+` |
| [已有·**缺失·本期必补**] | **无 `ipv6 route-static`**（`case "ipv6"` 内无 `route-static` 分支）；IPv4 侧 `ip route-static` 为**遗留结构体事实源**（`state.Routes`，`parser.go:555-571`），**IPv6 不得复制该模式**，须走 `DeviceConfig` 键 | `parser.go:555-571`、`state.go:65` |
| [已有·**持久化缺口·本期必补**] | 接口 `ipv6 address` 与（未来的）静态路由**不进 `buildSavedConfigSnapshot`**：接口块循环（`parser.go:5458+`）无 IPv6 行；系统级块区（STP `:5392-5395`、AAA）无 IPv6 路由块。`ipv6:enabled` 已由 `formatProtocolBlocks`（`:5040-5042`）输出 | `parser.go:5392-5395`、`:5458+`、`:5040-5042` |
| [已有·可复用] | 能力矩阵已有 `"ipv6": hostsAndL3()`（`capabilities.go:129`）；`isCommandSupported` 按首 token 匹配、**未声明默认放行**（`:141-152`）。纯函数 + 诚实占位范式：`greSimNote()`（`gre_eval.go:583-588`）、`aaaSimNote()`（`aaa_eval.go:593`，读 `sim.EngineModeName()` lite/full）；三件套文件范式 `gre_cmd.go` / `gre_display.go` / `gre_eval.go`；键 helper 精确匹配范式 `gre_eval.go` 常量段；IPv4 `display ip routing-table` 渲染范式 `formatRoutingTable`（`tools.go:406`）；接口名规范化 `parseInterface` / `sortInterfaceNames` | 见各处 |

### P0（本期核心 · 使能命令补全 + 地址校验 + display 真实 + 静态路由 + 诚实占位）

**A. 使能与视图语义（前置阻塞项）**

- **[P0-1 系统视图全局使能命令语义修正]**：系统视图**裸 `ipv6`** → 写 `DeviceConfig["ipv6:enabled"]="true"`，回显 `IPv6 enabled`（对齐真机）。🔴 **`ipv6 <任何非空参数>` → `Error: unrecognized command`**（修复现状"任意串都成功"）；`ipv6 enable` 在系统视图 → **报错引导**（真机全局使能无 `enable` 子参数）：`Error: Please run 'ipv6' in system view to enable IPv6 globally, or 'ipv6 enable' in interface view.`。系统视图 `ipv6 address` / `ipv6 route-static` → 按视图分派（P0-2 / P0-9），**不得再被全局使能分支吞掉**。
- **[P0-2 接口视图 `ipv6 enable` + `ipv6 address <addr>/<prefix>`（视图分派）]**：接口视图：
  - `ipv6 enable` → 写 `interface:<if>:ipv6-enable="true"`，回显 `IPv6 is enabled on <if>`；
  - `ipv6 address <addr>/<prefix>` → 写 `interface:<if>:ipv6-address="<规范化地址>/<prefix>"`（**规范化缩写存储**，见 P0-3），回显 `IPv6 address <addr>/<prefix> configured on <if>`。
  - 🔴 **前置条件（C1 拍板，PM 建议）**：`ipv6 address` 前**必须**已 `ipv6 enable`，否则 `Error: Please run 'ipv6 enable' on <if> first.`（口径与 GRE「未 `tunnel-protocol gre` 配 `source` 硬拒绝」一致，**不做隐式自动使能**，保留教学点）。
  - 非接口视图执行 `ipv6 enable` / `ipv6 address` → `Error: must be in interface view`。
  - 接口视图裸 `ipv6`（无子命令）→ `Error: unrecognized command`。
  - 🔴 **顶层 token 冲突核查（PM 已完成）**：`ipv6` 顶层 `case` 已存在（`parser.go:2120`），本期**重构该 case 而非新增**；`case "ip"`（`parser.go:442`）与 `display` 内 `case "ip"`（`parser.go:2657`）不受影响。**严禁新增第二个顶层 `case "ipv6"`**。
- **[P0-3 IPv6 地址/前缀纯函数核心（`ipv6_eval.go`）]**：新增 `internal/cli/ipv6_eval.go`，全部为**无副作用纯函数、不写 state、不碰 sim、不 import `internal/protocol`、零新增第三方依赖**（**使用 Go 标准库 `net/netip`，Go 1.26.5 内置**）：
  - `ValidateIPv6Address(s string) error`：合法性校验（地址部分必须可解析，拒绝含 `%zone`、拒绝 IPv4 兼容写法混入的歧义形态等）；
  - `ValidateIPv6Prefix(prefix string) error`：校验 `<addr>/<prefix>` 形态，prefix 范围 0–128；
  - `CompressIPv6(addr string) string`：RFC 5952 规范化压缩（前导零省略、最长全零段压缩为 `::`、每组至少一位、`::` 仅出现一次）；
  - `ExpandIPv6(addr string) string`：全展开（8 组各 4 位十六进制，`::` 展开为全零段）；
  - `IPv6AddressType(addr string) AddressType`：`linkLocal`（`fe80::/10`）/ `multicast`（`ff00::/8`）/ `loopback`（`::1`）/ `unspecified`（`::`）/ `globalUnicast`（其余非特殊）/ `uniqueLocal`（`fc00::/7`，P1）；
  - `EUI64InterfaceID(mac string) (string, error)`：48 位 MAC → 64 位接口标识（插入 `ff:fe`、翻转 U/L 位）；
  - `SimulatedLinkLocal(mac string) string`：`fe80::` + EUI-64（**仅当接口存在真实 MAC 键时调用**，见 P0-6）；
  - `NetworkFromPrefix(prefix string) (string, error)`：前缀 → 网络地址（直连路由用）。
  - 这些函数是**本期最重要的可单测资产**（AC3 专项），也是课程 43 的教学核心。

**B. 键命名空间（精确匹配，防前缀碰撞）**

- **[P0-4 IPv6 键命名空间（精确匹配，防前缀碰撞）]**：键统一为：
  - 全局：`ipv6:enabled`（既有）
  - 接口：`interface:<if>:ipv6-enable` / `interface:<if>:ipv6-address`
  - 静态路由：**`ipv6:route-static:<prefix>:<nexthop>` = `true`（多键形态，C2 拍板：ECMP 前瞻，避免 P2 键不兼容）**——前缀与下一跳各为键的一段，值统一为存在标记 `true`；v1 命令面单下一跳（同前缀同下一跳幂等），多下一跳追加语义见 P2-1
  **最终键名以架构师设计为准**，本条仅做预对齐。
  > 🔴 **键匹配红线（本期最高危，务必写进设计）**：**严禁 `strings.Contains(k, "ip")` / `strings.Contains(k, "ipv6")` 模糊匹配**。理由——既有 IPv4 键 `interface:<if>:ip`（`parser.go:516`、`parser.go:880` 等）与**新增** `interface:<if>:ipv6-address` **共享 `:ip` 子串**：`Contains(k, ":ip")` 会把 IPv4 键 `interface:GE0/0/1:ip` 与 IPv6 键 `interface:GE0/0/1:ipv6-address` **同时误判**（幽灵 IPv6 地址 / 幽灵 IPv4 地址），级联清理会**误删 IPv4 配置**——与 GRE 轮 `Bridge-Aggregation` 含 `gre`、AAA 轮 `00e0-fc12-0aaa` 含 `aaa` 同源同险。同理 `Contains(k, "ipv6")` 会把全局 `ipv6:enabled` 与接口 `interface:<if>:ipv6-*` 混为一谈（不同命名空间语义不同）。必须提供 `ipv6GlobalKey` / `ipv6IfaceKey(iface, field)` / `ipv6RouteStaticKeyPrefix()` / `ipv6RouteStaticKey(prefix, nexthop)` / `ipv6KeyPrefix()` 精确 helper（**精确前缀 + 精确分段**，多键形态下静态路由按 `ipv6:route-static:` 前缀 + 双段解析，口径同 `gre_eval.go` 键常量段）。

**C. 展示与诚实占位**

- **[P0-5 `display ipv6 interface brief`]**：VRP 风格简表，字段见 §4.2。**输出必须确定性**（接口名升序，复用 `sortInterfaceNames`）。**运行态诚实**：`Physical` 读既有 `interface:<if>:status`（管理态，真实）；`Protocol`（IPv6 协议状态）**恒 `-`**（运行态占位）；`IPv6 Address` 读 `interface:<if>:ipv6-address`（真实），未配 → `-`。空态 → `Info: No IPv6 address configured.`。末尾附 `ipv6SimNote()`。
- **[P0-6 `display ipv6 interface <if>` 详情]**：单接口详情（§4.3）：`current state` 读 `:status`（真实）、`Line protocol` 恒 `-`、`IPv6 is enable` 读 `:ipv6-enable`（真实）、`link-local address` 按 **C3 拍板（已确认）**：**接口存在真实 MAC 键（`interface:<if>:mac`）→ 用 `EUI64InterfaceID` 真实计算显示 `fe80::<EUI64>`（真实推导非伪造）；无 MAC 键 → 恒 `-` + 注记；严禁任何确定性伪 MAC**、`Global unicast address(es)` 真实、`Joined group address(es)` 按 **C4 拍板（已确认）**：P0 恒 `-`，P1 渲染协议常量 + solicited-node 推导、`MTU` 读既有接口 MTU（真实，缺省 1500）、ND/DAD/定时器等**运行态字段恒 `-`**。接口不存在 → `Error: invalid interface '<if>'`（复用 `parseInterface`）。空态（已使能未配地址）→ 显示使能状态 + `-` 地址。末尾附 `ipv6SimNote()`。
- **[P0-7 `ipv6SimNote()` 诚实占位（CRITICAL 红线）]**：新增注记函数，口径严格对齐 `greSimNote()` / `aaaSimNote()`（读 `sim.EngineModeName()`，lite / full 两态）：
  - lite → 「（IPv6 为静态配置模拟（lite 引擎），无真实 ND 邻居发现 / DAD / 动态路由学习，链路本地与运行态字段不可用）」
  - full → 「（IPv6 为静态配置模拟，无真实协议栈与动态路由状态机）」
  所有 `display ipv6 interface [brief]` / `display ipv6 routing-table` 输出末尾必须附加。**输出中不得出现任何伪造的 IPv6 运行态**（见 §4 占位标注表）。

**D. 静态路由与路由表**

- **[P0-8 `ipv6 route-static <prefix>/<len> <nexthop>`（系统视图）]**：写 **`ipv6:route-static:<prefix>:<nexthop>` = `true`（C2 拍板：多键形态，ECMP 前瞻）**。**前缀与下一跳均须通过 P0-3 校验**（合法 IPv6 前缀 + 合法 IPv6 地址）；非法 → `Error: Invalid IPv6 prefix <x>` / `Error: Invalid IPv6 address <x>`，**且不写任何键**。同前缀同下一跳重复配置 → **幂等**（键已存在不报错不覆盖）；`undo ipv6 route-static <prefix>` → **级联清 `ipv6:route-static:<prefix>:` 精确前缀全部键**（多下一跳形态下同前缀多键一并清除）。🔴 **命令形态（C2 拍板，已确认）**：默认接受 CIDR 一段式 `<prefix>/<len> <nexthop>`；VRP 真机另支持三段式 `<address> <len> <nexthop>`（P1-6）。仅限系统视图；设备守卫见 P0-11。
- **[P0-9 `display ipv6 routing-table`]**：VRP 风格路由表（§4.4）：
  - **Static 条目**：读 `ipv6:route-static:` 精确前缀键，**按 `:（前缀）:（下一跳）` 双段解析**（C2 多键形态），真实渲染（Protocol=Static，Preference=60，Interface=NULL0，Cost=0）；
  - **Direct 条目**：由已配接口地址 + 前缀经 `NetworkFromPrefix` **纯函数推导**（真实，Protocol=Direct，Interface=对应接口）——对齐 IPv4 `formatRoutingTable` 的 `buildDirectRoutes` 范式（`tools.go:337`）；
  - **动态学习条目（RIPng/OSPFv3）**：本版本**不出现**，输出末尾由 `ipv6SimNote()` 说明"动态路由学习未模拟"；
  - 统计行 `Destinations` / `Routes` 按真实条目数统计；空表 → `Info: No IPv6 route.`。
  - **输出确定性**：目标前缀升序。**禁止复制 IPv4 `state.Routes` 的 map 随机遍历**（无 map，键前缀扫描天然确定性）。

**E. 守卫与持久化**

- **[P0-10 `undo` 语义完整]**：接口视图 `undo ipv6 enable` → 清 `interface:<if>:ipv6-enable` **并级联清 `interface:<if>:ipv6-address`（C5 拍板，已确认：对齐 GRE `undo tunnel-protocol` 级联清 `gre-*` 口径，避免"IPv6 已禁用但地址还挂着"的幽灵配置）**；`undo ipv6 address` → 清 `interface:<if>:ipv6-address`；系统视图 `undo ipv6` → **只清全局 `ipv6:` 前缀键（C6 拍板，已确认）**：`ipv6:enabled` + `ipv6:route-static:` 全部键，**不清接口 `:ipv6-*` 键**（真机 `undo ipv6` 仅关全局能力，接口配置保留）；系统视图 `undo ipv6 route-static <prefix>` → **级联清 `ipv6:route-static:<prefix>:` 精确前缀全部键**（多下一跳形态）。🔴 **接口级 undo 必须走 `applyUndoSystemFeature` / 接口 undo 的 handled 模式**（复用 `parser.go:5053` 的 `applyUndoSystemFeature` 与 `parser.go:828+` handled 钩子范式），未命中时交回既有分支，**零回归**。
- **[P0-11 能力矩阵与分支内守卫]**：`capabilities.go:129` 已有 `"ipv6": hostsAndL3()`，**本期保持零改动**。但 `ipv6 address` / `ipv6 route-static` 等**配置命令**须在**分支内部**守卫设备类型：PC / Server / 二层 Switch 上执行 → 复用 `l3Devices()`（`capabilities.go:174-181`，Router / L3Switch / Firewall / VTEP，**严禁重定义**）拒绝，口径完全对齐 GRE / AAA 拍板。`display ipv6 ...` 为**只读命令、任意设备可读**，空态放行输出 `Info:`。新顶层 token `ripng` / `ospfv3` 不在矩阵 → `isCommandSupported` 默认放行 → 分支内守卫（P0-13/P0-14）。
- **[P0-12 `display current-configuration` 新增 IPv6 块 + save→reload 贯通]**：新增 `buildSavedIPv6InterfaceConfig(state, iface)`（接口块内输出 ` ipv6 enable` / ` ipv6 address <addr>/<prefix>`，挂 `parser.go:5458+` 接口块循环）与系统级 `buildSavedIPv6RouteConfig(state)`（输出 ` ipv6 route-static <prefix> <nexthop>`，挂系统级块区 `parser.go:5392-5395` 之后）。`ipv6:enabled` 已由 `formatProtocolBlocks`（`:5040-5042`）输出，**保留不改**。IPv6 键随既有 `SerializeToDeviceConfigData` ↔ `LoadFromDeviceConfigData` 自动往返（`parser.go:5206` / `:5237`，全量拷贝 `DeviceConfig`），**零新增持久化代码**。

**F. RIPng / OSPFv3（识别 + 存取 + display 占位，不仿真协议）**

- **[P0-13 RIPng 命令识别 + 配置存取 + display 诚实占位]**：仅实现命令识别与配置存取（**C7 拍板，已确认：按华为 VRP 真机形态**——系统视图 `ripng [<pid>]`，接口视图 `ripng <pid> enable`。🔴 **不加 Cisco 别名**（`ipv6 router rip` 是 Cisco IOS 语法，主理人已撤回转述；VRP 仿真器教 Cisco 语法是负资产，肌肉记忆必须可平移到真机，`ipv6 router rip` 输入 → `Error: unrecognized command`））：
  - 配置键（预对齐）：`ipv6:ripng:<pid>:enabled`（全局进程）+ `interface:<if>:ripng-<pid>-enable`（接口使能）；
  - `display ripng [<pid>]` → 配置态字段（进程号、使能状态、接口列表）**真实**；路由学习计数、邻居数、会话状态**恒 `-`** + `ipv6SimNote()`；
  - **不仿真** RIPng 路由学习/定时器/度量/毒性反转等任何协议状态机（§7）。
- **[P0-14 OSPFv3 命令识别 + 配置存取 + display 诚实占位]**：仅实现命令识别与配置存取（**C8 拍板，已确认：按华为 VRP 真机形态**——系统视图 `ospfv3 [<pid>]`，接口视图 `ospfv3 <pid> area <area-id>`，**接口裸 `ospfv3` 不合法**，必须带 pid + area）：
  - 配置键（预对齐）：`ipv6:ospfv3:<pid>:enabled` + `interface:<if>:ospfv3-<pid>-area`；
  - `display ospfv3 [<pid>]` → 配置态字段（进程号、Router-ID 配置态、接口/area 列表）**真实**；邻居表、LSDB、LSA 计数、SPF 运行次数**恒 `-`** + `ipv6SimNote()`；
  - **不仿真** OSPFv3 邻居状态机 / LSA 泛洪 / SPF 计算（§7）。

### P1（增强真实语义 · 建议默认纳入）

- **[P1-1 `display ipv6 routing-table <prefix>` 目标过滤]**：对齐 IPv4 `formatRoutingTable(state, targetIP, verbose)`（`tools.go:406`），支持按目标前缀过滤；过滤逻辑用 `NetworkFromPrefix` 纯函数判定命中。
- **[P1-2 `display ipv6 interface brief` 增加已使能未配地址接口的显示]**：接口已 `ipv6 enable` 但未配地址 → 简表仍列出该接口（IPv6 Address 列 `-`），并统计 `IPv6 enabled interfaces` 数量行（PM 建议），让学员看到"使能了但没有地址"的中间态。
- **[P1-3 `uniqueLocal`（`fc00::/7`）与 `anycast` 地址类型判定]**：扩展 `IPv6AddressType`，纳入 ULA 与任播（任播在 VRP 中通过 `ipv6 address <addr> anycast` 配置，本期仅类型判定、不实现配置命令）。
- **[P1-4 Joined group address(es) 渲染]**：`display ipv6 interface <if>` 渲染协议固定组播组（`ff02::1` all-nodes、`ff02::2` all-routers、`ff02::1:ffXX:XXXX` solicited-node 由地址纯函数派生）——这些是**协议常量 + 地址推导**，非运行态观察，可真实渲染（C4 拍板）。
- **[P1-5 多 IPv6 地址支持]**：真机接口可配多个全局地址。v1 单地址（`ipv6-address` 键），P1 支持 `interface:<if>:ipv6-address-2` 等多键 + `display` 多行渲染。**键形态以架构师设计为准**。
- **[P1-6 `ipv6 route-static` 三段式 `<address> <len> <nexthop>`]**：VRP 真机另一形态（**C2 拍板，已确认**），与 CIDR 一段式并存。
- **[P1-7 `display ipv6 routing-table` verbose 模式]**：对齐 IPv4 verbose（Preference / Cost / Flags / Age / Source 列），Age 运行态恒 `-`。
- **[P1-8 系统级 undo 细化]**：`undo ipv6 route-static` 无参 → 级联清全部 `ipv6:route-static:` 精确前缀键（枚举式，C5 复核）。

### P2（边界收敛 / 诚实边界 / 可选增强）

- **[P2-1 `ipv6 route-static` 多下一跳（ECMP）命令面]**：**键形态已在 P0 定多键（C2 拍板）**，本条仅补**命令面追加语义**：同前缀不同下一跳重复配置 → 追加第二键；`display ipv6 routing-table` 同前缀多行渲染；`undo ipv6 route-static <prefix> <nexthop>` 精确清单键（P0 已有级联清全前缀语义，本条细化单键 undo）。
- **[P2-2 接口 `ipv6 address <addr> anycast` / `<addr> link-local` 显式配置]**：任播 / 显式链路本地地址配置命令。本期**不实现**（`Error: unrecognized command`）。
- **[P2-3 `ipv6 route-static <prefix> <nexthop> preference <n>` / `tag` 扩展]**：静态路由属性扩展，本期不实现。
- **[P2-4 `display ipv6 neighbors`（ND 邻居表）占位展示]**：**C10 拍板，已确认：本期不做**，留 P2 候选（若未来做，输出表头 + 全部 `-` + 诚实注记）。
- **[P2-5 前端无变更]**：IPv6 仅在 CLI 文本体现，**不新增 API 字段、不做 UI**（与 NAT / 端口安全 / GRE / AAA 一致）。

---

## 4. UI / 交互设计稿（CLI 回显与 display 输出，纯文本）

> 本节为 **display 输出的唯一权威源**（沿用 GRE / AAA 那轮「display 渲染标签/列宽以 PRD §4 为准，设计不另定列宽」的团队约定）。工程师严格照样例实现，测试据此写子串断言。

### 4.1 配置命令序列回显（课程 43 主线操作流）

```
<R1> system-view
[R1] ipv6
IPv6 enabled
[R1] interface GigabitEthernet0/0/0
[R1-GigabitEthernet0/0/0] ipv6 enable
IPv6 is enabled on GigabitEthernet0/0/0
[R1-GigabitEthernet0/0/0] ipv6 address 2001:db8::1/64
IPv6 address 2001:db8::1/64 configured on GigabitEthernet0/0/0
[R1-GigabitEthernet0/0/0] quit
[R1] ipv6 route-static 2001:db8:2::/64 2001:db8:1::2
Static route added
[R1]
```

> VRP 风格：配置成功**静默或规范短回显**，失败才 `Error:`。**不得出现自造欢快文案**（现状 `IPv6 enabled` / `IPv6 configuration` 语义含糊，本期改为规范化短回显）。

**典型拒错回显（`Error:` 硬拒绝，且不写任何键）**：

```
[R1] ipv6 garbage
Error: unrecognized command                                        ← P0-1（现状"任意串都成功"）

[R1] ipv6 enable
Error: Please run 'ipv6' in system view to enable IPv6 globally, or 'ipv6 enable' in interface view.
                                                                    ← P0-1

[R1] ipv6 address 2001:db8::1/64
Error: must be in interface view                                   ← P0-2

[R1-GigabitEthernet0/0/0] ipv6 address 2001:db8::1/64
Error: Please run 'ipv6 enable' on GigabitEthernet0/0/0 first.     ← P0-2 前置条件（C1）

[R1-GigabitEthernet0/0/0] ipv6 address 2001:db8::gg/64
Error: Invalid IPv6 address 2001:db8::gg                          ← P0-3

[R1-GigabitEthernet0/0/0] ipv6 address 2001:db8::1/129
Error: Invalid IPv6 prefix length 129 (0-128)                     ← P0-3

[R1] ipv6 route-static 2001:db8::zz/64 2001:db8::1
Error: Invalid IPv6 prefix 2001:db8::zz/64                        ← P0-8

[R1] ipv6 route-static 2001:db8:2::/64 2001:db8::zz
Error: Invalid IPv6 address 2001:db8::zz                          ← P0-8
```

### 4.2 `display ipv6 interface brief`

```
*down: administratively down
^down: standby
(l): loopback
(s): spoofing
Interface           Physical  Protocol  IPv6 Address
GigabitEthernet0/0/0 up       -         2001:db8::1/64
GigabitEthernet0/0/1 up       -         -
（IPv6 为静态配置模拟（lite 引擎），无真实 ND 邻居发现 / DAD / 动态路由学习，链路本地与运行态字段不可用）
```

**字段真实性标注表**（架构师据此实现，测试据此断言）：

| 字段 | 数据来源 | 真实性 | 未配置时 |
|---|---|---|---|
| `Interface` | `interface:<if>:*` 键名解析 | **真实**（配置态） | 该接口不列出 |
| `Physical` | `interface:<if>:status`（管理态） | **真实**（配置态） | `up`（缺省） |
| `Protocol` | — | 🔴 **诚实占位 `-`**（IPv6 协议状态是运行态） | `-` |
| `IPv6 Address` | `interface:<if>:ipv6-address` | **真实**（配置态，规范化） | `-` |

> 🔴 = 仿真环境无真实数据源，**恒为 `-`，严禁编造数字或伪状态**。

### 4.3 `display ipv6 interface GigabitEthernet0/0/0`

```
GigabitEthernet0/0/0 current state : up
Line protocol current state : -
IPv6 is enable, link-local address is -
  Global unicast address(es):
    2001:db8::1, subnet is 2001:db8::/64
  Joined group address(es):
    -
  MTU is 1500 bytes
  ND DAD is enabled, number of DAD attempts : -
  ND reachable time : - (ms)
  ND retransmit interval : - (ms)
  IPv6 Packet statistics:
    InReceives: -    InErrors: -    InDiscards: -
    OutRequests: -   OutDiscards: -
（IPv6 为静态配置模拟（lite 引擎），无真实 ND 邻居发现 / DAD / 动态路由学习，链路本地与运行态字段不可用）
```

- `current state` / `Physical` 读 `interface:<if>:status`（真实管理态）；`Line protocol` / ND / DAD / 统计恒 `-`。
- `link-local address`：**C3 拍板（已确认）**——接口有真实 MAC 键（`interface:<if>:mac`）→ 显示 `fe80::<EUI64>`（真实计算，非伪造）；无 MAC → `-`（诚实占位，**严禁伪造、严禁确定性伪 MAC**）。
- `Global unicast address(es)`：真实（已配地址 + `NetworkFromPrefix` 推导 subnet）。
- `Joined group address(es)`：**C4 拍板（已确认）**——P0 恒 `-`，P1 渲染协议常量 + solicited-node 推导。
- 接口不存在 → `Error: invalid interface '<if>'`。

### 4.4 `display ipv6 routing-table`

```
Route Flags: R - relay, D - download to fib
Routing Table : Public
         Destinations : 2        Routes : 2

Destination  : 2001:db8::                              PrefixLength : 64
NextHop      : 2001:db8::1                            Preference   : 0
Cost         : 0                                      Protocol     : Direct
RelayNextHop : -                                      TunnelID     : -
Interface    : GigabitEthernet0/0/0

Destination  : 2001:db8:2::                            PrefixLength : 64
NextHop      : 2001:db8:1::2                          Preference   : 60
Cost         : 0                                      Protocol     : Static
RelayNextHop : -                                      TunnelID     : -
Interface    : NULL0
（IPv6 为静态配置模拟（lite 引擎），无真实 ND 邻居发现 / DAD / 动态路由学习，链路本地与运行态字段不可用）
```

**字段真实性标注表**：

| 字段 | 数据来源 | 真实性 | 说明 |
|---|---|---|---|
| `Destination` / `PrefixLength` | `ipv6:route-static:<prefix>:<nexthop>` 键双段解析 / 接口地址推导 | **真实**（配置态） | 前缀升序 |
| `NextHop` | 静态路由键第二段 / 接口地址 | **真实**（配置态） | Direct 的 NextHop = 接口地址 |
| `Protocol` | Static / Direct | **真实**（配置态） | 无动态协议条目 |
| `Preference` | Static=60 / Direct=0 | **真实**（常量） | 对齐 IPv4 |
| `RelayNextHop` / `TunnelID` | — | 🔴 **诚实占位 `-`** | — |
| 动态学习条目 | — | 🔴 **不出现** | 由 `ipv6SimNote()` 说明 |

### 4.5 `display current-configuration` 中的 IPv6 块（P0-12）

```
#
ipv6
#
interface GigabitEthernet0/0/0
 ip address 10.0.0.1 255.255.255.0
 ipv6 enable
 ipv6 address 2001:db8::1/64
#
ipv6 route-static 2001:db8:2::/64 2001:db8:1::2
#
```

> 输出顺序：全局 `ipv6` 由既有 `formatProtocolBlocks` 输出（`:5040-5042`，保留不改）；接口 `ipv6 enable` / `ipv6 address` 挂接口块循环（`parser.go:5458+`）；`ipv6 route-static` 挂系统级块区（`parser.go:5392-5395` 之后）。**缺省值不冗余输出**（`ipv6 enable` 是显式配置、必须输出；`ipv6` 全局使能同理）。快照**不可回灌**，与既有 STP / GRE / AAA 快照定位一致。

### 4.6 前端

**本期无变更**。IPv6 仅在 CLI 终端文本体现（P2-5）。

---

## 5. 验收标准（AC1–AC13，每条可用自动化测试证明，非恒真断言）

- **AC1（全局使能命令语义修正 · P0-1）**：① 系统视图裸 `ipv6` → 断言 `DeviceConfig["ipv6:enabled"]=="true"` 且回显含 `IPv6 enabled`；② 系统视图 `ipv6 garbage` / `ipv6 foo` → 断言返回含 `unrecognized command` 的 `Error:`，**且断言 `ipv6:enabled` 键未被写入**（现状"任意串都成功"，本条直击缺陷）；③ 系统视图 `ipv6 enable` → 断言返回含 `Please run 'ipv6'` 引导文案；④ 系统视图 `ipv6 address 2001:db8::1/64` → 断言返回含 `must be in interface view`（**不再被全局使能分支吞掉**）。

- **AC2（接口视图使能 + 地址配置与前置条件 · P0-2）**：① 接口视图裸 `ipv6` → `unrecognized command`；② `ipv6 enable` → 断言 `DeviceConfig["interface:<if>:ipv6-enable"]=="true"`；③ 未使能时 `ipv6 address 2001:db8::1/64` → 断言返回含 `Please run 'ipv6 enable'` 的 `Error:`（**C1 拍板，硬前置**），**且 `interface:<if>:ipv6-address` 键未写入**；④ 使能后 `ipv6 address 2001:db8::1/64` → 断言键值 == `"2001:db8::1/64"`（**规范化缩写存储**）；⑤ 非接口视图（系统 / 用户）执行 `ipv6 enable` → `must be in interface view`。

- **AC3（纯函数 golden 断言 · P0-3，本期最大可单测资产）**：逐条断言（**不得用「返回非空」这类恒真断言**）：
  - `ValidateIPv6Address("2001:db8::1")==nil`；`ValidateIPv6Address("2001:db8::gg")!=nil`；`ValidateIPv6Address("2001:db8::1%eth0")!=nil`（拒绝 zone）；
  - `ValidateIPv6Prefix("2001:db8::1/64")==nil`；`("2001:db8::1/129")!=nil`；`("2001:db8::1")!=nil`（缺前缀）；
  - `CompressIPv6("2001:0db8:0000:0000:0000:0000:0000:0001")=="2001:db8::1"`；`CompressIPv6("2001:db8:0:0:0:0:0:1")=="2001:db8::1"`（**`::` 仅一次**）；`CompressIPv6("2001:db8::1")=="2001:db8::1"`（幂等）；
  - `ExpandIPv6("2001:db8::1")=="2001:0db8:0000:0000:0000:0000:0000:0001"`；
  - `IPv6AddressType("fe80::1")==linkLocal`、`("ff02::1")==multicast`、`("::1")==loopback`、`("::")==unspecified`、`("2001:db8::1")==globalUnicast`（`fc00::/7` → uniqueLocal 为 P1，本期断言 `fc00::1` 不 panic 且归类合理）；
  - `EUI64InterfaceID("00e0-fc12-0aaa")=="02e0:fcff:fe12:0aaa"`（插入 ff:fe + 翻转 U/L 位：`00`→`02`）；**C9 拍板（已确认）补充无分隔输入**：`EUI64InterfaceID("00e0fc120aaa")=="02e0:fcff:fe12:0aaa"`（大小写不敏感：`EUI64InterfaceID("00E0-FC12-0AAA")` 同结果）；
  - `NetworkFromPrefix("2001:db8::1/64")=="2001:db8::"`。
  > 🔴 **键碰撞专项在 AC12**。

- **AC4（`display ipv6 interface brief` 忠实 + 确定性 · P0-5）**：① 配 2 个接口（GE0/0/0 有地址、GE0/0/1 已使能未配地址）→ 输出 2 行数据行，地址列分别为 `2001:db8::1/64` 与 `-`，**接口按名称升序**；② **`Protocol` 列恒 `-`**（正则断言 `Protocol` 表头下所有数据行 Protocol 字段不匹配 `up|down`）；③ 空态断言 `No IPv6 address configured`；④ **同一状态连续调用 `display ipv6 interface brief` 10 次，输出字节级完全一致**（确定性）；⑤ 输出末尾含 `ipv6SimNote()` 的「无真实 ND 邻居发现」注记。

- **AC5（`display ipv6 interface <if>` 详情 · P0-6）**：① 配地址后输出含 `current state : up`（读 `:status`）、`2001:db8::1, subnet is 2001:db8::/64`、`IPv6 is enable`；② **`Line protocol current state` / `number of DAD attempts` / `ND reachable time` / `InReceives` 等运行态字段值恒 `-`**（正则断言不匹配 `\d+`）；③ 未使能接口 → 输出含 `IPv6 is not enable` 或对应诚实态；④ `display ipv6 interface nosuch` → 断言返回含 `invalid interface` 的 `Error:`；⑤ 末尾含注记；⑥ `link-local address` 按 **C3 拍板（已确认）** 双分支断言：接口**无** `interface:<if>:mac` 键 → 恒 `-`，正则断言输出**不匹配 `fe80::[0-9a-f]` 伪地址**；接口**有**真实 MAC 键（如 `00e0-fc12-0aaa`）→ 输出匹配 `fe80::02e0:fcff:fe12:0aaa`（EUI-64 真实计算，与 AC3 `EUI64InterfaceID` 同源断言）。

- **AC6（`ipv6 route-static` 校验与存取 · P0-8，C2 多键形态）**：① 合法配置 → 断言 `DeviceConfig["ipv6:route-static:2001:db8:2::/64:2001:db8:1::2"]=="true"`（**C2 拍板多键形态：前缀 + 下一跳双段键**）；② 非法前缀 / 非法下一跳 → 断言返回对应 `Error:`，**且断言无任何 `ipv6:route-static:` 键被写入**；③ 同前缀同下一跳重复配置 → **幂等**（键仍存在、值仍 `true`，不报错不覆盖）；④ `undo ipv6 route-static 2001:db8:2::/64` → 断言 `ipv6:route-static:2001:db8:2::/64:` 精确前缀**全部键被清除而非留空串**（`_, ok := DeviceConfig[k]; ok == false`，多下一跳级联）；⑤ 系统视图外执行 → 视图报错。

- **AC7（`display ipv6 routing-table` · P0-9）**：① 配 1 条静态 + 1 个接口地址 → 输出 **2 条数据块**（Static 与 Direct 各一），Protocol 字段分别为 `Static` / `Direct`，Preference 分别为 `60` / `0`；② **不出现任何动态协议条目**（断言输出不匹配 `RIPng|OSPFv3|BGP` 的 Protocol 行）；③ `RelayNextHop` / `TunnelID` 恒 `-`；④ 空表 → `Info: No IPv6 route.`；⑤ **连续调用 10 次字节级一致**（确定性，禁止 map 随机遍历）；⑥ 目标前缀按升序输出（`2001:db8::` 在 `2001:db8:2::` 之前）。

- **AC8（save → reload 持久化贯通 · P0-12，本期最大价值点）**：完成 AC2/AC6 配置后执行 `save`，经 `SerializeToDeviceConfigData` → `LoadFromDeviceConfigData` 往返，reload 后断言：① `DeviceConfig` 中 `ipv6:` / `interface:<if>:ipv6-` 精确前缀键集与 reload 前**逐键完全一致**；② `display ipv6 interface brief` / `display ipv6 routing-table` 完整复现；③ **`display current-configuration` 复现 §4.5 全部 IPv6 行**且**两次快照字节级一致**；④ **补一条对照断言：改造前该场景 reload 后接口地址与静态路由全部丢失**（证明缺陷确被修复）。

- **AC9（诚实占位 · CRITICAL 红线 · P0-7）**：lite 引擎下 `display ipv6 interface` / `display ipv6 interface brief` / `display ipv6 routing-table` 输出**均含** `ipv6SimNote()` 注记；用**正则断言输出中不存在任何伪造运行态数字**——具体：`Line protocol` / `DAD attempts` / `reachable time` / `retransmit interval` / `InReceives` / `OutRequests` / `RelayNextHop` / `TunnelID` 的值**必须恒为 `-`**，断言其**不匹配** `\d+`；**link-local address 在接口无真实 MAC 键时恒为 `-`，断言不匹配 `fe80::[0-9a-fA-F:]+`**（防伪造地址；**有真实 MAC 键时按 C3 真实计算显示，属例外**）；**该 AC 失败即视为违反项目核心价值观，不得以「观感更好」为由放行。**

- **AC10（`undo` 语义完整 · P0-10）**：① `undo ipv6 address` → 清 `interface:<if>:ipv6-address` 键；② `undo ipv6 enable` → 清 `interface:<if>:ipv6-enable` 键**并级联清 `interface:<if>:ipv6-address`（C5 拍板，已确认）**，且其它接口键完好；③ `undo ipv6`（系统）→ 清 `ipv6:enabled` 与 `ipv6:route-static:` 全部键，**断言 `interface:<if>:ipv6-*` 键完好（C6 拍板，已确认：仅清全局 `ipv6:` 前缀）**；④ `undo ipv6 route-static <prefix>` → 清 `ipv6:route-static:<prefix>:` 精确前缀全部键且其它路由键完好；⑤ **断言既有 `undo` 分支（接口视图 GRE / LAG / VRRP、系统视图各协议）行为逐字不变**（零回归）。

- **AC11（能力守卫 · P0-11）**：
  - **AC11a（配置命令按设备类型守卫）**：PC / Server / 二层 Switch 上执行 `ipv6 address 2001:db8::1/64`（接口视图）、`ipv6 route-static 2001:db8:2::/64 2001:db8::1` → **拒绝**（设备集 = `l3Devices()`，复用 `capabilities.go:174-181`，**不新增不重定义**）；Router / L3Switch / Firewall / VTEP 正常放行。
  - **AC11b（display 只读、任意设备可读）**：PC / Server 上执行 `display ipv6 interface brief` / `display ipv6 routing-table` **不得返回能力拒绝**，应放行并输出空态 `Info:`；断言输出**不含** `is not supported on`。
  - **AC11c（零回归）**：断言 `capabilities.go` **零改动**（`"ipv6": hostsAndL3()` 保持原样，且**未新增** `ripng` / `ospfv3` 矩阵行——它们由分支内守卫处理）。

- **AC12（键碰撞专项 · P0-4，本期最高危项）**：构造同时存在
  `interface:GigabitEthernet0/0/1:ip`（**IPv4 键，含 `:ip` 子串，仓库实存写法见 `parser.go:516`**）、
  `interface:GigabitEthernet0/0/1:ipv6-address`（IPv6 键）、
  `ipv6:enabled`（全局键）、
  `ipv6:route-static:2001:db8:2::/64:2001:db8:1::2`（**C2 多键形态静态路由键，键内含多个 `:` 与 `/`**）、
  `interface:Bridge-Aggregation1:lag:mode`（**异族键，含 `gre` 历史教训**）
  的状态，断言 ① IPv6 接口扫描**只命中 `ipv6-address`**，IPv4 接口扫描**只命中 `ip`**，互不误判（`collectIPv6Interfaces` 返回的接口地址键集不含 IPv4 键，反之亦然）；② **静态路由前缀扫描按 `ipv6:route-static:` 精确前缀 + 双段解析，正确解析出 `prefix=2001:db8:2::/64`、`nexthop=2001:db8:1::2`，且不会把 `ipv6:enabled` 或 `interface:...:ipv6-address` 误判为路由键**（多键形态前缀扫描专项，C2）；③ `undo ipv6` 级联清理后，**`interface:...:ip`、`ipv6:enabled`、`interface:Bridge-Aggregation1:lag:mode` 完好无损**（全局 `ipv6:` 前缀清理不得波及接口键与异族键）；④ 静态断言 `internal/cli/ipv6_*.go` 中 **`strings.Contains(k, "ip")` 与 `strings.Contains(k, "ipv6")` 零命中**（`grep` 断言）；⑤ 静态断言 `state.go` **无任何 IPv6 内嵌结构体**（`grep -n "IPv6\|Ipv6" internal/cli/state.go` 无命中，对照 GRE / AAA 口径）。

- **AC13（纯函数无副作用 / 架构基线合规 + RIPng/OSPFv3 识别存取 · P0-3/P0-13/P0-14）**：
  - `ValidateIPv6Address` / `CompressIPv6` / `ExpandIPv6` / `IPv6AddressType` / `EUI64InterfaceID` / `NetworkFromPrefix` / `ipv6SimNote` 单测证明——不修改 `sim` 引擎、不写 `state`、**不 import `internal/protocol`**、零新增第三方依赖、连续两次调用结果一致且**不改写任何 `DeviceConfig` 键**（调用前后对 `DeviceConfig` 做 deep-equal 断言）。
  - **RIPng（C7 拍板，已确认，按华为 VRP 真机形态）**：系统视图 `ripng 1` → 断言 `ipv6:ripng:1:enabled` 键写入；接口视图 `ripng 1 enable` → 断言 `interface:<if>:ripng-1-enable` 键写入；`display ripng 1` → 配置态字段真实、**邻居数/路由计数恒 `-`** + 注记；🔴 **`ipv6 router rip`（Cisco 语法）→ 断言返回 `Error: unrecognized command`，且不写任何键**（不加 Cisco 别名，主理人修正）。
  - **OSPFv3（C8 拍板，已确认，按华为 VRP 真机形态）**：系统视图 `ospfv3 1` → 断言 `ipv6:ospfv3:1:enabled` 键写入；接口视图 `ospfv3 1 area 0` → 断言 `interface:<if>:ospfv3-1-area` 键写入；**接口视图裸 `ospfv3` → 断言返回 usage / `unrecognized command` 类 `Error:` 且不写键**（接口裸 `ospfv3` 不合法，必须带 pid + area）；`display ospfv3 1` → 配置态字段真实、**邻居/LSA 计数恒 `-`** + 注记。
  - 静态断言 `ipv6_*.go` 中**无任何协议状态机逻辑**（`grep -n "neighbor\|LSA\|SPF\|metric\|hop count" internal/cli/ipv6_*.go` 仅命中占位文案与注释，不命中计算逻辑——以架构师设计为准复核）。

---

## 6. 约束与红线（项目铁律，实现与评审一律以本节为准）

> 本节为**不可协商项**。任何设计 / 实现 / QA 结论与本节冲突时，以本节为准。

1. 🔴 **诚实占位原则（最重要，信誉红线）**：本项目 Windows 侧跑的是 **lite 仿真引擎，不做真实数据平面**。凡是无法真实产生的运行时数据——**链路本地地址（无真实 MAC 时）、ND 邻居表、DAD 尝试次数、可达/重传定时器、IPv6 报文统计、动态路由学习条目、RIPng/OSPFv3 邻居与会话**——**一律输出 `-` 或明确占位文案，严禁编造随机数字、严禁伪造 `fe80::xxxx` 假地址、严禁伪造 `up` 协议状态、严禁输出 `time.Now()` 派生的假时间**，并在 `display` 输出末尾追加 `ipv6SimNote()` 诚实提示（参考 `greSimNote()` / `aaaSimNote()` 写法）。
2. **纯函数评估**：核心判定逻辑（地址/前缀校验、压缩、展开、类型、EUI-64、网络推导）必须是**无副作用纯函数**（`internal/cli/ipv6_eval.go`），不写 `state`、不碰 `sim` 引擎实例、不 import `internal/protocol`、零新增第三方依赖（**用 `net/netip` 标准库**），便于单测。副作用集中在 `ipv6_cmd.go` 单一入口。
3. **配置态单一事实源**：所有配置落在设备的 `DeviceConfig` 键值里（`ipv6:` / `interface:<if>:ipv6-` / `ipv6:route-static:` 精确前缀），**不新增散落的 struct 字段**。**严禁在 `CLIState` 上新增任何 IPv6 内嵌结构体**（对照 GRE 删 `state.GRE`、AAA 删 `state.LocalUsers` 口径）。**特别提示：IPv4 `state.Routes` 是遗留结构体事实源（`state.go:65`），IPv6 不得复制该模式**。静态路由**多键形态**（`ipv6:route-static:<prefix>:<nexthop>` = `true`，C2 拍板）必须从 P0 保持到 P2，不得中途改键。
4. 🔴 **键碰撞红线**：读取配置键必须用**精确前缀 + 精确分段匹配**，**严禁 `strings.Contains(k, "ip")` / `strings.Contains(k, "ipv6")`**——`interface:<if>:ip`（IPv4）与 `interface:<if>:ipv6-address`（IPv6）**共享 `:ip` 子串**，模糊匹配会把 IPv4 键误判为 IPv6（幽灵地址），级联清理会**误删 IPv4 配置**；`Contains(k, "ipv6")` 会把全局 `ipv6:` 与接口 `:ipv6-*` 混为一谈。（历史教训：`gre` 误伤 `Bridge-Aggregation`、`aaa` 误伤 `00e0-fc12-0aaa`；本期 `:ip` 风险与这两次同源同险。）
5. **save → reload 一致性**：配置 `save` 后重新加载，`display current-configuration` 必须**字节级还原** IPv6 块；接口地址与静态路由必须随 `DeviceConfig` 全量拷贝自动往返（`parser.go:5206` / `:5237`）。
6. **零回归底线**：不得改坏既有 `ip` / `display ip routing-table` / `ip route-static` / GRE / AAA / VRRP / LAG / STP / DHCP 中继任何行为；`capabilities.go` 本期**零改动**；`formatProtocolBlocks` 的 `ipv6 enable` 输出**保留不改**。
7. **构建入口**：Windows `./build.ps1`，其余 `make build`（**禁止直接 go build**，否则版本信息不注入、二进制自报 stale=true）。验证步骤：在 `internal/cli` 跑 `go test ./internal/cli/ -run 'IPv6|Ipv6' -v`（Windows 亦可用 `go test`，构建用 build 入口即可）。
8. **RIPng/OSPFv3 边界**：本期**只做命令识别 + 配置存取 + display 诚实占位**，**不仿真任何协议状态机**（无路由学习、无邻居、无 LSA、无 SPF、无定时器）。命令形态**按华为 VRP 真机**（系统 `ripng [<pid>]` / `ospfv3 [<pid>]`，接口 `ripng <pid> enable` / `ospfv3 <pid> area <area-id>`），**严禁加 Cisco 别名**（`ipv6 router rip` → `Error: unrecognized command`；C7 主理人修正）。任何协议逻辑都属 out-of-scope（§7）。

---

## 7. 明确的非目标（Out of Scope）

- **真实 IPv6 协议栈**：不实现 ND（邻居发现）/ NDP、DAD（重复地址检测）、地址自动配置（SLAAC）、ICMPv6、PMTUD；
- **链路本地地址自动生成**：不模拟"使能即自动生成 fe80::/10"；**接口有真实 MAC 键时按 EUI-64 真实计算显示（C3 拍板，真实推导非伪造），无 MAC 键时显示 `-` + 注记，严禁任何确定性伪 MAC**；
- **动态路由协议状态机**：RIPng 路由学习/度量/毒性反转/定时器、OSPFv3 邻居状态机/LSA 泛洪/SPF 计算/DR 选举——全部不实现（§6.8）；
- **`ipv6 address <addr> anycast` / `link-local` 显式配置**（P2-2）；
- **`ipv6 route-static` preference / tag**（P2-3）；**多下一跳 ECMP 命令面**（P2-1，键形态 P0 已定多键，仅命令面追加语义后置）；
- **`display ipv6 neighbors` / `display ipv6 statistics` 真实实现**（C10 拍板：本期不做，留 P2 候选）；
- **IPv6 转发 / ping ipv6 / traceroute ipv6**：无数据平面，不实现；
- **前端图形化 IPv6 配置 UI / 新增 API 字段**（P2-5）；
- **重写 IPv4 `state.Routes` 遗留结构体**（独立技术债，建议另开工单按 DeviceConfig 范式整改，本期不动）；
- **重写 NAT / 端口安全 / VRRP / STP / LAG / DHCP 中继 / GRE / AAA**（仅 IPv6 增量）。

---

## 8. 待确认问题（已全部拍板，结论已回填）

> 沿用 GRE / AAA 那轮的拍板模式：每项给候选方案 + PM 建议 + 影响面。**C1–C10 已于 2026-08-09 由主理人全部拍板，逐项回填 ✅ 结论于下，实现与验收一律以拍板为准**。其中 **C7 为主理人修正撤回**（撤回 Cisco 转述 `ipv6 router rip`，改按华为 VRP 真机）。

- **C1（决定 P0-2 与 AC2 ③）：`ipv6 address` 是否要求先 `ipv6 enable`？**
  - **(a) 硬前置（PM 建议）**：未 `ipv6 enable` 配地址 → `Error: Please run 'ipv6 enable' on <if> first.`。理由——与 GRE「未 `tunnel-protocol gre` 配 `source` 硬拒绝」、AAA「先建方案后绑域」教学点同构，保留"使能 → 配地址"顺序认知；真机接口也使能后才配地址。
  - (b) 隐式自动使能：配地址时自动写 `:ipv6-enable`。更省事，但丢掉教学点、与真机"使能是独立命令"的肌肉记忆有偏差。
  - ✅ **拍板结论（2026-08-09 主理人）：(a) 硬前置**。`ipv6 address` 必须先在接口 `ipv6 enable`，否则 `Error: Please run 'ipv6 enable' on <if> first.` 且不写键。AC2 ③ 按此断言。

- **C2（决定 P0-8 与 AC6 断言面）：`ipv6 route-static` 命令形态？**
  - **(a) CIDR 一段式 `<prefix>/<len> <nexthop>`（PM 建议 P0）**：与主理人转述范围一致、实现最简；三段式 `<address> <len> <nexthop>` 作 P1-6。
  - (b) 两形态都支持（P0 就做）：更贴近真机，但 AC 面扩大约 30%。
  - ✅ **拍板结论（2026-08-09 主理人）：(a) 命令形态 CIDR 一段式 P0 + 三段式 P1-6**；**键形态 P0 即定多键 `ipv6:route-static:<prefix>:<nexthop>` = `true`**（ECMP 前瞻，避免 P2 键不兼容）。AC6 / AC12 ② 按多键形态断言。

- **C3（决定 P0-6 / AC5 ⑥ / AC9 断言面）：link-local address 如何呈现？**
  - **(a) 无真实 MAC → 恒 `-`（PM 强烈建议）**：本工具接口多无真实 MAC 键（`interface:<if>:mac` 仅主机路径 `parser.go:473` 写入），伪造 `fe80::` 违反诚实红线；EUI-64 纯函数仅作教学工具单测。
  - (b) 用确定性伪 MAC 推导：观感更好，但是**编造数据**，与"严禁伪造 `Status: up`"同红线。
  - ✅ **拍板结论（2026-08-09 主理人）：(a) 采纳并增强**——接口**有真实 MAC 键（`interface:<if>:mac`）→ 用 EUI-64 真实计算显示**（真实推导非伪造）；无 MAC 键 → 恒 `-` + 注记；**严禁任何确定性伪 MAC**。AC5 ⑥ / AC9 按双分支断言。

- **C4（决定 P0-6 / P1-4 断言面）：`display ipv6 interface <if>` 的 `Joined group address(es)`？**
  - **(a) P0 恒 `-`，P1 渲染协议常量 + solicited-node 推导（PM 建议）**：协议常量（`ff02::1`/`ff02::2`）与地址推导（`ff02::1:ffXX:XXXX`）是**静态可计算**数据，不算编造，但为控制本期体量放 P1。
  - (b) P0 就渲染：教学价值高，但扩 AC 面。
  - ✅ **拍板结论（2026-08-09 主理人）：(a)**。P0 恒 `-`，P1 渲染协议常量 + solicited-node 推导。

- **C5（决定 P0-10 / AC10 ① 断言面）：`undo ipv6 enable` 是否级联清 `ipv6-address`？**
  - **(a) 级联清理（PM 建议）**：对齐 GRE `undo tunnel-protocol` 级联清 `gre-*`、DHCP 拍板 #3 口径，避免"IPv6 已禁用但地址还挂着"的幽灵配置。
  - (b) 仅清使能键，地址保留：真机 `undo ipv6 enable` 后地址配置仍保留（再 enable 即恢复），语义更"还原"，但 display 会出现"未使能但有地址"的中间态。
  - ✅ **拍板结论（2026-08-09 主理人）：(a) 级联清理**。`undo ipv6 enable` 级联清 `interface:<if>:ipv6-address`。AC10 ② 按此断言。

- **C6（决定 P0-10 / AC10 ③ 断言面）：系统视图 `undo ipv6` 的清理范围？**
  - **(a) 仅清全局 `ipv6:` 前缀（PM 建议）**：`ipv6:enabled` + `ipv6:route-static:*`，**不动接口 `:ipv6-*` 键**。理由——真机 `undo ipv6` 关闭全局能力，接口配置保留；且接口级清理应走 `undo ipv6 enable`（C5），职责清晰。
  - (b) 级联清全部（含接口）：实现简单但语义过重，可能误删用户想保留的接口配置。
  - ✅ **拍板结论（2026-08-09 主理人）：(a)**。系统 `undo ipv6` 仅清全局 `ipv6:` 前缀键（enabled + route-static:*），接口 `:ipv6-*` 键保留。AC10 ③ 按此断言。

- **C7（🔴 课程冲突 · 决定 P0-13 / AC13 RIPng 断言面）：RIPng 命令形态？**
  - 主理人转述课程 44 为 `ipv6 router rip` / `ripng enable`。**`ipv6 router rip` 是 Cisco IOS 语法，华为 VRP 真机为系统视图 `ripng [<process-id>]` 进进程视图 + 接口视图 `ripng <pid> enable`**。`docs/reference/huawei-vrp-course.md:48` 对课程 44 仅列 `ospfv3` 为关键命令，未给 RIPng 命令形态。
  - **(a) 按华为真机 `ripng [<pid>]` + 接口 `ripng <pid> enable`（PM 建议）**：肌肉记忆可平移到 eNSP / 真机。
  - (b) 按主理人转述 `ipv6 router rip`：需对照课程视频确认是否真机如此（若课程视频确为此形态，可作为识别别名）。
  - 🔴 **拍板结论（2026-08-09 主理人修正撤回）：(a) 按华为 VRP 真机**——系统视图 `ripng [<pid>]`，接口视图 `ripng <pid> enable`。**撤回此前转述的 `ipv6 router rip`（主理人确认那是 Cisco IOS 语法、记忆带错；课程文档 course 44 关键命令只列 `ospfv3`，未给 RIPng 形态）**。🔴 **不要加 Cisco 别名**——VRP 仿真器教 Cisco 语法是负资产，肌肉记忆必须可平移到真机；`ipv6 router rip` 输入 → `Error: unrecognized command`。AC13 RIPng 断言按此形态写。

- **C8（🔴 课程冲突 · 决定 P0-14 / AC13 OSPFv3 断言面）：OSPFv3 接口使能命令形态？**
  - 主理人转述课程 44 为「`ospfv3 [<pid>]` 进程 + 接口使能」。**华为 VRP 真机接口使能为 `ospfv3 <pid> area <area-id>`（必须带 area）**，`ospfv3` 裸命令在接口视图不合法。
  - **PM 建议按真机形态 `ospfv3 <pid> area <area-id>`**；若课程视频确为更简形态，请以视频为准。本期仅识别 + 存取 + 占位，冲突影响面可控。
  - ✅ **拍板结论（2026-08-09 主理人）：(a) 按华为 VRP 真机**——系统视图 `ospfv3 [<pid>]`，接口视图 `ospfv3 <pid> area <area-id>`（必带 area，接口裸 `ospfv3` 不合法）。AC13 OSPFv3 断言按此形态写。

- **C9（决定 P0-3 / AC3 断言面）：EUI-64 的 MAC 输入格式？**
  - PM 建议接受 `00e0-fc12-0aaa`（连字符）与 `00e0fc120aaa`（无分隔）两种，输出统一小写十六进制冒号分段（`02e0:fcff:fe12:0aaa`）；大小写不敏感。
  - ✅ **拍板结论（2026-08-09 主理人）：采纳**。MAC 输入接受 `00e0-fc12-0aaa` 与 `00e0fc120aaa` 两种格式，输出统一小写冒号分段，大小写不敏感。AC3 EUI-64 断言补充无分隔输入用例。

- **C10（决定 P2-4 是否纳入 / 占位分组形态）：`display ipv6 neighbors` 占位展示是否做？**
  - **(a) 本期不做（PM 建议）**：诚实占位收益最低（一个全 `-` 的表），且与 ND 无真实实现矛盾；纳入 P2 候选即可。
  - (b) 做占位分组（表头 + 全 `-` + 注记）：学员能看到"真机这里会有邻居表"。
  - ✅ **拍板结论（2026-08-09 主理人）：(a) 本期不做**，留 P2 候选。P2-4 按此标注。

---

## 附：关键 file:line 证据索引（供架构师直接定位，主理人可逐条 grep 验证）

**A. 本期重构对象（缺陷现状）**

- `internal/cli/parser.go:2120-2130` `case "ipv6"` —— **半截入口**：`:2121-2123` 系统视图**不校验参数**（`ipv6 任意串` → 写 `ipv6:enabled` + 回显 `IPv6 enabled`）；`:2124-2128` 接口视图 `ipv6 address <x>` **零校验**存 `interface:<if>:ipv6-address`；`:2130` 兜底 `return "IPv6 configuration"`（语义含糊）。
- `internal/cli/parser.go:5040-5042` `formatProtocolBlocks` —— `ipv6:enabled` 已输出 ` ipv6 enable`（**保留不改**，零回归）。
- `internal/cli/parser.go:5142-5144` `applyUndoSystemFeature` 的 `case "ipv6"` —— 只删 `ipv6:enabled`（P0-10 扩展为清 `ipv6:` 前缀，**不删接口键**）。
- `internal/cli/parser.go:555-571` `case "route-static"`（`ip` 子命令）—— **IPv4 遗留结构体事实源** `state.Routes`（`state.go:65`），**IPv6 不得复制**（§6.3）。
- `internal/cli/parser.go:2490+` `case "display", "dis"` —— **无 `case "ipv6"`**（`display ipv6 ...` 落入默认报错）；`:2657` 有 `case "ip"`（IPv4 display 范式，新增 `case "ipv6"` 参照此结构）。
- `internal/cli/parser.go:5458+` `buildSavedConfigSnapshot` 接口块循环 —— **无 IPv6 行**（P0-12 挂载点）；系统级块区 `:5392-5395`（STP）→ 之后挂 `ipv6 route-static` 块。
- `internal/cli/parser.go:516` / `:880` `interface:<if>:ip` —— **IPv4 键，含 `:ip` 子串**，是 AC12 键碰撞专项的对偶键。

**B. 本期复用基线（正面范式）**

- 🔴 **键碰撞证据（AC12 专项断言依据）**：`interface:<if>:ip`（IPv4，`parser.go:516`）与新增 `interface:<if>:ipv6-address` **共享 `:ip` 子串**——`strings.Contains(k, ":ip")` 会把两者同时命中（幽灵地址 / 误删 IPv4）。对照历史教训：`lag_eval.go:391-393` `Bridge-Aggregation` 含 `gre`（GRE 轮）、`p2_portsec_qa_t07_test.go:275` `00e0-fc12-0aaa` 含 `aaa`（AAA 轮）。
- `internal/cli/gre_eval.go:583-588` `greSimNote()`、`internal/cli/aaa_eval.go:593` `aaaSimNote()`（lite/full 两态，读 `sim.EngineModeName()`）—— `ipv6SimNote()` 照此实现（P0-7）。
- **三件套文件范式**：`gre_cmd.go`（副作用唯一入口 + 三态守卫顺序：视图 → 设备 → 前置条件）/ `gre_display.go`（display 渲染 + `buildSavedGRE*Config`）/ `gre_eval.go`（纯函数）—— **`ipv6_cmd.go` / `ipv6_display.go` / `ipv6_eval.go` 照此三分**（P0-3）。
- `internal/cli/tools.go:406` `formatRoutingTable`、`:337` `buildDirectRoutes` —— IPv4 路由表渲染范式，IPv6 `display ipv6 routing-table` 参照（但**不复制 `state.Routes` 结构体事实源**）。
- `internal/cli/parser.go:5206` `SerializeToDeviceConfigData`、`:5237` `LoadFromDeviceConfigData` —— **全量拷贝 `DeviceConfig`**（`:5219-5221`），IPv6 键自动往返，**零新增持久化代码**。
- `internal/cli/capabilities.go:129` `"ipv6": hostsAndL3()`（**本期零改动**）；`:141-152` `isCommandSupported` 未声明默认放行（分支内守卫依据）；`:174-181` `l3Devices()`（Router / L3Switch / Firewall / VTEP，**复用不重定义**）。
- 接口名规范化：`parseInterface` / `sortInterfaceNames`（`display interface brief` 共用，`parser.go:5872` 注释）—— `display ipv6 interface brief` 复用。
- 课程依据：`docs/reference/huawei-vrp-course.md:47` 第 43 讲「IPv6 基础」关键命令 `ipv6 enable`、`ipv6 address`；`:48` 第 44 讲「IPv6 路由基础」关键命令 `ospfv3`；`:86` 功能矩阵「IPv6 / OSPFv3｜`ipv6 address`、`ospfv3`｜📋 Roadmap」（本期交付后需同步更新为 ✅）。

---

## 文档状态

- **✅ 决策状态：C1–C10 已全部拍板，结论已回填 §8，PRD 可转架构设计**（2026-08-09 主理人拍板；其中 C7 为主理人修正撤回——撤回 Cisco 转述 `ipv6 router rip`，按华为 VRP 真机 `ripng [<pid>]` / `ripng <pid> enable` 且不加别名）。
- 基线核查完成：`ipv6` 半截 case（`parser.go:2120-2130`，系统视图不校验参数、接口视图零校验存串）、缺接口 `ipv6 enable`、缺 `display ipv6 interface [brief]` / `display ipv6 routing-table` / `ipv6 route-static`、缺接口级 undo、接口地址与静态路由不进快照、`capabilities.go:129`、`formatProtocolBlocks`（`:5040-5042`）、系统级 undo（`:5142-5144`）、IPv4 遗留 `state.Routes`（`state.go:65`）、持久化全量拷贝（`:5206`/`:5237`）均已核实到 file:line。
- **核心结论**：IPv6 **并非"完全缺失"，而是"半截且不校验"** —— 一条不校验参数的系统使能 + 一条零校验存串的接口地址 + 缺失的 display / 静态路由 / undo / 快照。本期是**补全 + 加固**，严重度低于 GRE/AAA 两轮（无结构体死状态、无跨包死代码），但**新增"地址/前缀纯函数核心"与"键碰撞风险（`:ip` 子串）"两块新内容**。
- **最高危技术点（务必写进设计）**：① **`interface:<if>:ip`（IPv4）与 `interface:<if>:ipv6-address`（IPv6）共享 `:ip` 子串**，任何 `strings.Contains(k, "ip")` / `Contains(k, "ipv6")` 式扫描都会造成 IPv4/IPv6 键互判与误删——风险与 GRE 轮 `Bridge-Aggregation`、AAA 轮 `0aaa` 同源同险（AC12 专项断言）；② **系统视图 `ipv6 任意串` 也"成功"** 的命令面缺陷必须修复（AC1 ②）；③ **RIPng/OSPFv3 命令形态按华为真机（已拍板 C7/C8）**，不加 Cisco 别名（AC13 专项断言）。
- 需求池 **27 条**（P0 14 / P1 8 / P2 5），验收标准 **AC1–AC13**（AC11 拆为 11a–11c、AC13 拆为纯函数 + RIPng/OSPFv3 两组），其中 **AC9 为诚实占位红线断言**（运行态字段恒 `-`、无伪造 `fe80::`）、**AC3 为纯函数 golden 断言**（本期最大可单测资产）、**AC8 为本期最大价值断言**（save→reload 接口地址与静态路由不丢）、**AC12 为键碰撞专项断言**（含 C2 多键形态前缀扫描）。
- **§8 的 10 项待确认（C1–C10）已全部拍板并回填结论**：C1 硬前置 / C2 CIDR 一段式 + P0 定多键 `ipv6:route-static:<prefix>:<nexthop>` / C3 有真实 MAC 才 EUI-64 计算、无则 `-` / C4 P0 恒 `-` / C5 级联清 / C6 仅清全局前缀 / C7（主理人修正）华为真机 RIPng 不加别名 / C8 华为真机 OSPFv3 带 area / C9 两种 MAC 格式 / C10 本期不做。受影响的 AC2/AC3/AC5/AC6/AC9/AC10/AC12/AC13 已同步修订。
- 键命名（`ipv6:enabled` / `interface:<if>:ipv6-enable` / `interface:<if>:ipv6-address` / **`ipv6:route-static:<prefix>:<nexthop>`（多键，C2 拍板）**）为 PM 预对齐建议，**最终以架构师设计文档为准**。

_Last updated: 2026-08-09 · 产品经理 许清楚（Xu）_
