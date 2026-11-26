# Plan — Trivita WS Gateway (module 1533161)

Trạng thái: **thiết kế local xong, chưa tạo scenario trong app, chưa chạy.** Chờ duyệt.

## Đích
- Project Trivita-Platform (1380475); nhánh AI `ai/20261125-from-main-trivita-platform-tests` (id 1432970); module 1533161 (đã có 18 endpoint, import từ swagger).
- Folder test-scenario `Trivita WS Gateway` (chưa tạo) → `dryrun/ids.json.folderId`.
- Environment `Trivita Local` (project-level, dùng chung 4 module): `baseUrls.default` và `baseUrls["1533161"]` = `http://localhost:8007`; biến `authUrl=http://localhost:8006`, `wsUrl=http://localhost:8007`; `clientId`/`clientSecret` nhập lúc chạy bằng `--env-var` (từ `auth seed project`), không lưu.

## Số lượng (offline đã pass)
- 14 scenario, 78 bước (10 trong đó là bước Chuẩn bị), 88 dòng data: Positive 20, Negative 38, Boundary 17, Security 13.
- `check_test_assets.py`: 0 ERROR, 15 WARNING (5 là chủ ý Security→202/200; 10 là scenario không cần data).
- 14 `*.create.json` hợp lệ theo `test-scenario-create`; 14 `*.update.json` dựng offline hợp lệ theo `test-scenario-update`.

| # | slug | P | Data | Ghi chú |
|---|---|---|---|---|
| 1 | health-smoke | 0 | – | 4 probe, không token |
| 2 | discovery | 0 | – | 3 well-known |
| 3 | hub-lifecycle-stu3 | 0 | 3 | đăng ký→mở→GET→heartbeat→đóng→hủy→hủy lại |
| 4 | hub-lifecycle-stu2 | 1 | 3 | GET trả mảng |
| 5 | hub-lifecycle-hub-alias | 1 | 3 | `/hub` = STU3 |
| 6 | hub-diagnosticreport-flow | 1 | – | open/update/stale/close, versionId |
| 7 | hub-validation-subscribe | 1 | 34 | |
| 8 | hub-validation-event | 1 | 21 | |
| 9 | hub-topic-path | 1 | 6 | |
| 10 | hub-get-topic | 1 | 9 | |
| 11 | hub-content-types | 2 | – | form |
| 12 | hub-requires-auth-all-routes | 1 | – | 9 route → 401 |
| 13 | hub-authz-bad-token | 1 | 6 | Security |
| 14 | hub-authz-scope | 1 | 3 | cần G2 |

## Chặn (blockers) trước khi chạy
1. **FHIRcast chưa bật trong stack**: `docker-compose.full.yml` không đặt biến FHIRCAST_* cho ws-gateway → mọi route Hub 404. Cần file override local (không sửa file repo), ví dụ `apidog-tests/trivita-ws-gateway/dryrun/compose.fhircast.yml`:
   `FHIRCAST_ENABLED=true`, `FHIRCAST_JWKS_URL=http://auth:8000/.well-known/jwks.json`, `FHIRCAST_JWT_ISSUER=FHIRCAST_JWT_AUDIENCE=http://localhost:8006`, `FHIRCAST_PUBLIC_URL=ws://localhost:8007` (Redis đã có `REDIS_URL`). → cần duyệt.
2. Token `client_credentials` phải mang scope `fhircast/*` (G2) — thử bằng curl trước.
3. Docker/OrbStack đang tắt mặc định; cần bật, `make up-full` + override, seed project/client bằng `auth seed project`.

## Lệnh ghi theo thứ tự (sau duyệt)
```bash
P=1380475; B=ai/20261125-from-main-trivita-platform-tests
apidog folder create --project $P --type test-scenario --name "Trivita WS Gateway" --branch $B   # → ids.json.folderId
python3 -I $SK/sync_scenarios.py apidog-tests/trivita-ws-gateway --project $P --branch $B
python3 -I $SK/write_import_guide.py apidog-tests/trivita-ws-gateway "Trivita WS Gateway"        # data/IMPORT.md
# environment dùng chung (do phiên điều phối tạo một lần); thêm baseUrls["1533161"]
python3 -I $SK/run_scenarios.py apidog-tests/trivita-ws-gateway --project $P --env <envId> --branch $B --smoke --env-var clientId=… --env-var clientSecret=…
```
`$SK=~/.claude/skills/apidog-module-tests/scripts`. Test data chỉ nằm local, người dùng import tay theo `data/IMPORT.md` (CLI không tải được dòng data).

## Tác động lên hệ thống
- Redis (dùng chung ws-gateway): tạo khoảng 60 đăng ký kênh (lease 1 giờ) và vài chục ngữ cảnh topic `qa-*` (TTL 24 giờ); không ghi DB Postgres; không gọi ra ngoài (Hub không gọi `hub.callback`).
- Auth: mỗi lần lặp lấy 1 token client_credentials (~100 token); kiểm tra hạn mức đăng nhập của auth nếu có.
- Không đụng main; không có hành động phá hủy.

## Cần kiểm ở lần chạy đầu (smoke)
- G3: script bước Chuẩn bị đặt được `access_token`/`topic` cho bước sau (`pm.sendRequest`, `pm.variables.set`).
- G4: bundle update hợp lệ + vị trí `context.versionId` ở `hub-diagnosticreport-flow`.
- G5: `null` JSON body (SV-24).
- Bước có form (`hub-content-types`) đã build customHttp; kiểm `Content-Type` thật là `application/x-www-form-urlencoded`.
- Dòng token rỗng `Bearer ` (AZ-01): Apidog có thể bỏ hẳn header → vẫn 401 nhưng ghi nhận.

## Product bug phát hiện
Chưa có (chưa chạy).

## Mismatch/ghi chú tài liệu
M1 (security scheme apiKey thay Bearer), M2 (thiếu 400/409 trong swagger), M3 (nhãn startup): xem `spec/coverage.md`.
