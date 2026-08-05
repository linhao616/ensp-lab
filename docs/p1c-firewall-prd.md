# ensp-lab P1-C「Firewall 真实过滤」增量产品需求文档（简单 PRD）

> 文档类型：简单 PRD（增量，不写实现代码、不含竞品分析）
> 负责人：产品经理 许清楚（Xu）
> 范围：`internal/cli/`（CLIState 层仿真 ACL 评估器），路线依据 `docs/firewall-route-decision.md` 定调结论（2026-08-05 用户确认「路线 B」）。
> 技术栈：Go 1.26.5 + Gin；前端 React(Vite) 透传后端字符串；lite（ns-x，Windows 默认）/ full（gont，Linux）双模式通用。

---

## 0. 增量背景（必读，界定本期范围）

本期是**增量开发**，技术路线已被定调文档钉死，本 PRD 不重做市场/技术调研，只把「路线 B」翻译成产品需求。

**现状（已确认，证据见定调文档 §2）**：
- `acl` / `rule` / `traffic-filter` / `nat outbound` 命令**已解析、已存储、可 display、可持久化**，但**零条被应用到包路径**。
- 数据已就绪，单一事实源就是 `cli.CLIState`：
  - `CLIState.ACLs`：`map[string][]*ACLRule`（`state.go:51`），规则结构见 `ACLRule`（`state.go:158-172`）。
  - `CLIState.DeviceConfig["traffic-filter:<dir>:<acl>"]`：字符串（`parser.go:1211-1213`）。
  - `CLIState.NATConfig.Outbounds[]`（`NATOutbound`，`state.go:207-212`；`parser.go:982-1011`）。
- 可达性/探测路径**全不查 ACL**：CLI 真实 ping/tracert 经 `ResolveTraceroute` 钩子（`state.go:98`）→ `traceroute.go` 渲染但无 ACL 判定；`cli.CheckReachability`（`parser.go:93-131`）为纯 BFS；`protocol.ProtocolSimulator.CheckReachability`（`protocol.go:636`）同样不查 ACL。
- 存在**三套互不相通的 ACL 模型**（定调 §2）：`cli.CLIState`（事实源）、`protocol.Firewall`（桩 `HandlePacket`，`firewall.go:362`）、`protocol.ProtocolSimulator.MatchACL`（未被可达性调用）。本期仅 `cli.CLIState` 接入，另两套不接入或废弃/旁路。

**本期唯一新增**：一个 ACL 评估器 + 把它接到上述三条路径的 permit/deny 判定；deny 如实体现为丢包/不可达（延续 Bandwidth/PCAP「诚实占位」先例）。

---

## 1. 产品目标

让 CLI 的 firewall / ACL / traffic-filter 从「已配置未生效」变为「仿真真实过滤」——用户在设备配的 ACL 规则能真实影响 ping / tracert / 可达性结果，且 lite 与 full 双模式通用、Windows 默认用户也可生效。

---

## 2. 用户故事

1. **作为网络工程师**，我在路由器上配 `acl 2000` + `rule deny source 192.168.2.0 0.0.0.255` 后，从本机 `ping 192.168.2.10` 应返回 `destination unreachable`（100% loss），而非照通不误。
2. **作为网络工程师**，我在接口配 `traffic-filter inbound acl 2000` 后，发往该接口入向、命中 deny 的流量应被过滤（ping 不可达 / tracert 在该跳显示 `* * *`）。
3. **作为网络工程师**，我对被 ACL deny 的目标做 `tracert`，命中 deny 的跳应如实显示为超时/不可达，而非伪造完整路径。
4. **作为网络工程师**，我执行 `display ...` 或诊断面板里基于 `CheckReachability` 的可达性判断时，若路径被 ACL deny，应体现「不可达（ACL 拦截）」，与真实实验现象一致。
5. **作为学员**，我在 lite 引擎（Windows 默认）下用 ACL 过滤也能看到正确拦截效果，并在提示中理解「这是模拟过滤，非内核级真实过滤」。
6. **作为网络工程师（P2）**，我在做 `nat outbound` 的设备上同时配 ACL，期望 ACL 与 NAT 的先后顺序与匹配侧有可预期、文档化的一致行为。

---

## 3. 需求池（按优先级 P0 / P1 / P2）

### P0 — 本期必须（基础 IP/协议 ACL 评估器 + 介入全路径）

> 目标：用 `cli.CLIState` 现有数据，让「基础 ACL（源/目的 IP + 协议号）」真实影响 ping / tracert / CheckReachability。

| # | 需求 | 说明 | 依据 |
|---|------|------|------|
| P0-1 | **ACL 评估器（纯函数、可单测）** | 输入「流五元组（srcIP / dstIP / proto + 占位 srcPort/dstPort）+ 设备 + 方向(in/out)」，查 `state.ACLs` 与 `DeviceConfig["traffic-filter:..."]`，输出 `permit \| deny`。仅用基础层字段：`ACLRule.SrcIP/SrcWildcard/DstIP/DstWildcard/Protocol`（`state.go:164-167`）；通配符匹配对齐 `protocol.matchIP` / `wildcardToMask`（`protocol.go:568-603`），避免再造一套。 | 定调 §7.1 |
| P0-2 | **单一事实源** | 评估器**只读** `cli.CLIState.ACLs` + `DeviceConfig["traffic-filter:..."]`；不新增配置入口，不重复存储。 | 定调「单一事实源」 |
| P0-3 | **介入 CLI 真实 ping** | 在 `FormatEnginePing`（`traceroute.go:20`）渲染前，经 `ResolveTraceroute` 钩子（`state.go:98`）拿到的源→目的路径，对沿途 L3/防火墙设备上的 traffic-filter ACL 评估；命中 deny → 改写为 100% 丢包 / 不可达（沿用现有如实渲染风格，不伪造成功）。 | 定调 §7.2 |
| P0-4 | **介入 CLI 真实 tracert** | 同上，`FormatEngineTraceroute`（`traceroute.go:66`）按路径评估，命中 deny 的跳渲染为超时/不可达。 | 定调 §7.2 |
| P0-5 | **介入 `cli.CheckReachability`** | 对 `parser.go:93` 的 BFS 途径 L3/防火墙设备的入向 ACL 做评估，deny 即视为不可达（返回 `false`）。 | 定调 §7.2 |
| P0-6 | **诚实占位文案** | 引擎为 lite 时，探测/显示结果明确标注「ACL 为模拟过滤（lite 引擎），非内核级真实过滤」，口径复用 Bandwidth/PCAP 占位先例。 | 定调 §7.3 |

**P0 完成判据**：基础 ACL（src/dst IP + proto）能真实改变 ping/tracert/CheckReachability 结果，且全程不修改 `sim` 引擎。

### P1 — 本期建议做（方向绑定 + 诊断面板体现）

| # | 需求 | 说明 | 依赖 |
|---|------|------|------|
| P1-1 | **`traffic-filter` in/out 方向绑定语义落地** | 明确 `inbound`=命中设备入接口评估、`outbound`=命中设备出接口评估；定义跨多跳时方向如何沿路径累积/取交集（任一 deny 即丢）。**语义边界见 §5 待确认 #1，需用户/架构师拍板**。 | 待确认 #1 |
| P1-2 | **诊断面板 / `display ip routing` 等下游统一体现 ACL 拦截** | `CheckReachability` 的下游调用方（诊断面板/可达性显示）需统一呈现「ACL 拦截」原因，而非仅内部返回 false。**是否覆盖全部下游见 §5 待确认 #5**。 | 待确认 #5 |
| P1-3 | **`protocol.ProtocolSimulator.CheckReachability` 调用点同步评估（可选）** | 按需介入 `protocol.go:636` 的同义路径；注意与 `protocol.MatchACL`（`protocol.go:526`）**不重复实现**，以 CLIState 评估器为准，旧 `MatchACL` 明确旁路/废弃。 | 定调开放项 #4 |

### P2 — 本期可选 / 下期（与 nat outbound 交互，依赖高、风险高）

| # | 需求 | 说明 | 依赖 / 风险 |
|---|------|------|------------|
| P2-1 | **`nat outbound` 与 ACL 交互** | 定义评估器在 NAT 场景的判定：先 ACL 后 NAT 还是先 NAT 后 ACL；源/目的 IP 在转换**前**还是**后**参与匹配；本期基础层是否在 NAT 路径生效。**范围与边界见 §5 待确认 #2，需拍板是否纳入本期**。 | 待确认 #2；风险：NAT 转换前后 IP 语义复杂，易与真实 VRP 行为偏离 |

### 本期不做（明确排除）

- **advanced ACL 端口 / 端口范围**：`ACLRule.DstPort` / `DstPortOp` / `DstPortEnd` / `SourcePort`（`state.go:168-171`）、TCP `established` 状态语义——本期不纳入（定调开放项 #3）。
- **真实内核级包过滤**：lite（ns-x）架构定位「不触及真实协议栈」，永远不支持内核级过滤（定调 §1.1）；full 模式的内核级 FRR enforce 列为 gont 数据面成熟后的独立增强项，不阻塞本期（定调路线 A 为长期目标）。
- **另两套既有 ACL 模型接入**：`protocol.Firewall`（桩）与 `protocol.ProtocolSimulator.MatchACL` 本期**不接入** CLIState 评估器，明确为废弃/旁路，避免双写与语义漂移（定调开放项 #4）。
- **`traffic-policy` / `traffic behavior`**：本仓库映射为 QoS，非防火墙，不计入本期范围。

---

## 4. 边界与约束

- **lite / full 通用**：评估器与引擎模式（`EngineModeName()`，`engine_mode_other.go:11` / `engine_mode_gont.go:11`）无关，纯在 CLIState + 拓扑路径上计算，覆盖 Windows 默认用户。
- **复用现有 ACL 数据**：仅读 `state.ACLs` + `DeviceConfig["traffic-filter:..."]` +（P2）`NATConfig.Outbounds`，不新增配置入口。
- **评估器为纯函数式**：输入「包五元组 + 设备 + 方向」→ 输出 `permit/deny`；无副作用、可单测，便于回归。
- **不修改引擎**：`sim` 引擎核心（ns-x / gont）零改动；仅在 `cli` 包内新增评估器并接入已存在的 `ResolveTraceroute` 钩子与 `CheckReachability` 调用点（定调 §6 理由 5）。
- **诚实占位**：只改「规则确实匹配」的结果，匹配不到则如实反映拓扑，绝不伪造拦截/成功（延续 Bandwidth/PCAP 先例）。
- **CLIState 单一事实源**：另两套 ACL 模型在本期不接入或废弃/对齐到 CLIState 评估器。

---

## 5. 待确认问题（交主理人 / 用户拍板）

> 下列为定调文档「留待 P1-C 实现阶段解决的开放项」中仍含糊、需在实现排期前拍板的部分。

1. **`traffic-filter` 的 in/out 方向语义**（开放项 #1，影响 P1-1 与评估器方向入参定义）
   - `inbound` / `outbound` 在评估器如何映射：源设备出方向 vs 目的设备入方向？
   - **跨多跳时方向如何累积/取交集**：是按「沿途所有设备的 traffic-filter 取交集（任一 deny 即丢）」，还是「仅源/目的设备」？（关联定调 §8 #6）
   - 基础 ACL 末尾是否补齐「隐式 deny any」（deny 未匹配到时默认丢弃），还是「未匹配即 permit」？需明确默认动作。

2. **与 `nat outbound` 的交互范围**（开放项 #2，影响 P2-1 是否纳入本期）
   - 是否本期纳入 P2，还是明确推到下期？
   - ACL 与 NAT 先后顺序：先 ACL 过滤再 NAT 转换，还是先转换再 ACL？
   - 源/目的 IP 在转换**前**还是**后**参与 ACL 匹配？

3. **诊断面板 / `display` 下游可见性**（开放项 #5，影响 P1-2 范围）
   - 哪些 `CheckReachability` 下游调用方需统一体现 ACL 拦截（诊断面板 / `display ip routing` / 其他）？是否全部统一？

4. **三套 ACL 模型收敛方式**（开放项 #4）
   - `protocol.Firewall` 与 `protocol.ProtocolSimulator.MatchACL` 是「直接废弃」还是「保留并旁路对齐到 CLIState 评估器」？需在代码层明确，避免后续维护者误接线。

5. **进阶端口 / established 纳入节奏**（开放项 #3，本期已明确不纳入，但需定排期口径）
   - 是否作为 P1-C 后续增强、何时纳入、纳入时是否需要扩展评估器接口（目前仅占位 srcPort/dstPort）？

---

## 6. 验收标准（可测试）

### P0 完成定义（必须）
- [ ] 配置 `acl 2000` + `rule deny source <网段> <通配>` 后，从本机 `ping` 该网段内 IP，返回 `destination unreachable`（100% packet loss），而非照通。
- [ ] 配置 `rule permit ...` 且未命中 deny 时，`ping` 仍正常可达（不伪造成功、也不误拦截）。
- [ ] `display` / 可达性判断（基于 `cli.CheckReachability`）在路径被 ACL deny 时体现不可达。
- [ ] 真实 `tracert` 命中 deny 的跳如实显示为 `* * *` / 不可达，不伪造完整路径。
- [ ] `undo acl <num>` 后，ping / 可达性恢复为未过滤状态（无残留、不 panic）。
- [ ] 评估器为纯函数，单测覆盖：通配符匹配、协议号匹配（IP/ICMP/TCP/UDP）、多规则顺序、未命中默认动作。
- [ ] lite 引擎下 ACL 过滤生效，并带「模拟过滤（非内核级真实过滤）」占位提示。

### P1 完成定义（建议）
- [ ] `traffic-filter inbound/outbound` 按拍板的方向语义生效（P1-1）。
- [ ] 诊断面板 / 可达性 display 下游统一呈现 ACL 拦截原因（P1-2）。
- [ ] 跨多跳多设备 ACL 按拍板规则（取交集/仅端设备）正确累积（P1-1 + 待确认 #1）。

### P2 完成定义（可选）
- [ ] （若纳入本期）`nat outbound` + ACL 按拍板顺序/匹配侧一致生效，且有文档化行为说明与单测。

### 非目标（本期不做，重申）
- 不实现 advanced ACL 端口 / `established` 语义。
- 不做 lite 内核级真实过滤（架构上不可行）。
- 不接入 `protocol.Firewall` / `protocol.ProtocolSimulator.MatchACL` 到真实路径。
- 不改动 `sim` 引擎核心。

---

*证据索引（关键 file:line，详见定调文档 §证据索引）：*
- `internal/cli/state.go:51,158-172,207-212`（ACLs / ACLRule / NATOutbound）
- `internal/cli/parser.go:869-937,982-1011,1206-1219,93-131`（acl/rule/nat/traffic-filter/CheckReachability）
- `internal/cli/state.go:98` + `internal/cli/traceroute.go:20,66`（真实 ping/tracert 渲染路径，未查 ACL）
- `internal/sim/engine_mode_other.go:11` / `engine_mode_gont.go:11`（引擎模式，评估器无关）
- `internal/protocol/firewall.go:362`、`internal/protocol/protocol.go:526,636`（另两套 ACL 模型，本期不接入）
