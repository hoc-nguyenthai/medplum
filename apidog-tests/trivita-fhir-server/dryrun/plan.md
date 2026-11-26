# Dry-run plan: Trivita FHIR Server

Trạng thái 2026-11-25: bước 1-4 xong (local). **Chưa ghi gì vào Apidog cho scenario/environment/folder** (nhánh AI và module + 32 endpoint đã tạo trước đó bởi phiên chính). **Chưa chạy** (chưa dựng server).

## Đích
- Project 1380475 (Trivita-Platform), module **Trivita FHIR Server** id 1533159, nhánh AI `ai/20261125-from-main-trivita-platform-tests` (id 1432970). Không đụng main.
- Environment (chưa tạo): "Trivita Local", baseUrls: `default` và `1533159` = URL fhir-server (compose full: `http://localhost:8005`). Biến chạy xem `spec/environment.json`.

## Khối lượng
| Scenario | Ưu tiên | Step | Dòng | P/N/B/S | Resource tạo (≈) |
|---|---|---|---|---|---|
| smoke | P0 | 6 | 1 | 1/0/0/0 | 0 |
| crud-lifecycle | P0 | 14 | 8 | 4/0/2/2 | 8 (rồi expunge) |
| write-validation | P1 | 2 | 16 | 1/11/2/2 | 4 |
| conditional-write-flow | P1 | 10 | 1 | 1/0/0/0 | 4 |
| conditional-create-negative | P1 | 2 | 5 | 1/3/0/1 | 2 |
| conditional-update-negative | P1 | 2 | 6 | 1/5/0/0 | 1 |
| update-rules | P1 | 14 | 1 | 1/0/0/0 | 1 |
| patch-negative | P1 | 3 | 15 | 4/9/1/1 | 15 |
| search | P1 | 5 | 38 | 16/12/6/4 | 114 |
| search-post | P1 | 4 | 6 | 3/3/0/0 | 12 |
| batch-transaction-flow | P1 | 6 | 1 | 1/0/0/0 | 4 |
| bundle-negative | P1 | 2 | 14 | 0/10/3/1 | 0 |
| validate-operation | P1 | 2 | 11 | 2/9/0/0 | 0 |
| everything | P1 | 8 | 10 | 2/5/3/0 | 30 |
| bulk-export-flow | P1 | 10 | 1 | 1/0/0/0 | 1 Patient + 1 job + 1 Binary (xóa lúc cuối) |
| bulk-export-negative | P1 | 2 | 5 | 0/4/0/1 | 0 |
| status-unknown-id | P2 | 4 | 5 | 0/2/1/2 | 0 |
| terminology-lookup | P1 | 3 | 10 | 4/4/1/1 | 10 CodeSystem |
| terminology-expand | P1 | 4 | 10 | 3/5/1/1 | 10 CodeSystem + 10 ValueSet |
| code-system-import | P2 | 4 | 6 | 2/4/0/0 | 6 CodeSystem |
| graphql | P1 | 3 | 11 | 4/5/2/0 | 11 |
| authz-matrix | P1 | 9 | 6 | 1/0/0/5 | ≈12 (dọn bằng $expunge) |
| authn-negative | P1 | 5 | 4 | 0/0/1/3 | 0 |
| authz-project-header | P2 | 2 | 3 | 0/0/1/2 | 0 |
| project-isolation | P1 | 10 | 1 | 0/0/0/1 | 1 (project A) |
| rate-limits-flow | P2 | 4 | 1 | 1/0/0/0 | 0 |
| **Tổng** | | **140** | **196** | **54/91/24/27** | ≈250 resource, 1 job |

Chỉ thiết kế (không dựng): xem `scenarios/scenarios.md`.

## Offline đã làm
- `check_test_assets.py`: 0 ERROR; WARNING chỉ là cột được kiểm trong customScript (`pm.iterationData.get`) và ca Security/Negative mong 2xx có chủ đích (SQLi/XSS được lưu nguyên văn; GraphQL lỗi cấp field trả 200).
- Mọi `*.create.json` qua `cli-schema validate test-scenario-create`; mọi `*.update.json` (build offline từ builder, 140 step: 122 customHttp, 18 http gắn API) qua `test-scenario-update`.

## Lệnh ghi theo thứ tự (sau khi duyệt)
```bash
P=1380475; B=ai/20261125-from-main-trivita-platform-tests; D=apidog-tests/trivita-fhir-server
# 1. folder scenario (ghi folderId vào dryrun/ids.json)
apidog folder create --project $P --type test-scenario --name "Trivita FHIR Server" --branch $B
# 2. tạo + cập nhật 26 scenario, đọc lại và so từng step
python3 -I ~/.claude/skills/apidog-module-tests/scripts/sync_scenarios.py $D --project $P --branch $B
# 3. data: file CSV/JSON import thủ công ở app (CLI không upload được hàng): python3 -I .../write_import_guide.py $D "Trivita FHIR Server"
# 4. environment
apidog environment create "Trivita Local" --project $P --base-url http://localhost:8005
#    rồi environment update thêm baseUrls {"default":url,"1533159":url} (validate environment-update trước)
```

## Chạy (sau khi duyệt, môi trường local)
1. `make up-full` (9 container healthy). Seed 2 project: `echo "mat-khau" | docker compose -f docker-compose.full.yml --env-file .env.full exec -T auth /app seed project -name "QA A" -admin-email qa-a@example.org -admin-first QA -admin-last A` (in `projectId/clientId/clientSecret`); lặp lại với "QA B" cho `otherClient*`.
2. `python3 -I $SK/run_scenarios.py $D --project $P --env <envId> --branch $B --smoke --env-var authUrl=http://localhost:8006 --env-var clientId=... --env-var clientSecret=... --env-var projectId=... --env-var otherClientId=... --env-var otherClientSecret=...` rồi bỏ `--smoke`.
3. Chạy tuần tự (thứ tự trong `ids.json`); `bulk-export-flow` cần fhir-worker (đã trong compose full).

## Tác động lên hệ thống
- Ghi ≈250 resource vào project QA A (Patient, Observation, Condition, CodeSystem, ValueSet) tiền tố `Qa`/`qa.trivita.test`; expunge ở crud-lifecycle và authz-matrix, phần còn lại để lại (xóa bằng `make down-full` hoặc `$expunge`). Một AsyncJob `$export` + Binary, được DELETE cuối luồng.
- Không có hạn mức bật theo cấu hình mặc định của compose; nếu bật `FHIR_RATE_LIMIT`, `search` (38 dòng × 5 bước) dễ chạm 429.
- Không gọi dịch vụ ngoài. Không dùng môi trường prod.

## Điều cần kiểm ở lần chạy đầu (smoke)
G5 (đường dẫn tuyệt đối `{{authUrl}}/...` ở customHttp), G4 (ghi đè `Content-Type`), G7 (`pm.variables.set/get`), G6 (tên query là biến), G2/G3 (scope SMART cho client_credentials), G8 (client là admin). Sau `sync_scenarios.py` đọc kỹ MISMATCH/WARNING.

## Bug sản phẩm nghi ngờ / câu hỏi cho chủ service
Chưa có kết quả chạy. Ghi nhận trước từ tài liệu: G1 (binding `gender` không kiểm).
