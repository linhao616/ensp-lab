# Firewall / ACL 真实过滤 — 路线定调决策建议

> 文档定位：给主理人 / 用户拍板的**架构决策输入**，不是实现方案。
> 范围：P1-C「让 CLI 的 firewall / ACL / traffic-filter 真正过滤流量（真实包过滤）」的可行性调研与路线建议。
> 调研方式：直接读真实代码，结论均附 `file:line` 证据。

---

## 0. TL;DR（结论先行）

- **今天没有任何「真实防火墙过滤」**：所有 firewall/ACL/traffic-filter 命令都只被**解析、存储、可显示、可持久化**，**没有一条被应用到包处理路径**。
- **lite 引擎（ns-x，Windows/macOS 默认构建）没有任何 ACL/包过滤钩子**，且 `EngineModeName()` 在 Windows 上恒为 `"lite"`（`engine_mode_other.go:11`），无法做真实内核级过滤。
- **full 引擎（gont，Linux+gont 构建）底层是真实命名空间 + 真实 FRR**，FRR 理论上能 enforce `ip access-list`，但**当前 gont 数据面本身是桩**（SendPacket 不真发包、链路未接线、Traceroute 未实现），**ACL 也未被应用到任何接口**。所以 full 是「潜力」而非「现实」。
- 代码里已存在**三套互不相通的 ACL 模型**，但**没有任何一套接入真实包路径**（见 §3）。
- **推荐路线 B**：在 `CLIState` 层建「仿真 ACL 模型」，在 ping/tracert/`CheckReachability` 路径按 ACL 规则模拟放行/丢弃；lite 与 full 都可用，且沿用本项目「诚实占位」先例（仅当规则确实匹配才改结果，否则如实反映拓扑）。理由见 §6。

---

## 1. 可行性结论（按引擎模式）

### 1.1 lite 模式（ns-x 纯 Go 仿真，Windows/macOS 默认构建）

**结论：不可行做「真实」内核级包过滤。引擎没有任何 ACL / firewall / packet-filter 钩子。**

证据：

- 模式判定：`internal/sim/engine_mode_other.go:10-12` —— 非 gont 构建（Windows/macOS 或不含 gont tag 的 Linux）`EngineModeName()` 恒返回 `"lite"`，注释明言「ns-x 纯 Go 事件驱动仿真，**不触及真实协议栈**」。
- 包转发完全基于拓扑图遍历，无任何过滤点：
  - `internal/sim/engine_nsx.go:101` `BridgeNode.Transfer` —— 纯转发/泛洪逻辑，无 ACL 检查。
  - `internal/sim/engine_nsx.go:685` `makeReact` —— 仅处理 ICMP echo、TTL、VLAN 不匹配时 `emit(PacketEventDrop)`（`engine_nsx.go:130-142`），**唯一的 drop 场景是 VLAN 不匹配，与 ACL 无关**。
  - 全局搜索 `acl|firewall|filter|packet-filter` 在 `engine_nsx.go` 中**零命中**（该文件内无上述任一概念）。
- 抓包能力也明确不支持真实栈：`internal/sim/engine_nsx.go:1323-1325` `CapturePCAP` 直接返回错误 `"sim: pcap capture not supported in ns-x mode, use gont mode on Linux"`。

→ **lite 引擎层面无法挂载真实 ACL/包过滤**，与其「不触及真实协议栈」的设计定位一致。

### 1.2 full 模式（gont，Linux + gont 构建，真实命名空间 + FRR）

**结论：底层具备「真做」的物质基础，但当前**未接线、未实现**，所以今天同样做不到真实过滤。**

证据：

- 模式判定：`internal/sim/engine_mode_gont.go:10-12` —— 启用 gont 的 Linux 构建返回 `"full"`，注释「真实网络命名空间，使用真实协议栈收发报文」。
- 路由器确实跑了 FRR：`internal/sim/gont_emulator.go:140-142` `e.routers[id] = router.NewFRRRouter(host, dev.ID)`，且 `Start()` 会 `r.Start()` 拉起 FRR（`gont_emulator.go:157-179`）。FRR 支持 `ip access-list` / `ip prefix-list`，理论上可在内核层 enforce ACL。
- **但 gont 当前并未把 ACL 应用到接口**：
  - `router` 包内搜索 `access-list|ApplyACL|ip access` **零命中**（已在 `internal/router` 全量检索），即 `FRRRouter` 没有「应用 ACL 到接口」的封装。
  - gont 的 `ApplyOSPFConfig/ApplyBGPConfig`（`gont_emulator.go:317-335`）确实转发给 FRR，但**不存在对应的 `ApplyACLConfig`**。
- **且 gont 数据面本身还是桩**（比 ns-x 还「空」）：
  - `SendPacket`（`gont_emulator.go:229-239`）只 `emit` 一个 send 事件，**不通过 raw socket 真正注入报文**。
  - `Ping`（`gont_emulator.go:245-257`）返回占位结果（`Sent:1, Received:0`，仅打印一句话），**不真发 ICMP**。
  - `Traceroute`（`gont_emulator.go:262-264`）直接 `return nil, fmt.Errorf("gont engine: traceroute not implemented")`。
  - `build()` 中链路**未接线**，注释明确 `// Links are not wired up in Task 3 ... TODO(Task 4)`（`gont_emulator.go:144-147`）。
  - 仅 `CapturePCAP`（`gont_emulator.go:347-391`）是真实可用的（依赖 libpcap）。

→ **full 模式是「可真做」的目标态，但需要先补齐：链路接线 + SendPacket 真发包 + FRR access-list 应用到接口 + 真实数据面跑通**。当前它离「真实防火墙过滤」还差完整数据面与 ACL 接线两步。

### 1.3 关键判断

| 维度 | lite (ns-x) | full (gont) |
|------|-------------|-------------|
| 真实内核包过滤 | ❌ 不可能（无真实栈、无钩子） | ⚠️ 潜力具备，但未接线/未实现 |
| FRR ACL 能力 | 无 | 有（需自行封装 ApplyACL→接口） |
| 数据面是否真跑通 | 是（图仿真，但不是真实栈） | 否（SendPacket/Ping 均为桩） |
| 默认构建可用？ | ✅（Windows 默认即此） | ❌ 需 Linux + `-tags gont` + CGO + libpcap，CI 默认排除 |
| 现状能否拦包 | 否 | 否 |

**一句话：两种模式今天都不能真实拦包；lite 永远不能（架构定位），full 能但需要先把数据面和 ACL 接线做完。**

---

## 2. 现状盘点：今天有哪些 firewall / ACL CLI 命令

命令均被解析并落库到 `CLIState`，可 `display`、可随拓扑 `save/reload` 持久化，但**均不被任何包路径执行**。

| 命令 | 解析位置（file:line） | 行为 | 状态 |
|------|----------------------|------|------|
| `acl <num>` / `acl name <name> basic\|advanced` | `internal/cli/parser.go:869-898` | 进入 ACL 视图，写入 `state.ACLs`（类型 `map[string][]*cli.ACLRule`，定义见 `state.go:50,157-171,497`） | ✅ 已解析 / ✅ 已存储 / ❌ 未应用 |
| `rule <id> permit\|deny ...` | `internal/cli/parser.go:900-936` | 往当前 ACL 追加 `ACLRule`（Action/Protocol/SrcIP/SrcWildcard/DstIP/DstPort 等） | ✅ 已解析 / ✅ 已存储 / ❌ 未应用 |
| `traffic-filter inbound\|outbound acl <num>` | `internal/cli/parser.go:1206-1219` | 写入 `state.DeviceConfig["traffic-filter:<dir>:<acl>"]` 字符串 | ✅ 已解析 / ✅ 已存储 / ❌ 未应用（全仓无消费者） |
| `nat outbound <acl-num> [address-group <id>]` | `internal/cli/parser.go:982-1011` | 在 `NATOutbound` 结构里记 ACL 引用（`state.go:206-211`） | ✅ 已解析 / ✅ 已存储 / ❌ 未应用 |
| `display acl [num]` | `internal/cli/parser.go:3281-3336` | 渲染 `state.ACLs` | ✅ 可显示 |
| `undo acl <num\|name>` | `internal/cli/parser.go:4218-4224` | `delete(state.ACLs, ...)` | ✅ 可撤销 |
| 设备类型 `firewall`（USG6000） | `internal/cli/parser.go:3869-3870, 3941-3942, 4101-4102` | 仅影响 `display version` / 帮助里的设备型号字符串 | ✅ 识别为防火墙设备，但无过滤语义 |
| 能力矩阵 `acl`/`rule`/`traffic-filter` | `internal/cli/capabilities.go:91-94` | 声明在 router / L3Switch / firewall / VTEP 可用 | ✅ 仅能力声明 |

**重要：存在「三套互不相通的 ACL 模型」，彼此没有任何桥接：**

1. **CLI 层**：`cli.CLIState.ACLs` + `DeviceConfig["traffic-filter:..."]`（`internal/cli/state.go:50`）。用户配置入口，未被消费。
2. **protocol 层 `Firewall`**：`internal/protocol/firewall.go:12-42` 定义 `Firewall`/`ACL`/`ACLRule`，含**完整匹配逻辑** `ApplyACL`（`firewall.go:175-195`），但 `HandlePacket` 是**空桩**（`firewall.go:362-364`，注释：「the firewall currently does not participate in packet-level simulation」）。**全仓无 `protocol.Firewall` 实例化/接线**。
3. **protocol 层 `ProtocolSimulator`**：`internal/protocol/protocol.go:19-23` + `RouterState.ACLs`（`protocol.go:28`），提供 `AddACL`（`protocol.go:521`）、`MatchACL`/`checkACL`/`matchRule`（`protocol.go:526-566`）——**又一套 ACL 匹配逻辑**，但 `CheckReachability`（`protocol.go:636`）是纯 BFS，**不调用 ACL**。

> `traffic-policy` / `traffic behavior` 在本仓库**不对应防火墙**，而是映射到 QoS（`QoSClassifier/QoSBehavior/QoSPolicy`，`state.go:414-439`；能力矩阵为 `qos`，`capabilities.go:77`）。本节不将其计入防火墙过滤范围。

---

## 3. 可达性路径与 ACL 的关系（P1-C 能否「真拦包」的关键）

要「真正过滤流量」，ACL 必须介入「包能否到达」的计算。现三条可达性/探测路径**均不查询 ACL 状态**：

1. **CLI 真实 ping/tracert（主路径）**：经 `CLIState.ResolveTraceroute` 钩子（注入点 `state.go:97`）调用 `sim.Engine.Ping` / `Traceroute`，渲染见 `internal/cli/traceroute.go:20-109`。而 `sim.Engine`（ns-x）的 `Ping`（`engine_nsx.go:1034`）/`makeReact`（`engine_nsx.go:685`）/`Traceroute`（`engine_nsx.go:1141`）**完全基于拓扑图，无任何 ACL 查询**。→ ACL 对 ping/tracert **零影响**。
2. **`cli.CheckReachability`**（`internal/cli/parser.go:93-131`）：对拓扑做无向 BFS，**不查 ACL**。
3. **`protocol.ProtocolSimulator.CheckReachability`**（`internal/protocol/protocol.go:636-665`）：同样纯 BFS；虽同文件有 `checkACL`/`MatchACL`（`protocol.go:526-566`），但**未被 CheckReachability 或任何 ping 调用**（全仓检索 `checkACL`/`MatchACL` 仅定义、无在可达性路径内的调用）。

**结论：当前「过滤」无处生效——既无引擎钩子，也无可达性路径查询 ACL。要让 P1-C 成真，必须新增「ACL 介入可达性/探测」这一环。**

---

## 4. lite vs full 差距汇总（真实防火墙过滤需要什么）

**lite 需要**：一个包过滤点（内核/数据面钩子）。但 lite = ns-x 图仿真，设计上就「不触及真实协议栈」（`engine_mode_other.go:3-4`），且 `engine_nsx.go` 无任何 filter 钩子。**结论：lite 无法支持真实内核过滤，只能「模拟」或「诚实占位」。**

**full 需要**（按依赖顺序）：
1. 链路接线（`gont_emulator.go:144-147` 的 TODO Task 4 尚待做）；
2. `SendPacket` 经 raw socket 真发包（现为桩，`gont_emulator.go:229-239`）；
3. `FRRRouter` 新增 `ApplyACLConfig`，把 FRR `ip access-list` 应用到接口（`internal/router` 当前零 ACL 封装）；
4. 真实数据面跑通后，内核自动按 FRR ACL enforce。

→ full 的「真实过滤」是**功能完整 + 接线 + 平台受限（仅 Linux+gont 构建）**三者的组合，工作量远大于 lite 侧的「仿真」方案。

---

## 5. 路线选项

### 路线 A：仅 full 模式实现真实 ACL（gont + FRR），lite 走诚实占位
- full 模式：在 `router.FRRRouter` 加 `ApplyACLConfig`，将 CLI 的 `acl`/`traffic-filter` 翻译成 FRR `ip access-list` 并 `traffic-filter` 到接口；依赖 gont 数据面先跑通（接线 + 真发包）。
- lite 模式：沿用 Bandwidth/PCAP 的「诚实占位」先例——`display` 出配置但 ping/tracert 结果标注「当前引擎（lite）不支持真实 ACL 过滤」，绝不伪造拦截。
- **优点**：full 下是货真价实的包过滤。
- **缺点**：① 仅 Linux+gont 构建可用，覆盖不到 Windows 默认用户（`EngineModeName()=="lite"`）；② 前置依赖重（数据面接线 + 真发包 + FRR ACL 封装），是「先修引擎再谈防火墙」；③ 投入大、周期长、与主线进度强耦合。

### 路线 B：在 CLIState 层建「仿真 ACL 模型」（引擎无关），接管 ping/tracert/CheckReachability 的放行/丢弃判断
- 以 `cli.CLIState.ACLs` + `DeviceConfig["traffic-filter:..."]`（已有数据，§2）为单一事实源；新建一个轻量 ACL 评估器，在以下路径介入：
  - `sim.Engine` 真实 ping/tracert 返回后，按沿途设备（源接口、途经 L3/防火墙设备）上绑定的 `traffic-filter` ACL 规则**判定某 ICMP 流是否被 deny**；命中 deny 则把结果改写为丢包/不可达（与现有「不可达如实显示」风格一致，`traceroute.go:24-32`）。
  - `cli.CheckReachability` / `protocol.CheckReachability` 的调用点，对穿过的 ACL 设备做 permit/deny 评估。
- lite 与 full 都可用（与引擎模式无关，纯在 CLIState + 拓扑路径上算）。
- **优点**：① 立即可用、覆盖全部用户（含 Windows）；② 复用已解析已持久化的 ACL 数据，无需动引擎；③ 投入小、独立、不阻塞主线；④ 与「诚实占位」先例天然契合（只改「规则确实匹配」的结果，不做假成功）。
- **缺点**：本质是「模拟」而非「真实内核过滤」；若规则语义（端口/协议/L3 细节）做全，需与 VRP 行为对齐，有一定建模工作量；full 模式下与「真实 FRR 过滤」可能重复（需约定优先级）。

### 路线 C：暂不实现真实过滤，仅补全命令解析 + 配置持久化 + 诚实占位（最低成本）
- 现有命令已能解析、存储、`display`、持久化（§2 已满足）。本路线只补强：当拓扑 reload 后 ACL 配置完整保留（已满足）、`display` 与帮助信息明确「本工具 ACL 为配置态，不参与实时包过滤」，并在 UI 提示「未接入/未支持」（同 Bandwidth/PCAP 占位）。
- **优点**：零引擎改动、零新逻辑、最快。
- **缺点**：P1-C「真正过滤流量」目标未达成，仍是摆设；与「让 firewall/ACL 真过滤」的需求字面背离。

---

## 6. 推荐路线 + 理由

**推荐：路线 B（CLIState 层仿真 ACL 模型），并把路线 A 的 full 真实过滤列为「长期/条件达成」目标。**

理由：

1. **贴合本项目「诚实占位」先例与价值观**：Bandwidth/PCAP 已确立「lite 不伪造、如实显示/占位」的范式。路线 B 同样——ACL 规则匹配到就如实改丢包/不可达，不匹配就如实反映拓扑，**不造任何假拦截**。这比在 lite 上「假装真过滤」更符合项目操守。
2. **Windows = lite 是铁现实**：默认构建 `EngineModeName()=="lite"`（`engine_mode_other.go:11`），绝大多数用户（尤其 Windows）跑不到 gont。路线 A 会让这批用户永远用不上真实过滤；路线 B 对所有人都生效。
3. **投入产出比最高**：真实 ACL 数据（`state.ACLs` + `DeviceConfig`）已就绪，缺的只是「评估器 + 介入可达性路径」一小段逻辑；路线 A 却要先把 gont 数据面整体跑通（路线长、风险高、与引擎主线强耦合）。
4. **不排斥未来 full 真实化**：路线 B 是「仿真层」，可明确优先级低于真实内核过滤；待某天 gont 数据面 + FRR ACL 接线完成（路线 A 的前提），full 模式下可切换为「真实 enforce」，lite 仍走 B。两者可共存、渐进。
5. **独立、不阻塞主线**：B 只在 `cli` 包内新增评估器并接入已存在的 `ResolveTraceroute` 钩子与 `CheckReachability` 调用点，无需改 `sim` 引擎，工程风险低。

**一句话建议**：用路线 B 把「ACL 真能拦 ping/tracert/可达性」做出来（全员可用、复用现有数据、低风险）；把「full 模式内核级真实过滤」作为 gont 数据面成熟后的增强项，不阻塞本期。

---

## 7. 后续落地建议（若采纳路线 B，仅草拟模块/文件与顺序，不含代码）

> 以下为落地草图，供工程排期参考，不在此写实现。

1. **ACL 评估器（新增，纯函数、可单测）**
   - 文件建议：`internal/cli/acl_eval.go`（或 `internal/acl/`）。
   - 职责：输入「流五元组（srcIP/dstIP/proto/srcPort/dstPort）+ 设备 + 方向(in/out)」，查 `state.ACLs` 与 `state.DeviceConfig["traffic-filter:..."]`，返回 `permit | deny`。
   - 复用：`cli.ACLRule`（`state.go:157-171`）字段；匹配语义对齐 `protocol.matchRule`（`protocol.go:544-566`）的 wildcard/port 逻辑，避免再造一套。

2. **介入可达性/探测路径（最小改动点）**
   - `internal/cli/traceroute.go:20` `FormatEnginePing` / `:66` `FormatEngineTraceroute`：在拿到 `sim.PingResult`/`TracerouteResult` 后，按「源设备 → 目的」沿途设备上的 `traffic-filter` ACL 评估；命中 deny 则把结果改写为 100% 丢包 / 不可达（沿用现有如实渲染风格，不伪造成功）。
   - `internal/cli/parser.go:93` `CheckReachability`：对 BFS 途径的 L3/防火墙设备的入向 ACL 做评估，deny 即视为不可达。
   - （可选）`internal/protocol/protocol.go:636` `CheckReachability` 调用点：同上，按需介入；注意与 `protocol.ProtocolSimulator` 已有 `MatchACL`（`protocol.go:526`）对接，避免重复实现。

3. **诚实占位文案（沿用先例）**
   - 当设备类型为 firewall 但引擎为 lite，或 `traffic-filter` 已配置但评估器判定「引擎/路径不支持细分」时，`display` 与探测结果明确标注「ACL 为模拟过滤（lite 引擎），非内核级真实过滤」——与 Bandwidth/PCAP 占位文案口径一致。

4. **测试（复用现有 QA 范式）**
   - 参考 `internal/cli/p1f_qa_test.go:223-225`（`undo acl 2000` 不 panic 且报 `removed`）的写法，新增「配置 deny ACL 后 ping 应丢包」「配置 permit 后 ping 仍通」「undo 后恢复」等用例。

**与路线 A 的衔接**：若后续做 full 真实过滤，只需在 `internal/router`（FRRRouter）加 `ApplyACLConfig` 并在 `gont_emulator.go` 暴露 `ApplyACLConfig` 入口，让 CLI 在 `EngineModeName()=="full"` 时优先走真实 enforce，lite 仍走 B。接口保持「CLI 配置 → 评估器/引擎」单侧入口。

---

## 8. 待确认事项（需齐活林 / 用户拍板）

1. **是否接受「模拟过滤」而非「内核真实过滤」作为本期交付？** —— 即是否认可路线 B 的「仿真 ACL」定义（P1-C 的「真实」是「结果真实反映 ACL 规则」，而非「内核级包丢弃」）。
2. **路线 B 的 ACL 语义覆盖范围**：本期要做到多细？仅 `permit/deny + srcIP/dstIP + proto`（基础 ACL）？还是也要 advanced ACL 的 `dstPort`、`tcp/udp` 端口范围？影响评估器工作量。
3. **介入哪些路径算「达成」**：仅 ping/tracert 结果受 ACL 影响即可，还是也要让 `display ip routing` / `CheckReachability` 调用方（诊断面板）体现 ACL 拦截？
4. **full 真实过滤（路线 A）的优先级**：是否列为本期目标，还是明确推到「gont 数据面成熟后」的独立后续任务？（涉及是否先投入 gont 链路接线 + 真发包）。
5. **诚实占位文案口径**：是否复用 Bandwidth/PCAP 的「未接入/未支持」措辞，还是为 ACL 单独定义提示语？
6. **多设备串联 ACL**：ping 跨多台 L3/防火墙设备时，是按「沿途所有设备的 traffic-filter 取交集（任一 deny 即丢）」还是「仅源/目的设备」？需明确语义边界。

---

*证据索引（关键 file:line）：*
- `internal/sim/engine_mode_other.go:10-12`（lite 判定）、`engine_mode_gont.go:10-12`（full 判定）
- `internal/sim/engine_nsx.go:101,685,130-142,1034,1141,1323-1325`（ns-x 无 ACL 钩子）
- `internal/sim/gont_emulator.go:140-142,144-147,229-239,245-257,262-264,317-335,347-391`（gont FRR 有但 ACL 未接线、数据面为桩）
- `internal/protocol/firewall.go:12-42,175-195,362-364`（Firewall 模型 + 桩 HandlePacket）
- `internal/protocol/protocol.go:19-23,28,521,526-566,636-665`（ProtocolSimulator ACL 模型 + CheckReachability 不查 ACL）
- `internal/cli/state.go:50,157-171,206-211,497`、`internal/cli/parser.go:869-898,900-936,982-1011,1206-1219,3281-3336,4218-4224`（CLI 命令解析/存储）
- `internal/cli/capabilities.go:91-94`（acl/rule/traffic-filter 能力）
- `internal/cli/state.go:97` + `internal/cli/traceroute.go:20-109`（真实 ping/tracert 渲染路径，未查 ACL）
- `internal/cli/parser.go:93-131`（cli.CheckReachability 不查 ACL）

---

## 定调结论（2026-08-05 用户确认）

> 本节记录用户对 P1-C Firewall 路线定调的正式拍板结果，作为后续实现阶段的范围基线。**仅记结论，不含实现代码。**

### 已确认的三项决策

1. **采纳路线 B —— CLIState 层仿真 ACL 评估器**
   - 在 `cli.CLIState.ACLs` + `DeviceConfig["traffic-filter:..."]`（现有已解析已持久化数据，见 §2）之上新增一个评估器，介入 ping / tracert / `CheckReachability` 的 permit/deny 判定。
   - **lite 与 full 通用**：与引擎模式（`EngineModeName()`，见 `engine_mode_other.go:11` / `engine_mode_gont.go:11`）无关，纯在 CLIState + 拓扑路径上计算。
   - **覆盖 Windows 默认用户**：不依赖 gont/Linux，所有默认构建均生效。
   - **复用现有 ACL 数据**：不新增配置入口，避免重复存储。

2. **ACL 语义覆盖「基础层」**
   - 支持：**源 IP / 目的 IP（含通配符 wildcard 匹配）** + **协议号（IP / ICMP / TCP / UDP）**。
   - 范围界定：`cli.ACLRule` 的 `SrcIP/SrcWildcard/DstIP/DstWildcard/Protocol` 字段（定义见 `state.go:157-171`）；通配符匹配逻辑可对齐 `protocol.matchIP` / `wildcardToMask`（`protocol.go:568-603`），避免再造一套。
   - **本期不做**：advanced ACL 的 `dstPort` 端口 / 端口范围、TCP `established` 状态语义（列为下方开放项）。

3. **评估器介入「全路径」**
   - CLI 真实 `ping`（经 `ResolveTraceroute` 钩子 `state.go:97` → `traceroute.go:20` 渲染）按沿途设备 ACL 评估，命中 deny 改写为丢包/不可达（沿用如实渲染风格，不伪造成功）。
   - CLI 真实 `tracert`（同上，`traceroute.go:66`）同样受 ACL 评估影响。
   - `cli.CheckReachability`（`parser.go:93`）与 `protocol.ProtocolSimulator.CheckReachability`（`protocol.go:636`）的调用点，对途经 L3 / 防火墙设备的入向 ACL 做评估，deny 即视为不可达。

### 留待 P1-C 实现阶段解决的剩余开放项

以下问题在定调阶段不做结论，交由实现阶段在路线 B 框架内处理：

1. **`traffic-filter` 的 in/out 方向绑定语义**
   - `DeviceConfig["traffic-filter:<dir>:<acl>"]`（`parser.go:1211-1219`）的 `inbound` / `outbound` 在评估器中如何映射：源设备出方向 vs 目的设备入方向？跨多跳时方向沿路径如何累积/取交集？需在实现时明确并写入单测。

2. **与 `nat outbound` 的交互**
   - `NATOutbound` 已引用 ACL（`state.go:206-211`，`parser.go:982-1011`）。评估器在 NAT 场景下如何判定：是先 ACL 后 NAT，还是先 NAT 后 ACL？源/目的 IP 在转换前后用哪一侧参与匹配？本期基础层是否要在 NAT 路径上生效，需实现时定。

3. **进阶端口 / established 语义是否后续纳入**
   - advanced ACL 的 `DstPort`（`state.go:167`）、`DstPortOp`/`DstPortEnd`（`state.go:168-169`）、TCP `established` 状态语义，本期明确**不纳入**。是否作为 P1-C 后续增强、何时纳入，待实现阶段评估工作量后决定。

4. **3 套既有 ACL 模型如何统一到 CLIState 评估器**
   - 现状存在互不相通的模型：`cli.CLIState.ACLs`、`protocol.Firewall`（`firewall.go`，`HandlePacket` 为桩 `:362`）、`protocol.ProtocolSimulator`（`protocol.go:521,526`）。定调要求以 **`cli.CLIState` 为单一事实源**，评估器只读 CLI 层数据；`protocol.Firewall` / `ProtocolSimulator` 的 ACL 能力在实现阶段应明确「不接入」或「废弃/对齐」，避免双写与语义漂移。

5. **（`CheckReachability` 调用方的可见性）**
   - 诊断面板 / `display ip routing` 等调用 `CheckReachability` 的下游，是否统一体现 ACL 拦截结果，由实现阶段按需求确认（关联原 §8 待确认项 3）。

