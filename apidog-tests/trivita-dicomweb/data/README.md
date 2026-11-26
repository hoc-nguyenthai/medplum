# Test data trivita-dicomweb (sinh bởi `generate.py`, không sửa tay)

127 dòng ở 7 tệp; mỗi dòng = một lần lặp của scenario cùng tên. Dòng **chỉ nằm ở máy** cho tới khi import trong app (Apidog CLI không upload được dòng test data; xem `IMPORT.md` sau khi tạo scenario) hoặc chạy bằng CLI (`run_scenarios.py` dùng thẳng tệp).

| Tệp | Dòng | Kỹ thuật | Ghi chú |
|---|---|---|---|
| `dicomweb-health.csv` | 1 | smoke | 4 probe không token |
| `dicomweb-lifecycle.csv` | 2 | phân vùng tương đương (hình dạng ảnh) | 3 frame 2×2 và 2 frame 3×3; UID sinh mới mỗi lần chạy bằng script ở bước token nên chạy lại không đụng |
| `dicomweb-stow-validation.json` | 18 | bảng quyết định (Content-Type × boundary × thân × metadata × UID), biên UID 64/65 ký tự | JSON vì ô chứa body multipart có CRLF; UID cố định (…`.400.`) nên chạy lại vẫn 200 với SV-14/SV-18 |
| `dicomweb-qido-matrix.csv` | 30 | tổ hợp thuộc tính (EP), khoảng ngày, biên limit/offset (0/1/1001/abc/-3), lỗi giá trị, chuỗi tấn công | 2 study seed cố định (`…300.1/.2`, PatientID `QA-QIDO-1/2`) |
| `dicomweb-retrieve-matrix.csv` | 69 | phân vùng theo route × tham số; biên frame (1/50/51, 9 và 10 chữ số), quality (1/100/999/0), 404 từng mắt xích, 406, 416, 501 | study seed cố định `…200.1`, pixel `ABCDEFGHIJKL`; đường dẫn nằm ở cột `req_path` |
| `dicomweb-authz-invalid-token.csv` | 6 | token xấu: rác, 3 đoạn giả, alg=none, sửa chữ ký, cắt chữ ký, rỗng | token xấu **dựng bằng script từ token thật**, không có JWT trong tệp |
| `dicomweb-routing-isolation.csv` | 1 | luồng cố định | tiền tố, project khác, 501, 404, header bảo mật |

Cột đặc biệt: `expected_message` = chuỗi `error` nguyên văn (rỗng = không kiểm); `expected_ct` (chứa trong Content-Type), `expect_body` (chuỗi có trong thân), `expected_count` (độ dài mảng QIDO), `expect_warning` (`more`/`none`), `expect_failed` (=1: phải có `00081198`), `bad_kind`. Các cột này được đọc bằng `pm.iterationData.get()` trong script nên `check_test_assets.py` báo WARNING "never referenced": **bình thường**.

## Nhóm
- **Positive/Negative/Boundary/Security** đều có. Security gồm chuỗi tấn công trong tham số (SQL, script, 2000 ký tự), UID trong đường dẫn, token giả; kỳ vọng 2xx/404 của các dòng này nghĩa là "xử lý như giá trị thường, không 5xx" (cảnh báo "Security expects success" của checker là chủ đích).
- Không có dòng nào cần mật khẩu/secret: `clientId`/`clientSecret` nhập lúc chạy (`--env-var`).

## Cố ý bỏ qua
- 413 `Body quá lớn` (trần 4 GiB), 429 hạn mức IP (mặc định tắt ở local), `X-Forwarded-For`: cần cấu hình riêng hoặc dữ liệu cực lớn.
- 403 token chỉ đọc / thiếu quyền `Binary` / super-admin: chưa có cách xin token đó bằng client_credentials (coverage.md G1).
- STOW dạng tệp nhị phân thật, JPEG baseline/2000, RLE, 413 frame > 64 MiB: Apidog không dựng byte nhị phân từ dữ liệu; có sẵn trong `tests/full_smoke` và test Go.
- PatientName có ký tự non-ASCII: JSON metadata UTF-8 → server tự thêm ISO_IR 192; chưa có dòng riêng (ghi vào G nếu cần).

## Dữ liệu tạo ra trên hệ thống thử
STOW tạo study/series/instance + Binary trong project của client seed: PatientID `QA-*` (lifecycle sinh theo `QA-<timestamp>`, các ma trận dùng cố định). Dọn bằng cách xóa project seed (`make down-full` xóa volume).
