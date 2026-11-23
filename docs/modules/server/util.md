# server/util

> Hàm tiện ích: ID, hash, mật khẩu, HTTP, ngày giờ, chuỗi.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/util` |
| Đường dẫn đích | `apps/medplum-server/src/util` |
| Số file nguồn (không tính test) | 18 |
| Cụm | lõi |

## Phụ thuộc (số import)

- Gọi tới: `config` (3), `fhir` (3), `(root)` (2), `bots` (1)
- Được gọi bởi: `fhir/operations` (38), `auth` (18), `(root)` (9), `oauth` (7), `admin` (6), `workers` (6), `fhir` (6), `bots` (5), `email` (3), `cloud` (2), `ws` (2), `scim` (1), `migrations` (1), `fhircast` (1), `ai` (1)

## Ghi chú tách

Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.

## File chính

- `util/array.ts`
- `util/auditevent.ts`
- `util/console.ts`
- `util/date.ts`
- `util/extension.ts`
- `util/layereddict.ts`
- `util/log-tag.ts`
- `util/logthrottle.ts`
- `util/patch.ts`
- `util/pdf.ts`
- `util/streams.ts`
- `util/text.ts`
- `util/time.ts`
- `util/tracing.ts`
- `util/url.ts`
- `util/validator.ts`
- `util/version.ts`
- `util/withpath.ts`
