# 贡献指南 (Contributing)

感谢你关注 eNSP Lab！本项目欢迎 Issue、讨论与 Pull Request。

## 开发环境

- Go 1.26+
- Node.js 22+（仅前端需要）
- 操作系统：Windows / Linux / macOS 均可；Linux 下如需真实网络命名空间（gont + FRR）需 root 权限

## 从源码构建

仓库默认忽略 `frontend/dist`，因此**克隆后必须先构建前端，再构建 Go 二进制**：

```bash
make build                 # Linux / macOS / CI
./build.ps1                # Windows（未安装 make 时的等价入口）
```

两个入口等价：都会先增量构建前端（源码没变则跳过），再把版本信息注入
`internal/buildinfo` 并产出唯一交付物 `ensp-lab`（Windows 上为 `ensp-lab.exe`）。

> ⚠️ **禁止直接 `go build`。** 绕过构建入口就不会注入版本/commit/构建时间，
> 二进制会在启动日志与 `/version` 中自报 `stale=true`。同理，运行期若发现
> 二进制的 commit 与当前 HEAD 不一致、或工作树有未提交改动，也会标记 `stale`——
> 看到这个告警请重新 `make build`，不要忽略它。

本地调试可直接 `go run cmd/server/main.go`（同样会嵌入前端构建产物，但同样不注入版本信息，会显示 `stale=true`，属预期）。

## 运行测试

```bash
make test            # 全部单元测试
make test-unit       # 仅 internal/ 单元测试
make test-integration  # 集成测试（需 -tags=integration）
make race            # 竞态检测
make vet             # go vet 静态检查
```

提交前请确保 `make vet` 与 `make test` 通过。

## 提交规范

遵循 [Conventional Commits](https://www.conventionalcommits.org/)：

- `feat:` 新功能
- `fix:` 缺陷修复
- `docs:` 文档
- `refactor:` 重构
- `test:` 测试
- `chore:` 构建/工具链

示例：`fix(cli): 修正 VXLAN peer IP 输出遍历字符的 bug`

## 变更文档要求

- 新增/调整 API 端点请同步更新 `README.md` 的 API 章节
- 重要变更请追加 `CHANGELOG.md` 条目（参考 Keep a Changelog 格式）
- 路线图相关进展见 `ROADMAP.md`，可在 Issue 中讨论调整

## 提交 Pull Request

1. Fork 并创建特性分支（`feat/xxx` 或 `fix/xxx`）
2. 确保本地 `make vet` 与 `make test` 绿灯
3. 在 PR 中说明改动目的与验证方式
4. 关联相关 Issue（如 `#123`）

更多路线信息与待办领域见 `ROADMAP.md` 的「贡献指南」一节。
