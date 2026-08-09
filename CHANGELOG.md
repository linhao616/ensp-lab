# Changelog

本项目所有重要变更记录于此。格式参考 [Keep a Changelog](https://keepachangelog.com/)，版本里程碑见 `ROADMAP.md`，功能细节见 `docs/ensp-lab_manual.md` 与 `docs/vxlan_verification_report.md`。

## [v0.9.0] - 2026-08-07

P2 第八项 AAA 本地认证（course 71）纠正式重构，延续「纯函数仿真评估 + 诚实占位」路线，经独立 QA 两轮验收（PASS，零源码缺陷）。

### Added
- **P2 AAA 本地认证 AAA Local Auth（course 71）**：`internal/cli/aaa_eval.go` / `aaa_cmd.go` / `aaa_display.go` 三件套；标准 `[R1-aaa]` 视图 + 方案子视图 `[R1-aaa-authen-<name>]` / 域子视图 `[R1-aaa-domain-<name>]`，`quit` 正确回退层级（子视图→ViewAAA→ViewSystem）；`local-user <name> password cipher` / `privilege level <0-15>` / `service-type`（多值规范化去重）/ `state active|block`；`authentication-scheme` + `authentication-mode local|radius|none`（**`authentication-mode` 改为按视图分派**，复用 VTY 既有逻辑、不新增重复顶层 case，闭合 VTY `authentication-mode aaa` 悬空引用）；`domain` + 方案绑定 + 引用完整性守卫（绑不存在的方案硬拒、删被引用的方案硬拒）；授权 P1 `authorization-scheme` / 计费 P2 `accounting-scheme` 同构扩展。
- **事实源迁移（删技术债）**：删除不落盘的 `state.LocalUsers` / `LocalUser` 结构体与读写路径，全配置改 DeviceConfig 单一事实源 `aaa:local-user:<name>:<field>` / `aaa:authen-scheme:<name>:mode` / `aaa:author-scheme:*` / `aaa:acct-scheme:*` / `aaa:domain:<name>:<field>`（精确前缀 + 精确分段）；`display ssh` 的 Local Users 段改读新事实源 + 确定性排序 + 脱敏。
- **展示与诚实占位**：`display aaa` / `display local-user` / `display domain [<name>]` 真实渲染（用户/方案/域按名称升序）；口令恒脱敏 `****`（且明确声明未实现 VRP 密文算法、明文存本地配置），严禁伪造 `%^%#` 密文串；认证运行态（成功/失败次数、在线会话、计费流量、最后登录时间、访问接受/拒绝）一律 `-` + `aaaSimNote()` 注记（lite/full 两态），绝不编造数字/时间/`Online`/`Never`；`display current-configuration` 新增 `aaa` 块（缺省值不冗余输出）+ save→reload 字节级贯通。
- **键碰撞红线（最高危）**：`aaa` 是合法十六进制串，禁用 `strings.Contains(k,"aaa")` / `strings.Contains(k,"domain")` 模糊扫描，全部走 `aaaLocalUserKey` / `aaaSchemeKey` / `aaaDomainKey` / `aaaKeyPrefix` 精确 helper；端口安全粘滞 MAC 键（`interface:...:port-security-sticky-learned:00e0-fc12-0aaa` / `aaaa-bbbb-cccc`）不被 `collectAAALocalUsers` 误判、不被 `undo aaa` 级联清理误删（AC13 专项断言锁死）。
- **能力守卫**：配置命令按 `l3Devices()` 在分支内守卫（PC/Server/二层 Switch 拒绝），`display` 只读命令任意设备可读空态；`capabilities.go` 零改动。

### Fixed
- BUG-QA-01（AC7⑥）：`local-user` 缺参（`local-user` / `local-user <name>` / `local-user <name> privilege` / `local-user <name> privilege level`）原返回 `unrecognized command`，改为按统一口径回 `usage:` 文案（接上既有声明但此前零使用的 `ErrLocalUserUsage` / `ErrPrivilegeUsage` 常量）。

### Security
- AAA 为纯函数仿真评估，lite 引擎对「真实登录握手 / RADIUS 协议交互 / 计费采集 / 在线会话」标记为诚实占位（恒 `-`，不编造统计）；口令展示层脱敏 `****`，明文仅存于本地 JSON 配置文件并如实声明；**键碰撞红线**——精确前缀匹配，避免误伤端口安全粘滞 MAC 键（已单元测试锁死）。

### Known Issues
- OBS-2：本期新增顶层 `case "state"` 会捕获任意视图下裸 `state` 命令（当前无冲突命令，影响面小），后续单独确认是否有意。
- 既有技术债（独立 ticket）：`prefixToSubnet`（`internal/cli/tools.go`）分类网近似（`/30` 误算 `255.255.255.0`）；`Internet Address` mask 重复渲染。

## [v0.8.0] - 2026-08-07

P2 GRE 隧道（course 69）纠正式重构，延续「纯函数仿真评估 + 诚实占位」路线，经独立 QA 验收（NoOne，零源码缺陷）。

### Added
- **P2 GRE 隧道 GRE Tunnel（course 69）**：`internal/cli/gre_eval.go` / `gre_cmd.go` / `gre_display.go` 三件套；Tunnel 接口视图配置 `tunnel-protocol gre` → `source`/`destination`（IP 或接口名双形态，原样保存）/ `gre key`（0–4294967295，未配显示 `-`）/ `keepalive period <p> retry-times <r>`（仅配置态，缺省 p5/r3）；DeviceConfig 单一事实源 `interface:<if>:tunnel-protocol` + `interface:<if>:gre-{source,destination,key,keepalive-period,keepalive-retry}`；三态守卫（接口视图 / l3Devices() / GRE 前置，未 `tunnel-protocol gre` 直接配源/目的被拒且不写键）；`undo tunnel-protocol gre` 级联清 `interface:<if>:gre-*` 精确前缀；`display gre tunnel`（重定向自 `display gre`）/ `display interface Tunnel<x>` 真实渲染；current-configuration 差异值口径 + save→reload 贯通（`buildSavedGREConfig` 独立通道 + `savedInterfaceIPLine` 还原 `ip address` 行）。
- **纠正式重构（删除技术债）**：移除早期自创系统视图 `gre <name> <src> <dst>` 命令与 `state.GRE` / `GREConfig` 字段（只写不读、形态不符 VRP）；删除死代码 `protocol.AddGRETunnel`；`display gre` 重定向至 `display gre tunnel`。

### Fixed
- GRE 配置 save→reload 丢 `ip address` 行：因 `LoadFromDeviceConfigData` 不重建 Tunnel 逻辑口进 `state.Interfaces`，reload 改走 `buildSavedGREConfig` 独立通道读取 `interface:<if>:ip` 还原（AC2 字节级一致）。

### Security
- GRE 为纯函数仿真评估，lite 引擎对「真实 GRE 数据平面」标记为诚实占位（运行时字段恒 `-`，不编造统计/MTU）；`greSimNote()` 与 `dhcpRelaySimNote()` 口径一致；**键碰撞红线**——采用精确前缀/后缀匹配，禁用 `strings.Contains(k, "gre")`，避免误伤 H3C `Bridge-Aggregation` 聚合口键（已单元测试锁死）。

### Known Issues
- 既有技术债（非本轮引入，建议单独 ticket）：`prefixToSubnet`（`internal/cli/tools.go`）为分类网近似（仅 /8 /16 /24 /32），`/30` 会算成 `255.255.255.0` 而非 `255.255.255.252`；另 `Internet Address is ...` mask 重复渲染（物理口亦受影响）。GRE save→reload 不经此路径（ip 以空格形式存储），不触发。

## [v0.7.0] - 2026-08-07

本轮延续 P2 协议特性累积发布（续 v0.6.0），继续「纯函数仿真评估 + 诚实占位」路线，全部经独立 QA 两轮验收。

### Added
- **P2 链路聚合 Eth-Trunk（course 63）**：`internal/cli/lag_eval.go` / `lag_cmd.go` / `lag_display.go` 三件套；DeviceConfig 单一事实源 `interface:<iface>:lag:<field>`；纯函数 `EvaluateLAG` + `lagSimNote()` 诚实占位；根治「幽灵组」（`Bridge-Aggregation` 残留）save→reload 缺陷；真实成员接口聚合 / 负载分担 / 展示。
- **P2 DHCP 中继 DHCP Relay（course 27）**：`internal/cli/dhcp_relay_eval.go` / `dhcp_relay_cmd.go` / `dhcp_relay_display.go` 三件套；`dhcp select` 从系统视图迁移至接口视图并删除只写不读死字段 `DHCPSelectMode`；DeviceConfig 单一事实源 `interface:<iface>:dhcp-select` + `interface:<iface>:dhcp-relay:<field>`；三层守卫（接口视图 / l3Devices() / relay 前置）、三态互斥级联清理（精确前缀，不误伤 `dhcp-pool`）；诚实占位（转发统计 6 字段恒 `-`，Source IP 未配恒 `-` 不推导主 IP）；`display dhcp relay` 只读、任意设备可读；current-configuration 差异值口径 + save→reload 贯通。

### Fixed
- `display current-configuration` VLAN 块按 vlan-id 升序输出（修正既有 map 随机遍历导致的快照非确定性，保障 AC7 字节级一致）。

### Security
- 链路聚合 / DHCP 中继均为纯函数仿真评估，lite 引擎对「真实转发」标记为诚实占位（非内核级真实转发），避免误导。

## [v0.6.0] - 2026-08-07

本轮为 P2 协议特性累积发布（自 v0.5.0 起落地的 NAT / 端口安全 / VRRP / STP·RSTP·MSTP），延续「纯函数仿真评估 + 诚实占位」路线，全部经独立 QA 两轮验收。

### Added
- **P2 NAT 真实过滤（course 38）**：`internal/cli/acl_eval.go` 填 `evaluateNATACL` 空桩，复用 P1-C ACL 评估器；`applyNAT` 替代原空桩 + `EvaluatePathACL` 接线 + `ComputeL3PathNAT`；tracert/ping 显示接入与 `natSimNote` 诚实占位（数据源未接入时明确提示，不编造）。
- **P2 端口安全（course 49）**：真实 MAC 准入纯函数 `EvaluatePortSecurity` + `simulate frame` L2 触发 + `portSecSimNote()` 诚实占位；种子 MAC Type 归一化为小写 `static`。
- **P2 VRRP（course 60/61）**：移除 `state.VRRP`，DeviceConfig 单一事实源 `interface:<iface>:vrrp:<vrid>:<field>`；纯函数 `EvaluateVRRP` + `vrrpSimNote()` 诚实占位；真实配置 / 选举 / 展示与 P1 track/auth/undo；current-config 认证密钥脱敏；根治 save→reload 丢配置缺陷。
- **P2 STP/RSTP/MSTP（course 55/56/57）**：增量 PRD + 增量设计（**拍板#2 方向更正**为 Bridge ID 比较「Priority 小者胜，同优先则 MAC 小者胜」）；方案 A 落地——移除 `state.STP`/`STPConfig`/`STPPort`，全状态改 `stp:<field>` + `interface:<iface>:stp:<field>` DeviceConfig 键，根治 save→reload 丢配置（P0-1）；`stp_eval.go` 纯函数评估器（`CompareBridgeID`/`EvaluateSTP`/`collectSTPInstances`/`SelectRootBridge`/`stpSimNote`）；`parser.go` 重写 `applySTP`/`buildSTPDisplay`/`buildSavedSTPConfig`/`applyUndoSTP` 并新增 `ViewMSTRegion`。

### Fixed
- STP `undo stp instance <id> root` 参数索引错位（真源 bug，`1544304` 修复）；VRRP current-config 认证密钥脱敏；端口安全种子 MAC 归一化。

### Security
- NAT / 端口安全 / VRRP / STP 均为纯函数仿真评估，lite 引擎对「真实过滤」标记为诚实占位（非内核级真实过滤），避免误导；密钥在 current-config 输出中脱敏。

## [v0.5.0] - 2026-08-06

### Added
- **P1-C Firewall 真实过滤（路线 B）**：`internal/cli/acl_eval.go` 新增纯函数式 CLIState 仿真 ACL 评估器，按设备 ID 从拓扑级 `map[string]*CLIState` registry 逐设备读取 ACL，隐式 `deny any`（未绑定设备放行、绑定但无 permit 匹配则丢弃）；方向模型 `src=outbound` / `transit=inbound+outbound` / `dst=inbound`，沿路径取交集、首 deny 即停；介入 CLI `ping` / `tracert` / `cli.CheckReachability` 与诊断 `blockedBy`。CLIState 为单一事实源，旁路 `protocol.Firewall` / `MatchACL`（加 Deprecated 注释）；NAT 保留空桩（P2）。
- **华为 VRP 实训课程参考索引**：`docs/reference/huawei-vrp-course.md` 逐讲索引（4 部分 ~36 讲）+ ensp-lab 功能覆盖矩阵（已实现/待校验/待开发）+ 安全视角（高级 ACL / NAT / 端口安全 / AAA）优先借鉴。来源百度网盘课程（MCP 无下载能力，以摘要索引）。
- **P1-C 设计文档签名同步**：`docs/p1c-firewall-design.md` §3.1/§3.2/§4.1–§4.3 由单一 `state` 改为 `states` 注册表版本，与 `acl_eval.go` 实际签名对齐。

### Fixed
- QA Round 1 发现真实 Bug（单源 `state` 套全路径致中转/目的 ACL 不生效），引入 registry 透传修复；Round 2 独立回归 NoOne 全绿。

### Security
- ACL 评估器默认 `deny any` 与华为 VRP 语义一致，避免「未匹配即放行」的越权风险；lite 引擎对 ACL 标记为「仿真过滤，非内核级真实过滤」诚实占位。

## [v0.4.0] - 2026-08-05

### Added
- **VRP CLI 仿真广度（P1-F）**：新增 `isis` / `quit-cli` / `vlanif` 引导 / `port-security` / `nslookup` / `http` / `https` / `dns` / `ftp` 命令；ISIS 真实配置（`network` / `import-route`）与 `display isis`；系统视图 `undo`（ospf/vlan/acl/stp/dhcp/bgp/ipv6/**isis**）；`display current-configuration`（VR 风格）、`display bgp peer`、`display diagnostic-information`；`tracert` 真实引擎兜底（无引擎不 panic）。新增 `CLIState.ISISConfig` 及配置全量序列化落盘。
- **P1-F 遗留修补**：`undo isis` 实现；OSPF/BGP 配置在拓扑 reload 后持久化（`ospf:*` / `bgp:*` DeviceConfig 键镜像 + 加载还原块）。
- **真实诊断（P1-D）**：诊断网关 `POST /api/diagnostic/:id/{ping,traceroute,dns}` 与 `GET /api/system/status`；引擎模式按 build-tag 区分（`linux && gont` = full，否则 = lite）；前端诊断接真实 API，移除造假的随机 RTT / 路径 / DNS；Bandwidth / PCAP 改为诚实占位（数据源未接入时明确提示，不编造数字）。
- **Firewall 真实过滤路线定调**：`docs/firewall-route-decision.md` 完成可行性调研与用户拍板（路线 B：CLIState 仿真 ACL 评估器，基础 IP / 协议语义，介入 ping / tracert / 可达性全路径）。本期仅含决策文档，真实过滤实现留待 P1-C。
- **部署与数据**：Dockerfile + `.dockerignore` 单二进制容器部署，CI 新增 `docker-verify` job；新增 12 个实验拓扑 JSON 数据文件；MIT LICENSE。

### Changed
- **安全加固（P0）**：API 写类 handler 全量深拷贝（`Topology.Clone()`）消除数据竞争；CORS 收紧为严格白名单（默认仅 `127.0.0.1` / `localhost` 同源 + `ENS_CORS_ORIGINS` 追加可信源，`AllowHeaders` 仅含 `Origin` / `Content-Type`）；外部诊断接口增加门控；引擎轮询常量化（5ms，支持 `ENS_ENGINE_POLL_MS` 覆盖）并修复同步去抖（R1 / R4）。

### Fixed
- protoSim 多拓扑路由失效（`CheckReachability` 增加 `topo` 入参，零生命周期改动）；若干并发修正与 CLI 历史 / 资源监控稳定性修复；安全相关 5xx / 存储 404 统一为泛化错误响应（细节仅入日志）。

### Security
- 集中输入校验器 `internal/api/validation.go`（TopoID 正则 / 设备类型枚举 / 控制字符拒绝 / `net.ParseIP`·`ParseCIDR` / OSPF Area / ASN / Finite / TopologyPayload 校验）；路径穿越防护（topology id 与导出文件名 `sanitizeForFilename`）；统一错误响应 `clientError`（不向客户端泄露内部细节）；pprof token 守卫（空则自动生成并 Warn）；导出文件名安全转义；CI 安全门禁（SAST / 依赖扫描 / secrets 扫描）。

## [v0.3.0] - 2026-07-22

### Added
- **设备详情浮动窗口（类 eNSP）**：设备的 CLI 终端与配置信息从底部固定面板改为可拖动的浮动小窗。触发方式：双击拓扑中的设备、右键设备选择「查看详情」、或点击设备列表项。窗口内分 CLI / 配置两个 Tab；标题栏可拖动（限制在视口内），支持最小化 / 最大化 / 关闭，右下角可拖拽调整大小。
- **多窗口 + 任务栏**：每台设备一个独立浮动窗口，已打开不会重复；右上角任务栏显示设备名与状态标记（✓ 点击聚焦）。窗口位置与大小通过 `localStorage`（键名 `ensp-lab-windows-<拓扑ID>`）持久化，刷新页面后恢复布局。
- **Ping 测试面板增强**：工具栏「Ping 测试」打开右上角面板，可自由选择**任意源 / 目标设备**（PC/Server/交换机任意组合）、设置探测包数、开启**连续 Ping**（对应 `ping -t`，每秒一次实时累积输出，点击「停止」结束）；本轮结果以 `[时间] 源 → 目标 ✅/❌` 形式记入面板内历史。面板打开时源/目标设备会在画布上显示橙色高亮光环，切换拓扑自动停止并清空。
- **后端 Ping `count` 参数**：`GET /api/topology/:id/ping?src=&dst=&count=` 支持探测包数（默认 4，最大 100），后端循环调用 `eng.Ping` 并聚合 `sent/received/lost/rtt_ms` 与 `round-trip min/avg/max`。

### Changed
- **仿真引擎改为懒启动**：移除前端「启动拓扑」按钮与后端 `POST /api/topology/:id/start` 端点（含 `api.startTopology`、`StartTopologyResponse`）。引擎在首次调用 Ping 或 CLI 时通过 `getOrCreateEngine` 自动创建并 `eng.Start()`，整张拓扑即「上电」运行；删除拓扑时自动释放引擎资源。
- **API handler 拆分重构完成**：`router.go` 仅保留 `NewRouter()` 路由注册，业务 handler 拆分至 `topology_/device_/link_/cli_/annotation_/system_handlers.go`。`internal/api` 构建恢复通过，根目录 `ensp-lab.exe` 为最新构建（含嵌入前端）。

### Removed
- 前端「启动拓扑」按钮、`startTopology` handler、`/start` 路由、`StartTopologyResponse` 类型。
- 集成测试 `TestAPITopologyStartWithDevices`（依赖已删除的 `/start` 端点）；集成测试现为 7 个。

### Fixed
- 连线清单标题重复显示（`ConnectionList` 内部 header 与 `LeftPanel` subhead 叠加，移除前者）。

## [v0.3.1] - 2026-07-23

### Added
- **IP 合法性校验**：新增 `internal/topology/validate.go` 的 `ValidateIPConfig`，在创建拓扑/设备时检测非法 IP（格式错误、网络地址、广播地址等），直接返回 HTTP 400，避免此前延迟到运行时才抛 500。配套单测 `internal/topology/validate_test.go`。
- **低资源稳定性测试报告** `docs/low-resource-test-report.md`：5 个低资源场景（内存 22–50MB、goroutine 无泄漏、无 OOM）全部通过。
- **最小启动配置文档** `docs/min-startup-config.md`：在 `GIN_MODE=release` / `GOMAXPROCS=2` / `GOGC=200` 组合下，文件句柄占用 ↓40%、峰值 CPU ↓22%。

### Changed
- **`dbgSim` 调试输出限流**：为调试日志增加 1 秒滑动窗口限流，根治此前日志洪泛刷爆磁盘（单次可达约 1GB）的问题。配套单测 `internal/sim/dbg_test.go`。

### Fixed
- 创建含非法 IP 的拓扑在运行时报 500 的问题（现已在创建期校验并返回 400）。

## [v0.2.0] - 2026-07

> 里程碑详情见 `ROADMAP.md`。本节仅作摘要。

- VXLAN Spine-Leaf 演示拓扑（`-demo-vxlan` + 前端规划模板）
- 拓扑标注（Annotation）API + 前端画布标注层
- 文件持久化存储（每拓扑一个 JSON，重启自动加载）
- `internal/protocol` 20+ 协议状态模型（OSPF/BGP/VXLAN/ACL/IPsec/STP/LLDP/DHCP/DNS/HTTP/FTP/SMTP/MPLS/PPP 等）

## [v0.1.0] - 2026-07

- 基础框架：拓扑管理（设备、链路 CRUD）、ICMP Ping 功能、RESTful API、Web 前端 UI
