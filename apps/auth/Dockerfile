# syntax=docker/dockerfile:1
# Dockerfile RIÊNG của auth. Build từ GỐC repo (thư mục chứa go.mod).
# Thư viện quan sát nằm trong repo (pkg/observability) nên không cần build context phụ:
#
#   docker build -f apps/auth/Dockerfile -t trivita-auth .
#   (hoặc: make docker APP=auth)
#
# Chỉ những gì cần cho `go build ./apps/auth/cmd/auth` được đưa vào context (xem apps/auth/Dockerfile.dockerignore).

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
    go build -ldflags="-s -w" -o /out/app ./apps/auth/cmd/auth \
    && mkdir -p /out/data

# ---- Stage 2: runtime gọn, non-root, không shell ----
FROM gcr.io/distroless/static-debian12:nonroot
# SQLite chỉ để dev: staging/production cấm SQLite và buộc PostgreSQL (DATABASE_URL) kèm AUTH_BASE_URL, AUTH_SIGNING_KEY_PATH.
ENV DATABASE_URL="sqlite:///data/auth.db" \
    HTTP_ADDR=":8000" \
    LOG_JSON="true"
COPY --from=builder /out/app /app
COPY --from=builder --chown=nonroot:nonroot /out/data /data
VOLUME ["/data"]
EXPOSE 8000
# UID/GID số (65532 = `nonroot` của distroless): K8s `runAsNonRoot: true` không xác minh được USER dạng tên
USER 65532:65532

HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
    CMD ["/app", "healthcheck"]

# SIGTERM -> readiness 503 -> drain -> shutdown (xem SHUTDOWN_* trong .env.example). Lệnh con: migrate, keys, seed, client, user, healthcheck.
ENTRYPOINT ["/app"]
