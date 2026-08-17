# 全检报告：诊断 Traceroute 修复 + 发布就绪（ensp-lab）

**日期**：2026-08-12
**场景**：代码审查 + 安全审计 + QA 测试（全检三件套）
**参与成员**：产品评审员（代码审查）+ 安全官（OWASP+STRIDE）+ 质量门神（QA与发布）
**执行模式**：⚠️ TeamCreate 不可用 → 按 gstack 降级策略，主理人（软件工坊 CEO）单 Agent 直调，**直接执行三件套并汇编**（与上一轮 08:55 全检同一降级路径）。本检在 🟠 缺陷修复之后，承接并扩展 `full-audit-ensp-lab-2026-08-12.md`。

---

## 📌 TL;DR（执行摘要）

- **整体结论**：🟡 **条件 Go** —— 代码质量与安全门控全绿，无阻塞性缺陷；发布前需完成「清工作树（已本次完成）+ 提交 🟠 修复 + 统一 CHANGELOG 口径 + 推送标签」。
- **阻塞项**：🔴 代码/安全缺陷 **0**；🟠 发布卫生项 **1**（工作树残留，**本次已清理完毕**）。
- **三大实证（实测，非印象）**：
  - `go vet ./...` → 退出 0；`go build ./...` → 退出 0
  - `go test ./...` → 全绿（10 包 ok，**本次未触发 360 拦截**）
  - `npx tsc --noEmit` → 退出 0
- **🟠 修复验证**：代码审查判定合规 + 后端设备 ID 路径已有自动化覆盖（`TestDiagnosticTraceroutePath` 用 `dst:"pc2"`）+ 前端与已验证的 `DiagnosticPing` 同范式。
- **下一步**：提交 🟠 修复 → 补 CHANGELOG 条目与口径 → 推标签 → 补 Playwright e2e（需 MCP 会话）。

---

## 🎯 核心结论卡片

| 项目 | 内容 |
|------|------|
| Go / No-Go | 🟡 条件 Go（代码与安全达标，发布卫生待收尾） |
| 严重度分布 | 🔴 0 / 🟠 1 / 🟡 4 / 🟢 4+ |
| 关键行动项 | 5 条（见行动清单） |
| 建议负责人 | 寇豆码（提交 🟠）/ 主理人（标签推送需用户凭据） |

---

## 1. 各成员核心结论

### 🔍 产品评审员（代码审查）
- **🟠 修复判定：🟢 通过（合规）**。
  - `DiagnosticTraceroute.tsx` 第 48 行改为 `target = (dstId||'').trim() || (dstIp||'').trim()`，优先传**设备 ID**，与已通过真 UI 验证的 `DiagnosticPing.tsx` 完全一致；第 58 行 `api.diagnosticTraceroute(topologyId, srcDevice, target)` 传设备 ID；第 7 行移除因改动而变的未用导入 `firstIp`（避免 `noUnusedLocals` 编译失败）。
  - 后端 `resolveDstIP`（diagnostic_handlers.go:72）既收设备 ID 也收 IP 字面量，契约匹配，无回归。
- **质量门实测**：`go vet ./...`=0、`go build ./...`=0、`npx tsc --noEmit`=0。`display_registry` 单一事实源、写类 handler 深拷贝等既有约定未被本次前端改动破坏。

### 🛡️ 安全官（OWASP + STRIDE）
- **🔴 0，安全达标**。
- 🟢 **V-03 诊断门控正确**：`isTopologyDevice(req.Dst)` + `ENS_DIAG_ALLOW_EXTERNAL` 开关（默认禁止外部目标，防网络侦察/DoS 放大）。**本次 🟠 修复"传设备 ID"反而更贴合契约**——内网设备不再被误判为"外部"，而真正的外部 IP 目标仍 403，安全语义不变。
- 🟢 **FRR 注入防护到位**：`applyOSPFConfig`（router.go:785）先 `validateCIDR`/`validateOSPFArea`；`applyBGPConfig`（router.go:843）先 `validateASN`/`validateIP`（每个 neighbor）。全部先校验后写入 FRR 配置，杜绝配置注入/非预期邻居。
- 🟢 **路径穿越防护**：`topoFilePath`（file_storage.go:27）拒绝含 `/ \ ..` 的 ID；写盘走 `tmp + rename` 原子提交。
- 🟢 **CORS V-02 严格白名单**：仅放行 `localhost/127.0.0.1` 同源 + `ENS_CORS_ORIGINS` 显式追加，取代早期"反射任意 localhost 源"宽松策略。
- 🟡 **无鉴权/无 CSRF**：本地单用户设计，风险仅在 `BindAddr=0.0.0.0` 暴露到非 localhost 时；建议在非 localhost 绑定时强制告警或要求认证。

### ✅ 质量门神（QA 与发布）
- **🟢 测试全绿**：`go test ./...` 10 包全部 ok，本次未触发 360 拦截（遇 FAIL 仍须先排除杀软再判回归）。
- 🟢 **后端设备 ID 路径已自动化覆盖**：`TestDiagnosticTraceroutePath`（diagnostic_test.go:214）用 `dst:"pc2"` 走通真实路径发现，证明修复对应的后端契约。
- 🟠（**已清理**）**工作树曾残留测试产物**：`data/p1demo.json`（未删）+ `data/lab01.json`（pc1/pc2 开机态泄漏，未还原）——上一轮 08:55 声称"已清理"却未执行。本次主理人已 `git checkout -- data/lab01.json` 还原 + `rm -f data/p1demo.json`，工作树现仅余 🟠 修复与本次报告目录。
- 🟡 **前端无单测框架**：`frontend/package.json` 无 `test` 脚本、无 vitest/jest。🟠 修复仅靠 `tsc` + 后端 e2e 覆盖，**缺组件级单测与真 UI e2e**。真浏览器 UI 复验（确认 403 消失、浮层交互）需 Playwright MCP，**本会话不可用** → 验证缺口。
- 🟡 **发布口径不一致**：CHANGELOG 标 `v0.11.0 (candidate)`（第 5 行）而 ROADMAP 标 `✅ 已完成`（第 59 行）；且 🟠 修复尚未进入 CHANGELOG/ROADMAP。
- 🟡 **文档过时**：CHANGELOG 第 17 行 Known Issues 称"Playwright 沙箱不可用（未安装浏览器）"——本机已配置 Playwright MCP + 系统浏览器，该声明过时。
- 🟡 **标签未推送**：`git tag` 本地已有 v0.10.0/v0.11.0，但远端最高仅 v0.9.0（沙箱无 git 凭据），待用户在真实终端 push。
- **判定**：🟡 条件 Go。

---

## 2. 综合审查发现（去重合并，按严重度排序）

| # | 严重度 | 类别 | 位置 | 问题描述 | 建议 | 来源 | 状态 |
|---|--------|------|------|---------|------|------|------|
| 1 | 🟠 | 发布卫生 | working tree | 测试残留 p1demo.json + lab01.json 开机泄漏未清理（上轮声称清理却未做） | 已本次 `checkout` + `rm` 清理 | QA | ✅ 已清理 |
| 2 | 🟡 | QA 缺口 | frontend | 无单测框架；🟠 修复无前端单测/真 UI e2e | 引入 vitest；补 Playwright e2e 断言设备 ID | QA | 待办 |
| 3 | 🟡 | 发布口径 | CHANGELOG/ROADMAP | v0.11.0 candidate vs ✅已完成 不一；🟠 修复未入档 | 提交后统一口径 + 补条目 | 产品/QA | 待办 |
| 4 | 🟡 | 文档过时 | CHANGELOG:17 | "Playwright 沙箱不可用" 已过时 | 更新为本机可用 + e2e 指引 | QA | 待办 |
| 5 | 🟡 | 发布流程 | git tags | v0.10.0/v0.11.0 未推远端 | 真实终端 `git push origin v0.10.0 v0.11.0` | QA/用户 | 待办 |
| 6 | 🟡 | 安全加固 | router BindAddr | 无鉴权，绑 0.0.0.0 时无防护 | 非 localhost 绑定时强制告警/要求认证 | 安全 | 待办 |
| 7 | 🟢 | 安全 | diagnostic_handlers.go | V-03 门控正确，🟠 修复贴合契约 | 维持 | 安全 | — |
| 8 | 🟢 | 安全 | router.go:785-912 | FRR 注入防护到位 | 维持 | 安全 | — |
| 9 | 🟢 | 安全 | file_storage.go:27-35 | 路径穿越防护 + 原子写 | 维持 | 安全 | — |
| 10 | 🟢 | 代码 | DiagnosticTraceroute.tsx | 🟠 修复正确、无回归、tsc 绿 | 提交 | 产品 | 待提交 |

---

## ✅ 行动清单（至少 3 条）

| # | 行动 | 负责方 | 紧急度 | 期望完成 |
|---|------|--------|--------|---------|
| 1 | 提交 🟠 修复 `frontend/src/components/DiagnosticTraceroute.tsx`（已在工作树，未提交） | 寇豆码 | **P0** | 即时/本会话 |
| 2 | 补 CHANGELOG 条目（Traceroute 面板 403 修复）+ 统一 v0.11.0 candidate/已完成 口径 | 主理人 | P1 | 提交前 |
| 3 | 补 Playwright e2e：诊断 Traceroute 面板传设备 ID（403 消失） | QA | P2 | 挂 MCP 会话 |
| 4 | 更新 CHANGELOG 第 17 行过时声明 | 主理人 | P2 | 提交前 |
| 5 | 推送标签 v0.10.0/v0.11.0 至远端 | 用户（需 git 凭据） | P1 | 真实终端 |

---

## ⚠️ 待完善 / 已知局限

- **真浏览器 UI 复验缺口**：本会话无 Playwright MCP，🟠 修复的真 UI 行为（403 消失、浮层交互）未实测；验证上限 = 后端设备 ID 路径 e2e 通过 + 前端与 `DiagnosticPing` 同范式 + `tsc` 绿。
- **前端无自动化测试**：组件级回归仅靠 `tsc`，缺 vitest/jest 单测。
- **标签推送依赖外部凭据**：沙箱无 git 凭据，push 须用户在真实终端执行。

---

## 📚 成员产出索引

- 本全检由主理人（软件工坊 CEO）在 TeamCreate 不可用下降级直调执行；三位成员角色产出已并入上文第 1 节，无独立子文件。
- 承接文档：`deliverables/gstack/full-audit-ensp-lab-2026-08-12.md`（08:55 轮，🟠 修复前）。

---

> 本报告由软件工坊 AI 协作生成，关键决策请由工程负责人复核。
