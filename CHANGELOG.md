# Changelog

本项目所有重要变更记录于此。格式参考 [Keep a Changelog](https://keepachangelog.com/)，版本里程碑见 `ROADMAP.md`，功能细节见 `docs/ensp-lab_manual.md` 与 `docs/vxlan_verification_report.md`。

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

## [v0.2.0] - 2026-07

> 里程碑详情见 `ROADMAP.md`。本节仅作摘要。

- VXLAN Spine-Leaf 演示拓扑（`-demo-vxlan` + 前端规划模板）
- 拓扑标注（Annotation）API + 前端画布标注层
- 文件持久化存储（每拓扑一个 JSON，重启自动加载）
- `internal/protocol` 20+ 协议状态模型（OSPF/BGP/VXLAN/ACL/IPsec/STP/LLDP/DHCP/DNS/HTTP/FTP/SMTP/MPLS/PPP 等）

## [v0.1.0] - 2026-07

- 基础框架：拓扑管理（设备、链路 CRUD）、ICMP Ping 功能、RESTful API、Web 前端 UI
