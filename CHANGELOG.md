# Changelog

本项目所有重要变更记录于此。格式参考 [Keep a Changelog](https://keepachangelog.com/)，版本里程碑见 `ROADMAP.md`，功能细节见 `docs/ensp-lab_manual.md` 与 `docs/vxlan_verification_report.md`。

> **发布状态注记（2026-08-19 更新）**：`v0.12.0` 已于 2026-08-19 发布——tag `v0.12.0` 指向 `9a018f9`，并已 push 至 GitHub 远端。v0.11.1 技术债修复（`prefixToSubnet` / `display interface` mask / F1 依赖升级）未单独打 tag，已并入本次 v0.12.0 发布。上一版本 `v0.11.0`（tag → `7cecdcb`）`/version` 的 `stale` 已解除；`v0.10.0` 源码从未入库（其功能已含于 v0.11.0 全树），故未补打该 tag。

## [Unreleased]

> 记录 v0.12.1 之后的增量（2026-08-23 ~ 08-24）。

### Added
- **网闸（GAP / 安全隔离网闸）设备**（commit `1698ba3` + `7fb4a4a`）：`gap` 设备类型 + 前端 `[GAP]` 图标 + 连线约束（GAP↔Spine/PC/Server）+ CLI 三件套（`gap` 视图 → `channel`/`policy` 子视图：`mapping tcp A:B <-> C:D`、`permit source X dest Y`、`enable|disable`；`display gap channel|policy|statistics`，统计诚实占位 `-`）+ 补全支持（`displayParamSpecs["gap"]`、视图关键字表）+ 单测 6 用例。仿真语义：未配通道完全隔离（与防火墙本质区别），`mapping`+`enable` → `Up`。
- **示例拓扑**：`lab13-gap`（网闸摆渡）、`lab14-bigdata`（云大数据中心多安全域 16 设备，commit `9fe514b`）、`lab15-vxlan-dc`（综合数据中心 VLAN+VXLAN+网闸 16 设备，commit `296cd11`）、`gap-test`（开发验证残留，待清理）。
- **设备库面板新增网闸选项**（commit `9fe514b`）：`getDeviceTypes` API 补 `GAP`（面板由该 API 驱动）。
- **lab14/lab15 节点布局防缠绕优化**（commit `8bbe92a`）：区域分组 + 从左到右分层，消除倒走连线与长斜线交叉。

### Fixed
- **stale 误报（开发副本构建）**（commit `7d891b2` + `522532b` + `1d920e8` + `6b78eb9`）：无 `.git` 目录的开发副本（ensp-lab 目录）构建后 `/version` 误报 `stale=true`。根因：git 命令解析到父仓库（`D:/Projects/Go`）→ 注入父仓 commit + dirty。修复：`build.ps1` 仅当仓库根匹配时注入 git 信息；`buildinfo` 规则 4 在 Commit 未注入（dev 构建）时跳过工作树检查；`sameRepoRoot` 归属判断。
- **stale 误报（data/ 数据改动）**（commit `e853909` + `ee45a1a` + `6b78eb9`）：用户在 UI 改拓扑后 autosave 落盘 `data/*.json`，git status 变脏 → 误报 stale。修复：规则 4 忽略 `data/` 目录改动（数据变更≠源码变更）；新增 `gitOutputRaw` 保留 porcelain 前导空格（`gitOutput` 的 TrimSpace 破坏固定列导致路径偏移）。
- **依赖树损坏修复**：`node_modules` 损坏导致 `tsc` 找不到 / `picomatch` 缺失 → 删 `node_modules`+`package-lock.json` 重装，lockfile 更新入库（commit `1d920e8`）。

### Changed
- **目录收口**：`ensp-lab-main` 重建为独立 git 仓库（原 worktree 链接随 ensp-lab/.git 移除失效），并重命名为 `ensp-lab` 为唯一工作区；src 下冗余目录清理（bak/backup/v090 删除，回收站可恢复）。
- **拓扑数据首次入库**（commit `522532b`）：`data/*.json`（13 拓扑）从 gitignore 豁免强制加入版本管理，配合 stale 规则 4 的 data/ 忽略不再污染 `/version`。
- 前端依赖：`vite` ^5.4.10 → **^6.4.3**（dev server CVE 修复），`esbuild` overrides ^0.25.0；`npm audit` 0 vulnerabilities（仅 devDeps）。

### Dependencies

- **frontend 依赖漏洞清零（`npm audit` 0 vulnerabilities）**：`vite` ^5.4.10 → **^6.4.3**（修复 `<=6.4.2` 三个 dev server CVE：`.map` 优化依赖路径穿越 GHSA-4w7w-66w2-5vf9、Windows launch-editor NTLMv2 泄露 GHSA-v6wh-96g9-6wx3、`server.fs.deny` 绕过 GHSA-fx2h-pf6j-xcff）；`esbuild` 经 `overrides` 升至 **^0.25.0**（修复 GHSA-67mh-4wv8-2f99 dev server 跨源读取）；`nanoid`/`postcss` 随 `npm audit fix` 消解。全部为 devDeps，不影响 `go:embed` 生产产物。Node 22（本地与 CI）均满足 vite 6 engines。

## [v0.12.1] - 2026-08-20

> CLI 参数级补全 + `?` 就地帮助热键增量版本。改动均已通过 `go vet ./...`（清零）、`go test ./...`（全绿）与 Playwright 浏览器端到端复验（`dis aaa ?` / `dis interface ?` 浮层实测）。功能细节见 `internal/cli/paramspec.go` 与 `docs/ensp-lab_manual.md`。

### Fixed
- **CLI Tab 补全前端从未接线**：后端 `cli.Complete` + `POST .../cli/complete` 端点 + 前端 `cliCompleteClient.ts` 早已就绪，但 `CliTerminal.tsx` 的 `onKeyDown` 一直缺 Tab 分支、也无候选浮层，导致浏览器里按 Tab 毫无反应（默认跳焦点）。现补齐：
  - Tab 触发 `requestCliComplete`（仅计算、零命令提交）；单候选自动补最后 token + 尾随空格，多候选计算最长公共前缀续补并弹出候选浮层（方向键移动高亮、Enter 确认、Esc 关闭、再次 Tab 循环高亮、点选亦可）。
  - 补全严格按当前视图（`user`/`system`/`interface`/`aaa`/`bgp`/`acl`/`vty`/`dhcp-pool`/`isis`/`mst-region`/`mlag`）给出候选，无越权暗示（如 `user` 视图 `sy`→`system-view`，`system` 视图 `sy`→`syslog`/`sysname`）；`dis interface <name>` / `system: interface <name>` 补全真实接口名。
  - 经 Playwright 端到端复验：`dis aa`→`dis aaa`、`dis a`→浮层 `[aaa,acl,arp]`、`interface` 视图 `de`→`[delay,description]`（链路质量命令可见）、`sh`→`shutdown`、浮层 Enter 确认高亮候选→`delay `。

### Added
- **参数级 Tab 补全（CLI 补全从「命令关键字层」下沉到「参数层」）**：此前 `cli.Complete` 只补全到命令关键字，`dis aaa `（尾随空格）后本应列出二级参数却返回空集。现新增统一数据模型 `ParamSpec` / `CommandGrammar`（`internal/cli/paramspec.go`）+ `completeParams` 算法，并落地三个代表命令：
  - `display aaa [ configuration | statistics | online-user | local-user <user-name> | domain <domain-name> ]`：二级子命令 + 真实用户名/域名（StateProvider 源自 `aaa:local-user:`/`aaa:domain:` 命名空间，与键碰撞红线一致的精确前缀解析）。
  - `display ip [ interface | pool | routing-table ]` 及嵌套 `display ip interface <if-name>`。
  - `display interface [ brief | <if-name> ]`（brief 关键字与真实接口名共用同一混合槽位）。
  - 配置视图首 token 之后亦接入同一算法：`system` 视图 `interface <if-name>`、`aaa` 视图 `local-user <user-name>` 补全真实实例名。
  - 执行器同步：原 `display aaa` 在 parser 巨型 switch 中忽略二级参数，现委托 `regAaaDisplay` 按 `arg1` 路由（`configuration`/`local-user`/`domain` 复用既有渲染，`statistics`/`online-user` 走 `buildAAAStatsDisplay` 诚实占位，字段恒 `-`），与补全候选严格一致，受 `TestCompletionParamNoDrift` 锁死。
  - 全程只读、零副作用（`completeParams` 不执行命令、不改 `CLIState`）；`<cr>` 语义由前端「Enter 即执行」自然承载，前端浮层无需改动。

### Added
- **VRP 风格 `?` 就地帮助热键**：此前 `?` 在终端里只是普通字符，无法像真机那样就地列出可接参数。现新增 `onKeyDown` 的 `?` 分支 + `doHelp`：按下 `?` 时拦截该字符、在输入尾补一个空格把光标推进到下一 token 位置，复用同一 `POST .../cli/complete` 端点（单一事实源、零副作用）弹出候选浮层，显示「这一位置可接的所有参数」。与 Tab 的区别：`?` 只“显示”不“替写”——即便只有一个候选也只弹浮层、不自动续补。`dis aaa ?` → 浮层显示 `[configuration, statistics, online-user, local-user, domain]`；`dis aaa local-user ?` → 显示已配用户名；`system interface ?` / `aaa local-user ?` 同理显示真实实例名。纯前端改动，后端 `cli.Complete` 与 `completeParams` 契约不变。

## [v0.12.0] - 2026-08-19（已发布，tag `v0.12.0` → `9a018f9`）

> 链路质量特性版本。改动均已通过 `go vet ./...`（清零）、`go test ./...`（全绿）与 `./build.ps1`（含 embed 前端）验证，并经 Playwright 浏览器端到端复验。功能细节见手册 4.8.6。

### Added
- **链路质量配置（接口视图 `delay` / `loss`）**：
  - **CLI 三件套**：接口视图 `delay <1-1000>` / `loss <1-100>` 配置、`display link-quality` 只读渲染（Measured / Jitter 运行态诚实占位恒 `-`）、`undo delay` / `undo loss` 回落、`save` 差异落盘与 `reload` 字节级复现、`displayRegistry` / 补全接线。
  - **引擎校准**：Ping 按逐跳链路延迟累加往返 RTT——修复首跳延迟被硬记为 0 的缺陷（两节点直连拓扑 `delay` 此前完全不生效）；`TestTracePathFirstHopQuality` / `TestPingAccumulatesLinkDelay` 锁死。
  - **端到端累积丢包模型**：`P = 1 - ∏(1 - p_i/100)`，逐跳独立、包级随机判定；丢包率 0 时短路不消耗随机源（`TestPingZeroLossNeverConsultsSampler`）；随机源可注入保证单测确定性。
  - **api 同步**：按命令触发 `syncLinkQualityForInterface` 落到 `topology.Link.Delay/Loss`；两端配置取较大值（确定性、与下发顺序无关、悲观）；未连线接口与无关命令不误清 REST `PUT /api/link` 设置。
  - **e2e 实测**：`delay 20` + `loss 25` → Ping RTT≈45ms / 25% 丢包；`undo` 后回落基线（1–5ms / 0% 丢包）。

### Fixed
- **技术债：`prefixToSubnet` 有类近似**（`internal/cli/tools.go`）：此前仅分 /8 /16 /24 /32 四档，`/30` 误算成 `255.255.255.0`；改为按位精确计算，任意前缀长度（0–32）均正确（`/30 → 255.255.255.252`）。`TestPrefixToSubnetExact` 锁死。
- **技术债：`display interface <if>` 掩码重复渲染**：`interface:<if>:ip` 配置键以 `"IP MASK"` 空格形态存储，原 `case "ip"` 把整串当作 IP、随后显示又补 `/Mask`，导致 `Internet Address is` 行输出 `10.0.0.1 255.255.255.252/255.255.255.252`（物理口亦受影响；GRE 因 save→reload 走空格形态不触发）。现拆出 IP 与 Mask 分别填充（`display_registry.go` 与 `parser.go` 两份 `display interface` 实现同步修复）。`TestDisplayInterfaceIPMaskNoDuplicate` / `TestDisplayInterfaceIPPrefixMaskNoDuplicate` 锁死。

### Security
- **F1 依赖安全升级（build(deps) `a2d7b78`）**：`go` 指令 1.26.5→1.26.6，随工具链修复 3 个实际被调用的 std-lib 漏洞（crypto/tls `GO-2026-6090` / net/http `GO-2026-6089` / encoding/asn1 `GO-2026-5972`）；`golang.org/x/*` 升补丁版（x/net `v0.56.0` / x/crypto `v0.53.0` / x/sys `v0.46.0` / x/text `v0.39.0`）。govulncheck 复核 `Your code is affected by 0 vulnerabilities`，仅余 `GO-2026-5932`（x/crypto）无上游修复版、代码未调用，仅监控。

## [v0.11.0] - 2026-08-18（已发布，tag `v0.11.0` → `7cecdcb`）

> 本版本于 2026-08-18 合并发布；合并点 `b67e68b`、共同祖先 `ca8aa87`。以下改动均已通过 `go vet ./...`（清零）、`go test ./...`（全绿）与 `go build`（含 embed 前端）验证。

### Added
- 文档优化：开发者指南新增 7.5 开发机制（构建与版本 / CLI 三件套 / 引擎模式 / 质量门禁 / 安全约束 / 提交发布纪律）；术语基线、错误码与安全合规章节筹划。

### Fixed
- **P0R1 `executeCLI` 数据竞争**：入口改为 `t.Clone()` 工作副本后再写 `DeviceConfig`/`Interfaces`，消除与后台 `StartAutoSave`（每 5s）并发读写共享 map 的不可恢复 fatal；`TestP0R1CLIMustNotMutateSharedTopologyInPlace` / `TestP0R1CLINonTerminalDeviceAlsoUsesClone` 锁死。
- **F11 CLI 补全 endpoint**：`POST .../cli/complete` 仅计算候选不执行；回归 `TestCompletionNoDrift`。
- **F2 限流器淘汰**：修正 token-bucket 淘汰逻辑。
- **补全表漂移**：CLI 补全候选表与 `parser.go` 实际 case 对齐（无退化）。
- **IPv6 命令 / 显示 / undo 分发接线**：`parser.go` 顶层 `ipv6`/`ripng`/`ospfv3`、display `ipv6`/`ripng`/`ospfv3`、undo 各分支、`buildSavedConfigSnapshot` 挂载全部接到既有 `ipv6_*.go` 三件套，13 个 IPv6 AC 测试由红灯转绿，并经 Playwright 浏览器端到端复验。
- **`display registry` 从未被真正接线（v0.11.0 号称单一事实源却是孤儿）**：`parser.go` 内层 `switch arg0` 补 `case "evpn"` / `case "ndp"` 调 `regEvpnDisplay` / `regNdpDisplay`，`display bgp` 补 `arg1 == "evpn"` 分支调 `buildEVPNBGPDisplay`；此前这三条命令均回 unrecognized command。
- **前端视图提示符缺失 8 个**：`CliTerminal.tsx` 的 `buildPrompt` 补 `aaa` / `aaa-authen` / `aaa-domain` / `vty` / `mlag` / `isis` / `dhcp-pool` / `mst-region`，进入这些视图后提示符不再退化。
- **手册 4.8.4 缺 legend**：IPv6 示例补 legend 块与中文 `ipv6SimNote()` 口径，与其他章节一致。

### Security
- 安全审计（2026-08-12）17 项问题：**V-2 / V-3 / V-5 与 P0-R1 / P0-R2** 已带回归测试修复（详见 `.workbuddy/安全审计与质量报告_真实代码v0.11_修复记录_2026-08-15.md`）。⚠️ **口径更正**：**V-1 / V-4 此前只入库了红灯用例、修复代码并未落地**（`go test ./...` 实测为 FAIL），2026-08-17 补齐：
  - **V-1 context / cancelFunc 泄漏（gosec G118）**：`nsxEngine` 的 `e.cancelFunc` 此前只被赋值、全文件从未调用，每轮 `Start()` 都在 `context.Background()` 上挂一个永不释放的子节点。修复：`Stop()` 持锁取出 `cancelFunc`、置 nil、锁外调用（天然幂等）；`Start()` 派生新 ctx 前先取消上一轮遗留句柄；`NewNSxEngine` 的 `build` 失败路径补 `cancel()`。`internal/sim/engine_ctxleak_test.go` 5 个用例转绿。
  - **V-4 指标负耗时回绕**：`metrics.Collector.RecordRebuild` 直接 `uint64(dur.Nanoseconds())`，负耗时（时钟回拨）会回绕成天文数字并永久污染累计值。修复：负值夹断为 0。`internal/metrics/metrics_negdur_test.go` 4 个用例转绿。
- **P0-R3 HTTP 超时加固（Slowloris）**：`cmd/server/main.go` 设 `ReadHeaderTimeout=10s` / `IdleTimeout=120s`；刻意不设 `WriteTimeout`（会掐断 `/api/sim/events` SSE 长连接）与 `ReadTimeout`（请求体已由 10MB 限制兜底，叠加读超时可能误伤大拓扑导入）。此加固曾因 `cmd/server/main.go` 未被 git 跟踪而在合并中丢失，已恢复。

### Build
- **v0.11.0 全树入库**：将只存在于孤儿 worktree 的 v0.11.0 源码导入分支（`f81e209`），以共同祖先 `ca8aa87` 与主线三方合并（`b67e68b`），自动并回 BUILD-01（`dd9aec1`：双入口构建 + `internal/buildinfo` 版本注入 + 运行期 stale 自检 + 修复 `.gitignore` 未根锚定误伤 `cmd/server`）与 VXLAN 标注空白修复（`cdc1e0e`）。3 处冲突（`CONTRIBUTING.md` / `build.ps1` / `docs/ensp-lab_manual.md`）均保留主线更准确内容 + 孤儿版关键注释。

### P2 CLI 增强明细（特性完成于 2026-08-11，纳入本次 v0.11.0 发布）

P2 CLI 增强，统一命令注册与补全体系，经独立 QA 验收（NoOne，零源码缺陷）。

### Added
- **P2 CLI 显示统一注册表 `display_registry`**：`internal/cli/display_registry.go` 将 `display`/`dis` 巨型 switch（约 1300 行）收敛为单一事实源 `displayRegistry`，每个原 case 逐字迁移为 `regXxxDisplay(state, cmd, arg0, arg1)`（统一 `reg` 前缀，禁与既有 `buildXxxDisplay` 重名）；新增 display 命令只改一处。`TestDisplayRegistryDispatch` 锁死迁移无回归。
- **Tab 补全**：后端 `cli.Complete(state, tokens)` 计算候选（注册表 key + 视图感知关键字表 `userViewCommands`/`systemViewCommands`/... + 真实接口名）；前端 `CliTerminal.tsx` Tab 分支 + 候选浮层，零命令提交；`POST .../cli/complete` 仅计算不执行；`TestCompletionNoDrift` 锁死候选表每个 token 必为 parser 实际 case 标签。
- **历史分层**：localStorage（UI/历史，默认去重 + 上限 200）与 DeviceConfig（拓扑，save 落盘，`RecordHistory`→`SerializeToDeviceConfigData`→`LoadFromDeviceConfigData`，FIFO 256）两层共存，注释澄清边界。
- **EVPN / NDP 只读诚实占位**：`dis evpn` / `dis bgp evpn`（`regBgpDisplay` 的 `arg1=="evpn"` 分支）/ `dis ndp`，运行态恒 `-` + `evpnSimNote`/`ndpSimNote`；手册补 4.8.5 专章。

### Deprecated
- 早期散落的 `display` 巨型 switch 分支（已收敛至 `displayRegistry`，原 case 标签逐一迁移，无行为变更）。

### Compatibility
- 无破坏性 API 变更；CLI 补全为新增能力，不影响既有命令语义；`display` 输出格式与该注册表前逐字一致。

## [v0.10.0] - 2026-08-11

P2 IPv6 支持（course 43-44），延续「纯函数仿真评估 + 诚实占位」路线，经独立 QA 验收（NoOne，零源码缺陷）。

### Added
- **P2 IPv6（course 43-44）**：`internal/cli/ipv6_eval.go` / `ipv6_cmd.go` / `ipv6_display.go` 三件套；全局 `ipv6` 使能 + 接口 `ipv6 enable` / `ipv6 address` / `ipv6 route-static` + RIPng（course 33 形态）/ OSPFv3（course 43 形态）华为真机形态；DeviceConfig 精确前缀键——`ipv6:enabled`、`interface:<if>:ipv6-enable`、`interface:<if>:ipv6-address`、`ipv6:route-static:<prefix>:<nexthop>`、`ipv6:ripng:<pid>:enabled`、`interface:<if>:ripng-<pid>-enable`、`ipv6:ospfv3:<pid>:enabled`、`interface:<if>:ospfv3-<pid>-area`；禁用 `strings.Contains(k,"ip"/"ipv6")` 子串碰撞；`display ipv6 interface brief` / `display ipv6 interface <if>` / `display ipv6 routing-table` / `display ripng` / `display ospfv3` 真实渲染；current-configuration 差异值口径（`buildSavedIPv6InterfaceConfig` / `buildSavedIPv6RouteConfig`） + save→reload 字节级贯通；手册 4.8.4 专章 + PRD/设计文档入库。
- **诚实占位**：IPv6 协议状态 / ND / DAD / 统计恒 `-`；link-local 仅真实 MAC 才 EUI-64 派生，否则 `SimulatedLinkLocal`；`ipv6SimNote()` 与既有口径一致。

### Deprecated
- 早期 `ipv6` 顶层残桩（直接写 `ipv6:enabled` + 无子命令派发的旧实现，仅返回 "IPv6 enabled"），由 `applyIPv6*` 精确派发取代。

### Compatibility
- 无破坏性 API 变更；IPv6 为新增能力，不影响 IPv4 路径；与 v0.9.0 配置键（`interface:<if>:ip` 等）共存、互不为前缀误伤（`undo ipv6` 仅清 `ipv6:` 前缀键，保留 `:ip`/`:lag:mode` 等异族键）。

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
- （`prefixToSubnet` 有类近似 / `Internet Address` mask 重复渲染两项技术债已于 `v0.11.1` 修复，见 `[Unreleased]`。）

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
- 既有技术债（非本轮引入，建议单独 ticket；**已于 `v0.11.1` 修复**）：`prefixToSubnet`（`internal/cli/tools.go`）为分类网近似（仅 /8 /16 /24 /32），`/30` 会算成 `255.255.255.0` 而非 `255.255.255.252`；另 `Internet Address is ...` mask 重复渲染（物理口亦受影响）。GRE save→reload 不经此路径（ip 以空格形式存储），不触发。

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
