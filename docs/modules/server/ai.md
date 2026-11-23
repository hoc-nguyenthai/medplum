# server/ai

> Tích hợp OpenAI cho operation `$ai`.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/ai` |
| Đường dẫn đích | `apps/medplum-server/src/ai` |
| Số file nguồn (không tính test) | 1 |
| Cụm | phụ |

## Phụ thuộc (số import)

- Gọi tới: `(root)` (1), `util` (1)
- Được gọi bởi: `fhir/operations` (2)

## Ghi chú tách

Cụm nhỏ, phụ thuộc một chiều vào `fhir` và root. Ứng viên tách sớm ở giai đoạn 2.

## File chính

- `ai/openai.ts`
