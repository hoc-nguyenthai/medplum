# Scenario của bộ trivita-full-flow (sinh bởi generate.py)

Bộ này xuyên 4 service nên mọi bước là customHttp với URL tuyệt đối; không bind endpoint của module nào. Dữ liệu: `data/<slug>.csv`.

## S-01 full-flow-e2e — Full flow: auth → FHIR → $export → DICOM → FHIRcast → dọn dẹp

Ưu tiên P0; data: `data/full-flow-e2e.csv` (1 dòng); 43 bước.

1. auth: GET /health/live — `GET {{authUrl}}/health/live`
2. fhir-server: GET /healthz — `GET {{fhirUrl}}/healthz`
3. dicomweb: GET /healthz — `GET {{dicomUrl}}/healthz`
4. ws-gateway: GET /healthz — `GET {{wsUrl}}/healthz`
5. Lấy token client_credentials (auth) — `POST {{authUrl}}/oauth2/token`
6. auth: POST /oauth2/introspect token vừa cấp -> active — `POST {{authUrl}}/oauth2/introspect`
7. auth: GET /auth/me của ClientApplication — `GET {{authUrl}}/auth/me`
8. fhir: POST Patient — `POST {{fhirUrl}}/fhir/R4/Patient`
9. fhir: GET Patient/{id} — `GET {{fhirUrl}}/fhir/R4/Patient/{{patient_id}}`
10. fhir: tìm Patient theo identifier -> đúng 1 — `GET {{fhirUrl}}/fhir/R4/Patient`
11. fhir: PUT Patient/{id} đổi gender (phiên bản 2) — `PUT {{fhirUrl}}/fhir/R4/Patient/{{patient_id}}`
12. fhir: GET Patient/{id}/_history -> 2 phiên bản — `GET {{fhirUrl}}/fhir/R4/Patient/{{patient_id}}/_history`
13. fhir: POST Observation gắn Patient — `POST {{fhirUrl}}/fhir/R4/Observation`
14. fhir: GET Patient/{id}/$everything có Patient + Observation — `GET {{fhirUrl}}/fhir/R4/Patient/{{patient_id}}/$everything`
15. fhir: GET $export (Prefer: respond-async) -> 202 — `GET {{fhirUrl}}/fhir/R4/$export`
16. fhir: GET bulkdata/{id}/status (lần 1) — `GET {{fhirUrl}}/fhir/R4/bulkdata/{{job_id}}/status`
17. fhir: GET bulkdata/{id}/status (lần 2) — `GET {{fhirUrl}}/fhir/R4/bulkdata/{{job_id}}/status`
18. fhir: GET bulkdata/{id}/status (lần 3) — `GET {{fhirUrl}}/fhir/R4/bulkdata/{{job_id}}/status`
19. fhir: GET bulkdata/{id}/status -> 200 manifest (do fhir-worker chạy) — `GET {{fhirUrl}}/fhir/R4/bulkdata/{{job_id}}/status`
20. fhir: GET Binary/{id} (ndjson từ kho dùng chung) — `GET {{fhirUrl}}/fhir/R4/Binary/{{bin_id}}`
21. fhir: DELETE bulkdata/{id}/status (dọn job) — `DELETE {{fhirUrl}}/fhir/R4/bulkdata/{{job_id}}/status`
22. dicomweb: STOW-RS dạng metadata 1 instance (200) — `POST {{dicomUrl}}/dicomweb/studies`
23. dicomweb: QIDO-RS study theo StudyInstanceUID -> 1 — `GET {{dicomUrl}}/dicomweb/studies`
24. dicomweb: WADO-RS metadata instance — `GET {{dicomUrl}}/dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances/{{sop}}/metadata`
25. dicomweb: WADO-RS tải instance (Part 10) — `GET {{dicomUrl}}/dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances/{{sop}}`
26. dicomweb: WADO-RS frame 1 đúng byte đã STOW — `GET {{dicomUrl}}/dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances/{{sop}}/frames/1`
27. fhir: GET DicomStudy thấy study do dicomweb ghi (schema dùng chung) — `GET {{fhirUrl}}/fhir/R4/DicomStudy`
28. auth: token client_credentials scope fhircast/* cho ws-gateway — `POST {{authUrl}}/oauth2/token`
29. ws-gateway: GET .well-known/fhircast-configuration (công khai) — `GET {{wsUrl}}/fhircast/STU3/.well-known/fhircast-configuration`
30. ws-gateway: subscribe websocket (STU3) — `POST {{wsUrl}}/fhircast/STU3`
31. ws-gateway: mở ngữ cảnh Patient-open theo Patient vừa tạo — `POST {{wsUrl}}/fhircast/STU3/{{topic}}`
32. ws-gateway: GET ngữ cảnh hiện tại = Patient — `GET {{wsUrl}}/fhircast/STU3/{{topic}}`
33. ws-gateway: đóng ngữ cảnh Patient-close — `POST {{wsUrl}}/fhircast/STU3/{{topic}}`
34. ws-gateway: unsubscribe — `POST {{wsUrl}}/fhircast/STU3`
35. fhir: DELETE Observation — `DELETE {{fhirUrl}}/fhir/R4/Observation/{{obs_id}}`
36. fhir: DELETE Patient (xóa mềm) — `DELETE {{fhirUrl}}/fhir/R4/Patient/{{patient_id}}`
37. fhir: GET Patient đã xóa -> 410 — `GET {{fhirUrl}}/fhir/R4/Patient/{{patient_id}}`
38. auth: đăng nhập admin project (để $expunge) — `POST {{authUrl}}/auth/login`
39. auth: đổi code lấy token admin — `POST {{authUrl}}/oauth2/token`
40. fhir: POST Patient/{id}/$expunge (xóa vĩnh viễn) — `POST {{fhirUrl}}/fhir/R4/Patient/{{patient_id}}/$expunge`
41. fhir: POST Observation/{id}/$expunge — `POST {{fhirUrl}}/fhir/R4/Observation/{{obs_id}}/$expunge`
42. auth: POST /oauth2/logout thu hồi phiên của token — `POST {{authUrl}}/oauth2/logout`
43. auth: sau logout /auth/me -> 401 — `GET {{authUrl}}/auth/me`

## S-02 full-flow-negative-no-token — Full flow negative: không token → 401 trên cả 4 service

Ưu tiên P1; data: `data/full-flow-negative-no-token.csv` (1 dòng); 13 bước.

1. auth: GET /health/live — `GET {{authUrl}}/health/live`
2. fhir-server: GET /healthz — `GET {{fhirUrl}}/healthz`
3. dicomweb: GET /healthz — `GET {{dicomUrl}}/healthz`
4. ws-gateway: GET /healthz — `GET {{wsUrl}}/healthz`
5. auth: GET /auth/me không token -> 401 — `GET {{authUrl}}/auth/me`
6. fhir: GET /fhir/R4/Patient không token -> 401 — `GET {{fhirUrl}}/fhir/R4/Patient`
7. dicomweb: GET /dicomweb/studies không token -> 401 — `GET {{dicomUrl}}/dicomweb/studies`
8. ws-gateway: GET /fhircast/STU3/{topic} không token -> 401 — `GET {{wsUrl}}/fhircast/STU3/qa-no-token`
9. auth: GET /.well-known/jwks.json không token -> 200 (công khai) — `GET {{authUrl}}/.well-known/jwks.json`
10. fhir: GET /fhir/R4/metadata không token -> 200 (công khai) — `GET {{fhirUrl}}/fhir/R4/metadata`
11. ws-gateway: GET /fhircast/STU3/.well-known/fhircast-configuration không token -> 200 (công khai) — `GET {{wsUrl}}/fhircast/STU3/.well-known/fhircast-configuration`
12. dicomweb: ngoài tiền tố GET /fhir/R4/Patient -> 404 (không qua xác thực) — `GET {{dicomUrl}}/fhir/R4/Patient`
13. auth: POST /oauth2/token thiếu client_secret -> 4xx — `POST {{authUrl}}/oauth2/token`

## S-03 full-flow-negative-bad-token — Full flow negative: token sai → 401 trên cả 4 service

Ưu tiên P1; data: `data/full-flow-negative-bad-token.csv` (6 dòng); 6 bước.

1. auth: token hợp lệ (đối chứng) — `POST {{authUrl}}/oauth2/token`
2. đối chứng: fhir GET Patient với token hợp lệ -> 200 — `GET {{fhirUrl}}/fhir/R4/Patient`
3. auth: GET /auth/me với token sai -> 401 — `GET {{authUrl}}/auth/me`
4. fhir: GET /fhir/R4/Patient với token sai -> 401 — `GET {{fhirUrl}}/fhir/R4/Patient`
5. dicomweb: GET /dicomweb/studies với token sai -> 401 — `GET {{dicomUrl}}/dicomweb/studies`
6. ws-gateway: GET /fhircast/STU3/{topic} với token sai -> 401 — `GET {{wsUrl}}/fhircast/STU3/qa-no-token`

## S-04 full-flow-cross-service — Full flow: một JWT của auth dùng chung cho các service

Ưu tiên P1; data: `data/full-flow-cross-service.csv` (1 dòng); 8 bước.

1. Lấy token client_credentials (auth) — `POST {{authUrl}}/oauth2/token`
2. fhir: token của auth dùng được ở fhir-server — `GET {{fhirUrl}}/fhir/R4/Patient`
3. dicomweb: cùng token dùng được ở dicomweb — `GET {{dicomUrl}}/dicomweb/studies`
4. auth: token scope fhircast/* — `POST {{authUrl}}/oauth2/token`
5. ws-gateway: token scope fhircast/* dùng được ở hub — `GET {{wsUrl}}/fhircast/STU3/{{topic}}`
6. auth: POST /oauth2/logout thu hồi phiên của token — `POST {{authUrl}}/oauth2/logout`
7. auth: sau logout /auth/me -> 401 — `GET {{authUrl}}/auth/me`
8. fhir: token đã logout ở auth khi dùng ở fhir-server (ghi nhận hành vi: 200 hoặc 401) — `GET {{fhirUrl}}/fhir/R4/Patient`

## Chỉ thiết kế (không dựng trong Apidog)

| Mục | Lý do | Nằm ở |
|---|---|---|
| Webhook rest-hook do fhir-notifier giao | cần máy nhận HTTP ở máy chạy test (host.docker.internal) | tests/full_smoke/smoke.py (`make smoke-full`) |
| Attachment ngoài do fhir-downloader tải | cần máy chủ file ở máy chạy test, tắt chống SSRF chỉ trong override của test | smoke.py |
| Nhận event-notification qua WebSocket (Subscription) của ws-gateway | Apidog CLI không có bước WebSocket | smoke.py (helper Go) |
| STOW dạng tệp nhị phân thật | không dựng được nhị phân từ data | smoke.py (pkg/dicomtest) |
