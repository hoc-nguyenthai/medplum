# Dry-run plan: Trivita Auth (module 1533158)

Trạng thái: bước 1-4 xong (local). **Chưa tạo scenario trong Apidog, chưa chạy.** Module + endpoint + nhánh đã có (do phiên chính tạo): project 1380475, nhánh `ai/20261125-from-main-trivita-platform-tests`, module 1533158 (71 endpoint).

## Con số
25 scenario, 164 bước, 167 hàng dữ liệu (Positive 26, Negative 79, Boundary 34, Security 28), 13 tệp data. Phủ 45/71 endpoint; 26 endpoint chỉ thiết kế (lý do ở `scenarios/scenarios.md`).

| Scenario | Ưu tiên | Bước | Hàng dữ liệu |
|---|---|---|---|
| auth-admin-authz | P1 | 12 | tĩnh |
| auth-admin-client-validation | P1 | 3 | 22 |
| auth-admin-invite-validation | P1 | 3 | 13 |
| auth-admin-lifecycle | P1 | 28 | tĩnh |
| auth-admin-secrets-validation | P1 | 3 | 17 |
| auth-admin-settings-validation | P1 | 3 | 17 |
| auth-admin-sites-validation | P1 | 3 | 15 |
| auth-authz-bad-bearer | P1 | 9 | 7 |
| auth-client-credentials-lifecycle | P0 | 8 | tĩnh |
| auth-code-reuse | P1 | 4 | tĩnh |
| auth-login-poll-scope | P1 | 11 | tĩnh |
| auth-login-validation | P1 | 1 | 13 |
| auth-negative-misc | P1 | 8 | tĩnh |
| auth-public-lookups | P2 | 9 | tĩnh |
| auth-scim-create-validation | P2 | 3 | 9 |
| auth-scim-lifecycle | P2 | 16 | tĩnh |
| auth-scim-list-validation | P2 | 3 | 7 |
| auth-signup-lifecycle | P1 | 11 | tĩnh |
| auth-signup-validation | P1 | 1 | 21 |
| auth-smoke | P0 | 8 | tĩnh |
| auth-token-negative | P1 | 1 | 16 |
| auth-user-login-lifecycle | P0 | 9 | tĩnh |
| auth-userinfo-scope | P1 | 3 | 4 |
| auth-wellknown-caching | P1 | 3 | tĩnh |
| auth-wellknown-resource | P1 | 1 | 6 |

## Lệnh ghi theo thứ tự (chờ duyệt)
1. (đã có) nhánh + module. Folder: `apidog folder create --project 1380475 --type test-scenario --name "Trivita Auth" --branch ai/20261125-from-main-trivita-platform-tests` → ghi `folderId` vào `dryrun/ids.json`.
2. `python3 -I ~/.claude/skills/apidog-module-tests/scripts/sync_scenarios.py apidog-tests/trivita-auth --project 1380475 --branch ai/20261125-from-main-trivita-platform-tests` (tạo 25 scenario, dựng bước, kiểm lại từng bước; sửa mọi MISMATCH).
3. `python3 -I …/write_import_guide.py apidog-tests/trivita-auth "Trivita Auth"` → `data/IMPORT.md` (import tay vào Test Data trong app; CLI không upload được hàng dữ liệu).
4. Environment "Trivita Local": `baseUrls.default` và `baseUrls["1533158"]` = `http://localhost:8006`; biến `authUrl` = `http://localhost:8006` (đúng AUTH_BASE_URL).

## Chạy local (sau duyệt)
```bash
docker compose -p trivita-apidog -f docker-compose.full.yml -f apidog-tests/trivita-auth/dryrun/compose.apidog-auth.yml --env-file .env.full up -d --build --wait
# seed (lệnh ở đầu compose.apidog-auth.yml) → lấy projectId, clientId, clientSecret
python3 -I …/run_scenarios.py apidog-tests/trivita-auth --project 1380475 --env <envId> --branch <B> --smoke \
  --env-var clientId=… --env-var clientSecret=… --env-var projectId=… --env-var adminEmail=… --env-var adminPassword=… --env-var authUrl=http://localhost:8006
```
Thứ tự gợi ý: `auth-smoke` → `auth-client-credentials-lifecycle` → `auth-user-login-lifecycle` → các scenario còn lại → P2 (SCIM). Dọn: `docker compose -p trivita-apidog … down --volumes`.

## Tác động lên hệ thống thử
Tạo trong DB tạm: ~25 user `qa.*` (signup/invite/SCIM), ~12 client `QA …`, 2 project mới (auth-signup-lifecycle + hàng newuser không tạo project), nhiều Login. Setting/secret/site của project seed được đặt rồi dọn về rỗng (hàng cuối mỗi tệp). Không thay đổi mật khẩu admin seed (chỉ user `qa.*`). Không gửi thư, không gọi Internet (HIBP tắt trong override).

## Chặn / cần người dùng
- Biến chạy thật: `clientId`, `clientSecret`, `projectId` (in ra khi seed), `adminEmail`, `adminPassword` (mật khẩu tạm chọn lúc seed), `authUrl`. Không lưu vào file.
- Cần `compose.apidog-auth.yml` (admin/SCIM/feature scim, tắt HIBP); thiếu thì admin/SCIM scenario ra 404 và signup/password ra 500.
- Docker daemon (OrbStack) đang tắt mặc định.

## Kiểm ở lần chạy đầu (xem coverage.md G1-G9)
G1 login/token của client seed không cần redirectUri/PKCE; G2 client seed không admin; G3 newuser+PKCE không client; G5/G6 header và `pm.variables.set` trong customScript.

## Lỗi sản phẩm đã gặp
Chưa chạy nên chưa có.

## Kết quả chạy 2026-11-25 (local docker compose, project trivita-apidog)
Chạy đầy đủ: auth 25 scenario, 178/179 lượt, 1393/1395 assertion.

### Lỗi sản phẩm (giữ assertion)
**P1. `GET /.well-known/oauth-protected-resource?resource=` chấp nhận địa chỉ nhái tiền tố.**
- Mong đợi (data WK-05): `resource=<authUrl>.evil.example/` bị 400 `Invalid resource URL`.
- Thực tế: 200, phản chiếu nguyên `resource` vào JSON.
- Tái hiện: `curl "http://localhost:8006/.well-known/oauth-protected-resource?resource=http://localhost:8006.evil.example/"`.
- Mã: `apps/auth/internal/presentation/httpapi/oauth.go:379` dùng `strings.HasPrefix(vals[0], h.baseURL)` không kiểm ký tự phân cách sau host. Gợi ý: so khớp `baseURL` đúng, hoặc `baseURL + "/"` / `baseURL + "?"`.
- **ĐÃ SỬA 2026-11-25**: thêm `hasURLPrefix` (sau base phải hết chuỗi hoặc `/`, `?`, `#`); test `TestWellKnownAndJWKS` thêm 4 ca nhái tiền tố + 5 ca hợp lệ (đỏ trước khi sửa); sinh lại swagger/reference và cập nhật `cases/well-known.md`; chạy lại auth: 25 scenario không còn lỗi (`auth-wellknown-resource` 6/6).

### Ghi chú đã xác minh (không phải lỗi)
- SCIM PUT/PATCH `name.*` trả 200 nhưng không đổi tên với user SCIM mới tạo: `scimApply` chỉ ghi tên khi User thuộc project (`apps/auth/internal/application/scim.go:304`); đã bỏ assertion đổi tên.
- `{{$timestamp}}` của Apidog chỉ tới giây: email test thêm `{{$randomInt}}`.
