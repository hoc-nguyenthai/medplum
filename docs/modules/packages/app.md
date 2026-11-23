# @medplum/app

> Giao diện quản trị web (Vite + React) cho Medplum.

| | |
|---|---|
| Loại | app |
| Đường dẫn hiện tại | `medplum/packages/app` |
| Đường dẫn đích | `apps/medplum-app` |
| Số file nguồn (không tính test) | 109 |
| Thư mục `src/` | `App.css`, `App.test.tsx`, `App.tsx`, `AppRoutes.tsx`, `BatchPage.test.tsx`, `BatchPage.tsx`, `BulkAppPage.test.tsx`, `BulkAppPage.tsx`, `ChangePasswordPage.test.tsx`, `ChangePasswordPage.tsx`, `CreateResourcePage.test.tsx`, `CreateResourcePage.tsx`, `ErrorPage.tsx`, `FormPage.test.tsx`, `FormPage.tsx`, `HomePage.module.css`, `HomePage.test.tsx`, `HomePage.tsx`, `HomePage.utils.ts`, `MfaPage.test.tsx`, `MfaPage.tsx`, `OAuthPage.test.tsx`, `OAuthPage.tsx`, `RegisterPage.test.tsx`, `RegisterPage.tsx`, `ResetPasswordPage.test.tsx`, `ResetPasswordPage.tsx`, `SecurityPage.test.tsx`, `SecurityPage.tsx`, `SetPasswordPage.test.tsx`, `SetPasswordPage.tsx`, `SignInPage.test.tsx`, `SignInPage.tsx`, `SmartSearchPage.test.tsx`, `SmartSearchPage.tsx`, `VerifyEmailPage.test.tsx`, `VerifyEmailPage.tsx`, `admin`, `components`, `config.ts`, `index.css`, `index.test.tsx`, `index.tsx`, `lab`, `resource`, `test-mocks`, `test-utils`, `test.setup.ts`, `utils.test.ts`, `utils.ts` |
| Entry point | `—` |
| Lệnh CLI | — |
| Script npm | `build`, `dev`, `lint`, `test` |
| Thứ tự di chuyển | Bước 4, thứ tự 7/7 |

## Phụ thuộc

- Runtime: —
- Chỉ dev: [`core`](core.md), [`definitions`](definitions.md), [`fhirtypes`](fhirtypes.md), [`mock`](mock.md), [`react`](react.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/app`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/app` phải xanh.
