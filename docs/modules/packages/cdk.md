# @medplum/cdk

> Hạ tầng AWS bằng CDK (VPC, RDS, ECS, S3, CloudFront).

| | |
|---|---|
| Loại | app |
| Đường dẫn hiện tại | `medplum/packages/cdk` |
| Đường dẫn đích | `apps/medplum-cdk` |
| Số file nguồn (không tính test) | 12 |
| Thư mục `src/` | `backend.ts`, `cloudtrail.ts`, `config.test.ts`, `config.ts`, `frontend.ts`, `guardduty.ts`, `images.test.ts`, `images.ts`, `index.test.ts`, `index.ts`, `oai.ts`, `stack.ts`, `storage.ts`, `waf.ts` |
| Entry point | `—` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 4, thứ tự 6/7 |

## Phụ thuộc

- Runtime: [`core`](core.md)
- Chỉ dev: —
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/cdk`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/cdk` phải xanh.
