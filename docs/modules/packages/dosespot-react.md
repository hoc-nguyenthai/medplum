# @medplum/dosespot-react

> Component React cho DoseSpot.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/dosespot-react` |
| Đường dẫn đích | `libs/medplum-dosespot-react` |
| Số file nguồn (không tính test) | 11 |
| Thư mục `src/` | `common.ts`, `index.test.ts`, `index.ts`, `test.setup.ts`, `useDoseSpotClinicFormulary.test.tsx`, `useDoseSpotClinicFormulary.ts`, `useDoseSpotIFrame.test.tsx`, `useDoseSpotIFrame.ts`, `useDoseSpotNotifications.test.tsx`, `useDoseSpotNotifications.ts`, `useDoseSpotPatientPharmacy.test.tsx`, `useDoseSpotPatientPharmacy.ts`, `useDoseSpotPharmacySearch.test.tsx`, `useDoseSpotPharmacySearch.ts`, `useDoseSpotSelfEnrollment.test.tsx`, `useDoseSpotSelfEnrollment.ts`, `utils.test.ts`, `utils.ts` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt C |

## Phụ thuộc

- Runtime: [`core`](core.md), [`dosespot-core`](dosespot-core.md), [`fhirtypes`](fhirtypes.md), [`react-hooks`](react-hooks.md)
- Chỉ dev: [`mock`](mock.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/dosespot-react`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/dosespot-react` phải xanh.
