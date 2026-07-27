# ensp-lab Makefile
#
# 注意：前端构建产物 frontend/dist 已被 .gitignore 忽略，
# 因此从「干净克隆」构建前必须先执行前端构建（make ui），
# 否则 go:embed 找不到 frontend/dist 会导致编译失败。

BIN           := ensp-lab
FRONTEND_DIR  := frontend
TEST_TIMEOUT  ?= 120s
TEST_PARALLEL ?= 4
DOCKER_IMAGE  ?= ensp-lab
DOCKER_TAG    ?= latest

.PHONY: all
all: build

# 构建前端（npm install + vite build -> frontend/dist）
.PHONY: ui
ui:
	cd $(FRONTEND_DIR) && npm install && npm run build

# 构建包含前端的 Go 二进制（先确保前端已构建）
.PHONY: build
build: ui
	go build -o $(BIN) ./cmd/server

# 带版本信息的构建（注入 git tag / 构建时间）
.PHONY: version
version: ui
	go build -ldflags "-X 'main.version=$(shell git describe --tags --always)' -X 'main.buildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)'" -o $(BIN) ./cmd/server

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
	rm -rf $(BIN) $(FRONTEND_DIR)/dist

# ---- Docker ----
.PHONY: docker-build
docker-build:
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

.PHONY: docker-run
docker-run:
	docker run --rm -p 8080:8080 $(DOCKER_IMAGE):$(DOCKER_TAG)

.PHONY: docker-test
docker-test:
	docker build -t $(DOCKER_IMAGE):test .
	docker run --rm --entrypoint "" $(DOCKER_IMAGE):test ensp-lab -help
	$(info Docker image verified successfully)
