    # Checklist port medplum sang Go

Cập nhật: 2026-11-21. Tiến độ và giới hạn chi tiết: [go-port-status.md](go-port-status.md). `[x]` là đã làm và có test; `[ ]` là chưa làm.

Mỗi giai đoạn dùng cùng một "định nghĩa xong": code + test chạy trên Memory và SQLite (PostgreSQL khi có thể) + `go build ./...`, `go vet ./...`, `go test ./...` xanh + `-race` ở package liên quan + cập nhật `go-port-status.md` và README.

## Đã xong

### 1. Kiểu FHIR (`pkg/fhir`)
- [x] Sinh 171 resource và 41 kiểu từ StructureDefinition (`tools/fhirgen`)
- [x] Bảng schema, search parameter, compartment Patient
- [x] Round-trip 70MB dữ liệu thật

### 2. FHIRPath và validator (`pkg/fhirpath`, `pkg/fhirvalidate`)
- [x] Engine FHIRPath, bộ test HL7 679/686 (7 ca đã biết lỗi ghi trong `known-failures.txt`)
- [x] 1.481 biểu thức search parameter chạy được
- [x] Validator có invariant FHIRPath, 0 lỗi trên 14 file dữ liệu thật

### 3. Search và router REST (`pkg/fhirsearch`, `pkg/fhirrouter`)
- [x] string, token, reference, date, number, quantity, uri; sort; `_include`/`_revinclude`
- [x] CRUD, history, search, conditional, JSON Patch, batch, transaction, `/metadata`
- [x] `MemoryRepository`, adapter `net/http`
- [x] Composite search (12a)
- [x] 13a: `:in`/`:not-in`/`:above`/`:below` của token
- [x] 21a: `_filter`
- [ ] `_contained`; `_filter`: ew/ss/sb/in/re/po, chained, search hệ thống
- [x] 17a: update-as-create (`FHIR_UPDATE_AS_CREATE`)
- [x] 18a: conditional read (`If-None-Match`, `If-Modified-Since`)
- [x] 20a: GraphQL (tập con: đọc, danh sách, Connection, Reference.resource, tham chiếu ngược)
- [ ] GraphQL: introspection, schema validate, `fhirpath`/`format`, mutation

### 4. Repository SQL (`pkg/fhirstore`)
- [x] PostgreSQL 16 và SQLite, bảng chỉ mục theo loại, migration goose
- [x] Bộ test hành vi dùng chung (`repotest`) và bộ đối chiếu Memory/SQL, 0 khác biệt
- [ ] MySQL

### 5. App `fhir-server` (`apps/fhir-server`)
- [x] Echo, token tĩnh 3 mức, readiness, graceful shutdown, CLI migrate, healthcheck
- [x] e2e qua HTTP và binary thật
- [x] Build Docker image (25a: đã build và chạy thử cùng compose)

### 6a. Operation, job nền, Subscription
- [x] `$validate`, `Patient/$everything`, `$reindex`, `$expunge`, vai trò admin
- [x] Job `AsyncJob` + `GET /job/{id}/status`
- [x] Subscription rest-hook: HMAC, retry, SSRF guard, lọc interaction

### 7a. Xác thực JWT và tách project (`pkg/fhirauth`)
- [x] JWT HS256 (dev) và JWKS (RS/PS/ES/EdDSA), xoay khóa, chặn alg confusion
- [x] Vai trò theo scope, `meta.project` do server gán
- [x] Cô lập qua REST, batch, transaction, include, job, Subscription
- [x] Chạy lại bộ test tách project trên PostgreSQL  (đã chạy ở 24a)

### 8a. Bulk export và terminology
- [x] `$export` (system, Patient), file Binary NDJSON, `_type`, `_since`
- [x] `$expand`, `$lookup`, `$validate-code` trên CodeSystem/ValueSet lưu trong server
- [x] 14a: `Group/$export`
- [x] 18a: `_typeFilter`
- [x] 14a: `$subsumes`, `$translate`
- [ ] `$closure`, CodeSystem ngoài (SNOMED, LOINC)

### 9a. Độ bền job
- [x] Lease + heartbeat, khôi phục job mồ côi, không chạy trùng giữa replica
- [x] Nhả lease khi tắt êm, hủy và xóa export, hết hạn export
- [ ] Fencing token, checkpoint trong job, nối lease với adapter asynq/Temporal

### 10a. Chained search và `_has`
- [x] Chained thuận, lồng nhiều cấp, `_has` lồng, tách project
- [x] 15 truy vấn đối chiếu Memory/SQL mới, 0 khác biệt
- [ ] Chained trong `_sort` và `_include`

## Chưa làm

### A. Composite search và hoàn thiện search
- [x] Parse tham số composite (`Observation?combo-code-value-quantity=...`)
- [x] Khớp composite ở `Match` và SQL (bảng chỉ mục thành phần)
- [x] Thêm truy vấn composite vào bộ đối chiếu Memory/SQL
- [ ] `_sort` theo tham số chained
- [x] `:in`/`:above`/`:below` cho token (13a)
- [x] Cập nhật giới hạn trong `fhirsearch` và `fhirstore` (12a)
- [ ] Composite: modifier, `_sort`, (đã chạy trên PostgreSQL ở 24a)

### B. Websocket subscription
- [x] Thiết kế fan-out tới mọi replica (`messaging.Broadcaster`, 15a)
- [x] Thư viện websocket: `github.com/coder/websocket` (15a)
- [x] `$get-ws-binding-token`, giao thức bind và thông báo giống Medplum (15a)
- [x] Xác thực token trên kết nối websocket, tách project (15a)
- [x] Giới hạn số kết nối, ping/pong, dọn kết nối chết, tắt êm (15a)
- [x] Test nhiều instance với Redis giả (miniredis)
- [ ] Test nhiều replica với Redis thật; replay/bền; token dùng một lần; SubscriptionTopic

### C. Operation còn lại (làm theo nhu cầu)
- [ ] Lập danh sách ưu tiên trong ~100 operation chưa làm, bỏ những cái không cần
- [ ] Mỗi operation: handler, quyền (`RequiredRole`), test dùng chung, mục trong README
- [x] 14a: `$subsumes`, `$translate`, `Group/$export`
- [ ] Ứng viên còn lại: `$convert`, `$csv`, `$closure`

### D. Danh tính đầy đủ
- [x] 11c: job nền mang quyền người gọi (ký và kiểm ảnh chụp quyền), `$export` dùng được với token giới hạn
- [ ] Resource `Project`, `ProjectMembership`, `User` lưu trong server (đề xuất bỏ: identity service phát claim)
- [x] 11b: `AccessPolicy` từ claim token (loại, tiêu chí, readonly), test `pkg/fhirauth/policy_test.go` và e2e `TestJWTAccessPolicyClaim`
- [ ] AccessPolicy: nhiều rule mỗi loại (OR), biến `%patient`, resource `AccessPolicy` lưu trong server, chặn field
- [x] 11a: scope SMART `system/`/`user/` theo loại resource (test: `pkg/fhirauth/grants_test.go`, e2e `TestJWTSMARTScopesPerResourceType`)
- [ ] Scope `patient/` (cần compartment), mang Grants vào job nền
- [x] 11d: super-admin xuyên project (scope `fhir.super`, header chọn project, nhật ký `audit=true`)
- [ ] Kiểm toán bền: ghi `AuditEvent` vào kho thay vì chỉ log
- [x] 11d: lệnh `assign-project` gán `meta.project` cho dữ liệu cũ (dry-run, idempotent)
- [ ] Phối hợp phát claim `fhir_project` ở identity service
- [ ] Test cô lập chéo project cho mọi chức năng mới

### 7b. Bot, agent, DICOM, CLI
- [x] 16a: CLI `fhir-cli` (đọc, tìm, ghi, import, export, reindex)
- [x] 17a: `import` giữ id khi đích bật update-as-create
- [ ] CLI: lệnh websocket, validate, terminology, quản trị project
- [ ] Bot: chọn runtime và sandbox (WASM, container hoặc function), giới hạn tài nguyên, bí mật, kiểm toán. Cần quyết định thiết kế trước
- [ ] Agent: HL7 v2 và thiết bị (phụ thuộc Bot và phần cứng)
- [ ] DICOM (phụ thuộc hệ thống hình ảnh bên ngoài)

## Vận hành (không tính là giai đoạn)
- [ ] Commit theo từng giai đoạn (chưa có commit nào từ giai đoạn 1 đến 10a; `medplum/` giữ ngoài git)
- [x] Dockerfile và compose đã sửa, build và chạy thử image (25a)
- [x] 19a: `make test-pg` và test tách project chạy được trên PostgreSQL (đã chuẩn bị, chưa chạy: máy này không có Docker)
- [x] Chạy `make test-pg` trên máy có Docker và xử lý lỗi nếu có (giai đoạn 7 đến 18) (24a: đã chạy bằng PostgreSQL nhúng, tất cả PASS)
- [ ] Test tải (search, bulk export, nhiều replica tranh job)
- [ ] CI chạy `go vet`, `go test -race`, kiểm tra kiến trúc
- [x] 22a: runbook (`runbook.md`): sao lưu, migrate, xoay khóa, job kẹt, Subscription, sự cố
- [x] 23a: Redis tự xử lý lại message lỗi (XCLAIM) và dead-letter sau 5 lần giao
- [ ] Công cụ replay dead-letter; cấu hình ClaimIdle/MaxDeliveries qua env; thử với Redis thật
- [ ] Diễn tập runbook trên staging (sao lưu/khôi phục, xoay khóa, tắt êm)
- [x] 27a: Redis thật và hai replica dùng chung PG + Redis (webhook một lần, websocket chéo replica, job chéo replica, migrate đồng thời)
- [ ] Còn thiếu: tranh job dưới tải (không chạy đúp), mất kết nối Redis giữa chừng, Redis cluster/sentinel, JWKS thật, tải lớn
- [x] 25a: build và chạy thử Docker image + compose trên PostgreSQL thật
- [x] 26a: e2e HTTP của fhir-server (JWT, nhiều project, job, Subscription, GraphQL) chạy trên PostgreSQL
- [x] 28a: test tranh chấp (If-Match, create có điều kiện) và sửa lỗi tạo trùng bằng KeyLocker; e2e hai replica
- [ ] Tranh chấp chưa kiểm: PATCH, Bundle transaction có nhiều thao tác có điều kiện song song, job/export dưới tải
- [ ] COMMIT repo (git chưa có commit nào: một lỗi sửa file đã suýt mất ~900 dòng test, phải khôi phục từ transcript)
- [x] 29a: tranh chấp update-as-create, update/delete có điều kiện (3 lỗi thật đã sửa)
- [x] 30a: benchmark Router+Store (make bench) và tối ưu chained search 211ms -> 17ms
- [ ] Tối ưu tiếp: _has/chained thành truy vấn con SQL, _sort không dùng truy vấn con tương quan, gom INSERT khi lập chỉ mục; đo lại ở 100k+ resource

## Kế hoạch 31 đến 40

Xem [plan-31-40.md](plan-31-40.md) (bản đề xuất, chờ duyệt): 31 commit và CI, 32 hiệu năng 1, 33 hiệu năng 2 (`_has`/chained SQL), 34 độ tin cậy, 35 vận hành thật (staging, JWKS), 36 search, 37 operation, 38 phân quyền và AuditEvent, 39 GraphQL/CLI/websocket, 40 quyết định thiết kế (Bot, Agent, DICOM, MySQL).
