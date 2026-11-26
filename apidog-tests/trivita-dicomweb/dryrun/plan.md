# Kế hoạch dry-run trivita-dicomweb (bước 1-4 xong, chờ duyệt bước 5-6)

## Trạng thái
| Hạng mục | Trạng thái |
|---|---|
| Module Apidog 1533160 "Trivita DICOMweb" + 23 endpoint trên nhánh AI | **Đã có** (parent tạo) |
| `spec/` (openapi, endpoint-details, coverage, environment) | Xong, local |
| `data/` 7 tệp, 127 dòng | Xong, local (import vào app = thủ công) |
| `scenarios/` 7 scenario, 58 bước | Xong, local; chưa tạo trong app |
| Folder test-scenario, environment, scenario trong app | **Chưa** (cần duyệt) |
| Chạy | **Chưa** (cần stack: `make up-full` + seed client) |

## Offline (level 1) đã chạy
- `check_test_assets.py`: 0 ERROR; 12 WARNING đã xét (cột đọc bằng `pm.iterationData` ⇒ "never referenced"; dòng Security kỳ vọng 2xx là chủ đích).
- `cli-schema validate test-scenario-create` (7/7) và `test-scenario-update` (7/7, steps dựng bằng `tools/build_scenario_update.py`).

## Scenario
| Slug | P | Bước | Dòng | Nội dung |
|---|---|---|---|---|
| dicomweb-health | 0 | 4 | 1 | 4 probe |
| dicomweb-lifecycle | 0 | 29 | 2 | token → STOW (200/idempotent/409 study sai/202) → QIDO 4 cấp + Warning + phân trang → WADO metadata/tải/frame/bulkdata/rendered/WADO-URI → 404 instance SR |
| dicomweb-stow-validation | 1 | 2 | 18 | lỗi Content-Type/boundary/thân/metadata/bulkdata/UID, biên UID 64/65 |
| dicomweb-qido-matrix | 1 | 3 | 30 | QIDO /studies mọi tổ hợp lọc, phân trang, lỗi 400, tấn công |
| dicomweb-retrieve-matrix | 1 | 3 | 69 | WADO-RS/URI/QIDO cấp con: 200 so byte, 404/400/416/406/501, biên |
| dicomweb-authz-invalid-token | 1 | 5 | 6 | 6 loại token xấu × 4 route → 401 + WWW-Authenticate |
| dicomweb-routing-isolation | 1 | 12 | 1 | 401, 4 tiền tố, 403 project khác, 501, 404 ngoài tiền tố, header bảo mật |

## Lệnh ghi (sau khi duyệt, theo thứ tự)
```bash
P=1380475; B=ai/20261125-from-main-trivita-platform-tests; R=apidog-tests/trivita-dicomweb
apidog folder create --project $P --type test-scenario --name "Trivita DICOMweb" --branch $B   # folderId -> dryrun/ids.json
python3 -I $R/tools/sync_scenarios.py $R --project $P --branch $B                             # dùng bản vá tools/, KHÔNG dùng bản của skill
python3 -I ~/.claude/skills/apidog-module-tests/scripts/write_import_guide.py $R "Trivita DICOMweb"
apidog environment create "Trivita Local" --project $P --base-url http://localhost:8008        # rồi environment update: baseUrls.default = dicomweb; biến dicomUrl, authUrl
python3 -I ~/.claude/skills/apidog-module-tests/scripts/run_scenarios.py $R --project $P --env <envId> --branch $B --smoke --env-var clientId=... --env-var clientSecret=...
```
`tools/build_scenario_update.py` = bản của skill + 2 dòng (bodyType `raw`). **Cần đưa vào skill** (hoặc giữ `tools/`): thiếu nó STOW bị gửi `application/json`.
Environment dùng chung với 3 module khác: `dicomUrl` (http://localhost:8008), `authUrl` (http://localhost:8006); nếu environment đã có thì chỉ thêm `baseUrls["1533160"]`.

## Tác động lên hệ thống thử
Mỗi lần chạy: 1 token (Login granted), STOW tạo khoảng 3 study + ~10 instance mới (lifecycle) và ghi cố định 4 study seed cho các ma trận (idempotent); không xóa gì. Không gọi hệ thống ngoài.

## Rủi ro cần kiểm ở lần chạy đầu (smoke)
G4 (raw + CRLF + header Content-Type), G5 (`atob`), G2 (Accept rỗng), G3 (UID trong đường dẫn). Bước tiền điều kiện: stack local (`make up-full`), seed client (`docker compose ... exec auth /app seed project ...` in `clientId`/`clientSecret`), cổng 8005-8011 trống. Bước gọi sang `auth` dùng URL tuyệt đối `{{authUrl}}`.

## Product bug / phát hiện
Chưa có (chưa chạy).
