# Kế hoạch di chuyển medplum vào monorepo (giữ TypeScript)

Trạng thái: **đề xuất, chưa thực hiện**. Ngày lập: 2026-11-20.

## Mục tiêu

Đưa toàn bộ domain module của `medplum/` vào cấu trúc `apps/` của monorepo `trivita-codebase-v1`, giữ nguyên TypeScript, giữ tên package `@medplum/*`.

## Phát hiện ảnh hưởng đến kế hoạch

1. `medplum/` có `.git` riêng (bản clone upstream); repo gốc chưa phải git.
2. Mọi package `extends "../../tsconfig.json"` và nhiều script trỏ `../../`. Dùng thư mục phẳng sâu 2 cấp (`libs/medplum-*`, `apps/medplum-*`, `tools/medplum-*`) để giữ nguyên các đường dẫn này.
3. Tên package `@medplum/*` giữ nguyên nên không sửa import.
4. Trùng tên file ở root: `Dockerfile`, `docker-compose.yml`, `README.md`, `.gitignore`, `.dockerignore`.
5. Yêu cầu runtime: Node `>=22.22 <23 || >=24.2 <25`, npm 11. Chưa cài `node_modules`.

## Cấu trúc đích

```
trivita-codebase-v1/
├── go.mod, pkg/, apps/<service Go>            (giữ nguyên)
├── package.json  turbo.json  tsconfig.json  biome.json  eslint.config.mjs  vitest.config.ts
├── libs/medplum-*      thư viện TS dùng chung
├── apps/medplum-*      server, agent, app, cli, cli-wrapper, cdk, bot-layer
├── tools/medplum-*     generator, e2e, storybook, graphiql, docs, examples, eslint-config, create-medplum
├── examples/*          giữ nguyên
├── deploy/medplum/     scripts, postgres, charts, terraform, docker-compose
└── docs/       tài liệu này
```

Bảng ánh xạ từng package nằm ở [modules/README.md](modules/README.md) (cột "Đích").

Quy tắc phụ thuộc: `libs → libs`, `apps → libs`, `tools → libs|apps`; `libs` không import `apps`; `apps` không import nhau.

## Các bước

### Bước 0. Chốt quyết định và tạo mốc an toàn
- Quyết định về `medplum/.git`:
  - A (khuyến nghị): ghi commit và version upstream vào `docs/UPSTREAM.md`, bỏ `.git` lồng.
  - B: giữ lịch sử bằng `git subtree` hoặc `git filter-repo`.
- Backup: `tar czf medplum-backup.tgz medplum/` (kèm `.git`).
- Mốc xanh: trong `medplum/` chạy `npm ci && npm run build && npm test` bằng đúng Node và npm yêu cầu. Lưu số test pass.

### Bước 1. Khởi tạo repo gốc
- `git init`, commit phần Go hiện có làm mốc đầu.
- Gộp `.gitignore`: thêm `node_modules/`, `dist/`, `build/`, `.turbo/`, `coverage/`, `*.tsbuildinfo`.

### Bước 2. Dựng workspace ở root (chưa chuyển package)
- Chuyển lên root: `package.json`, `package-lock.json`, `turbo.json`, `tsconfig.json`, `biome.json`, `eslint.config.mjs`, `vitest.config.ts`, `api-extractor.json`, `tsdoc.json`, `sonar-project.properties`, `.prettierignore`.
- Sửa `package.json`: `name`, `workspaces: ["apps/medplum-*", "libs/medplum-*", "tools/medplum-*", "examples/*"]`, các filter trong script `build`, `sort-package-json`.
- Đặt `.nvmrc` (Node 24).
- Xử lý trùng tên: `Dockerfile` → `apps/medplum-server/Dockerfile`; `docker-compose*.yml` → `deploy/medplum/`; `README.md`, `AGENTS.md`, `CLAUDE.md`, `SECURITY.md` → `docs/upstream/`.

### Bước 3. Chuyển lib theo thứ tự phụ thuộc
Mỗi đợt một commit; sau mỗi đợt `npm install` rồi `turbo run build test --filter=<đợt>`.
- Đợt A: `fhirtypes`, `definitions`, `core`.
- Đợt B: `hl7`, `ccda`, `fhir-router`, `mock`.
- Đợt C: `react-hooks`, `react`, `react-scheduling`, `dosespot-core`, `dosespot-react`, `health-gorilla-core`, `health-gorilla-react`, `scriptsure-react`.
- Điểm dừng: đợt chưa xanh thì không sang đợt sau.

### Bước 4. Chuyển app và tool
- Thứ tự: `bot-layer` → `server` → `agent`, `cli`, `cli-wrapper`, `cdk` → `app` → `tools/*` → `examples/*`.
- Với `server` kiểm tra thêm `build.mjs`, `vite.config.ts`, `medplum*.config.json`, `turbo.json` riêng.
- Sau mỗi package: `grep -rn "\.\./\.\./"` để bắt đường dẫn còn lệch.

### Bước 5. Nối lại hạ tầng
- Scripts (`deploy/medplum/scripts/*.sh`, khoảng 30 file): đổi `packages/<x>` sang đường dẫn mới.
- Docker: `ENTRYPOINT` trong `Dockerfile` đổi `packages/server/dist/...` thành `apps/medplum-server/dist/...`; context build tính từ root; `.dockerignore` loại `node_modules`.
- CI: chuyển workflow từ `medplum/.github` lên root, thêm `paths` filter để Go và TS chạy độc lập.
- `sonar-project.properties`: sửa `sonar.sources`, `exclusions`.
- `Makefile`: thêm `medplum-install`, `medplum-build`, `medplum-test`, `medplum-lint`, `run-medplum-server`; `make ci` gọi cả hai phía.
- Ranh giới phụ thuộc TS: thêm `dependency-cruiser` với luật ở mục "Quy tắc phụ thuộc" (tương đương `architecture_test.go`).

### Bước 6. Kiểm chứng
- `npm ci` sạch; `turbo run build lint test` xanh; số test pass bằng mốc bước 0.
- `make test` phần Go xanh; `architecture_test.go` không bị chậm hoặc lỗi.
- `docker build` server từ root; chạy với Postgres và Redis; kiểm tra `/healthcheck` và đăng nhập.
- Cập nhật `README.md` root.

### Bước 7. Dọn dẹp
- Xóa `medplum/` rỗng.
- Cập nhật mẫu loại trừ codegraph thành `apps/medplum-*`, `libs/medplum-*`, `tools/medplum-*`, `examples/*` và cập nhật memory.

## Giai đoạn sau (ngoài phạm vi đợt này)

Tách `medplum-server` khi đã ổn định:

| App mới | Lấy từ server | Điều kiện |
|---|---|---|
| `medplum-identity` | `auth`, `oauth`, `admin`, `scim` | `auth` gọi `fhir` 48 lần: cần tách `libs/medplum-fhir-repo` trước; hoặc thay bằng service Go identity của Trivita |
| `medplum-worker` | `workers`, `migrations` | phá vòng `fhir → workers → migrations → fhir` |
| `medplum-fhir-bridge` | `dicom`, `fhircast`, `mcp`, `cds`, `webhook` | cụm nhỏ, một chiều vào `fhir` |

Chi tiết phụ thuộc từng module xem [modules/server](modules/README.md).

## Rủi ro

| Rủi ro | Cách giảm |
|---|---|
| Sót đường dẫn trong scripts và Dockerfile | `grep -rn "packages/"` toàn repo sau bước 5, kết quả phải rỗng |
| Lock file đổi, kéo phiên bản mới | copy `package-lock.json` cũ lên root trước khi `npm install` |
| Mất khả năng nâng cấp upstream | ghi commit upstream vào `UPSTREAM.md` |
| `node_modules` làm chậm hoặc nhiễu phần Go | kiểm `go build ./...` và `go test ./...` sau bước 2 |

## Quyết định còn mở

1. `medplum/.git`: phương án A hay B.
2. Giữ hay thay module danh tính (`auth`, `oauth`, `admin`, `scim`) bằng service Go.
3. Thực hiện từng bước có dừng kiểm tra, hay chạy liên tục tới bước 6.
