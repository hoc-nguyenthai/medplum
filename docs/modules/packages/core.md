# @medplum/core

> Thư viện lõi: MedplumClient, FHIRPath, parser và validator kiểu FHIR, search parameter, tiện ích chung. Gần như mọi package khác phụ thuộc vào đây.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/core` |
| Đường dẫn đích | `libs/medplum-core` |
| Số file nguồn (không tính test) | 79 |
| Thư mục `src/` | `__fixtures__`, `access.test.ts`, `access.ts`, `agent.ts`, `array.test.ts`, `array.ts`, `base-schema-utils.test.ts`, `base-schema-utils.ts`, `base-schema.json`, `base64.test.ts`, `base64.ts`, `bundle.test.ts`, `bundle.ts`, `cache.test.ts`, `cache.ts`, `cds.test.ts`, `cds.ts`, `client-fhircast.test.ts`, `client-subscriptions.test.ts`, `client-test-utils.ts`, `client.test.ts`, `client.ts`, `config.ts`, `constants.ts`, `contenttype.ts`, `cron.test.ts`, `cron.ts`, `crypto.ts`, `datasampler.test.ts`, `datasampler.ts`, `default-values.test.ts`, `default-values.ts`, `elements-context.test.ts`, `elements-context.ts`, `environment.test.ts`, `environment.ts`, `eventtarget.test.ts`, `eventtarget.ts`, `fhircast`, `fhirlexer`, `fhirmapper`, `fhirpath`, `filebuilder.test.ts`, `filebuilder.ts`, `filter`, `format.test.ts`, `format.ts`, `hl7.test.ts`, `hl7.ts`, `index.test.ts`, `index.ts`, `jwt.test.ts`, `jwt.ts`, `keyvalue.test.ts`, `keyvalue.ts`, `logger.test.ts`, `logger.ts`, `medication-order-utils.test.ts`, `medication-order-utils.ts`, `outcomes.test.ts`, `outcomes.ts`, `patch`, `pharmacy-utils.test.ts`, `pharmacy-utils.ts`, `readablepromise.test.ts`, `readablepromise.ts`, `scheduling.test.ts`, `scheduling.ts`, `schema-crawler.ts`, `schema.test.ts`, `schema.ts`, `search`, `sftp.test.ts`, `sftp.ts`, `smarthealthlinks.test.ts`, `smarthealthlinks.ts`, `sql-on-fhir`, `static-client.test.ts`, `static-client.ts`, `storage.test.ts`, `storage.ts`, `subscriptions`, `types.test.ts`, `types.ts`, `typeschema`, `utils.test.ts`, `utils.ts`, `version-utils.test.ts`, `version-utils.ts`, `websockets` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt A |

## Phụ thuộc

- Runtime: —
- Chỉ dev: [`definitions`](definitions.md), [`fhirtypes`](fhirtypes.md)
- Được dùng bởi: [`agent`](agent.md), [`bot-layer`](bot-layer.md), [`ccda`](ccda.md), [`cdk`](cdk.md), [`cli`](cli.md), [`cli-wrapper`](cli-wrapper.md), [`dosespot-core`](dosespot-core.md), [`dosespot-react`](dosespot-react.md), [`fhir-router`](fhir-router.md), [`health-gorilla-core`](health-gorilla-core.md), [`health-gorilla-react`](health-gorilla-react.md), [`hl7`](hl7.md), [`mock`](mock.md), [`react`](react.md), [`react-hooks`](react-hooks.md), [`react-scheduling`](react-scheduling.md), [`scriptsure-react`](scriptsure-react.md), [`server`](server.md)

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/core`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/core` phải xanh.
