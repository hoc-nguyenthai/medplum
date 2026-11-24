# dicomweb: vận hành

Cập nhật 2026-11-24 (D8: lệnh reindex). Cấu hình: [config.md](config.md).

## Chạy bộ test và đo tải trên PostgreSQL và S3 thật

Mặc định test dùng SQLite và thư mục tệp. Cùng bộ test (`go test ./apps/dicomweb/...`), e2e nhiều tiến trình và bài đo tải chạy được trên PostgreSQL và S3 thật bằng biến môi trường (mỗi test một schema và một tiền tố riêng, tự dọn schema; đối tượng S3 để lại trong bucket):

```
# máy chủ thử (Docker): PostgreSQL 16 và một máy chủ S3 thật (RustFS; image minio/minio không còn pull được)
docker run -d --name pg-test -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=fhir -p 55432:5432 postgres:16-alpine
docker run -d --name s3-test -p 59000:9000 -e RUSTFS_ACCESS_KEY=testkey -e RUSTFS_SECRET_KEY=testsecret123 -e RUSTFS_VOLUMES=/data -e RUSTFS_ADDRESS=:9000 rustfs/rustfs:latest
mc alias set rt http://127.0.0.1:59000 testkey testsecret123 && mc mb rt/dicom-test

export DICOMWEB_TEST_POSTGRES_URL='postgres://postgres:pw@127.0.0.1:55432/fhir?sslmode=disable'
export DICOMWEB_TEST_S3_ENDPOINT=http://127.0.0.1:59000 DICOMWEB_TEST_S3_BUCKET=dicom-test DICOMWEB_TEST_S3_ACCESS_KEY_ID=testkey DICOMWEB_TEST_S3_SECRET_ACCESS_KEY=testsecret123
go test ./apps/dicomweb/...                                        # test đơn vị và tích hợp trên cả hai
DICOMWEB_LOAD=1 go test -v ./apps/dicomweb/tests/load             # đo tải (DICOMWEB_LOAD_STUDIES, DICOMWEB_LOAD_INSTANCES_PER_STUDY đổi quy mô)
E2E_POSTGRES_URL=$DICOMWEB_TEST_POSTGRES_URL E2E_S3_ENDPOINT=$DICOMWEB_TEST_S3_ENDPOINT E2E_S3_BUCKET=dicom-test \
  E2E_S3_ACCESS_KEY_ID=testkey E2E_S3_SECRET_ACCESS_KEY=testsecret123 TRIVITA_E2E=1 go test -run TestDicomweb ./e2e   # fhir-server và dicomweb là hai tiến trình dùng chung
FHIR_S3_TEST_ENDPOINT=$DICOMWEB_TEST_S3_ENDPOINT FHIR_S3_TEST_BUCKET=dicom-test FHIR_S3_TEST_ACCESS_KEY_ID=testkey FHIR_S3_TEST_SECRET_ACCESS_KEY=testsecret123 go test ./pkg/binarystore/s3store   # hợp đồng kho
```

Với testcontainers của các gói khác (`pkg/testkit`, `tests/dbmodel`...) trên OrbStack đặt `DOCKER_HOST=unix://$HOME/.orbstack/run/docker.sock` và `TESTCONTAINERS_RYUK_DISABLED=true`. **Số đo (2026-11-24, máy phát triển, máy chủ chạy trong Docker cùng máy nên độ trễ mạng gần 0; chỉ để so sánh các cấu hình với nhau, không phải số của production; 16 client, 500 instance 215 MB):**

| Cơ sở dữ liệu + kho | STOW instance/s (p50, p95) | WADO frame 512 KiB yêu cầu/s (p50, p95) | QIDO study yêu cầu/s (p50) | metadata series 25 instance p50 |
|---|---|---|---|---|
| SQLite + tệp | 135 (204 ms, 1,7 s) | 602 (26 ms, 31 ms) | 474 (13 ms) | 20 ms |
| PostgreSQL + tệp | 130 (529 ms, 737 ms) | 1924 (6 ms, 18 ms) | 911 (4 ms) | 10 ms |
| SQLite + S3 | 189 (211 ms, 681 ms) | 567 (28 ms, 32 ms) | 480 (13 ms) | 20 ms |
| PostgreSQL + S3 | 161 (372 ms, 496 ms) | 1227 (11 ms, 26 ms) | 801 (4 ms) | 10 ms |

Đỉnh heap của STOW 150 đến 160 MB, của WADO 17 đến 41 MB, như khi chạy trên SQLite và tệp. **Quy mô lớn hơn (PostgreSQL + tệp, 10 000 instance 4,3 GB, 200 study):** STOW 141 instance/s, QIDO study p50 10 ms p95 31 ms, metadata series 50 instance p50 22 ms: độ trễ tìm kiếm không tăng theo số instance. SQLite ở cùng quy mô: QIDO p50 1,1 s, STOW một phần bị từ chối (202, một người ghi) và lỗi đọc metadata: **SQLite chỉ để phát triển.**

## `DicomInstance.pixelData` (tùy chọn, gấp đôi dung lượng ảnh)

Bật `DICOMWEB_PIXELDATA_FRAMES=true` nếu có client đọc frame thẳng từ `DicomInstance.pixelData` qua API FHIR (`Binary/{id}`); WADO-RS không cần. Mỗi frame được lưu thêm một lần: tính dung lượng kho gấp đôi cho ảnh mới. Worker chạy nền ngay sau STOW (hàng đợi trong bộ nhớ, thử lại 3 lần); **không bền**: tiến trình tắt hoặc hàng đợi đầy (log `hàng đợi pixelData đầy`) hay lỗi sau 3 lần (log `không dựng được pixelData`) làm instance thiếu pixelData cho tới khi chạy bù:

```
dicomweb pixeldata --dry-run      # đếm instance còn thiếu
dicomweb pixeldata                # dựng (cũng dùng cho instance có từ trước khi bật tính năng)
dicomweb pixeldata --limit 500    # chia đợt
dicomweb pixeldata --force        # dựng lại (Binary mới, xóa Binary cũ)
```

Lệnh in `đã duyệt, dựng (số frame), đã đủ, không có ảnh, lỗi` và thoát khác 0 nếu có lỗi; chạy lại an toàn và chạy được khi server đang phục vụ. Mỗi frame cần một lượt đọc và một lượt ghi kho (S3: tính phí theo số request). Xóa `DicomInstance` không xóa các Binary frame của nó. Muốn tắt tính năng: đặt lại `false`; pixelData đã dựng vẫn còn.

## Đĩa tạm cho STOW dạng metadata

STOW dạng metadata (`application/dicom+json` kèm bulkdata) ghi bulkdata ra thư mục tạm (đổi bằng biến môi trường `TMPDIR`; mặc định `/tmp`) trước khi lưu vào kho, rồi xóa. Cần chỗ trống cỡ tổng bulkdata của các yêu cầu đang chạy song song (tối đa `DICOMWEB_MAX_REQUEST_BYTES` mỗi yêu cầu). Image distroless chạy user không phải root: mount một volume có quyền ghi vào `TMPDIR` nếu `/tmp` chỉ đọc. STOW tệp thường (`application/dicom`) không dùng đĩa tạm. Thư mục lỗi thời `dicomstow-*` (tiến trình bị kill giữa chừng) an toàn để xóa khi không có yêu cầu nào đang chạy.

## Lập lại chỉ mục frame (instance cũ)

Instance lưu trước D5, hoặc có chỉ mục hỏng hay sai số frame, vẫn được WADO-RS phục vụ đúng nhưng chậm (quay về quét tệp, chi phí tỉ lệ với vị trí frame). Lập lại chỉ mục bằng lệnh vận hành, cùng cấu hình với server (DATABASE_URL, FHIR_BINARY_STORAGE...), duyệt mọi project:

```
dicomweb reindex --dry-run      # chỉ đếm: bao nhiêu instance cần lập
dicomweb reindex                # lập chỉ mục
dicomweb reindex --force        # lập lại cả chỉ mục đã đủ (ví dụ sau khi sửa lỗi bộ lập chỉ mục)
dicomweb reindex --limit 1000   # chia nhỏ theo đợt
```

Lệnh in `đã duyệt, lập chỉ mục, đã đủ, không có ảnh, không lập được, lỗi` và thoát khác 0 nếu có `lỗi`. Chạy lại bao nhiêu lần cũng an toàn (instance đã đủ bị bỏ qua) và có thể chạy khi server đang phục vụ: ghi dùng If-Match theo phiên bản đã đọc nên không ghi đè thay đổi song song (instance đó tính là lỗi, chạy lại là xong). `không lập được` là ảnh đóng gói không tách được frame hoặc chỉ mục quá 256 KB (WADO-RS vẫn đúng nhờ quét). `lỗi` thường là mất nội dung trong kho (xem log `không lập được chỉ mục frame`), tệp hỏng hoặc số frame trong metadata khác tệp. Mỗi instance cần lập tốn một lượt đọc toàn bộ tệp từ kho (với S3 là một lần tải cả tệp), nên với kho lớn chạy theo đợt `--limit` ngoài giờ cao điểm. Lệnh dùng kết nối cơ sở dữ liệu trực tiếp, không qua token, và cũng từ chối chạy nếu schema chưa migrate.

## Triển khai

1. `fhir-server migrate up` trước (dicomweb không migrate; chưa migrate thì dicomweb thoát với "schema chưa được migrate").
2. Cùng `DATABASE_URL`, `FHIR_JWT_*` và `FHIR_BINARY_STORAGE` với fhir-server (dùng chung tệp môi trường được).
3. Chạy `trivita-dicomweb` (image từ `apps/dicomweb/Dockerfile`). Kho `file:` cần thư mục ghi được bởi UID 65532; production dùng `s3:`.
4. Sau khi rollout: `/health/ready` 200; `GET /dicomweb/studies` không token phải 401.

## Sự cố thường gặp

| Triệu chứng | Nguyên nhân thường gặp | Xử lý |
|---|---|---|
| Thoát ngay: "schema chưa được migrate" | fhir-server chưa chạy `migrate up` trên database này | chạy `fhir-server migrate up` |
| Thoát ngay: lỗi tên biến (`FHIR_JWT_ISSUER`, `FHIR_BINARY_STORAGE`...) | cấu hình thiếu hoặc sai; mọi lỗi được gom và nêu đủ | sửa theo thông báo, xem [config.md](config.md) |
| 401 với mọi token | `FHIR_JWT_ISSUER` hoặc `FHIR_JWT_AUDIENCE` khác của `auth`; JWKS không tải được (xem log "token bị từ chối") | đối chiếu với `auth` và `fhir-server` |
| 403 "Token không có quyền truy cập FHIR" | token thiếu claim project hoặc scope `fhir.*`/SMART | kiểm token do `auth` cấp |
| 403 "Không được dùng project khác" | URL `/projects/{id}/dicomweb` hoặc header `X-Fhir-Project` khác project của token | dùng đúng project, hoặc tiền tố không có project |
| 501 "Chưa cài đặt" | route DICOMweb chưa cài (rendered cấp series và study, xóa, bulkdata ngoài PixelData trả 404) | xem api-contract.md |
| WADO frame 404 "Pixel data not found" | instance không có ảnh (báo cáo có cấu trúc), hoặc Binary chứa nội dung đã bị xóa | kiểm instance; nếu Binary bị xóa, tải lại |
| WADO frame 416 | số frame vượt `NumberOfFrames` của instance | frame đánh số từ 1 |
| WADO frame 403 | token thiếu quyền đọc `Binary` (ngoài `Dicom*`) | cấp scope `system/Binary.r` |
| WADO frame chậm với tệp lớn | instance không có chỉ mục frame (ghi trước D5, chỉ mục hỏng, hoặc ảnh đóng gói nhiều fragment không có Basic Offset Table) nên mỗi yêu cầu quét cả tệp; hoặc kho không đọc theo khoảng | tải lại instance qua STOW để lập chỉ mục (STOW bỏ qua instance đã có nên cần xóa instance trước); xem log `bỏ qua chỉ mục frame hỏng` |
| Phản hồi metadata hoặc frame bị cắt giữa chừng | kho nội dung lỗi sau khi đã bắt đầu ghi (server cố ý ngắt kết nối) | xem log `DICOMweb: lỗi khi ghi`; kiểm kho |
| STOW 409, `FailureReason` 49152 (0xC000) | tệp không phải DICOM, bị cắt giữa PixelData, SQ lồng quá sâu, thiếu hoặc sai UID, transfer syntax không hỗ trợ (Big Endian, Deflated) | kiểm tệp ở phía gửi; thông điệp từng instance nằm trong log (không có dữ liệu bệnh nhân) |
| STOW 409 hoặc 202, `FailureReason` 42752 (0xA700) | instance vượt `DICOMWEB_MAX_INSTANCE_BYTES`, hoặc phần đầu DICOM vượt `DICOMWEB_MAX_HEADER_BYTES` | tăng trần có cân nhắc; phần đầu lớn bất thường thường là tệp độc |
| STOW 403 | token thiếu quyền ghi (`fhir.write`), hoặc chính sách truy cập/SMART không cho tạo `Binary`, `DicomStudy`, `DicomSeries`, `DicomInstance` | cấp scope `system/Binary.c system/Dicom*.cru` hoặc quyền ghi |
| STOW chậm hoặc timeout ở proxy | tải lên lớn qua proxy có `proxy_read_timeout` ngắn hoặc bộ đệm yêu cầu | tắt bộ đệm yêu cầu và tăng timeout cho `/dicomweb` ở proxy |
| `/health/ready` 503 | database không trả lời trong `READINESS_TIMEOUT`, hoặc đang tắt | kiểm database; không restart pod chỉ vì ready (liveness độc lập) |
| 413 | body vượt `DICOMWEB_MAX_REQUEST_BYTES` | chia nhỏ yêu cầu STOW hoặc tăng trần có cân nhắc bộ nhớ và thời gian |
| 429 | hạn mức theo IP của từng instance | đặt `DICOMWEB_RATE_LIMIT` phù hợp, hoặc `DICOMWEB_TRUSTED_PROXIES` nếu IP bị gộp do proxy |

## Định cỡ

Số đo (máy phát triển, tệp tổng hợp, SQLite và kho tệp; chưa phải số của production; PostgreSQL và S3 thật xem bảng ở mục "Chạy bộ test và đo tải trên PostgreSQL và S3 thật"): tải một tệp 104 MB qua HTTP đỉnh heap 11 MB (đo cả phía client trong cùng tiến trình, `TestStowStreamsLargeFiles`); bộ nhớ mỗi yêu cầu STOW là hằng số, gồm phần đầu DICOM (≤ `DICOMWEB_MAX_HEADER_BYTES`, mặc định 1 MiB) cộng vài bộ đệm 64 KB, không phụ thuộc kích thước tệp. Định cỡ bộ nhớ: số yêu cầu STOW đồng thời × (tối đa khoảng 1,2 MiB) cộng nền. Thời gian tải bị giới hạn bởi băng thông và kho (S3: nhân với độ trễ mỗi lần ghi), không phải CPU. Từ chối tệp độc (SQ lồng 2000 lớp, 60 KB) dưới 1 giây và cấp phát không đáng kể (đưa thẳng vào thư viện sẽ tốn 2,2 GB). Lấy một frame 8 MB từ tệp 104 MB đọc đúng 8 MB từ kho và đỉnh heap 8 MB (`TestWadoFrameOfLargeFileStreams`); thời gian tỉ lệ với kích thước frame, không phụ thuộc vị trí frame trong tệp (nhờ chỉ mục frame và đọc theo khoảng). **Đo tải (D6, `make test-load-dicomweb`; máy phát triển, SQLite và kho tệp tạm, 16 client, 500 instance 215 MB; chỉ để so sánh với chính nó):** STOW 135 instance/s (58 MB/s), p50 245 ms, p95 1,4 s, đỉnh heap 140 MB (16 yêu cầu song song, mỗi yêu cầu 5 instance, tức khoảng 9 MB mỗi yêu cầu đang chạy do các bộ đệm của client và server trong cùng tiến trình); WADO frame 512 KiB 629 yêu cầu/s (83 MB/s), p50 25 ms, p99 31 ms, đỉnh heap 15 MB; QIDO study 489 yêu cầu/s, p50 12 ms; metadata series 25 instance p50 20 ms. STOW chậm ở p95 vì SQLite chỉ có một người ghi; PostgreSQL chưa đo (không có Docker). Cũng nhờ bài đo này phát hiện và sửa lỗi hiệu năng tìm kiếm của SQLite (xem `docs/plans/cds-dicom/dicom.md`).
