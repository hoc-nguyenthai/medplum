# Makefile riêng của fhir-worker: mọi lệnh gọi Makefile gốc với APP=fhir-worker, để làm việc với một service mà không cần nhớ tên
# target của cả repo. Chạy từ thư mục này: `make test-unit`, `make image`, ...
APP  := fhir-worker
ROOT := ../..

.DEFAULT_GOAL := help
.PHONY: help run build image test-unit test-integration test-service

help: ## Liệt kê lệnh của service này
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

run: ## Chạy service :8011 (đọc .env nếu có; cần schema đã migrate bởi fhir-server; FHIR_BINARY_STORAGE và FHIR_JOB_SIGNING_KEY trùng fhir-server)
	$(MAKE) -C $(ROOT) run-fhir-worker

build: ## Build binary vào ../../bin/
	$(MAKE) -C $(ROOT) build APP=$(APP)

image: ## Build image Docker trivita-fhir-worker từ apps/fhir-worker/Dockerfile (context là gốc repo)
	$(MAKE) -C $(ROOT) docker APP=$(APP) IMAGE=trivita-fhir-worker:local

test-unit: ## Test đơn vị (cạnh mã), không cần Docker
	$(MAKE) -C $(ROOT) test-unit APP=$(APP)

test-integration: ## Test tích hợp (PostgreSQL và Redis thật bằng testcontainers, cần Docker)
	$(MAKE) -C $(ROOT) test-integration APP=$(APP)

test-service: ## e2e bằng image dựng từ Dockerfile (cần Docker)
	$(MAKE) -C $(ROOT) test-service APP=$(APP)
