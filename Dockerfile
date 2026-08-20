# ==========================================================
# ensp-lab Dockerfile — single-binary deployment
#
# Multi-stage build:
#   1. Node (frontend)  → Vite build → frontend/dist/
#   2. Go  (backend)   → embed dist  → single binary
#   3. Alpine (runtime) → minimal image
#
# Usage:
#   docker build -t ensp-lab .
#   docker run --rm -p 8080:8080 ensp-lab
#   # 使用非默认端口（如 9090）：
#   docker build --build-arg ENS_PORT=9090 -t ensp-lab .
#   docker run --rm -p 9090:9090 -e ENS_PORT=9090 ensp-lab
# ==========================================================

# ---- Frontend build ----
FROM node:22-alpine AS frontend-builder
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ---- Go build ----
FROM golang:1.26-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-builder /src/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o ensp-lab ./cmd/server

# ---- Runtime ----
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai
ARG ENS_PORT=8080
ENV ENS_PORT=${ENS_PORT}
COPY --from=go-builder /app/ensp-lab /usr/local/bin/
EXPOSE ${ENS_PORT}
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:${ENS_PORT}/health || exit 1
# 注意：必须 -bind 0.0.0.0——服务默认只绑 127.0.0.1，docker -p 端口映射经容器 eth0
# 转发会连接被拒，导致 host 侧健康检查/外部访问失败（2026-08-20 修复，曾致 CI smoke 一直红）。
ENTRYPOINT ["sh", "-c", "exec ensp-lab -port ${ENS_PORT} -bind 0.0.0.0 \"$@\"", "ensp-lab"]
