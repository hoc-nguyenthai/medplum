# @medplum/react

> Thư viện component UI (form, bảng, resource viewer, timeline, ...), nhóm lớn nhất frontend.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/react` |
| Đường dẫn đích | `libs/medplum-react` |
| Số file nguồn (không tính test) | 255 |
| Thư mục `src/` | `AddressDisplay`, `AddressInput`, `AnnotationInput`, `AppShell`, `AsyncAutocomplete`, `AttachmentArrayDisplay`, `AttachmentArrayInput`, `AttachmentButton`, `AttachmentDisplay`, `AttachmentInput`, `BackboneElementDisplay`, `BackboneElementInput`, `BookmarkDialog`, `CalendarDateInput`, `CalendarInput`, `CcdaDisplay`, `CheckboxFormSection`, `CodeInput`, `CodeableConceptDisplay`, `CodeableConceptInput`, `CodingDisplay`, `CodingInput`, `ContactDetailDisplay`, `ContactDetailInput`, `ContactPointDisplay`, `ContactPointInput`, `Container`, `DateTimeInput`, `DefaultResourceTimeline`, `DescriptionList`, `DiagnosticReportDisplay`, `Document`, `ElementsInput`, `EncounterTimeline`, `ErrorBoundary`, `ExtensionDisplay`, `ExtensionInput`, `FhirPathDisplay`, `FhirPathTable`, `Form`, `FormSection`, `GoogleButton`, `HumanNameDisplay`, `HumanNameInput`, `IdentifierDisplay`, `IdentifierInput`, `InfoBar`, `LabOrdersPanel`, `LinkTabs`, `ListWithDetailPane`, `Loading`, `Logo`, `MeasureReportDisplay`, `MedplumLink`, `Modal`, `MoneyDisplay`, `MoneyInput`, `NoteDisplay`, `NotificationIcon`, `OperationOutcomeAlert`, `Panel`, `PasswordInput`, `PatientAccountsForm`, `PatientExportForm`, `PatientHeader`, `PatientSummary`, `PatientTimeline`, `PeriodInput`, `PlanDefinitionBuilder`, `QrCodeScanner`, `QuantityDisplay`, `QuantityInput`, `QuestionnaireBuilder`, `QuestionnaireForm`, `QuestionnaireResponseDisplay`, `RangeDisplay`, `RangeInput`, `RatioDisplay`, `RatioInput`, `ReferenceDisplay`, `ReferenceInput`, `ReferenceRangeEditor`, `RequestGroupDisplay`, `ResourceArrayDisplay`, `ResourceArrayInput`, `ResourceAvatar`, `ResourceBadge`, `ResourceBlame`, `ResourceBoard`, `ResourceDiff`, `ResourceDiffRow`, `ResourceDiffTable`, `ResourceForm`, `ResourceHistoryTable`, `ResourceInput`, `ResourceName`, `ResourcePropertyDisplay`, `ResourcePropertyInput`, `ResourceTable`, `ResourceTimeline`, `ResourceTypeInput`, `Scheduler`, `ScrollToTop`, `SearchControl`, `SearchExportDialog`, `SearchFieldEditor`, `SearchFilterEditor`, `SearchFilterValueDialog`, `SearchFilterValueDisplay`, `SearchFilterValueInput`, `SearchPopupMenu`, `SearchSortEditor`, `SensitiveTextarea`, `ServiceRequestTimeline`, `SignatureInput`, `SliceDisplay`, `SliceInput`, `SmartAppLaunchLink`, `StatusBadge`, `Timeline`, `TimingInput`, `UnavailableNote`, `ValueSetAutocomplete`, `auth`, `buttons`, `chat`, `constants.ts`, `globals.d.ts`, `index.test.ts`, `index.ts`, `stories`, `styles.css`, `test-mocks`, `test-utils`, `test.setup.ts`, `utils` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt C |

## Phụ thuộc

- Runtime: [`core`](core.md), [`react-hooks`](react-hooks.md)
- Chỉ dev: [`definitions`](definitions.md), [`fhirtypes`](fhirtypes.md), [`mock`](mock.md)
- Được dùng bởi: [`health-gorilla-react`](health-gorilla-react.md), [`react-scheduling`](react-scheduling.md)

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/react`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/react` phải xanh.
