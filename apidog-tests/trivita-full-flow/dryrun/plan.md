# Dry-run plan: Trivita Full Flow (xuyên auth, fhir-server, dicomweb, ws-gateway)

Trạng thái 2026-11-25: bước 2-4 xong (local). **Chưa ghi gì vào Apidog, chưa chạy server.**

## Đích
- Project 1380475 (Trivita-Platform), nhánh AI `ai/20261125-from-main-trivita-platform-tests` (id 1432970). Không đụng main.
- Folder test-scenario riêng **"Trivita Full Flow"** (chưa tạo). Không thuộc module nào: bộ này không có endpoint của Apidog để bind.
- Environment "Trivita Local" dùng chung 4 module; biến `authUrl, fhirUrl, dicomUrl, wsUrl` (URL tuyệt đối trong từng bước nên baseUrls của module không ảnh hưởng bộ này).

## Khối lượng
| Scenario | Ưu tiên | Step | Dòng | Nhóm |
|---|---|---|---|---|
| full-flow-e2e | P0 | 41 | 1 | Positive |
| full-flow-negative-no-token | P1 | 13 | 1 | Security |
| full-flow-negative-bad-token | P1 | 6 (×6 dòng) | 6 | Negative 3 / Security 2 / Boundary 1 |
| full-flow-cross-service | P1 | 8 | 1 | Positive |
| **Tổng** | | **68** | **9** | |

`full-flow-e2e` (theo thứ tự): health 4 service → token → introspect + /auth/me → FHIR (POST/GET/tìm/PUT/_history/Observation/$everything) → `$export` async (202 → poll 3 lần + chờ 3s → manifest → đọc ndjson từ Binary → hủy job) → dicomweb (STOW metadata → QIDO → WADO metadata/instance/frame đúng byte) → fhir thấy `DicomStudy` → token scope `fhircast/*` → hub FHIRcast (subscribe → Patient-open → GET context → Patient-close → unsubscribe) → dọn (DELETE + `$expunge` Patient/Observation) → logout, /auth/me 401.

## Chỉ thiết kế (đã có trong tests/full_smoke/smoke.py, `make smoke-full`)
Webhook rest-hook (fhir-notifier), attachment ngoài (fhir-downloader), event-notification qua WebSocket, STOW tệp nhị phân thật. Lý do: cần máy nhận/máy chủ file ở máy chạy test hoặc client WebSocket; Apidog CLI không có.

## Builder và endpoint tổng hợp
- Mọi bước là customHttp, URL tuyệt đối. Builder cần `spec/endpoint-details/<id>.json` cho từng bước: generate.py ghi mỗi bước một **endpoint tổng hợp** (id 9100000+, path = chính URL của bước, không có `{param}`) — không tồn tại trong Apidog, `sync_scenarios.py` chỉ đọc file này nên chấp nhận được. Đã build offline 4 `update.json` bằng builder và `cli-schema validate test-scenario-update` đạt.
- **Phải dùng bản builder đã vá** `apidog-tests/trivita-dicomweb/tools/` (hỗ trợ `bodyType: raw` cho STOW). Bản của skill gửi STOW thành `application/json`.

## Offline đã làm
- `check_test_assets.py`: 0 ERROR, 1 WARNING (cột `bad_kind` chỉ được đọc trong customScript bằng `pm.iterationData.get`).
- 4 `create.json` và 4 `update.json` qua `cli-schema validate`.

## Lệnh ghi (sau khi duyệt, thực hiện SAU KHI 4 module đã sync xong)
```bash
P=1380475; B=ai/20261125-from-main-trivita-platform-tests; D=apidog-tests/trivita-full-flow
apidog folder create --project $P --type test-scenario --name "Trivita Full Flow" --branch $B   # folderId -> dryrun/ids.json
python3 -I apidog-tests/trivita-dicomweb/tools/sync_scenarios.py $D --project $P --branch $B
python3 -I ~/.claude/skills/apidog-module-tests/scripts/write_import_guide.py $D "Trivita Full Flow"
```
Chạy:
```bash
python3 -I ~/.claude/skills/apidog-module-tests/scripts/run_scenarios.py $D --project $P --env <envId> --branch $B --smoke \
  --env-var authUrl=http://localhost:8006 --env-var fhirUrl=http://localhost:8005 --env-var dicomUrl=http://localhost:8008 --env-var wsUrl=http://localhost:8007 \
  --env-var clientId=… --env-var clientSecret=…
```
(`run_scenarios.py` có thể cần chạy bằng bản trong `trivita-dicomweb/tools` nếu bản đó có vá; kiểm tra khi chạy.)

## Điều kiện để chạy
1. `make up-full` + **hai file override**: `apidog-tests/trivita-auth/dryrun/compose.apidog-auth.yml` (bật admin/SCIM, tắt kiểm HIBP và cookie secure) và file bật FHIRcast cho ws-gateway (xem `apidog-tests/trivita-ws-gateway/dryrun/plan.md`; thiếu thì hub trả 404 và các bước ws của e2e đỏ).
2. Seed 1 project/client: `auth seed project` → `clientId`, `clientSecret`.
3. Client phải xin được scope `fhircast/*` (G2 của ws-gateway), nếu không thì token ở bước ws trả lỗi.

## Tác động lên hệ thống
Tạo 1 Patient + 1 Observation (xóa mềm rồi `$expunge`), 1 job `$export` + 1 Binary (hủy job), 1 DicomStudy/Series/Instance + Binary pixel (**không có API xóa nên còn lại**; mất khi `make down-full`), 1 topic FHIRcast (unsubscribe). Mỗi lần chạy cấp ~3 token và logout 1-2 phiên.

## Chỗ chưa chắc (kiểm ở lần smoke đầu)
- G1: `pm.variables.set` trong customScript có truyền sang bước sau không (UID, topic, job_id, bin_id đều dựa vào đây); nếu không, đổi sang extractor hoặc `pm.environment.set`.
- G2: client seed có scope `fhircast/*` và quyền `$expunge`/`DicomStudy` không.
- G3: fhir-server có từ chối token đã logout ở auth không (step cuối của cross-service chỉ ghi nhận 200 hoặc 401).
- G4: thời gian `$export` của fhir-worker; 3 lần poll cách ~3s có thể không đủ trên máy chậm (tăng thêm lần poll nếu cần).
- G5: busy-wait 3s trong script sandbox; header `Content-Type` + CRLF của body raw STOW (như G4-G5 của dicomweb).
- G6: `GET /fhir/R4/DicomStudy?_count=50` chứa studyUid trong trang đầu; nếu nhiều study cũ thì cần lọc theo tham số tìm kiếm.
