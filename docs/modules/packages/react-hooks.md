# @medplum/react-hooks

> Hook React (useMedplum, useSearch, useResource, ...) và MedplumProvider.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/react-hooks` |
| Đường dẫn đích | `libs/medplum-react-hooks` |
| Số file nguồn (không tính test) | 27 |
| Thư mục `src/` | `MedplumProvider`, `index.test.ts`, `index.ts`, `test.setup.ts`, `useCachedBinaryUrl`, `useDebouncedValue`, `useMedicationCart`, `useMedicationIFrame`, `useMedicationOrder`, `useMedicationOrderSet`, `useNotificationCount`, `usePatientSummaryData`, `usePharmacySearch`, `usePrevious`, `useQuestionnaireForm`, `useResource`, `useResourceBoard`, `useResourceModified`, `useSearch`, `useStabilizedCallback`, `useSubscription`, `useSyncOrderSet`, `useThreadInbox`, `useValueSetAvailability`, `useWhisper` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt C |

## Phụ thuộc

- Runtime: [`core`](core.md)
- Chỉ dev: [`definitions`](definitions.md), [`fhirtypes`](fhirtypes.md), [`mock`](mock.md)
- Được dùng bởi: [`dosespot-react`](dosespot-react.md), [`react`](react.md), [`react-scheduling`](react-scheduling.md), [`scriptsure-react`](scriptsure-react.md)

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/react-hooks`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/react-hooks` phải xanh.
