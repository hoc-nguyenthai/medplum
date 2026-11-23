# @medplum/scriptsure-react

> Component React tích hợp ScriptSure (kê đơn điện tử).

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/scriptsure-react` |
| Đường dẫn đích | `libs/medplum-scriptsure-react` |
| Số file nguồn (không tính test) | 13 |
| Thư mục `src/` | `common.test.ts`, `common.ts`, `index.ts`, `loadScriptSureQuantityQualifiers.test.ts`, `loadScriptSureQuantityQualifiers.ts`, `pharmacy-search.test.ts`, `pharmacy-search.ts`, `test.setup.ts`, `useScriptSureCart.ts`, `useScriptSureIFrame.test.tsx`, `useScriptSureIFrame.ts`, `useScriptSureMessageTask.test.tsx`, `useScriptSureMessageTask.ts`, `useScriptSureOrderMedication.test.tsx`, `useScriptSureOrderMedication.ts`, `useScriptSureOrderSet.test.tsx`, `useScriptSureOrderSet.ts`, `useScriptSurePharmacySearch.test.tsx`, `useScriptSurePharmacySearch.ts`, `useScriptSurePractice.test.tsx`, `useScriptSurePractice.tsx` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt C |

## Phụ thuộc

- Runtime: [`core`](core.md), [`fhirtypes`](fhirtypes.md), [`react-hooks`](react-hooks.md)
- Chỉ dev: [`mock`](mock.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/scriptsure-react`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/scriptsure-react` phải xanh.
