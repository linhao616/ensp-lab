# ensp-lab P0 收尾：protoSim 重构设计 + 安全加固 Sweep 任务分解

> 作者：架构师 高见远（Gao）
> 日期：2026-08-04
> 范围：`internal/protocol`（protoSim 重构）+ `internal/api`（F4/F5/F7/F8/F10 + README CORS 文档）
> 技术栈（**不变更**）：Go 1.26.5 + Gin v1.12.0（http 框架已定，本次无框架选型变更，不引入新依赖）

---

## 1. 实现方案 + 框架选型说明

### 1.1 框架选型

- **HTTP 框架**：Gin v1.12.0，沿用不变（`NewRouter(store, staticFS, cfg)` 签名保持不变，不破坏 `internal/api/integration_test.go`）。
- **CORS**：`github.com/gin-contrib/cors` v1.7.7，沿用；仅调整 `AllowHeaders` 去冗余（F8）。
- **日志**：`go.uber.org/zap` + 项目 `internal/logging`（F7 泛化错误用其记录内部详情）。
- **pprof**：标准库 `net/http/pprof`，不引入新依赖（F10 仅加一层 gin 中间件做 token 校验）。
- **结论**：本次两项工作均为「最小变更」级别的存量代码调整，**不新增任何第三方依赖，不引入新框架**。

### 1.2 第一部分：protoSim per-topology 重构 —— 选定方案 A

**审计结论回顾**：`internal/protocol` 的 `ProtocolSimulator` 持有单一 `topology` 字段；但 `Router` 是全局单例、可管理多个拓扑，`NewProtocolSimulator(nil)` 使 `CheckReachability`/`FindRoute` 在多拓扑下惰性失效。

**实测研判（已读源码 + 全仓 grep）**：

- `ProtocolSimulator.CheckReachability(srcDevice, dstDevice, srcIP, dstIP string) bool`（protocol.go:636）确实读取 `p.topology` 做无向 BFS；当 `p.topology == nil`（即 `NewProtocolSimulator(nil)`）时**恒返回 false**。
- `ProtocolSimulator.FindRoute(deviceID, destIP string) *RouteEntry`（protocol.go:605）**并不读取 `p.topology`**，它只查 `routers[deviceID]` 的路由表（按 deviceID 索引），属于 per-device 方法，本就无拓扑图依赖。
- **关键**：全仓 grep 显示这两个方法**在生产代码中均未被调用**（仅 `internal/protocol/protocol_test.go` 调用了 `CheckReachability`，且 `cli.CheckReachability` 是 `cli` 包内的同名独立函数，与 `ProtocolSimulator` 无关）。所以该「设计错配」目前是**潜在/惰性**问题，修复风险可控。
- `Router.protoSim` 当前仅用于 `RemoveRouter`（topology_handlers.go:145、device_handlers.go:129），生命周期与 `Router` 单例绑定。

**方案对比与选定**：

| 维度 | 方案 A（调用方传入 topo） | 方案 B（per-topology 实例） |
|---|---|---|
| 改动面 | 仅 `CheckReachability` 签名 + 其测试调用点 | `Router.protoSim` → `map[topoID]*ProtocolSimulator`；所有 `r.protoSim.X()` 调用改为 `r.getProtoSim(topoID).X()`；`RemoveRouter`/`InitRouter`/引擎同步路径全部改 |
| 破坏 `NewRouter` 约定 | 否 | 否（内部字段变化，但对外签名不变） |
| 生命周期影响 | 无（单例不变） | 大（需在 createTopology/loadAll 时按拓扑实例化、在 delete 时销毁） |
| 与「调用方已持有 topology」契合度 | 高（handler 已 `store.GetTopology`） | 低 |
| 风险 | 低（仅 1 个未接线方法 + 测试） | 高（触及并发 `routers` 表与多拓扑 deviceID 碰撞） |

**选定：方案 A**。理由：① 最小变更、零生命周期改动；② 调用方（未来 handler / 测试）本就持有 `topology`，传入即正确；③ `FindRoute` 无需改签名（它不依赖拓扑图）；④ 符合项目「最小变更 + 不破坏集成测试约定」硬约束。

**接口签名变化（伪代码级）**：

```go
// 重构前
func (p *ProtocolSimulator) CheckReachability(srcDevice, dstDevice, srcIP, dstIP string) bool

// 重构后（方案 A：拓扑由调用方显式传入）
func (p *ProtocolSimulator) CheckReachability(srcDevice, dstDevice, srcIP, dstIP string, topo *topology.Topology) bool
```

`NewProtocolSimulator(t *topology.Topology)` 签名**保持不变**（保持 `Router` 调用的 `NewProtocolSimulator(nil)` 与测试 `NewProtocolSimulator(newChainTopo())` 均编译通过）；内部 `topology` 字段保留（无编译错误，留作后续用途），但 `CheckReachability` 不再读取它。

**受影响文件清单**：
- `internal/protocol/protocol.go`：仅 `CheckReachability` 方法体（protocol.go:636-676）改为使用入参 `topo`，移除 `if p == nil || p.topology == nil` 早期返回中对 `p.topology` 的依赖（保留 `topo == nil` 的防御分支）。
- `internal/protocol/protocol_test.go`：`TestCheckReachability`（约 line 24-51）中所有 `sim.CheckReachability(...)` 调用追加 `topo` 实参；`NewProtocolSimulator(newChainTopo())` 不变。
- 生产代码（api 包）：**无调用点需改**（当前未接线）。若未来 `simulatePacket`/`diagnostic` 等 handler 调用，直接传 `topo` 即可。

**向后兼容性与风险**：
- 编译兼容：`FindRoute`、其余 `ProtocolSimulator` 方法签名不变；`NewProtocolSimulator` 不变；`Router`/`NewRouter` 不变。
- 仅 `CheckReachability` 为破坏性签名变更，唯一受影响是 `protocol_test.go`（测试文件，随本次一并改）。
- 风险：极低。多拓扑 deviceID 碰撞（同一 deviceID 跨拓扑在 `routers` 表互相覆盖）属既有 broader 问题，不在本项范围，已记入「待明确事项」。

---

## 2. 文件列表及相对路径（按模块分组）

### 2.1 protoSim 重构（第一部分）
```
internal/protocol/protocol.go          # CheckReachability 签名 + 方法体改
internal/protocol/protocol_test.go     # TestCheckReachability 调用点追加 topo 实参
```

### 2.2 安全加固 Sweep（第二部分）
```
internal/api/validation.go             # 已有 validateTopoID（F4/F5 复用，不改）
internal/api/topology_handlers.go      # F4: getTopology / deleteTopology 加 validateTopoID
internal/api/router.go                  # F4/F5: exportTopology 加校验; F7: 5xx 错误回显; F8: CORS AllowHeaders; F10: pprof token 守卫
internal/api/device_handlers.go        # (参考) deleteDevice 已用 r.protoSim.RemoveRouter，无需改
internal/api/diagnostic_handlers.go    # F7: InternalServerError 错误回显
internal/api/ipconfig_handlers.go      # F7: InternalServerError 错误回显
internal/api/errors.go                 # (新增) 共享 clientError 辅助函数（F7）
README.md                              # CORS 文档同步 V-02 + F8 + ENS_CORS_ORIGINS + F10 约束
```

> 注：`internal/api/errors.go` 为本次新增的轻量辅助文件，集中放 `clientError` helper，避免在每个 handler 重复 `logging + 泛化响应` 逻辑。

---

## 3. 数据结构与接口（protoSim 重构后）

```go
// ===== internal/protocol/protocol.go =====

// 结构体保持不动，topology 字段保留（不再被 CheckReachability 读取）。
type ProtocolSimulator struct {
    topology *topology.Topology          // 保留，留作后续用途；CheckReachability 不再依赖
    routers  map[string]*RouterState     // 按 deviceID 索引（既有）
    mu       sync.RWMutex
}

// FindRoute：保持原签名（per-device 路由表查询，无拓扑依赖）
func (p *ProtocolSimulator) FindRoute(deviceID, destIP string) *RouteEntry

// CheckReachability：方案 A —— 拓扑由调用方传入（核心变更）
// 返回 srcDevice 与 dstDevice 在网络层是否可达（交换机/集线器桥接视为同一广播域）。
// topo 为 nil 时防御性返回 false。
func (p *ProtocolSimulator) CheckReachability(
    srcDevice, dstDevice, srcIP, dstIP string,
    topo *topology.Topology,
) bool
```

**共享辅助（api 包，F7 新增）**：
```go
// internal/api/errors.go
// clientError 向客户端返回泛化错误文案（不含内部细节），
// 同时将完整错误以合适级别记入日志（internal/logging）。
func clientError(c *gin.Context, status int, publicMsg string, cause error)
```

---

## 4. 程序调用流程（protoSim 重构前后对比）

**重构前（当前，存在惰性失效）**：
1. `Router` 初始化时 `protoSim = protocol.NewProtocolSimulator(nil)`（router.go:212）。
2. 任意调用 `protoSim.CheckReachability(src, dst, ...)` 进入方法体，`p.topology == nil` → 直接 `return false`。
3. 结果：多拓扑下恒为 false（当前因生产未接线而不暴露，但属设计错配）。

**重构后（方案 A）**：
1. `Router` 仍 `NewProtocolSimulator(nil)`，单例不变。
2. 调用方（handler/测试）先 `topo, _ := r.store.GetTopology(topoID)` 取得拓扑。
3. 调用 `protoSim.CheckReachability(src, dst, srcIP, dstIP, topo)`，方法体对**传入的 topo** 做 BFS，多拓扑互不影响、结果真实。
4. 时序图见 `docs/sequence-diagram.mermaid`；类型关系见 `docs/class-diagram.mermaid`。

---

## 5. 任务列表（有序 / 依赖 / 按实现顺序）

> 约定：P0 = 必做；验证均以 `go build ./...` + `go vet ./...` 通过为硬性门槛，并给出针对性冒烟/单测。
> 依赖图：T1(F4) → T2(F5)；T6(README) → T4(F8)；protoSim 重构 T0 与其余任务**无冲突**（互不触碰同一文件）。

### T0 — protoSim CheckReachability 改为按调用传入 topology（方案 A）
- **目标文件**：`internal/protocol/protocol.go`、`internal/protocol/protocol_test.go`
- **具体改动**：
  1. `protocol.go:636` 方法签名加 `topo *topology.Topology` 末参；方法体内把 `topo := p.topology` 改为使用入参 `topo`，`if p == nil || p.topology == nil` 改为 `if topo == nil`（保留 nil 防御）。其余 BFS 逻辑不变。
  2. `protocol_test.go` 的 `TestCheckReachability`（line 24-51）所有 `sim.CheckReachability("A","C","","")` 等调用追加 `topo` 实参（测试内已构造 `newChainTopo()`/`isolated` topo 变量）。
  3. `NewProtocolSimulator`、`FindRoute`、其他 `ProtocolSimulator` 方法**不动**。
- **依赖**：无
- **优先级**：P0
- **验证方式**：`go test ./internal/protocol/...` 全绿；`go build ./...`、`go vet ./...` 通过；确认 `internal/api` 无对 `CheckReachability` 的调用（grep 验证）。

### T1 — F4：所有拓扑级 `:id` 入口统一走 `validateTopoID`
- **目标文件**：`internal/api/topology_handlers.go`、`internal/api/router.go`
- **具体改动**：
  1. `getTopology`（topology_handlers.go:34）：在 `id := c.Param("id")`（line 35）之后，查 store 之前加：
     ```go
     if err := validateTopoID(id); err != nil {
         c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
         return
     }
     ```
  2. `deleteTopology`（topology_handlers.go:137）：在 `id := c.Param("id")`（line 138）之后、查 store 之前加同样的 `validateTopoID(id)` 守卫。
  3. `exportTopology`（router.go:943）：在 `topoID := c.Param("id")` 后加 `validateTopoID(topoID)` 守卫（此行同时满足 F5 的前置要求）。
  4. `updateTopology`（topology_handlers.go:110）**已存在** `validateTopoID(updated.ID)`，无需改动，仅复核确认。
- **依赖**：无
- **优先级**：P0
- **验证方式**：`go build ./...`；冒烟：对非法 `:id`（如 `../../etc` 或含换行）发起 GET/DELETE/export，应返回 400（而非依赖存储层兜底）；合法 id 行为不变。

### T2 — F5：exportTopology 导出文件名加固（依赖 T1）
- **目标文件**：`internal/api/router.go`
- **具体改动**：
  1. 依赖 T1 已在 `exportTopology` 入口完成 `validateTopoID(topoID)` 校验，本任务在此之上进一步硬化 `Content-Disposition`：
     - 在 `router.go:953` 处，除既有的 `filename=%q`（已转义换行）外，额外基于校验后的 `topoID` 派生安全文件名（如 `sanitizeForFilename(topoID)`，仅保留 `[A-Za-z0-9_-]`），避免使用原始/边界 id 产生异常文件名；保留 `.json` 后缀。
     - 若 `topoID` 经 sanitize 后为空（理论不命中，因 T1 已拦截），回退为 `"topology.json"`。
  2. 不修改 `%q` 转义（双重保险）。
- **依赖**：T1（F4 必须先于 F5，因 F5 依赖 `validateTopoID` 已执行）
- **优先级**：P0
- **验证方式**：`go build ./...`；冒烟：合法 id 导出响应头 `Content-Disposition: attachment; filename="<id>.json"` 正常；构造边界 id（T1 已挡 400，不会到达此处）。

### T3 — F7：内部错误泛化响应（新增 `clientError` 并替换 5xx 回显）
- **目标文件**：`internal/api/errors.go`（新增）、`internal/api/router.go`、`internal/api/diagnostic_handlers.go`、`internal/api/ipconfig_handlers.go`、`internal/api/topology_handlers.go`
- **具体改动**：
  1. 新增 `internal/api/errors.go`：
     ```go
     package api
     import ("ensp-lab/internal/logging"; "github.com/gin-gonic/gin"; "go.uber.org/zap")
     func clientError(c *gin.Context, status int, publicMsg string, cause error) {
         if cause != nil {
             logging.Error(publicMsg, zap.Error(cause))
         }
         c.JSON(status, gin.H{"error": publicMsg})
     }
     ```
  2. 将以下**5xx（及存储派生 404）** `err.Error()` 透传替换为 `clientError(c, status, 泛化文案, err)`：
     - `router.go:604`（NotFound，store 错误）、`router.go:692`、`725`、`734`、`794`、`858`、`875`、`906`、`917`、`946`、`1015`、`1027`
     - `diagnostic_handlers.go:158`、`164`、`261`、`267`
     - `ipconfig_handlers.go:259`
     - `topology_handlers.go:84`、`123`、`343`
  3. **不改动** 400 类校验错误（如 `topology_handlers.go:47/64/68/105/111/115/121/169`、`device_handlers.go` 等）——这些由本系统 `validateXxx` 产生，属面向用户的安全/格式提示，回显无害且利于排错。
- **依赖**：无（与 T0/T1/T2/T4/T5 无文件冲突）
- **优先级**：P1
- **验证方式**：`go build ./...`、`go vet ./...`；冒烟：触发一处 500（如 export 一个不存在拓扑），响应体为泛化文案（如 `"internal error"`），且服务端日志含原始 `err` 细节；对比确认 400 校验错误仍返回具体提示。

### T4 — F8：CORS `AllowHeaders` 去掉 `Authorization`
- **目标文件**：`internal/api/router.go`
- **具体改动**：`router.go:192` `AllowHeaders: []string{"Origin", "Content-Type", "Authorization"}` → 改为 `[]string{"Origin", "Content-Type"}`（应用无鉴权，移除以免误导）。`AllowMethods`/`AllowCredentials`/`AllowOriginFunc` 不变。
- **依赖**：无
- **优先级**：P3
- **验证方式**：`go build ./...`；冒烟：浏览器/脚本带 `Authorization` 头的跨源预检（OPTIONS）应不被 CORS 放行该头；功能无回退（前端未用 Authorization）。

### T5 — F10：pprof 挂载加 token/localhost 守卫
- **目标文件**：`internal/api/router.go`
- **具体改动**（最小、无新依赖）：
  1. 在 `router.go:262` `if os.Getenv("ENSP_PPROF") != ""` 分支内：
     - 读取 `token := os.Getenv("ENSP_PPROF_TOKEN")`；若为空，自动生成随机 token 并经 `logging.Warn("pprof enabled, token:", zap.String("token", token))` 输出（仅本地调试可见）。
     - 新增 gin 中间件 `pprofGuard(token)`：校验请求 `?token=` 查询参数（或 `X-Pprof-Token` 头）与 `token` 相等，不符返回 403。
     - 将 `/debug/pprof/*pprof` 路由改用 `r.Group("/debug/pprof", pprofGuard(token))` 包裹，`gin.WrapF(http.DefaultServeMux.ServeHTTP)` 不变。
  2. 文档约束（T6）说明：仅在 `--bind 127.0.0.1`（默认）时启用 pprof 才安全。
- **依赖**：无
- **优先级**：P2
- **验证方式**：`go build ./...`；冒烟：设 `ENSP_PPROF=1` 启动，未带 token 访问 `/debug/pprof/` 返回 403；带正确 token 返回 200；不设 `ENSP_PPROF` 时该路由 404（默认关闭）。

### T6 — README CORS 文档同步（V-02 + F8 + ENS_CORS_ORIGINS + F10）
- **目标文件**：`README.md`
- **具体改动**：
  1. 「安全相关环境变量」表（README.md:184-189）更新：
     - `ENS_CORS_ORIGINS` 行补充：`AllowHeaders` 仅含 `Origin`/`Content-Type`（F8 已移除 `Authorization`，因无鉴权）；说明前端同源 localhost 默认放行，跨源可信前端用逗号分隔追加。
     - `ENSP_PPROF` 行补充：开启时**必须**携带启动时日志输出的 token（`ENSP_PPROF_TOKEN` 或自动生成），且该端点仅应在 `127.0.0.1` 绑定时使用。
  2. 依赖表 `github.com/gin-contrib/cors` 说明（README.md:1022）由「放行所有 localhost 源（端口无关）」修正为「严格白名单：默认仅 127.0.0.1/localhost 同源 + `ENS_CORS_ORIGINS` 追加，不含 Authorization 头」。
  3. 「安全默认说明」（README.md:191）追加 pprof token 约束一句。
- **依赖**：T4（F8 已落地才能准确描述 CORS 头）；引用 T5 的 pprof 约束
- **优先级**：P3
- **验证方式**：文档审阅；`grep -n "Authorization" README.md` 确认 CORS 段落不再宣称支持 Authorization；`ENS_CORS_ORIGINS` 用法示例清晰。

### 实现顺序（建议）
T0（protoSim，独立）→ T1(F4) → T2(F5) → T3(F7) → T4(F8) → T5(F10) → T6(README)。
其中 T0 与 T1–T6 可并行（无文件交集）；F4 须先于 F5；F8 须先于 README 的 CORS 段落。

---

## 6. 依赖包列表

本次**无新增依赖**。涉及的包均为项目已有：

```
- github.com/gin-gonic/gin            v1.12.0   # HTTP 框架（不变）
- github.com/gin-contrib/cors         v1.7.7    # CORS 中间件（仅改配置，不升级）
- go.uber.org/zap                     —         # 结构化日志（F7 使用 internal/logging 包装）
- net/http/pprof                      (标准库)   # F10 token 守卫，无新依赖
- ensp-lab/internal/logging           —         # 项目日志封装（F7 记录内部错误）
- ensp-lab/internal/topology          —         # CheckReachability 入参类型（方案 A）
```

`go.mod` 无需变更；`go get` 无需执行。

---

## 7. 共享知识（跨文件约定）

1. **`validateTopoID` 使用约定**：凡从 `c.Param("id")` / `c.Param("topoID")` 取得拓扑 ID 后、在调用 `store.GetTopology/UpdateTopology/DeleteTopology` 之前，**必须**先 `validateTopoID(id)`（F4 已为 get/delete/export 补齐；update 早已具备）。该函数允许空串（由调用方自动生成），非法形态返回带形态说明的 error，统一映射为 400。与 `storage.topoFilePath` 的拒绝规则（`/ \ ..`）形成纵深防御。
2. **错误响应封装约定（F7）**：所有 **5xx** 及**存储派生 404** 不得回显 `err.Error()` 原文；统一经 `clientError(c, status, 泛化文案, err)`，由 `internal/logging` 记录详情、向客户端仅返回泛化文案。400 类**校验错误**（`validateXxx` 产生）属安全面向用户的提示，可保留具体文案，不在此约定内。
3. **`Authorization` 头约定（F8）**：本应用无鉴权，`CORS AllowHeaders` 不含 `Authorization`；任何"需鉴权"的语义均属误导，后续若引入鉴权须同步评估 CORS 与 pprof。
4. **pprof 安全约定（F10）**：`ENSP_PPROF` 开启即视为调试模式，必须配 token 且仅限 `127.0.0.1` 绑定；token 缺失时由服务端生成并写入日志，不回显到响应。
5. **protoSim 单例约定**：`Router.protoSim` 为全局单例，`NewProtocolSimulator(nil)` 调用约定不变；`CheckReachability` 自 T0 起必须显式传入 `topo`，禁止再依赖内部 `topology` 字段。
6. **`NewRouter(store, staticFS, cfg)` 签名不可变**：任何内部重构不得改动该导出函数签名（集成测试依赖）。

---

## 8. 待明确事项（需主理人/用户拍板）

1. **protoSim 的 `routers` 表按 deviceID 全局索引**：方案 A 仅修复 `CheckReachability` 的拓扑图依赖。`FindRoute` 及所有 `InitRouter/AddRoute/...` 仍以 deviceID 为键，若两个拓扑恰好含**同名 deviceID**，状态会互相覆盖（既有多拓扑隐患，非本次引入）。是否需在后续将 `routers` 改为 `map[topoID]map[deviceID]*RouterState`？**建议本期不做**（超出最小变更 + 无生产调用），列入下个迭代观察项。请主理人确认是否接受"本期仅修 CheckReachability"。
2. **F7 范围边界**：本报告将 400 校验错误排除在"泛化"之外（保留用户友好提示）。若安全/合规要求连 400 也返回完全中性文案（如统一 `"bad request"`），需调整 T3 范围并可能影响前端错误提示体验。请确认当前"仅 5xx/存储 404 泛化"的口径是否通过评审。
3. **F10 守卫强度**：当前设计为"token 校验 + 文档约束 127.0.0.1"。若主理人认为需**代码强制** pprof 仅在 loopback 绑定下挂载（读取 `cfg.BindAddr` 判断），可升级 T5；当前选择 token 方案以匹配"最小变更 + 文档强约束"的审计建议。请确认采用 token 方案。
4. **F4 是否扩到子资源 `:id`**：本报告 F4 严格限定审计点名的四个拓扑级 `:id`（get/update/delete/export）。devices/links/annotations/ip-config/cli/router 等子路由的 `:id`（拓扑 id）未统一加 `validateTopoID`，依赖存储层兜底。是否需一并补齐？建议本期**不扩**（最小变更），后续统一收口。请主理人拍板。

---

## 附：本次不改动项（明确排除，避免范围蔓延）
- `internal/cli/parser.go` 的 `CheckReachability`（同名独立函数，按 state + topo BFS，与 `ProtocolSimulator` 无关，不在重构范围）。
- `simulatePacket` 的本地 `findPath` BFS（router.go:535-581，自带拓扑校验，不调用 protoSim）。
- `NewProtocolSimulator` / `FindRoute` / `Router` 生命周期 / `NewRouter` 签名（保持不变）。
- F1/F2/F3/F6/F9（已修复，不在本次）。
