# ensp-lab P2 第一项：NAT 真实过滤（增量 PRD · 简单 PRD）

> 文档类型：增量产品需求文档（PRD，简单模式）
> 关联：`docs/p1c-firewall-design.md` §5（P2 预留）/§9（拍板 #4 NAT 留空桩）、`docs/reference/huawei-vrp-course.md` 课程 38（静态/动态 NAT，验收 oracle）
> 作者：产品经理 许清楚（Xu）
> 语言：中文

---

## 0. 项目信息

- **Language**：中文
- **Programming Language**：Go 1.26.5（本期工作集中在 `internal/cli`；前端 React/Vite 本期无变更）
- **Project Name**：`p2_nat_real_filtering`
- **原始需求复述**：在 P1-C ACL 评估器基础上，把 P2 预留的 `evaluateNATACL` 空桩落地为**真实地址转换**，并接入 `EvaluatePathACL` 的 `// TODO(P2)` 调用点，使 ping/tracert 的 ACL 评估与可达性在跨 NAT 边界时基于**转换后的 IP** 正确工作。

---

## 1. 产品目标

在保持 P1-C「CLIState 层纯函数、诚实占位、不碰 sim 引擎」架构基线的前提下，把 NAT 从「空桩恒返回 permit」升级为可单测的真实地址转换：出向（`nat outbound` / Easy IP / 地址池）改写源 IP 为公网地址，入向（`nat server`）改写目的 IP 为内网地址，并让转换发生在路径评估的正确位置，使 NAT 之后的 ACL 评估、ping/tracert 可达性与跳数显示都能反映转换后的拓扑（对齐课程 38 的私网↔公网地址转换行为）。

---

## 2. 用户故事

1. **内网用户经出向 NAT 访问公网**：As an 内网主机用户，I want 从内网 ping/访问公网目标时源地址被改写为 NAT 设备公网地址，so that 回程流量能正确回到内网，且 NAT 之后的 ACL 评估基于转换后源 IP 进行。
2. **外网经 nat server 访问内网服务**：As an 外网用户，I want ping nat server 的公网 IP 能解析到内网真实服务器，so that 我能访问内网服务而不必知道内网地址。
3. **跨 NAT 的 ping/tracert 显示转换路径**：As a 网络学习者，I want tracert 经过 NAT 设备时显示转换前后的 IP 变化，so that 我能直观理解 NAT 如何改写报文地址。
4. **NAT 设备上的 ACL 仍生效**：As a 安全运维，I want NAT 设备上的 traffic-filter ACL 仍能按（转换前/后的）IP 正确 permit/deny，so that NAT 不会绕过防火墙策略。

---

## 3. 需求池

### 已有（本期不重做）

| 标记 | 内容 | 位置 |
|---|---|---|
| [已有] | NAT 数据结构：`NATConfig` / `NATServer` / `NATAddressPool` / `NATOutbound` / `NATTable` | `state.go:79-212` |
| [已有] | 命令行解析：`nat address-group` / `nat outbound` / `nat server` | `parser.go:977 / 1010 / 1048` |
| [已有] | `display nat` / `display nat server` / `display nat address-group` 输出 | `parser.go:3036-3077` |
| [已有] | 设备类型白名单：nat 仅 L3 设备（router/L3Switch/firewall） | `capabilities.go:63` |

### P0（本期核心）

- **[P0 新增] `evaluateNATACL` 真实地址转换（纯函数）**：
  - **出向（`nat outbound`）**：当报文 `SrcIP` 命中某条 outbound 的 ACL 匹配域时，改写 `flow.SrcIP` → 公网地址；**Easy IP** 取该 NAT 设备出接口 IP，**address-group** 取地址池 `StartIP`（多 IP 取首 IP，见待确认 #3）。
  - **入向（`nat server`）**：当报文 `DstIP` 命中某 `NATServer.GlobalIP` 时（协议/端口本期占位忽略），改写 `flow.DstIP` → `InsideIP`。
  - 返回**改写后的 `PacketTuple`**（而非 Decision），无副作用、不写 state、不碰引擎、可单测。
- **[P0 新增] 接入 `EvaluatePathACL` 的 TODO 点**（`acl_eval.go:183-184`）：路径上某设备携带 NAT 配置时，在该设备处改写 `flow`（出向改 `SrcIP` / 入向改 `DstIP`），后续设备的 ACL 评估与可达性使用转换后 IP。NAT 与 ACL 的先后顺序按「待确认 #1」拍板实现。

### P1

- **[P1 新增] ping/tracert 跨 NAT 边界可达性正确**：
  - 外网 ping nat server 公网 IP → 解析到内网设备，tracert 经过 NAT 设备并显示 InsideIP；
  - 内网 ping 公网目标经出向 NAT → 源 IP 显示为 NAT 公网地址；
  - 复用 P1-C 的 `aclPreCheck` / `ComputeL3Path` / `ResolveSourceIP` 路径推导，在 NAT 设备处改写 `flow` 后继续推导。

### P2（诚实占位 / 标注，非功能）

- **[P2] 诚实占位注记**：lite 引擎下 NAT 转换输出追加「模拟转换，非内核级真实 NAT」注记，口径与 `aclSimNote()` 一致；本期仅标注，不实现会话级真实 NAT。

---

## 4. 行为 / UI 设计要点（CLI 输出如何体现 NAT 转换）

- `display nat*` 系列：本期**无变化**（已有充分输出）。
- ping / tracert 输出：跨 NAT 时建议在 NAT 设备那一跳显示地址改写痕迹（如 `203.0.113.1 (NAT→192.168.1.100)` 或转换后 InsideIP），并追加 lite 诚实占位注记。
- **前端（诊断面板 / CLI 终端）：本期前端无变更**。NAT 转换仅在 CLI 文本与 `sim` 结果语义层体现；不新增 `diagnosticPing`/`diagnosticTraceroute` 响应字段（与 P1-C `blockedBy` 不同，本期不扩展 API 响应体）。

---

## 5. 验收标准（可测）

- **AC1**：配置 `nat server` 后，从外网设备 `ping <公网IP>` 可达内网服务器；`tracert <公网IP>` 经过 NAT 设备并显示转换后的 InsideIP。
- **AC2**：内网设备带 `nat outbound`（Easy IP）`ping <公网目标>`，路径评估中源 IP 显示为 NAT 设备出接口公网 IP。
- **AC3**：address-group 模式 `nat outbound`，源 IP 改写为地址池 `StartIP`（首 IP）。
- **AC4**：NAT 设备上的 traffic-filter ACL 仍按拍板顺序（转换前 / 后 IP）正确 permit / deny。
- **AC5**：`evaluateNATACL` 单测覆盖——Easy IP 改写、address-group 首 IP 改写、nat server DstIP 改写、无匹配不改写、纯函数无副作用（多次调用结果一致、不改 state）。
- **AC6**：lite 引擎下跨 NAT 输出带「模拟转换，非内核级真实 NAT」注记。

---

## 6. 待确认问题（交架构师拍板）

1. **NAT 与 traffic-filter ACL 先后顺序**：先 NAT 后 ACL，还是先 ACL 后 NAT？本仿真选哪种简化更符合 VRP 且实现可行？（直接决定 `EvaluatePathACL` 中「改写 flow」与 `EvaluateDeviceACL` 调用的相对位置）
2. **nat server 与 nat outbound 的方向触发条件**：两类转换在路径评估中的触发方向分别是什么？（预期：出向设备触发 outbound `SrcIP` 改写；入向设备触发 server `DstIP` 改写，需确认）
3. **地址池多 IP 策略**：本期取首 IP（`StartIP`）还是模拟轮询？建议最小可行 = 取首 IP + 诚实占位注记。
4. **转换后诚实占位口径**：沿用 `aclSimNote()` 文案（lite：「模拟转换，非内核级真实 NAT」），需确认注记文案与在 CLI 输出中的统一落点。

---

## 7. 不在本期范围

- NAT 会话表老化 / 状态化 PAT 端口级转换；
- ALG（应用层网关，FTP / H.323 等协议载荷地址修正）；
- NAT 与路由复杂交互（`PBR` 联动、`NAT` 与 `BGP` 重分布）；
- nat server 的端口级匹配（本期端口占位忽略，仅按 IP + 协议）；
- 重新实现 NAT 数据结构 / 命令行解析 / `display` 输出；
- 前端诊断面板新增 NAT 字段。
