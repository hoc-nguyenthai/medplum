# Bot và tự động hóa (bot, CDS Hooks, webhook)

Chạy mã tùy biến của khách hàng phản ứng với sự kiện hoặc gọi trực tiếp qua `$execute`.

## Module liên quan

- [`server/bots`](../modules/server/bots.md)
- [`server/cloud`](../modules/server/cloud.md)
- [`server/cds`](../modules/server/cds.md)
- [`server/webhook`](../modules/server/webhook.md)
- [`bot-layer`](../modules/packages/bot-layer.md)

## API contract

- [fhir-rest.md](../../apps/fhir-server/docs/api-contract/fhir-rest.md) (`Bot/$execute`)
- [fhir-operations.md](../../apps/fhir-server/docs/api-contract/fhir-operations.md) (`Bot/$init`, `Bot/:id/$deploy`)
- [others.md](../api-contract/others.md) (CDS, webhook)

## Ghi chú kỹ thuật và di chuyển

- `executeBot` chọn runtime theo `bot.runtimeVersion`: `awslambda`, `vmcontext` hoặc `fission`; giá trị khác trả "Unsupported bot runtime".
- Triển khai bot: `cloud/aws/deploy.ts` (Lambda) hoặc `cloud/fission/deploy.ts`.
- CDS Hooks và webhook đều thực thi qua bot.
- `bot-layer` đóng gói thư viện (core, ccda, definitions) thành Lambda layer.

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
