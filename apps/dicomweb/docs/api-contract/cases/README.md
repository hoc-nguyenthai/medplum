# Hợp đồng API theo từng trường hợp

Mỗi route một mục (neo `post-dicomweb-studies`...): thứ tự kiểm tra, bảng trường hợp (điều kiện → HTTP → header → body → tác dụng phụ), ví dụ. **Viết tay** nhưng có test `TestCasesCoverEveryRoute` bắt route thiếu mục hoặc mục mồ côi. Thông điệp lỗi trích nguyên văn từ mã và test.

| Nhóm | Tệp | Nội dung nổi bật |
|---|---|---|
| dicomweb | [dicomweb.md](dicomweb.md) | STOW tệp và metadata (200/202/409, FailureReason), QIDO (bộ lọc, Warning 299), WADO-RS (metadata, tải, frame, rendered, bulkdata), WADO-URI, 501 |
| health | [health.md](health.md) | Live, ready (draining, database), startup, healthz |

Đọc cùng: [../common.md](../common.md), [../reference/](../reference/README.md).
