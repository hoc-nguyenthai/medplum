# syntax=docker/dockerfile:1
# Dockerfile RIÊNG của ws-gateway. Build từ GỐC repo (thư mục chứa go.mod).
# Thư viện quan sát nằm trong repo (pkg/observability) nên không cần build context phụ:
#
#   docker build -f apps/ws-gateway/Dockerfile -t trivita-ws-gateway .
#   (hoặc: make docker APP=ws-gateway)
#
# Chỉ những gì cần cho `go build ./apps/ws-gateway/cmd/ws-gateway` được đưa vào context (xem apps/ws-gateway/Dockerfile.dockerignore).

# ---- Stage 1: build binary tĩnh ----
FROM golang:1.26-alpine AS builder
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
WORKDIR /src/trivita-codebase-v1
# Layer 1: chỉ module (cache tốt khi chỉ đổi source)
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
# Layer 2: source
COPY . ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/app ./apps/ws-gateway/cmd/ws-gateway

# ---- Stage 2: runtime gọn, non-root, không shell ----
FROM gcr.io/distroless/static-debian12:nonroot
# Không có cơ sở dữ liệu và không ghi đĩa (không cần VOLUME; chạy được với readOnlyRootFilesystem). Bắt buộc REDIS_URL và FHIR_WS_SIGNING_KEY
# (giống bên cấp token); xem apps/ws-gateway/docs/config.md.
ENV HTTP_ADDR=":8000" \
    LOG_JSON="true"
COPY --from=builder /out/app /app
EXPOSE 8000
# UID/GID số (65532 = `nonroot` của distroless): K8s `runAsNonRoot: true` không xác minh được USER dạng tên
USER 65532:65532

HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
    CMD ["/app", "healthcheck"]

# SIGTERM -> readiness 503 -> drain -> đóng 1001 mọi socket -> thoát (xem SHUTDOWN_* và FHIR_WS_CLOSE_TIMEOUT). Lệnh con: healthcheck.
ENTRYPOINT ["/app"]
