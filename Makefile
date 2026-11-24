# Lệnh thường dùng cho monorepo. Chạy `make help` để xem danh sách.

APP        ?= fhir-server
APP_DIR    := apps/$(APP)
BIN        := bin/$(APP)
IMAGE      ?= trivita-$(APP):local
COMPOSE    := docker compose --env-file $(APP_DIR)/.env
MIGRATE    ?= status
FHIR_SERVER_PORT ?= :8005
AUTH_PORT ?= :8006
WS_GATEWAY_PORT ?= :8007
DICOMWEB_PORT ?= :8008
comma := ,

.DEFAULT_GOAL := help
.PHONY: help swagger tidy build run run-fhir-server run-auth run-ws-gateway run-dicomweb test-unit test-db test-integration test-service test-app test-e2e bench-ws test-auth-pg migrate test test-race test-pg bench cover lint fmt ci docker up down

help: ## Liệt kê các lệnh
	@grep -E '^[a-zA-Z0-9_-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

# Thư mục phụ swag cần parse để phân giải kiểu dùng trong annotation.
SWAG_DIRS_fhir-server := pkg/fhir
SWAG_DIRS_auth := apps/auth/internal/application,apps/auth/internal/domain/scim,pkg/fhir
# Kiểu thay thế khi sinh OpenAPI (mặc định swag đọc .swaggo ở gốc). auth thay fhir.Extension bằng object tự do: kiểu này (value[x]) kéo theo ~30 kiểu dữ liệu
# FHIR (Address, Dosage, TimingRepeat...) làm spec phình gần gấp đôi và làm IDE đứng khi mở swagger.yaml. fhir-server còn thay fhir.ParametersParameter
# (value[x] của Parameters, cũng kéo cả bộ kiểu dữ liệu FHIR) bằng object tự do.
SWAG_OVERRIDES_auth := apps/auth/.swaggo
SWAG_OVERRIDES_fhir-server := apps/fhir-server/.swaggo

swagger: ## Sinh lại OpenAPI của một app: make swagger APP=fhir-server|auth|ws-gateway; cần `go install github.com/swaggo/swag/cmd/swag@latest`
	swag init -g swagger.go -d $(APP_DIR)/internal/presentation/httpapi$(if $(SWAG_DIRS_$(APP)),$(comma)$(SWAG_DIRS_$(APP))) -o $(APP_DIR)/docs --parseInternal$(if $(SWAG_OVERRIDES_$(APP)), --overridesFile $(SWAG_OVERRIDES_$(APP)))

tidy: ## go mod tidy
	go mod tidy

build: ## Build binary tĩnh vào bin/: make build APP=fhir-server|fhir-cli
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) ./$(APP_DIR)/cmd/$(APP)

run: ## Chạy một app: make run APP=fhir-server (đọc apps/<app>/.env nếu có)
	cd $(APP_DIR) && go run ./cmd/$(APP)

run-fhir-server: ## fhir-server :8005 (cần FHIR_WRITE_TOKENS trong .env hoặc FHIR_AUTH_DISABLED=true)
	cd apps/fhir-server && HTTP_ADDR=$(FHIR_SERVER_PORT) go run ./cmd/fhir-server

run-auth: ## auth :8006 (SQLite ./auth.db chỉ dev; đọc apps/auth/.env nếu có)
	cd apps/auth && HTTP_ADDR=$(AUTH_PORT) go run ./cmd/auth

run-ws-gateway: ## ws-gateway :8007 (cần Redis và FHIR_WS_SIGNING_KEY giống fhir-server; đọc apps/ws-gateway/.env nếu có)
	cd apps/ws-gateway && HTTP_ADDR=$(WS_GATEWAY_PORT) go run ./cmd/ws-gateway

run-dicomweb: ## dicomweb :8008 (cần schema đã migrate bởi fhir-server, FHIR_JWT_*, FHIR_BINARY_STORAGE; đọc apps/dicomweb/.env nếu có)
	cd apps/dicomweb && HTTP_ADDR=$(DICOMWEB_PORT) go run ./cmd/dicomweb

migrate: ## Chạy migration của APP có DB: make migrate APP=fhir-server MIGRATE=up|down|status
	cd $(APP_DIR) && go run ./cmd/$(APP) migrate $(MIGRATE)

test: ## Chạy toàn bộ test (đơn vị + tích hợp + e2e theo service + kiến trúc); test cần Docker bị bỏ qua nếu không có Docker (CI đặt TRIVITA_REQUIRE_DOCKER=1 để coi là lỗi)
	go test ./...

# Bốn tầng test (xem docs/plans/service-independence.md). APP là thư mục trong apps/.
#   unit         *_test.go cạnh mã, không cần Docker
#   integration  apps/<APP>/tests/integration: PostgreSQL/Redis thật bằng testcontainers, chạy trong process
#   service      apps/<APP>/tests/e2e: dựng image từ Dockerfile và chạy cùng PostgreSQL/Redis thật
#   system       tests nhiều service ở gốc repo (make test-e2e)
test-unit: ## Test đơn vị, không cần Docker: make test-unit [APP=ws-gateway]
	go test -short -count=1 $(if $(APP),./apps/$(APP)/internal/... ./apps/$(APP)/cmd/...,./...)

test-db: ## Mô hình DB dùng chung: script init THẬT, role, quyền, không FK chéo, Pending, lint SQL (cần Docker)
	go test -count=1 -timeout 20m ./tests/dbmodel/ ./pkg/database/ ./pkg/testkit/
	go test -count=1 -timeout 10m -run 'TwoServices|TestStartsOnOwnSchema|TestSeparateMigrationRoles' ./pkg/fhirstore/ ./apps/auth/internal/bootstrap/ ./apps/fhir-server/internal/bootstrap/
	go test -count=1 -run 'TestSQLIsSchemaAgnostic|TestSchemaLint' .

test-integration: ## Test tích hợp của một app (testcontainers, cần Docker): make test-integration APP=ws-gateway
	@test -n "$(APP)" || (echo "thiếu APP=<tên app>"; exit 2)
	@test -d apps/$(APP)/tests/integration || (echo "apps/$(APP) chưa có tests/integration"; exit 2)
	go test -count=1 -timeout 15m ./apps/$(APP)/tests/integration/...

test-service: ## e2e của một service bằng image dựng từ Dockerfile (cần Docker): make test-service APP=ws-gateway
	@test -n "$(APP)" || (echo "thiếu APP=<tên app>"; exit 2)
	@test -d apps/$(APP)/tests/e2e || (echo "apps/$(APP) chưa có tests/e2e"; exit 2)
	go test -count=1 -timeout 20m ./apps/$(APP)/tests/e2e/...

test-app: ## Mọi tầng của một app: unit, integration, service: make test-app APP=ws-gateway
	@test -n "$(APP)" || (echo "thiếu APP=<tên app>"; exit 2)
	go test -count=1 -timeout 25m ./apps/$(APP)/...

test-race: ## Test với race detector
	go test -race ./...

# Chạy bộ test PostgreSQL (fhirstore: bộ hành vi dùng chung, đối chiếu Memory/SQL, composite, terminology, chained...) trên một
# container postgres:16 tạm. Cần Docker. Hoặc đặt sẵn FHIRSTORE_TEST_POSTGRES_URL trỏ tới PostgreSQL có sẵn rồi chạy
# `go test ./pkg/fhirstore/... ./pkg/fhirauth/...` (mỗi test tự tạo và xóa schema riêng).
PG_TEST_PORT ?= 54329
REDIS_TEST_PORT ?= 56379
test-pg: ## (sẽ thay bằng testcontainers, bước T3) Test PostgreSQL + Redis thật bằng container tạm (cần Docker): store, auth, router, sub, messaging, fhircast
	@docker rm -f fhir-test-pg fhir-test-redis >/dev/null 2>&1 || true
	docker run -d --rm --name fhir-test-pg -e POSTGRES_PASSWORD=secret -p $(PG_TEST_PORT):5432 postgres:16-alpine >/dev/null
	docker run -d --rm --name fhir-test-redis -p $(REDIS_TEST_PORT):6379 redis:7-alpine >/dev/null
	@until docker exec fhir-test-pg pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
	FHIRSTORE_TEST_POSTGRES_URL="postgres://postgres:secret@localhost:$(PG_TEST_PORT)/postgres?sslmode=disable" \
	FHIR_TEST_REDIS_URL="redis://localhost:$(REDIS_TEST_PORT)/1" MESSAGING_TEST_REDIS_ADDR=localhost:$(REDIS_TEST_PORT) FHIRCAST_TEST_REDIS_ADDR=localhost:$(REDIS_TEST_PORT) \
	go test ./pkg/fhirstore/... ./pkg/fhirauth/... ./pkg/fhirrouter/... ./pkg/fhirsub/... ./pkg/messaging/... ./pkg/fhircast/... ./apps/fhir-server/... -count=1 -p 1; \
	status=$$?; docker rm -f fhir-test-pg fhir-test-redis >/dev/null 2>&1; exit $$status

test-e2e: ## (system e2e, sẽ chuyển sang tests/e2e và image thật ở bước T4) Test nhiều tiến trình thật: auth cấp token, fhir-server nhận, ws-gateway giữ websocket qua Redis (build binary, SQLite tạm, miniredis; không cần Docker)
	TRIVITA_E2E=1 go test ./e2e -count=1 -v

WS_LOAD_CONNS ?= 1000
bench-ws: ## Đo tải ws-gateway thật (cần Docker cho Redis): WS_LOAD_CONNS=4000 make bench-ws (RSS mỗi socket và độ trễ fan-out; kết quả in bằng t.Log)
	TRIVITA_E2E_LOAD=1 WS_LOAD_CONNS=$(WS_LOAD_CONNS) go test -count=1 -timeout 20m -run TestWSLoadFanOutAndMemory -v ./apps/ws-gateway/tests/load/

test-auth-pg: ## Test auth trên PostgreSQL thật (store, application, e2e) bằng container tạm; cần Docker
	@docker rm -f auth-test-pg >/dev/null 2>&1 || true
	docker run -d --rm --name auth-test-pg -e POSTGRES_PASSWORD=secret -p $(PG_TEST_PORT):5432 postgres:16-alpine >/dev/null
	@until docker exec auth-test-pg pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done; sleep 2
	AUTH_TEST_POSTGRES_URL="postgres://postgres:secret@localhost:$(PG_TEST_PORT)/postgres?sslmode=disable" \
	go test -race -count=1 ./apps/auth/... ; \
	status=$$?; docker rm -f auth-test-pg >/dev/null 2>&1; exit $$status

bench: ## Benchmark Router+Store trên SQLite (và PostgreSQL nếu đặt FHIRSTORE_TEST_POSTGRES_URL); BENCH_PATIENTS=5000 (mặc định)
	go test ./pkg/fhirstore -run '^$$' -bench FHIR -benchtime 30x -timeout 30m

cover: ## Coverage toàn repo (tính cả test chéo package) -> coverage.html
	go test -race -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html

lint: ## gofmt + go vet (+ golangci-lint nếu đã cài)
	@test -z "$$(gofmt -l .)" || (echo "gofmt cần chạy cho:"; gofmt -l .; exit 1)
	go vet ./...
	@command -v golangci-lint >/dev/null && golangci-lint run ./... || echo "(bỏ qua golangci-lint: chưa cài)"

fmt: ## gofmt -w
	gofmt -w .

ci: lint test-race ## Những gì CI phải chạy

# Mỗi service có Dockerfile RIÊNG (apps/<app>/Dockerfile), build từ gốc repo (thư viện quan sát nằm trong pkg/observability).

docker: ## Build image của một service: make docker APP=auth|fhir-server|ws-gateway (dùng apps/<APP>/Dockerfile)
	@test -f apps/$(APP)/Dockerfile || { echo "apps/$(APP) không có Dockerfile (chỉ service mới có; fhir-cli là công cụ dòng lệnh)"; exit 2; }
	docker build -f apps/$(APP)/Dockerfile -t $(IMAGE) .

up: ## Chạy fhir-server + PostgreSQL bằng compose
	$(COMPOSE) up --build fhir-server postgres

down: ## Dừng và xóa cả volume dữ liệu
	$(COMPOSE) down --volumes
