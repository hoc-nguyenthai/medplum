# syntax=docker/dockerfile:1
# Dockerfile RIÊNG của fhir-server. Build từ GỐC repo (thư mục chứa go.mod); thư viện quan sát (go.mod có `replace ../trivita-observability-go`)
# đi qua một build context CÓ TÊN nên không cần context ở thư mục cha và không phụ thuộc tên thư mục của repo:
#
#   docker build -f apps/fhir-server/Dockerfile --build-context observability=../trivita-observability-go -t trivita-fhir-server .
#   (hoặc: make docker APP=fhir-server)
#
# Chỉ những gì cần cho `go build ./apps/fhir-server/cmd/fhir-server` được đưa vào context (xem apps/fhir-server/Dockerfile.dockerignore).

# ---- Stage 1: build binary tĩnh ----
FROM golang:1.26-alpine AS builder
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
# Đường dẫn khớp `replace ../trivita-observability-go`: /src/trivita-codebase-v1 -> /src/trivita-observability-go
WORKDIR /src/trivita-codebase-v1
COPY --from=observability . /src/trivita-observability-go
# Layer 1: chỉ module (cache tốt khi chỉ đổi source)
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
# Layer 2: source
COPY . ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/app ./apps/fhir-server/cmd/fhir-server \
    && mkdir -p /out/data

# ---- Stage 2: runtime gọn, non-root, không shell ----
FROM gcr.io/distroless/static-debian12:nonroot
# SQLite chỉ để dev (giữ tên tệp /data/app.db như Dockerfile chung cũ để volume dev cũ vẫn dùng được); staging/production cấm SQLite và buộc
# PostgreSQL (DATABASE_URL), kèm JWT (FHIR_JWT_ISSUER, FHIR_JWKS_URL...) hoặc token tĩnh đủ dài.
ENV DATABASE_URL="sqlite:///data/app.db" \
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

# SIGTERM -> readiness 503 -> drain -> shutdown (xem SHUTDOWN_* trong .env.example). Lệnh con: migrate, assign-project, healthcheck.
ENTRYPOINT ["/app"]
