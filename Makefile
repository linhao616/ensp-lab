TEST_TIMEOUT ?= 30s
TEST_PARALLEL ?= 1

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

.PHONY: clean
clean:
	go clean ./...

.PHONY: build
build:
	go build -o ensp-lab ./cmd/server

.PHONY: version
version:
	go build -ldflags "-X 'main.version=$(shell git describe --tags --always)' -X 'main.buildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)'" -o ensp-lab ./cmd/server