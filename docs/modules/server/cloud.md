# server/cloud

> Adapter cloud: AWS (Lambda, S3, SES, Textract), Azure, GCP, Fission.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/cloud` |
| Đường dẫn đích | `apps/medplum-server/src/cloud` |
| Số file nguồn (không tính test) | 17 |
| Cụm | lõi |

## Phụ thuộc (số import)

- Gọi tới: `config` (15), `storage` (10), `(root)` (8), `bots` (4), `util` (2), `fhir` (2), `email` (1)
- Được gọi bởi: `workers` (4), `fhir/operations` (4), `bots` (3), `config` (3), `storage` (3), `admin` (1), `fhir` (1), `email` (1)

## Ghi chú tách

Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.

## File chính

- `cloud/aws/config.ts`
- `cloud/aws/deploy.ts`
- `cloud/aws/deploystreaming.ts`
- `cloud/aws/email.ts`
- `cloud/aws/execute.ts`
- `cloud/aws/executestreaming.ts`
- `cloud/aws/lambda.ts`
- `cloud/aws/storage.ts`
- `cloud/aws/textract.ts`
- `cloud/azure/config.ts`
- `cloud/azure/storage.ts`
- `cloud/fission/deploy.ts`
- `cloud/fission/execute.ts`
- `cloud/fission/utils.ts`
- `cloud/fission/wrapper.ts`
- `cloud/gcp/config.ts`
- `cloud/gcp/storage.ts`
