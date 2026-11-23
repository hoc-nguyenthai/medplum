# @medplum/react-scheduling

> Component lập lịch hẹn (chọn slot, đặt lịch) dựa trên $find, $book.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/react-scheduling` |
| Đường dẫn đích | `libs/medplum-react-scheduling` |
| Số file nguồn (không tính test) | 131 |
| Thư mục `src/` | `AppointmentFinder`, `AppointmentStatusBadge.tsx`, `Calendar`, `CalendarBase`, `ConfirmModal`, `MonthPickerButton`, `MultiCalendar`, `ScheduleAvailabilityEditor`, `SchedulingConfigWorkspace`, `SchedulingParametersEditor`, `SchedulingWorkspace`, `ServiceFacilityInput.tsx`, `ServiceTypeDisplay.tsx`, `actors.test.ts`, `actors.ts`, `availability.test.ts`, `availability.ts`, `bookingGeometry.test.ts`, `bookingGeometry.ts`, `colors.ts`, `configSearch.test.ts`, `configSearch.ts`, `constants.ts`, `hooks`, `index.ts`, `parameterValues.test.ts`, `parameterValues.ts`, `patients.test.ts`, `patients.ts`, `serviceTypes.ts`, `stories`, `styles.css`, `test-mocks`, `test-utils`, `test.setup.ts`, `timezones.test.ts`, `timezones.ts`, `types.ts` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt C |

## Phụ thuộc

- Runtime: [`core`](core.md), [`fhirtypes`](fhirtypes.md), [`react`](react.md), [`react-hooks`](react-hooks.md)
- Chỉ dev: [`definitions`](definitions.md), [`mock`](mock.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/react-scheduling`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/react-scheduling` phải xanh.
