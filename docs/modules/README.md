# Mục lục module medplum

Sinh tự động. Mỗi trang gồm vai trò, đường dẫn hiện tại và đích, phụ thuộc, thứ tự di chuyển.

## Package (`packages/`)

| Package | Loại | Đích | File | Phụ thuộc runtime |
|---|---|---|---|---|
| [`@medplum/fhirtypes`](packages/fhirtypes.md) | lib | `libs/medplum-fhirtypes` | 0 | — |
| [`@medplum/definitions`](packages/definitions.md) | lib | `libs/medplum-definitions` | 2 | — |
| [`@medplum/core`](packages/core.md) | lib | `libs/medplum-core` | 79 | — |
| [`@medplum/hl7`](packages/hl7.md) | lib | `libs/medplum-hl7` | 10 | core |
| [`@medplum/ccda`](packages/ccda.md) | lib | `libs/medplum-ccda` | 28 | core |
| [`@medplum/fhir-router`](packages/fhir-router.md) | lib | `libs/medplum-fhir-router` | 12 | core, definitions, fhirtypes |
| [`@medplum/mock`](packages/mock.md) | lib | `libs/medplum-mock` | 20 | core, definitions, fhir-router, fhirtypes |
| [`@medplum/react-hooks`](packages/react-hooks.md) | lib | `libs/medplum-react-hooks` | 27 | core |
| [`@medplum/react`](packages/react.md) | lib | `libs/medplum-react` | 255 | core, react-hooks |
| [`@medplum/react-scheduling`](packages/react-scheduling.md) | lib | `libs/medplum-react-scheduling` | 131 | core, fhirtypes, react, react-hooks |
| [`@medplum/dosespot-core`](packages/dosespot-core.md) | lib | `libs/medplum-dosespot-core` | 3 | core, fhirtypes |
| [`@medplum/dosespot-react`](packages/dosespot-react.md) | lib | `libs/medplum-dosespot-react` | 11 | core, dosespot-core, fhirtypes, react-hooks |
| [`@medplum/health-gorilla-core`](packages/health-gorilla-core.md) | lib | `libs/medplum-health-gorilla-core` | 7 | core, fhirtypes |
| [`@medplum/health-gorilla-react`](packages/health-gorilla-react.md) | lib | `libs/medplum-health-gorilla-react` | 9 | core, fhirtypes, health-gorilla-core, react |
| [`@medplum/scriptsure-react`](packages/scriptsure-react.md) | lib | `libs/medplum-scriptsure-react` | 13 | core, fhirtypes, react-hooks |
| [`@medplum/server`](packages/server.md) | app | `apps/medplum-server` | 513 | bot-layer, ccda, core, definitions, fhir-router |
| [`@medplum/bot-layer`](packages/bot-layer.md) | app | `apps/medplum-bot-layer` | 0 | ccda, core, definitions |
| [`@medplum/agent`](packages/agent.md) | app | `apps/medplum-agent` | 33 | core, hl7 |
| [`@medplum/app`](packages/app.md) | app | `apps/medplum-app` | 109 | — |
| [`@medplum/cli`](packages/cli.md) | app | `apps/medplum-cli` | 29 | core, hl7 |
| [`medplum`](packages/cli-wrapper.md) | app | `apps/medplum-cli-wrapper` | 1 | cli, core |
| [`@medplum/cdk`](packages/cdk.md) | app | `apps/medplum-cdk` | 12 | core |
| [`@medplum/generator`](packages/generator.md) | tool | `tools/medplum-generator` | 8 | — |
| [`@medplum/e2e`](packages/e2e.md) | tool | `tools/medplum-e2e` | 1 | — |
| [`@medplum/storybook`](packages/storybook.md) | tool | `tools/medplum-storybook` | 4 | — |
| [`@medplum/graphiql`](packages/graphiql.md) | tool | `tools/medplum-graphiql` | 3 | — |
| [`@medplum/docs`](packages/docs.md) | tool | `tools/medplum-docs` | 0 | — |
| [`@medplum/examples`](packages/examples.md) | tool | `tools/medplum-examples` | 81 | — |
| [`@medplum/eslint-config`](packages/eslint-config.md) | tool | `tools/medplum-eslint-config` | 0 | — |
| [`create-medplum`](packages/create-medplum.md) | tool | `tools/medplum-create-medplum` | 2 | — |

## Module trong `server/src`

| Module | Cụm | File | Gọi tới | Được gọi bởi |
|---|---|---|---|---|
| [`(root)`](server/root.md) | lõi | 24 | 24 module | 22 module |
| [`config`](server/config.md) | lõi | 4 | 3 module | 18 module |
| [`util`](server/util.md) | lõi | 18 | 4 module | 15 module |
| [`fhir`](server/fhir.md) | lõi | 49 | 12 module | 22 module |
| [`fhir/operations`](server/fhir-operations.md) | lõi | 93 | 15 module | 8 module |
| [`migrations`](server/migrations.md) | lõi | 191 | 8 module | 4 module |
| [`workers`](server/workers.md) | lõi | 16 | 12 module | 6 module |
| [`cloud`](server/cloud.md) | lõi | 17 | 7 module | 8 module |
| [`storage`](server/storage.md) | lõi | 7 | 4 module | 8 module |
| [`bots`](server/bots.md) | lõi | 4 | 8 module | 8 module |
| [`otel`](server/otel.md) | lõi | 2 | 4 module | 5 module |
| [`auth`](server/auth.md) | danh tính | 26 | 7 module | 6 module |
| [`oauth`](server/oauth.md) | danh tính | 12 | 6 module | 15 module |
| [`admin`](server/admin.md) | danh tính | 7 | 12 module | 3 module |
| [`scim`](server/scim.md) | danh tính | 3 | 7 module | 1 module |
| [`email`](server/email.md) | phụ | 3 | 7 module | 5 module |
| [`dicom`](server/dicom.md) | phụ | 5 | 4 module | 1 module |
| [`fhircast`](server/fhircast.md) | phụ | 2 | 5 module | 3 module |
| [`ws`](server/ws.md) | phụ | 6 | 11 module | 3 module |
| [`mcp`](server/mcp.md) | phụ | 2 | 3 module | 1 module |
| [`ai`](server/ai.md) | phụ | 1 | 2 module | 1 module |
| [`agent`](server/agent.md) | phụ | 1 | 0 module | 2 module |
| [`keyvalue`](server/keyvalue.md) | phụ | 2 | 3 module | 1 module |
| [`cds`](server/cds.md) | phụ | 1 | 3 module | 1 module |
| [`webhook`](server/webhook.md) | phụ | 1 | 2 module | 1 module |
| [`sharding`](server/sharding.md) | phụ | 1 | 1 module | 3 module |
| [`seeds`](server/seeds.md) | lõi | 3 | 2 module | 3 module |
| [`hl7`](server/hl7.md) | phụ | 1 | 0 module | 1 module |
