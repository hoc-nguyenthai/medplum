# Nghiệp vụ lâm sàng (everything, summary, C-CDA, lịch hẹn, SMART Health)

Các operation gắn với nghiệp vụ lâm sàng và vận hành phòng khám.

## Module liên quan

- [`server/fhir/operations`](../modules/server/fhir-operations.md)
- [`ccda`](../modules/packages/ccda.md)
- [`react-scheduling`](../modules/packages/react-scheduling.md)

## API contract

- [fhir-operations.md](../api-contract/fhir-operations.md)

## Ghi chú kỹ thuật và di chuyển

- `Patient/$everything|$summary|$match|$ccda-export`, `QuestionnaireResponse/$extract`, `Measure/$evaluate-measure`, `PlanDefinition/$apply`, `ChargeItemDefinition/$apply`.
- Lịch hẹn: `Appointment/$find`, `$book`, `$hold`, `$cancel`, `$confirm`, `$reschedule`.
- Thanh toán: `Claim/$submit|$export`, `CoverageEligibilityRequest/$submit`.
- SMART Health Card và Link: `$generate-smart-health-card`, `$verify-smart-health-card`, `$generate-smart-health-link`, `$resolve-smart-health-link`.

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
