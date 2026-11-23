# Makefile riêng của ws-gateway: mọi lệnh gọi Makefile gốc với APP=ws-gateway, để làm việc với một service mà không cần nhớ tên
# target của cả repo. Chạy từ thư mục này: `make test-unit`, `make test-integration`, ...
APP  := ws-gateway
ROOT := ../..

.DEFAULT_GOAL := help
.PHONY: help run build image test-unit test-integration test-service test-load test-app

help: ## Liệt kê lệnh của service này
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

run: ## Chạy service :8007 (đọc .env nếu có; cần Redis và FHIR_WS_SIGNING_KEY)
	$(MAKE) -C $(ROOT) run-ws-gateway

build: ## Build binary vào ../../bin/
	$(MAKE) -C $(ROOT) build APP=$(APP)

image: ## Build image Docker trivita-ws-gateway từ apps/ws-gateway/Dockerfile (context là gốc repo, thư viện quan sát qua build context có tên)
	$(MAKE) -C $(ROOT) docker APP=$(APP) IMAGE=trivita-ws-gateway:local

test-unit: ## Test đơn vị (cạnh mã), không cần Docker
	$(MAKE) -C $(ROOT) test-unit APP=$(APP)

test-integration: ## Test tích hợp với Redis thật (testcontainers, cần Docker)
	$(MAKE) -C $(ROOT) test-integration APP=$(APP)

test-service: ## e2e: dựng image từ Dockerfile rồi chạy cùng Redis thật (cần Docker)
	$(MAKE) -C $(ROOT) test-service APP=$(APP)

test-load: ## Đo tải: WS_LOAD_CONNS=4000 make test-load
	$(MAKE) -C $(ROOT) bench-ws

test-app: ## Mọi tầng ở trên
	$(MAKE) -C $(ROOT) test-app APP=$(APP)
