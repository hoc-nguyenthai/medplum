# Terminology (ValueSet, CodeSystem, ConceptMap)

Mở rộng ValueSet, tra cứu và xác thực mã, dịch ConceptMap, nhập CodeSystem và ConceptMap.

## Module liên quan

- [`server/fhir/operations`](../modules/server/fhir-operations.md)
- [`server/seeds`](../modules/server/seeds.md)
- [`definitions`](../modules/packages/definitions.md)

## API contract

- [fhir-operations.md](../../apps/fhir-server/docs/api-contract/fhir-operations.md) (`$expand`, `$lookup`, `$validate-code`, `$subsumes`, `$translate`, `$import`)

## Ghi chú kỹ thuật và di chuyển

- `seeds` nạp ValueSet, StructureDefinition, SearchParameter ban đầu; super admin có route nạp lại.

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
