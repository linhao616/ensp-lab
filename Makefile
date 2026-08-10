# ensp-lab Makefile
#
# 唯一构建入口。禁止直接 `go build`：那样不会注入版本信息，
# 二进制会在启动日志与 /api/version 中自报 stale=true。
#
# Windows 若未安装 make，请改用等价的 ./build.ps1（同一套 ldflags 与增量规则）。
#
# 注意：前端构建产物 frontend/dist 已被 .gitignore 忽略，
# 因此从「干净克隆」构建前必须先执行前端构建（make ui），
# 否则 go:embed 找不到 frontend/dist 会导致编译失败。

# GOEXE 在 Windows 上为 .exe，Linux/macOS 上为空 —— 让 make 的产物与
# 大家实际双击/运行的文件是同一个，杜绝「make 产 ensp-lab、你跑 ensp-lab.exe」。
EXE           := $(shell go env GOEXE)
BIN           := ensp-lab$(EXE)
FRONTEND_DIR  := frontend
TEST_TIMEOUT  ?= 120s
TEST_PARALLEL ?= 4
DOCKER_IMAGE  ?= ensp-lab
DOCKER_TAG    ?= latest
PORT          ?= 8080

# ---- 版本注入（单一事实源：internal/buildinfo）----
# 所有 git 调用都带 `|| echo <兜底>`：干净克隆、无 git、无 tag 时也不能让 make 中断。
BUILDINFO_PKG := ensp-lab/internal/buildinfo
GIT_VERSION   := $(shell git describe --tags --always 2>/dev/null || echo dev)
GIT_COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
GIT_DIRTY     := $(shell test -n "$$(git status --porcelain 2>/dev/null)" && echo true || echo false)
BUILD_TIME    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS       := -X '$(BUILDINFO_PKG).Version=$(GIT_VERSION)' \
                 -X '$(BUILDINFO_PKG).BuildTime=$(BUILD_TIME)' \
                 -X '$(BUILDINFO_PKG).Commit=$(GIT_COMMIT)' \
                 -X '$(BUILDINFO_PKG).Dirty=$(GIT_DIRTY)'

# 前端增量构建：以 dist/index.html 作为真实文件 target，
# 只有 src/ 下任一文件、package.json 或 vite.config.ts 比它新时才重跑 npm run build。
UI_BUNDLE   := $(FRONTEND_DIR)/dist/index.html
FRONTEND_SRC := $(shell find $(FRONTEND_DIR)/src -type f 2>/dev/null)

.PHONY: all
all: build

# node_modules 作为 order-only 依赖（| 之后）：保证干净克隆能装依赖，
# 但其目录 mtime 变动不会反复触发前端重建。
$(FRONTEND_DIR)/node_modules:
	cd $(FRONTEND_DIR) && npm install

$(UI_BUNDLE): $(FRONTEND_SRC) $(FRONTEND_DIR)/package.json $(FRONTEND_DIR)/vite.config.ts | $(FRONTEND_DIR)/node_modules
	cd $(FRONTEND_DIR) && npm run build

# 构建前端（增量；源码没变则直接跳过）
.PHONY: ui
ui: $(UI_BUNDLE)

# 强制重建前端（怀疑 dist 脏了时用）
.PHONY: ui-force
ui-force:
	cd $(FRONTEND_DIR) && npm install && npm run build

# 构建包含前端的 Go 二进制（先确保前端已构建，并注入版本信息）
.PHONY: build
build: $(UI_BUNDLE)
	rm -f server.exe
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/server
	@echo "built $(BIN)  version=$(GIT_VERSION)  commit=$(GIT_COMMIT)  dirty=$(GIT_DIRTY)  time=$(BUILD_TIME)"

.PHONY: test
test:
	go test -timeout $(TEST_TIMEOUT) -parallel $(TEST_PARALLEL) ./...

.PHONY: test-unit
test-unit:
	go test -timeout $(TEST_TIMEOUT) -parallel $(TEST_PARALLEL) ./internal/...

.PHONY: test-integration
test-integration:
	go test -timeout $(TEST_TIMEOUT) -parallel $(TEST_PARALLEL) -tags=integration ./internal/api/...

.PHONY: test-all
test-all:
	go test -timeout $(TEST_TIMEOUT) -parallel $(TEST_PARALLEL) -tags=integration ./...

.PHONY: race
race:
	go test -race -timeout $(TEST_TIMEOUT) -parallel $(TEST_PARALLEL) ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: clean
clean:
	go clean ./...
	rm -rf $(BIN) server.exe $(FRONTEND_DIR)/dist

# ---- Docker ----
.PHONY: docker-build
docker-build:
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

.PHONY: docker-run
docker-run:
	docker run --rm -p $(PORT):$(PORT) -e ENS_PORT=$(PORT) $(DOCKER_IMAGE):$(DOCKER_TAG)

.PHONY: docker-test
docker-test:
	docker build -t $(DOCKER_IMAGE):test .
	docker run --rm --entrypoint "" $(DOCKER_IMAGE):test ensp-lab -help
	$(info Docker image verified successfully)
