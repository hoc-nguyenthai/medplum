# Makefile riêng của fhir-notifier: mọi lệnh gọi Makefile gốc với APP=fhir-notifier, để làm việc với một service mà không cần nhớ tên
# target của cả repo. Chạy từ thư mục này: `make test-unit`, `make image`, ...
APP  := fhir-notifier
ROOT := ../..

.DEFAULT_GOAL := help
.PHONY: help run build image test-unit test-integration test-service

help: ## Liệt kê lệnh của service này
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

run: ## Chạy service :8010 (đọc .env nếu có; cần schema đã migrate bởi fhir-server và Redis)
	$(MAKE) -C $(ROOT) run-fhir-notifier

build: ## Build binary vào ../../bin/
	$(MAKE) -C $(ROOT) build APP=$(APP)

image: ## Build image Docker trivita-fhir-notifier từ apps/fhir-notifier/Dockerfile (context là gốc repo)
	$(MAKE) -C $(ROOT) docker APP=$(APP) IMAGE=trivita-fhir-notifier:local

test-unit: ## Test đơn vị (cạnh mã), không cần Docker
	$(MAKE) -C $(ROOT) test-unit APP=$(APP)

test-integration: ## Test tích hợp (PostgreSQL và Redis thật bằng testcontainers, cần Docker)
	$(MAKE) -C $(ROOT) test-integration APP=$(APP)

test-service: ## e2e bằng image dựng từ Dockerfile (cần Docker)
	$(MAKE) -C $(ROOT) test-service APP=$(APP)
