# Makefile riêng của fhir-downloader: mọi lệnh gọi Makefile gốc với APP=fhir-downloader, để làm việc với một service mà không cần nhớ tên
# target của cả repo. Chạy từ thư mục này: `make test-unit`, `make image`, ...
APP  := fhir-downloader
ROOT := ../..

.DEFAULT_GOAL := help
.PHONY: help run build image test-unit test-integration test-service

help: ## Liệt kê lệnh của service này
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

run: ## Chạy service :8009 (đọc .env nếu có; cần schema đã migrate bởi fhir-server, Redis và FHIR_BINARY_STORAGE)
	$(MAKE) -C $(ROOT) run-fhir-downloader

build: ## Build binary vào ../../bin/
	$(MAKE) -C $(ROOT) build APP=$(APP)

image: ## Build image Docker trivita-fhir-downloader từ apps/fhir-downloader/Dockerfile (context là gốc repo)
	$(MAKE) -C $(ROOT) docker APP=$(APP) IMAGE=trivita-fhir-downloader:local

test-unit: ## Test đơn vị (cạnh mã), không cần Docker
	$(MAKE) -C $(ROOT) test-unit APP=$(APP)

test-integration: ## Test tích hợp (PostgreSQL và Redis thật bằng testcontainers, cần Docker)
	$(MAKE) -C $(ROOT) test-integration APP=$(APP)

test-service: ## e2e bằng image dựng từ Dockerfile (cần Docker)
	$(MAKE) -C $(ROOT) test-service APP=$(APP)
