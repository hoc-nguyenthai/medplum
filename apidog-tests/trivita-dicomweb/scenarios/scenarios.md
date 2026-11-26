# Scenario của module trivita-dicomweb (sinh bởi generate.py)

Mọi scenario bắt đầu bằng bước token (auth). Dữ liệu: `data/<slug>.csv|json`. Bảng tổng kết nằm cuối.

## S-01 dicomweb-health — DICOMweb: health smoke

Ưu tiên P0 · dữ liệu `data/dicomweb-health.csv` (1 dòng)

Bốn probe không cần token: /health/live, /health/ready, /health/startup, /healthz (200, thân JSON, không bị hạn mức IP).

1. GET /health/live — `GET /health/live` (endpoint 47548542) · bound
2. GET /health/ready — `GET /health/ready` (endpoint 47548543) · bound
3. GET /health/startup — `GET /health/startup` (endpoint 47548544) · bound
4. GET /healthz — `GET /healthz` (endpoint 47548545) · bound

## S-02 dicomweb-lifecycle — DICOMweb: lifecycle STOW → QIDO → WADO

Ưu tiên P0 · dữ liệu `data/dicomweb-lifecycle.csv` (2 dòng)

Luồng chính: token → STOW-RS dạng metadata (200, idempotent, study sai 409, một phần 202) → QIDO 4 cấp + phân trang + Warning → WADO-RS metadata/tải/frame/bulkdata/rendered → WADO-URI. Pixel là chuỗi ASCII nên so khớp từng byte. Mỗi dòng dữ liệu = một hình dạng ảnh (số frame, kích thước).

1. Lấy access token (client_credentials của auth) — `GET {{authUrl}}/oauth2/token` (endpoint 9000001)
2. STOW-RS dạng metadata: 2 instance cùng study/series (200) — `POST /dicomweb/studies` (endpoint 47548526)
3. STOW-RS gửi lại cùng instance: idempotent (200) — `POST /dicomweb/studies` (endpoint 47548526)
4. STOW-RS vào study đúng của URL (200) — `POST /dicomweb/studies/{{studyUid}}` (endpoint 47548528)
5. STOW-RS vào study KHÁC của URL: A900 (409) — `POST /dicomweb/studies/{{otherStudyUid}}` (endpoint 47548528)
6. STOW-RS một phần: 1 SR hợp lệ + 1 UID sai (202) — `POST /dicomweb/studies` (endpoint 47548526)
7. QIDO study theo StudyInstanceUID — `GET /dicomweb/studies` (endpoint 47548525)
8. QIDO study theo PatientID + tham số không hỗ trợ (Warning 299) — `GET /dicomweb/studies` (endpoint 47548525)
9. QIDO series của study (2 series) — `GET /dicomweb/studies/{{studyUid}}/series` (endpoint 47548531)
10. QIDO series toàn project theo SeriesInstanceUID — `GET /dicomweb/series` (endpoint 47548524)
11. QIDO instance của series (2 instance) — `GET /dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances` (endpoint 47548533)
12. QIDO instance của study (3 instance) — `GET /dicomweb/studies/{{studyUid}}/instances` (endpoint 47548529)
13. QIDO instance theo SOPInstanceUID — `GET /dicomweb/instances` (endpoint 47548523)
14. QIDO phân trang: limit=1 còn trang sau (Warning 299) — `GET /dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances` (endpoint 47548533)
15. WADO-RS metadata của study — `GET /dicomweb/studies/{{studyUid}}/metadata` (endpoint 47548530)
16. WADO-RS metadata của series — `GET /dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/metadata` (endpoint 47548540)
17. WADO-RS metadata của instance — `GET /dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances/{{sop1}}/metadata` (endpoint 47548538)
18. WADO-RS tải instance (Part 10 trong multipart) — `GET /dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances/{{sop1}}` (endpoint 47548534)
19. WADO-RS tải series — `GET /dicomweb/studies/{{studyUid}}/series/{{seriesUid}}` (endpoint 47548532)
20. WADO-RS tải study — `GET /dicomweb/studies/{{studyUid}}` (endpoint 47548527)
21. WADO-RS frame 2: đúng byte đã STOW — `GET /dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances/{{sop1}}/frames/2` (endpoint 47548536)
22. WADO-RS frames 2,1: đúng thứ tự yêu cầu — `GET /dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances/{{sop1}}/frames/2,1` (endpoint 47548536)
23. WADO-RS bulkdata PixelData: đúng toàn bộ byte — `GET /dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances/{{sop1}}/bulkdata/7FE00010` (endpoint 47548535)
24. WADO-RS rendered (mặc định JPEG) — `GET /dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances/{{sop1}}/rendered` (endpoint 47548539)
25. WADO-RS rendered PNG (Accept image/png) — `GET /dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances/{{sop1}}/rendered` (endpoint 47548539)
26. WADO-RS frame 2 rendered PNG — `GET /dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances/{{sop1}}/frames/2/rendered` (endpoint 47548537)
27. WADO-URI tệp nguyên văn (application/dicom) — `GET /dicomweb/wado` (endpoint 47548541)
28. WADO-URI ảnh PNG frame 2 — `GET /dicomweb/wado` (endpoint 47548541)
29. Instance SR (không ảnh): frame -> 404 Pixel data not found — `GET /dicomweb/studies/{{studyUid}}/series/{{series2Uid}}/instances/{{sop3}}/frames/1` (endpoint 47548536)

## S-03 dicomweb-stow-validation — DICOMweb: STOW-RS validation (data-driven)

Ưu tiên P1 · dữ liệu `data/dicomweb-stow-validation.json` (18 dòng)

Một bước STOW-RS theo từng dòng: Content-Type, boundary, thân multipart, metadata JSON, bulkdata, UID (rỗng/sai/65 ký tự/đúng 64), dạng tệp không phải DICOM. Lỗi cả yêu cầu: 415/400; lỗi từng instance: 409 + FailedSOPSequence.

1. Lấy access token (client_credentials của auth) — `GET {{authUrl}}/oauth2/token` (endpoint 9000001)
2. STOW-RS theo dữ liệu — `POST /dicomweb/studies` (endpoint 47548526)

## S-04 dicomweb-qido-matrix — DICOMweb: QIDO-RS /studies (data-driven)

Ưu tiên P1 · dữ liệu `data/dicomweb-qido-matrix.csv` (30 dòng)

Seed 2 study cố định bằng STOW-RS (idempotent) rồi tìm study theo từng tổ hợp: khớp chính xác, OR, tiền tố, khoảng ngày, modality, tag hex, phân trang, giá trị sai (400), chuỗi tấn công. Mỗi dòng chạy lại bước seed (200 vì idempotent).

1. Lấy access token (client_credentials của auth) — `GET {{authUrl}}/oauth2/token` (endpoint 9000001)
2. Seed: STOW 2 study cố định — `POST /dicomweb/studies` (endpoint 47548526)
3. QIDO /studies theo dữ liệu — `GET /dicomweb/studies` (endpoint 47548525)

## S-05 dicomweb-retrieve-matrix — DICOMweb: WADO-RS / WADO-URI / QIDO cấp con (data-driven)

Ưu tiên P1 · dữ liệu `data/dicomweb-retrieve-matrix.csv` (69 dòng)

Seed 1 study cố định (CT 3 frame 2x2 'ABCDEFGHIJKL' + 1 SR không ảnh) rồi gọi GET theo đường dẫn trong dữ liệu: frame/bulkdata/rendered/WADO-URI (200 và so byte), 404 phân biệt, 400 tham số, 416, 406, 501 và biên (50/51 frame, quality 1/100/999).

1. Lấy access token (client_credentials của auth) — `GET {{authUrl}}/oauth2/token` (endpoint 9000001)
2. Seed: STOW study cố định — `POST /dicomweb/studies` (endpoint 47548526)
3. GET theo dữ liệu — `GET {{dicomUrl}}{{req_path}}` (endpoint 9000002)

## S-06 dicomweb-authz-invalid-token — DICOMweb: authz — token sai (data-driven)

Ưu tiên P1 · dữ liệu `data/dicomweb-authz-invalid-token.csv` (6 dòng)

Lấy token thật rồi biến thành token xấu theo từng dòng (rác, alg=none, sửa chữ ký, cắt chữ ký, rỗng) và gọi QIDO, STOW, WADO-URI, frame: mọi route DICOMweb phải 401 kèm WWW-Authenticate.

1. Lấy access token (client_credentials của auth) — `GET {{authUrl}}/oauth2/token` (endpoint 9000001)
2. GET /dicomweb/studies với token xấu — `GET /dicomweb/studies` (endpoint 47548525)
3. POST /dicomweb/studies với token xấu — `POST /dicomweb/studies` (endpoint 47548526)
4. GET /dicomweb/wado với token xấu — `GET /dicomweb/wado` (endpoint 47548541)
5. GET frame với token xấu — `GET /dicomweb/studies/1.2/series/1.2/instances/1.2/frames/1` (endpoint 47548536)

## S-07 dicomweb-routing-isolation — DICOMweb: bốn tiền tố, cô lập project, route chưa cài

Ưu tiên P1 · dữ liệu `data/dicomweb-routing-isolation.csv` (1 dòng)

Không token (401), bốn tiền tố tương đương (200), projectId/X-Fhir-Project khác project của token (403), route lạ/DELETE/POST /series (501), ngoài tiền tố (404), header bảo mật.

1. Lấy access token (client_credentials của auth) — `GET {{authUrl}}/oauth2/token` (endpoint 9000001)
2. Không token -> 401 Thiếu bearer token — `GET /dicomweb/studies` (endpoint 47548525)
3. Tiền tố /projects/{projectId}/dicomweb khớp project của token -> 200 — `GET {{dicomUrl}}/projects/{{projectId}}/dicomweb/studies` (endpoint 9000003)
4. Tiền tố /api/dicomweb -> 200 — `GET {{dicomUrl}}/api/dicomweb/studies` (endpoint 9000004)
5. Tiền tố /api/projects/{projectId}/dicomweb -> 200 — `GET {{dicomUrl}}/api/projects/{{projectId}}/dicomweb/studies` (endpoint 9000005)
6. Project khác trong URL -> 403 — `GET {{dicomUrl}}/projects/qa-not-my-project/dicomweb/studies` (endpoint 9000006)
7. Header X-Fhir-Project khác project của token -> 403 — `GET /dicomweb/studies` (endpoint 47548525)
8. Route lạ dưới tiền tố: GET /dicomweb/foo -> 501 — `GET {{dicomUrl}}/dicomweb/foo` (endpoint 9000007)
9. Xóa study chưa cài: DELETE /dicomweb/studies/{uid} -> 501 — `GET {{dicomUrl}}/dicomweb/studies/{{studyUid}}` (endpoint 9000008)
10. POST /dicomweb/series chưa cài -> 501 — `GET {{dicomUrl}}/dicomweb/series` (endpoint 9000010)
11. Ngoài tiền tố: GET /fhir/R4/Patient -> 404 (không qua xác thực) — `GET {{dicomUrl}}/fhir/R4/Patient` (endpoint 9000009)
12. Header bảo mật có mặt ở mọi phản hồi — `GET /dicomweb/studies` (endpoint 47548525)

## Chỉ thiết kế (chưa dựng) và lý do

- STOW-RS dạng **tệp** (`application/dicom` nhị phân, JPEG baseline, nhiều frame nén): Apidog step không dựng được byte nhị phân từ dữ liệu. Dạng metadata đã phủ cùng đường lưu; còn lại dùng `tests/full_smoke` (helper Go).
- Rendered JPEG baseline trả nguyên byte, JPEG2000/RLE 406, frame > 64 MiB 413: cần tệp nén thật.
- 413 `Body quá lớn` (trần 4 GiB), 429 hạn mức IP (mặc định tắt ở local): không an toàn/không có sẵn khi chạy local.
- 403 token chỉ đọc gửi STOW, thiếu quyền `Binary`, super-admin (`X-Fhir-Project`): cần token có scope khác (client_credentials bị chặn scope `fhir.*`); xem coverage.md G1.

## Tổng kết độ phủ (dòng dữ liệu theo nhóm)

| Scenario | Positive | Negative | Boundary | Security | Other | Tổng |
|---|---|---|---|---|---|---|
| dicomweb-health | 1 | 0 | 0 | 0 | 0 | 1 |
| dicomweb-lifecycle | 1 | 0 | 1 | 0 | 0 | 2 |
| dicomweb-stow-validation | 0 | 14 | 2 | 2 | 0 | 18 |
| dicomweb-qido-matrix | 14 | 6 | 7 | 3 | 0 | 30 |
| dicomweb-retrieve-matrix | 24 | 32 | 11 | 2 | 0 | 69 |
| dicomweb-authz-invalid-token | 0 | 0 | 0 | 6 | 0 | 6 |
| dicomweb-routing-isolation | 0 | 0 | 0 | 1 | 0 | 1 |
| **Tổng** | 40 | 52 | 21 | 14 | 0 | 127 |
