# Makefile riêng của dicomweb: mọi lệnh gọi Makefile gốc với APP=dicomweb, để làm việc với một service mà không cần nhớ tên
# target của cả repo. Chạy từ thư mục này: `make test-unit`, `make image`, ...
APP  := dicomweb
ROOT := ../..

.DEFAULT_GOAL := help
.PHONY: help run build image swagger test-unit

help: ## Liệt kê lệnh của service này
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

run: ## Chạy service :8008 (đọc .env nếu có; cần schema đã migrate bởi fhir-server và FHIR_JWT_*, FHIR_BINARY_STORAGE)
	$(MAKE) -C $(ROOT) run-dicomweb

build: ## Build binary vào ../../bin/
	$(MAKE) -C $(ROOT) build APP=$(APP)

image: ## Build image Docker trivita-dicomweb từ apps/dicomweb/Dockerfile (context là gốc repo)
	$(MAKE) -C $(ROOT) docker APP=$(APP) IMAGE=trivita-dicomweb:local

swagger: ## Sinh lại OpenAPI từ annotation (docs/swagger.*); rồi UPDATE_DOCS=1 go test ./apps/dicomweb/docs để cập nhật api-contract.md
	$(MAKE) -C $(ROOT) swagger APP=$(APP)

test-unit: ## Test đơn vị (cạnh mã), không cần Docker
	$(MAKE) -C $(ROOT) test-unit APP=$(APP)
