# 贡献指南 (Contributing)

感谢你关注 eNSP Lab！本项目欢迎 Issue、讨论与 Pull Request。

## 开发环境

- Go 1.26+
- Node.js 22+（仅前端需要）
- 操作系统：Windows / Linux / macOS 均可；Linux 下如需真实网络命名空间（gont + FRR）需 root 权限

## 从源码构建

仓库默认忽略 `frontend/dist`，因此**克隆后必须先构建前端，再构建 Go 二进制**：

```bash
# 方式一：直接用 Make
make build                 # 等同于下方两步

# 方式二：手动
cd frontend && npm install && npm run build && cd ..
go build -o ensp-lab ./cmd/server   # 注：直接 go build 跳过版本注入，/version 会报 stale=true；待发布须改用 make build / build.ps1
```

本地调试可直接 `go run cmd/server/main.go`（同样会嵌入前端构建产物）。

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
