# Mốc xanh trước khi di chuyển (bước 0)

Ngày: 2026-11-20. Mã: `medplum/` = tag `v5.2.18` (xem [UPSTREAM.md](UPSTREAM.md)), không sửa gì.
Môi trường: macOS (Darwin 25.6), Node 24.22.3, npm 11.19.1 (`packageManager` ghi 11.21.2, chỉ cảnh báo).

## Lệnh

```bash
source ~/.nvm/nvm.sh && nvm use 24
cd medplum
npm ci
npm run build
npx turbo run test --filter='!@medplum/docs' --filter='!@medplum/server' --concurrency=1 --continue
```

Dùng `--concurrency=1`: chạy song song mặc định làm test UI của `react`, `react-scheduling`, `medplum-provider` (và 3 package nhỏ) bị timeout trên máy này; chạy tuần tự thì xanh hết.

## Kết quả

| Hạng mục | Kết quả |
|---|---|
| `npm ci` | thành công (có cảnh báo install-scripts của npm 11, bỏ qua) |
| `npm run build` | 25/25 task thành công, 59 giây |
| Test, chạy song song lần đầu | 69/75 task xanh; 6 package có 45 test timeout |
| Test, chạy lại tuần tự 6 package đó | 21/21 task xanh, 0 fail |

Số test pass theo package (không tính skip):

| Package | Test |
|---|---|
| `@medplum/core` | 3126 |
| `medplum-provider` | 1853 |
| `@medplum/react` | 1595 |
| `@medplum/react-scheduling` | 1193 |
| `@medplum/agent` | 692 |
| `@medplum/app` | 503 |
| `@medplum/react-hooks` | 253 |
| `@medplum/cli` | 225 |
| `medplum-healthie-importer` | 191 |
| `@medplum/fhir-router` | 162 |
| `medplum-demo-bots` | 136 |
| `@medplum/mock` | 91 |
| `@medplum/cdk` | 81 |
| `@medplum/dosespot-react` | 63 |
| `@medplum/hl7` | 62 |
| `@medplum/examples` | 48 |
| `@medplum/scriptsure-react` | 38 |
| `@medplum/dosespot-core` | 31 |
| `@medplum/health-gorilla-core` | 28 |
| `@medplum/health-gorilla-react` | 23 |
| `medplum-patient-intake-demo` | 23 |
| `medplum-photon-integration` | 16 |
| `foomedical`, `medplum-task-demo` | 7 và 7 |
| `medplum-eligibility-demo` | 4 |
| `@medplum/generator` | 1 |

## Chưa có mốc

- `@medplum/server`: test cần PostgreSQL và Redis, máy chưa chạy hai dịch vụ này. Bỏ khỏi mốc.
- `@medplum/e2e`: cần trình duyệt và server chạy. Bỏ khỏi mốc.
- `@medplum/docs`: loại theo script `test` của upstream.

Hệ quả: bước 6 chỉ chứng minh được việc di chuyển không làm hỏng các package ở bảng trên và build. Muốn chứng minh cho `server`, cần dựng Postgres và Redis (xem `packages/server/README.md`) rồi chạy test server cả trước lẫn sau khi di chuyển.

## Tiêu chí so sánh ở bước 6

- Build: 25/25 thành công.
- Test: số pass của từng package bằng bảng trên, 0 fail, chạy với `--concurrency=1`.
