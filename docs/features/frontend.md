# Frontend (admin app, thư viện React, tích hợp kê đơn và xét nghiệm)

Giao diện quản trị và thư viện dùng lại để dựng ứng dụng lâm sàng.

## Module liên quan

- [`app`](../modules/packages/app.md)
- [`react`](../modules/packages/react.md)
- [`react-hooks`](../modules/packages/react-hooks.md)
- [`react-scheduling`](../modules/packages/react-scheduling.md)
- [`dosespot-react`](../modules/packages/dosespot-react.md)
- [`health-gorilla-react`](../modules/packages/health-gorilla-react.md)
- [`scriptsure-react`](../modules/packages/scriptsure-react.md)
- [`graphiql`](../modules/packages/graphiql.md)

## API contract

- Gọi toàn bộ API ở [api-contract](../api-contract/README.md) qua `MedplumClient` trong `@medplum/core`.

## Ghi chú kỹ thuật và di chuyển

- Chỉ phụ thuộc `core`, `fhirtypes`, `definitions`; không phụ thuộc `server`.
- Chuyển song song với backend; không có vòng phụ thuộc.

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
