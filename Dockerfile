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
COPY --from=go-builder /app/ensp-lab /usr/local/bin/
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:8080/health || exit 1
ENTRYPOINT ["ensp-lab"]
