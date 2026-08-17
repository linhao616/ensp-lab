# ensp-lab 全检报告：代码审查 + 安全审计 + QA 测试

**日期**：2026-08-12
**场景**：代码审查 / 安全审计 / QA测试+发布就绪
**参与成员**：产品评审员（代码审查）+ 安全官（STRIDE+OWASP）+ 质量门神（QA）
**执行模式**：⚠️ 本会话 `TeamCreate` 工具不可用，gstack 多 agent 协作降级为「主理人直调」——三项审查由主理人直接执行并汇编（使用 Bash / Read / Grep 与 Playwright MCP 实证，未启用 review / security-pentest-audit / qa 三个 skill 的子审查流水线）。

---

## 📌 TL;DR（执行摘要）
- 整体结论：🟢 通过（可发布；含 1 个须修复的前端功能缺陷）
- 阻塞项：0（无发布阻塞）。严重度分布：🔴 0 / 🟠 1 / 🟡 4 / 🟢 2。
- 质量门实测：`go vet ./...` 清零 ✅、`go build ./...` ✅、`go test ./...` 全绿 ✅（10 个包 ok，本会话未触发 360/Defender 拦截）。
- 三大网络定律修复（P0 源路由/网关、P1 tracert 仅列 L3、乙 100-command 补全）均经代码审查 + 实测验证，「诚实占位不编造」铁律落地。
- 记忆订正：此前记忆称「vet 红灯 + display_registry.go 约 20 处死 return」「go test 被 360 拦截」——**当前实测均不成立**（vet 0 unreachable、display_registry.go 0 个 `return ""`、测试全绿），已订正。

---

## 🎯 核心结论卡片

| 项目 | 内容 |
|------|------|
| Go / No-Go | 🟢 Go（修复 DiagnosticTraceroute 前端缺陷后可立即发布） |
| 严重度分布 | 🔴 0 / 🟠 1 / 🟡 4 / 🟢 2 |
| 关键行动项 | 5 条 |
| 建议负责人 | 寇豆码（前端修复）/ 主理人（记忆订正 + 文档） |

---

## 1. 各成员核心结论（每位 1 段，转述为主）

### 🔍 产品评审员（代码审查）
- **P0 `7ae8f37` `srcHasRouteTo`**：直连子网→`direct`；否则接口网关优先、其次 `DeviceConfig.DefaultGateway`，且网关须落在某直连子网→`gateway`；否则 `no-gateway`/`gateway-unreachable`。逻辑正确、注释完整；Ping/Traceroute 入口如实返回不可达，消除「假可达」，符合诚实占位。
- **P1 `462d892` `isL3Forwarding` + Traceroute 跳点循环**：仅保留具备 L3 转发能力（≥1 带 IP 接口）的设备与目标设备为跳点，纯 L2 交换机透明剔除，跳号重排 1..n。正确落地定律②（L2 不递减 TTL）。
- **乙 `37da2dc`**：`parser.go` 新增 7 个 system 视图 case，与 `completion.go` 的 `systemViewCommands` 七命令逐字一致，受 `TestCompletionNoDrift` 锁死；均为诚实占位（写 `DeviceConfig` 单一事实源），无补全漂移。
- **`display_registry.go`**：实测 0 个 `return ""` 死代码（记忆所述约 20 处已不存在），`go vet` 清零成立。
- 结论：代码质量良好，可合并发布。

### 🛡️ 安全官（STRIDE + OWASP）
- **注入面**：近期提交均为纯 Go 逻辑（无 shell 执行、无 SQL、无 FRR 配置拼接变更），未引入新注入点；FRR 注入防护（`applyOSPFConfig` 等）未被触碰，维持既有防护。`DiagnosticTraceroute.tsx` 的 ID/IP 错配是契约缺陷，非注入类漏洞。
- **路径穿越**：`FileStorage` 路径穿越防护未被近期提交改动，无新风险。
- **诊断外部目标守卫（V-03）**：`diagnostic_handlers.go:293` 默认 `ENS_DIAG_ALLOW_EXTERNAL` 禁用→拓扑外目标 403，是良好的 SSRF/信息泄露边界。残留风险：启用该开关后可探测任意外部 IP（localhost 本地实验工具，风险低，建议文档注明）。
- **localhost 无鉴权**：符合「本地单用户实验工具」定位，可接受；若未来远程访问，须补认证 + CSRF + 传输加密（既有约束已记录）。
- **XSS**：`CliTerminal.tsx` `escapeHtml` 兜底未改动，维持。

### ✅ 质量门神（QA 测试与发布）
- **质量门实测**：`go vet ./...` 退出 0（0 unreachable）；`go build ./...` 退出 0；`go test ./...` 全绿（internal/api、buildinfo、cli、metrics、protocol、sim、topology 均 ok）。本会话**未**触发 360/Defender 拦截 `.test.exe`（记忆所述拦截本会话未复现）。
- **近期修复测试覆盖**：P0 → `traceroute_test.go`（`TestSrcHasRouteTo` / `TestNSx*CrossSubnetNoGateway`）；P1 → `TestNSxTraceroutePath` 期望 `[r2,pc1]`；乙 → `TestCompletionNoDrift`。均存在且绿。
- **e2e（Playwright MCP）**：P0 UI Ping 不可达 + no-gateway 提示 ✅；乙 CLI Tab `i`→`igmp-snooping`/`info-center` ✅（后端补全 `s`→7 候选、`p`/`l`/`g` 同理可命中，七命令均在表）；P1 CLI/REST tracert `[r2,pc1]` L2 剔除 ✅。
- **独立前端缺陷**：`DiagnosticTraceroute.tsx:48` 发 IP 非设备 ID → 403（详见发现表 🟠）。
- **测试假阴**：e2e 中候选浮层未关闭即重触发 Tab 会确认陈旧候选（测试手法问题，非代码缺陷）；建议补一条「关闭浮层后重触发」的 e2e 断言。

---

## 2. 综合审查发现（去重合并后按严重度排序）

| # | 严重度 | 类别 | 位置 | 问题描述 | 建议 | 来源 |
|---|--------|------|------|---------|------|------|
| 1 | 🟠 | 功能/契约 | `frontend/src/components/DiagnosticTraceroute.tsx:48,58` | 诊断 Traceroute tab 发送 `firstIp(devices[dstId])`（IP）而非设备 ID；后端 `isTopologyDevice` 仅按 ID 匹配→所有内网目标被判外部→403「外部目标诊断已禁用」，该 tab 对内网目标完全失效（REST 以设备 ID 调用则正常）。 | 改为 `const target = dstId \|\| (dstIp\|\|'').trim();` 并传 `target`；REST e2e 已证设备 ID 路径正确。 | QA+安全 |
| 2 | 🟡 | 测试稳健 | frontend e2e | 候选浮层开启时清空输入再 Tab 会确认陈旧候选（`CliTerminal.tsx:321`），造成「s 前缀无下拉」假阴。 | 补 e2e：触发前先 Esc 关闭浮层；或输入变化时 `setCandOpen(false)`。 | QA |
| 3 | 🟡 | 并发 | `internal/cli/parser.go`（各 case 写 `state.DeviceConfig`） | `CLIState.DeviceConfig` 为共享 map，若同会话并发执行写命令存在 map 并发写风险（既有设计，非本次引入）。 | 评估 CLIState 是否按请求隔离；或命令执行路径加锁/深拷贝。 | 代码审查 |
| 4 | 🟡 | 可观测 | `internal/sim/engine_nsx.go:1243`（Traceroute）`_, reason := ...; _ = reason` | 丢弃 reason，跨网段不可达时 CLI 渲染缺少「no-gateway」提示（Ping 有，Traceroute 无）。 | 复用 Ping 的 reason→detail 映射，或渲染统一不可达说明。 | 代码审查 |
| 5 | 🟡 | 文档 | `internal/api/diagnostic_handlers.go:293` 外部守卫 | `ENS_DIAG_ALLOW_EXTERNAL=1` 放开后可达任意外部 IP，缺乏用户侧风险提示。 | 在诊断面板/README 注明该开关的 SSRF 含义与仅本地建议。 | 安全 |
| 6 | 🟢 | 记忆订正 | 项目记忆 | 记忆称「vet 红灯 + display_registry.go 约 20 处死 return」「go test 被 360 拦截」——当前实测 vet 0 unreachable、0 死 return、测试全绿，均不成立。 | 已订正 `MEMORY.md`。 | 主理人 |
| 7 | 🟢 | 质量 | 全仓 | `go vet`/`build`/`test` 全绿，三大定律修复经代码 + e2e 双验证。 | 维持 vet 清零门禁，合入前必跑。 | QA |

---

## ✅ 行动清单（具体可执行项）

| # | 行动 | 负责方 | 紧急度 | 期望完成 |
|---|------|--------|--------|---------|
| 1 | 修复 `DiagnosticTraceroute.tsx`：发送设备 ID 而非 IP（发现#1） | 寇豆码 | P1 | 下一提交 |
| 2 | 补 e2e 断言：补全浮层重触发前先关闭（发现#2） | QA | P2 | 本周 |
| 3 | 评估 `CLIState.DeviceConfig` 并发写隔离（发现#3） | 软件工程师 | P2 | 评估后定 |
| 4 | Traceroute 不可达补充 no-gateway 提示（发现#4） | 寇豆码 | P3 | 下个特性周期 |
| 5 | 文档注明 `ENS_DIAG_ALLOW_EXTERNAL` 风险（发现#5） | 主理人 | P3 | 随文档更新 |

---

## ⚠️ 待完善 / 已知局限
- gstack 多 agent 协作因 `TeamCreate` 不可用降级为主理人直调，三位成员产出由主理人依实证汇编，未启用 review / security-pentest-audit / qa 子审查流水线（环境限制）。
- 安全审计聚焦近期提交增量 + 已知约束复评，未做全量威胁建模（项目定位为本地实验工具，攻击面有限）。
- e2e 浏览器截图受 Playwright MCP root 限制无法落盘，验证证据以可访问性树（DOM 快照）与后端 API 直调返回为准。

---

## 📚 成员产出索引
- 主理人直调执行（TeamCreate 不可用）：代码审查依据 `git show 7ae8f37 / 37da2dc / 462d892`；安全审计依据 `diagnostic_handlers.go:280-323`、`parser.go` 增量、`completion.go`；QA 依据 `go vet ./...` + `go build ./...` + `go test ./...` 实测 + Playwright e2e + 补全 API 直调（`POST .../cli/complete` 返回 `s`→7 候选 / `i`→6 候选）。

> 本报告由软件工坊 AI 协作生成，关键决策请由工程负责人复核。
