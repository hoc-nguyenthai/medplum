# syntax=docker/dockerfile:1
# Dockerfile RIÊNG của dicomweb. Build từ GỐC repo (thư mục chứa go.mod):
#
#   docker build -f apps/dicomweb/Dockerfile -t trivita-dicomweb .
#   (hoặc: make docker APP=dicomweb)
#
# Chỉ những gì cần cho `go build ./apps/dicomweb/cmd/dicomweb` được đưa vào context (xem apps/dicomweb/Dockerfile.dockerignore).

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
    go build -ldflags="-s -w" -o /out/app ./apps/dicomweb/cmd/dicomweb

# ---- Stage 2: runtime gọn, non-root, không shell ----
FROM gcr.io/distroless/static-debian12:nonroot
# Không ghi đĩa (không cần VOLUME; chạy được với readOnlyRootFilesystem) khi kho là s3:. Kho `file:<thư mục>` cần thư mục ghi được bởi UID 65532
# (mount volume); production dùng S3. Bắt buộc DATABASE_URL (cùng schema fhir với fhir-server), FHIR_JWT_*, FHIR_BINARY_STORAGE: xem apps/dicomweb/docs/config.md.
ENV HTTP_ADDR=":8008" \
    LOG_JSON="true"
COPY --from=builder /out/app /app
EXPOSE 8008
# UID/GID số (65532 = `nonroot` của distroless): K8s `runAsNonRoot: true` không xác minh được USER dạng tên
USER 65532:65532

HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
    CMD ["/app", "healthcheck"]

# SIGTERM -> readiness 503 -> drain -> thoát (xem SHUTDOWN_*). Lệnh con: healthcheck. Không có migrate: schema do fhir-server sở hữu.
ENTRYPOINT ["/app"]
