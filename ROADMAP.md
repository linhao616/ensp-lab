# eNSP Lab 开发路线图

## 一、项目愿景

成为 Go 生态下最受欢迎的轻量级网络模拟器，为网络工程师和开发者提供跨平台、API 驱动的网络实验环境。

## 二、当前状态摘要

已实现基础拓扑管理（设备和链路 CRUD）、ICMP Ping 测试、跨平台模拟引擎（ns-x/gont 自动切换）、文件持久化存储（重启自动加载）、RESTful API 和 Web 前端 UI。VXLAN Spine-Leaf 组网已通过 `-demo-vxlan` 参数与前端规划模板落地；拓扑标注（Annotation）层、SSE 仿真事件流、包路径模拟、`/version` 健康检查等能力已具备；`internal/protocol` 已建模 20+ 协议（OSPF/BGP/VXLAN/ACL/IPsec/STP/LLDP/DHCP/DNS/HTTP/FTP/SMTP/MPLS/PPP 等），并在 Linux + gont + FRR 模式下通过 `internal/router` 真实下发 OSPF/BGP。系统支持结构化日志（zap）、CORS、pprof、Delve、staticcheck 等生产级与调试特性。近期前端完成设备详情浮动窗口（类 eNSP）、增强版 Ping 测试面板（任意源/目标 + 连续 Ping + 结果历史 + 拓扑高亮），并移除冗余的「启动拓扑」按钮，仿真引擎改为首次 Ping/CLI 懒启动。

**当前状态**：API handler 拆分重构（`router.go` → `topology_/device_/link_/cli_/annotation_/system_handlers.go`）已完成，`router.go` 仅保留 `NewRouter()` 路由注册，`internal/api` 可正常编译，根目录 `ensp-lab.exe` 为最新构建（含嵌入前端）。

**近期更新（v0.9.0）**：P2 第八项 **AAA 本地认证（course 71）** 交付——延续「纯函数仿真评估 + 诚实占位」路线，经独立 QA 两轮验收（PASS，零源码缺陷）。本期将社区半截且形态错误的 AAA（守卫在系统视图的 `local-user` + 不落盘的 `state.LocalUsers` + 只读不写的 `PrivilegeLevel` 死字段 + 名为 cipher 实为明文的双写 + `display` 里 map 随机遍历 + 指向空气的 VTY `authentication-mode aaa` 悬空引用）纠正式重构为：标准 `[R1-aaa]` 视图 + `local-user` / `authentication-scheme` / `domain` 三级链路、授权 P1（authorization-scheme）+ 计费 P2（accounting-scheme）同构扩展、DeviceConfig 单一事实源（`aaa:local-user:<name>:<field>` 等精确前缀键，删除 `state.LocalUsers`）、`display aaa` / `display local-user` / `display domain` 真实渲染 + 口令脱敏 `****` + `aaaSimNote()` 诚实占位（运行态恒 `-`）；**键碰撞红线**——`aaa` 是合法十六进制串，禁用 `strings.Contains(k,"aaa")`/`Contains(k,"domain")`，端口安全粘滞 MAC 键（`00e0-fc12-0aaa`/`aaaa-bbbb-cccc`）不被误判/误删。v0.8.0 已交付 GRE 隧道（course 69），此前 v0.5.0 已交付 P1-C Firewall 与华为 VRP 实训课程参考索引。

## 三、短中期计划（3~6 个月）

### P0（最高优先级）

- [x] VXLAN 隧道支持（Spine-Leaf 组网）- 已完成（`-demo-vxlan` + 前端规划模板）
- [x] 设备模型扩展（Switch、Router、L3 Switch、Firewall、VTEP 等 11 类）- 已完成
- [x] 一键拓扑模板引擎 - VXLAN 规划模板已完成，可扩展到更多场景
- [x] 拓扑标注（Annotation）层 - API + 前端画布标注已完成
- [x] 文件持久化存储 - `internal/storage/file_storage.go` 已完成
- [x] API handler 拆分重构 - 已拆分到 topology_/device_/link_/cli_/annotation_/system_handlers.go，`router.go` 仅保留路由注册，构建通过
- [x] 安全加固（P0） - 集中输入校验（`validation.go`）、CORS 严格白名单、外部诊断门控、API 限流、pprof token 守卫、路径穿越防护（topology id / 导出文件名）

### P1（高优先级）

- [x] 设备 CLI 仿真 - VRP CLI 解析与状态记录 + 前端 CLI 终端（WebSocket/SSE 交互）已完成，覆盖 ISIS/BGP/OSPF/ACL 等命令与 display
- [~] 数据包可视化 - SSE 事件流 + `PacketAnimator` 组件已完成，路径动画待完善
- [x] 拓扑导入/导出（JSON）- 支持拓扑配置文件的导入和导出（API + 前端）

### P2（中优先级）

- [x] OSPF/BGP 动态路由协议支持（基于 FRR）- Linux + gont 模式下通过 `internal/router` 真实下发
- [~] 链路质量模拟（延迟、丢包）- v0.12.0 已交付（接口视图 `delay` / `loss` + 引擎 Ping 延迟累加与端到端概率丢包）；带宽限制待 v1.0.0
- [x] Docker 化部署 - 提供 Dockerfile + `.dockerignore` 单二进制镜像与 CI `docker-verify` job

## 四、长期展望（6 个月以上）

- [ ] OpenFlow / SDN 控制器集成 - 支持软件定义网络实验
- [ ] 多用户隔离与协作 - 支持多人同时操作和共享拓扑
- [ ] 分布式大规模拓扑模拟 - 支持跨节点的大规模网络模拟

## 五、版本里程碑

| 版本 | 状态 | 发布时间 | 核心功能 |
|------|------|----------|----------|
| v0.1.0 | ✅ 已完成 | 2026-07 | 基础框架 + Ping 功能 + REST API |
| v0.2.0 | ✅ 已完成 | 2026-07 | VXLAN Spine-Leaf + 模板引擎 + 拓扑标注 + 文件持久化 + 20+ 协议建模 |
| v0.3.0 | ✅ 已完成 | 2026-07 | API handler 拆分重构（构建通过）+ 设备详情浮动窗口 + Ping 测试面板增强（任意源/目标 + 连续 Ping）+ 引擎懒启动（移除启动按钮） |
| v0.3.1 | ✅ 已完成 | 2026-07 | 稳定性加固：IP 合法性校验（HTTP 400）+ dbgSim 限流 + 低资源测试报告 + 最小启动配置 |
| v0.4.0 | ✅ 已完成 | 2026-08 | 安全加固(P0: V-01~V-04 / F1-F10) + protoSim 多拓扑修复 + 真实诊断(P1-D) + VRP CLI 广度(P1-F) + Firewall 路线定调 |
| v0.5.0 | ✅ 已完成 | 2026-08 | P1-C Firewall 真实过滤（路线 B 仿真 ACL：隐式 deny-any + 方向模型，介入 ping/tracert/可达性全路径 + 诊断 blockedBy）+ 华为 VRP 实训课程参考索引 |
| v0.7.0 | ✅ 已完成 | 2026-08 | P2 协议特性累积发布（续）：链路聚合 Eth-Trunk（course 63）/ DHCP 中继（course 27）已交付并通过独立 QA 两轮回归；叠加 v0.6.0 的 NAT / 端口安全 / VRRP / STP·RSTP·MSTP。纯函数仿真评估 + 诚实占位 |
| v0.8.0 | ✅ 已完成 | 2026-08 | P2 GRE 隧道（course 69）纠正式重构：删除野路子系统视图 `gre` 命令与 `state.GRE` 字段，改为标准 Tunnel 接口视图（`tunnel-protocol gre` / `source` / `destination` / `gre key` / `keepalive`）；`display gre tunnel` / `display interface Tunnel`；纯函数仿真评估 + 诚实占位 + 键碰撞红线（精确前缀匹配，不误伤 Bridge-Aggregation）；独立 QA 验收 NoOne |
| v0.9.0 | ✅ 已完成 | 2026-08 | P2 AAA 本地认证（course 71）纠正式重构：标准 `[R1-aaa]` 视图 + `local-user` / `authentication-scheme` / `domain` 三级链路（认证 P0 + 授权 P1 + 计费 P2 同构）；删除不落盘的 `state.LocalUsers` 结构体、改 DeviceConfig 单一事实源 `aaa:` 精确前缀键；`display aaa` / `display local-user` / `display domain` 真实渲染 + 口令脱敏 `****` + `aaaSimNote()` 诚实占位（运行态恒 `-`）；CLI 端到端浏览器验证；键碰撞红线（禁用 `strings.Contains(k,"aaa"/"domain")`，端口安全 MAC 不被误伤）；独立 QA 两轮验收 PASS。待续：IPv6（course 43/44）+ P2 CLI 增强（Tab 补全 / 历史 / EVPN display） |
| v0.10.0 | ✅ 已完成（并入 v0.11.0 发布） | 2026-08 | P2 IPv6（course 43/44）：全局 `ipv6` + 接口 `ipv6 enable` / `ipv6 address` + `ipv6 route-static` + RIPng / OSPFv3；接口视图仅放行 `ripng <pid> enable` / `ospfv3 <pid> area <x>`，其余回 unrecognized command。注：v0.10.0 源码从未单独入库（其功能已含于 v0.11.0 全树），未补打该 tag |
| v0.11.0 | ✅ 已完成 | 2026-08 | P2 CLI 增强：`displayRegistry` 单一事实源（`regXxxDisplay` 前缀收敛 1300 行 switch）+ Tab 补全 `cli.Complete` + `POST .../cli/complete`（仅计算不执行）+ 历史分层（UI localStorage / DeviceConfig FIFO 256）+ EVPN / NDP 只读诚实占位；P0R1 `executeCLI` 深拷贝修复 + V-1/V-4 修复 + F11 补全 endpoint + HTTP 超时加固；tag `v0.11.0` → `7cecdcb` |
| v0.12.0 | ✅ 已完成 | 2026-08 | P2 链路质量（接口视图 `delay` / `loss`）：引擎 Ping 逐跳延迟累加（修复首跳硬记 0）+ 端到端累积丢包 `P = 1 - ∏(1 - p_i/100)`（随机源可注入）；`display link-quality` 只读渲染 + undo 回落 + saved-config 差异落盘；api 按命令同步 `topology.Link`、两端取较大值；三层回归全绿 + go vet 清零 + Playwright e2e 实测（RTT≈45ms / 25% 丢包，undo 回落基线）。含 v0.11.1 技术债修复（`prefixToSubnet` / `display interface` mask / F1 依赖升级）；tag `v0.12.0` → `9a018f9` |
| v1.0.0 | 📅 后续 | - | 完整功能发布 + 链路质量模拟（带宽限制） |

## 六、贡献指南

### 如何参与路线图的讨论和调整

1. **提交 Issue**：在项目仓库中创建 Issue，描述您希望添加的功能或改进建议
2. **参与讨论**：对现有 Issue 发表评论，提供您的想法和反馈
3. **提交 PR**：如果您有能力实现某个功能，欢迎提交 Pull Request

### 当前最需要帮助的领域

| 领域 | 描述 | 难度 |
|------|------|------|
| **设备 CLI 仿真** | 实现网络设备命令行接口模拟，支持常见命令解析和执行 | 中 |
| **数据包可视化** | 基于 SSE 事件流实现前端路径动画展示 | 中 |
| **FRR 集成** | 在 gont 模式下集成 FRRouting，实现动态路由协议 | 高 |
| **链路质量模拟** | 带宽限制（延迟 / 丢包已随 v0.12.0 交付） | 中 |

### 开发规范

- 代码风格：遵循 Go 官方规范，使用 `go fmt` 格式化代码
- 测试要求：新增功能必须编写单元测试，核心功能需添加集成测试
- 文档要求：新增 API 端点需更新文档，复杂功能需添加注释说明
- 提交信息：遵循 Conventional Commits 规范