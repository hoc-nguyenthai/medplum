#!/usr/bin/env python3
"""Sinh data/ và scenarios/ cho module Trivita Auth (apps/auth).

Chạy:  python3 -I apidog-tests/trivita-auth/generate.py apidog-tests/trivita-auth
Nguồn sự thật của bộ test; không sửa tay các tệp được sinh. Quy tắc lấy từ apps/auth/docs/api-contract/cases/*.md
(thông điệp lỗi nguyên văn từ mã). Mọi thông tin đăng nhập là biến môi trường Apidog (xem spec/environment.json).
"""
import csv
import json
import sys
from pathlib import Path

ROOT = Path(sys.argv[1] if len(sys.argv) > 1 else "apidog-tests/trivita-auth")
(ROOT / "data").mkdir(exist_ok=True)
(ROOT / "scenarios").mkdir(exist_ok=True)

EPS = {(e["method"].lower(), e["path"]): e["id"] for e in json.loads((ROOT / "spec/endpoints.json").read_text(encoding="utf-8"))["data"]}

# PKCE: cặp mẫu của RFC 7636 phụ lục B (không phải bí mật)
VERIFIER = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
CHALLENGE = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
ZERO_UUID = "00000000-0000-4000-8000-000000000000"
OTHER_UUID = "00000000-0000-4000-8000-000000000001"
SCIM_CORE = "urn:ietf:params:scim:schemas:core:2.0:User"
SCIM_PATCH = "urn:ietf:params:scim:api:messages:2.0:PatchOp"

SCENARIOS = []  # (slug, priority, name)


# ---------------------------------------------------------------- helpers
def J(obj):
    return json.dumps(obj, ensure_ascii=False)


def step(name, method, tpl, path=None, query=None, body=None, form=None, auth=None, asserts=(), extracts=(), scripts=(), headers=None):
    ep = EPS.get((method.lower(), tpl))
    if ep is None:
        raise SystemExit(f"endpoint không có: {method} {tpl}")
    s = {"name": name, "endpointId": ep, "method": method.upper(), "path": path or tpl}
    if query:
        s["query"] = query
    if headers:
        s["headers"] = headers
    if body is not None:
        s["body"] = body if isinstance(body, str) else J(body)
    if form is not None:
        s["form"] = form
    if auth:
        s["auth"] = auth
    s["assertions"] = list(asserts)
    pp = [{"type": "extractor", "data": {"variableName": v, "expression": e, "subject": "responseJson"}} for v, e in extracts]
    pp += [{"type": "customScript", "data": {"script": sc}} for sc in scripts]
    if pp:
        s["postProcessors"] = pp
    return s


def scenario(slug, priority, title, description, steps):
    (ROOT / "scenarios" / f"{slug}.design.json").write_text(
        J({"slug": slug, "name": f"Auth | {title}", "priority": priority, "steps": steps}) + "\n", encoding="utf-8")
    (ROOT / "scenarios" / f"{slug}.create.json").write_text(
        J({"name": f"Auth | {title}", "description": description, "folderId": 0, "priority": priority, "tags": ["trivita-auth", "ai-generated"]}) + "\n",
        encoding="utf-8")
    SCENARIOS.append((slug, priority, title, description, steps))


def row(case_id, group, desc, status, code="", message="", **cols):
    r = {"case_id": case_id, "case_group": group, "description": desc, "expected_status": str(status), "expected_code": code, "expected_message": message}
    r.update(cols)
    return r


def write_csv(slug, rows):
    cols = []
    for r in rows:
        cols += [k for k in r if k not in cols]
    with (ROOT / "data" / f"{slug}.csv").open("w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=cols)
        w.writeheader()
        w.writerows(rows)
    DATA[slug] = rows


def write_json(slug, rows):
    (ROOT / "data" / f"{slug}.json").write_text(J(rows) + "\n", encoding="utf-8")
    DATA[slug] = rows


DATA = {}

# kiểm lỗi theo từng họ phản hồi (đọc cột expected_code / expected_message của hàng dữ liệu)
CHECK_OAUTH = ("const c = pm.iterationData.get('expected_code'), m = pm.iterationData.get('expected_message'); let j = {}; try { j = pm.response.json(); } catch (e) {} "
               "if (c) { pm.test('error = ' + c, () => pm.expect(j.error).to.eql(c)); } "
               "if (m) { pm.test('error_description = ' + m, () => pm.expect(j.error_description).to.eql(m)); }")
CHECK_OO = ("const c = pm.iterationData.get('expected_code'), m = pm.iterationData.get('expected_message'); let j = {}; try { j = pm.response.json(); } catch (e) {} "
            "const is = j.issue || []; "
            "if (c) { pm.test('issue code = ' + c, () => pm.expect(is.length ? is[0].code : '').to.eql(c)); } "
            "if (m) { pm.test('details.text = ' + m, () => pm.expect(is.map(i => (i.details || {}).text)).to.include(m)); }")
CHECK_SCIM = ("const c = pm.iterationData.get('expected_code'), m = pm.iterationData.get('expected_message'); let j = {}; try { j = pm.response.json(); } catch (e) {} "
              "if (c) { pm.test('scimType = ' + c, () => pm.expect(j.scimType).to.eql(c)); } "
              "if (m) { pm.test('detail = ' + m, () => pm.expect(j.detail).to.eql(m)); }")
EXP = ["status == {{expected_status}}"]

# ---------------------------------------------------------------- khối bước dùng lại
LOGIN_BODY = {"email": "{{adminEmail}}", "password": "{{adminPassword}}", "clientId": "{{clientId}}", "scope": "openid profile email"}


def login_steps(scope="openid profile email", var="userToken"):
    """Đăng nhập admin của project seed bằng client seed, đổi code lấy access token (authorization_code, client bí mật không PKCE)."""
    body = dict(LOGIN_BODY, scope=scope)
    return [
        step("Đăng nhập admin (email + mật khẩu)", "POST", "/auth/login", body=body, auth="none",
             asserts=["status == 200", "$.code exists", "$.login exists"], extracts=[("loginId", "$.login"), ("code", "$.code")]),
        step("Đổi code lấy token (authorization_code)", "POST", "/oauth2/token",
             form={"grant_type": "authorization_code", "code": "{{code}}", "client_id": "{{clientId}}", "client_secret": "{{clientSecret}}"},
             asserts=["status == 200", "$.access_token exists", "$.token_type == Bearer"], extracts=[(var, "$.access_token")]),
    ]


def cc_step(var="ccToken", cid="{{clientId}}", secret="{{clientSecret}}", scope=None, name="Lấy token client_credentials"):
    form = {"grant_type": "client_credentials", "client_id": cid, "client_secret": secret}
    if scope:
        form["scope"] = scope
    return step(name, "POST", "/oauth2/token", form=form, asserts=["status == 200", "$.access_token exists"], extracts=[(var, "$.access_token")])


def me(token, status=200, name="GET /auth/me", extra=()):
    return step(name, "GET", "/auth/me", auth=f"Bearer {{{{{token}}}}}", asserts=[f"status == {status}", *extra])


# ================================================================= S-01 smoke
scenario("auth-smoke", 0, "Smoke: health + discovery",
         "Kiểm nhanh service sống và các tài liệu công khai: /health/*, JWKS, openid-configuration, oauth-authorization-server, smart-configuration, smart-styles, oauth-protected-resource. Không cần thông tin đăng nhập.",
         [
             step("Liveness", "GET", "/health/live", asserts=["status == 200", "$.status == ok"]),
             step("Readiness (DB + accepting_traffic)", "GET", "/health/ready", asserts=["status == 200", "$.ready == true"]),
             step("JWKS có khóa", "GET", "/.well-known/jwks.json", asserts=["status == 200", "$.keys is array", "$.keys[0].kid exists"]),
             step("OpenID configuration", "GET", "/.well-known/openid-configuration",
                  asserts=["status == 200", "$.issuer exists", "$.token_endpoint exists", "$.jwks_uri exists"]),
             step("OAuth authorization server metadata", "GET", "/.well-known/oauth-authorization-server", asserts=["status == 200", "$.issuer exists"]),
             step("SMART configuration", "GET", "/.well-known/smart-configuration", asserts=["status == 200", "$.token_endpoint exists"]),
             step("SMART styles", "GET", "/.well-known/smart-styles.json", asserts=["status == 200"]),
             step("Protected resource metadata (mặc định)", "GET", "/.well-known/oauth-protected-resource", asserts=["status == 200", "$.resource exists"]),
         ])

# ================================================================= S-02 well-known: ETag + resource
scenario("auth-wellknown-caching", 1, "JWKS ETag và 304",
         "GET JWKS lấy ETag rồi gửi lại If-None-Match: 304 không body. Kiểm Cache-Control public, max-age=300.",
         [
             step("JWKS lần 1: lưu ETag", "GET", "/.well-known/jwks.json", asserts=["status == 200"],
                  scripts=["const e = pm.response.headers.get('ETag'); pm.test('có ETag', () => pm.expect(e).to.be.a('string').and.not.empty); pm.variables.set('etag', e);",
                           "pm.test('Cache-Control public, max-age=300', () => pm.expect(pm.response.headers.get('Cache-Control')).to.include('max-age=300'));"]),
             step("JWKS lần 2: If-None-Match đúng ETag → 304", "GET", "/.well-known/jwks.json", headers={"If-None-Match": "{{etag}}"}, asserts=["status == 304"]),
             step("JWKS: If-None-Match lạ → 200", "GET", "/.well-known/jwks.json", headers={"If-None-Match": "\"khong-khop\""}, asserts=["status == 200", "$.keys is array"]),
         ])

write_csv("auth-wellknown-resource", [
    row("WK-01", "Positive", "resource bằng chính base URL", 200, resource="{{authUrl}}"),
    row("WK-02", "Positive", "resource là đường con của base URL", 200, resource="{{authUrl}}/fhir/R4"),
    row("WK-03", "Security", "resource ngoài base URL bị từ chối", 400, message="Invalid resource URL", resource="https://evil.example/"),
    row("WK-04", "Security", "resource rỗng", 400, message="Invalid resource URL", resource=""),
    row("WK-05", "Security", "resource nhái tiền tố (base + .evil.example)", 400, message="Invalid resource URL", resource="{{authUrl}}.evil.example/"),
    row("WK-06", "Security", "resource kiểu file://", 400, message="Invalid resource URL", resource="file:///etc/passwd"),
])
scenario("auth-wellknown-resource", 1, "oauth-protected-resource: tham số resource",
         "Data-driven: resource hợp lệ (bắt đầu bằng AUTH_BASE_URL) 200; resource lạ/rỗng 400 văn bản 'Invalid resource URL' (RFC 9728 chống mở rộng).",
         [step("GET protected-resource?resource=…", "GET", "/.well-known/oauth-protected-resource", query={"resource": "{{resource}}"},
               asserts=EXP, scripts=["const m = pm.iterationData.get('expected_message'); if (m) { pm.test('thân văn bản = ' + m, () => pm.expect(pm.response.text().trim()).to.eql(m)); }"])])

# ================================================================= S-03 client_credentials lifecycle
scenario("auth-client-credentials-lifecycle", 0, "Vòng đời client_credentials: token → me → userinfo → introspect → logout",
         "Luồng máy-với-máy bằng client seed: xin token, đọc /auth/me (ClientApplication: không có user/security), userinfo, introspect, đăng xuất; token chết ngay sau logout.",
         [
             cc_step(scope="openid"),
             step("Introspect chính token (cùng client) → active", "POST", "/oauth2/introspect", form={"token": "{{ccToken}}"}, auth="Bearer {{ccToken}}",
                  asserts=["status == 200", "$.active == true", "$.client_id == {{clientId}}", "$.iss exists"]),
             me("ccToken", extra=["$.project.id == {{projectId}}", "$.user not exists", "$.security not exists"], name="GET /auth/me của ClientApplication"),
             step("GET /oauth2/userinfo (chỉ sub)", "GET", "/oauth2/userinfo", auth="Bearer {{ccToken}}",
                  asserts=["status == 200", "$.sub == {{clientId}}", "$.email not exists"]),
             step("POST /oauth2/userinfo cùng kết quả", "POST", "/oauth2/userinfo", auth="Bearer {{ccToken}}", asserts=["status == 200", "$.sub == {{clientId}}"]),
             step("Đăng xuất POST /oauth2/logout", "POST", "/oauth2/logout", auth="Bearer {{ccToken}}", asserts=["status == 200"]),
             me("ccToken", 401, "Sau logout: /auth/me → 401", extra=["$.issue[0].code == login"]),
             step("Sau logout: introspect bằng token chết → 401", "POST", "/oauth2/introspect", form={"token": "{{ccToken}}"}, auth="Bearer {{ccToken}}", asserts=["status == 401"]),
         ])

# ================================================================= S-04 user login lifecycle
scenario("auth-user-login-lifecycle", 0, "Vòng đời đăng nhập admin: login → token → me → userinfo → revoke",
         "Admin của project seed đăng nhập email+mật khẩu, đổi code lấy token, đọc me/userinfo/mfa status, introspect rồi thu hồi phiên: token chết ngay.",
         login_steps() + [
             me("userToken", extra=["$.user.email == {{adminEmail}}", "$.project.id == {{projectId}}", "$.membership.admin == true", "$.security.sessions is array"],
                name="GET /auth/me của admin"),
             step("GET /oauth2/userinfo (scope profile email)", "GET", "/oauth2/userinfo", auth="Bearer {{userToken}}",
                  asserts=["status == 200", "$.email == {{adminEmail}}", "$.sub exists", "$.email_verified exists"]),
             step("GET /auth/mfa/status", "GET", "/auth/mfa/status", auth="Bearer {{userToken}}", asserts=["status == 200"]),
             step("Introspect token admin (cùng client)", "POST", "/oauth2/introspect", form={"token": "{{userToken}}"}, auth="Bearer {{userToken}}",
                  asserts=["status == 200", "$.active == true"]),
             step("Thu hồi phiên bằng loginId", "POST", "/auth/revoke", body={"loginId": "{{loginId}}"}, auth="Bearer {{userToken}}", asserts=["status == 200"]),
             me("userToken", 401, "Sau revoke: /auth/me → 401"),
             step("Revoke lần hai bằng token chết → 401", "POST", "/auth/revoke", body={"loginId": "{{loginId}}"}, auth="Bearer {{userToken}}", asserts=["status == 401"]),
         ])

# ================================================================= S-05 code reuse
scenario("auth-code-reuse", 1, "Đổi authorization code hai lần thu hồi login",
         "AC8: dùng lại code → 400 invalid_grant 'Token already granted' và Login bị thu hồi, access token đã cấp chết (401).",
         login_steps() + [
             step("Đổi lại cùng code → 400", "POST", "/oauth2/token",
                  form={"grant_type": "authorization_code", "code": "{{code}}", "client_id": "{{clientId}}", "client_secret": "{{clientSecret}}"},
                  asserts=["status == 400", "$.error == invalid_grant", "$.error_description == Token already granted"]),
             me("userToken", 401, "Token đã cấp ở lần 1 chết (login bị thu hồi)"),
         ])

# ================================================================= S-06 login poll / scope / profile
scenario("auth-login-poll-scope", 1, "GET /auth/login/{login}, /auth/scope, /auth/profile",
         "Login đang dở: GET /auth/login/{id} phát code mới và vô hiệu code cũ; /auth/scope thu hẹp scope (mở rộng bị chặn 'Invalid scope'); /auth/profile thiếu trường; login đã cấp token không đọc lại được.",
         [
             step("Đăng nhập admin", "POST", "/auth/login", body=LOGIN_BODY, auth="none", asserts=["status == 200", "$.code exists"], extracts=[("loginId", "$.login"), ("code", "$.code")]),
             step("GET /auth/login/{login} phát code mới", "GET", "/auth/login/{login}", path="/auth/login/{{loginId}}", auth="none",
                  asserts=["status == 200", "$.code exists"], extracts=[("code2", "$.code")]),
             step("Code cũ bị vô hiệu → Invalid code", "POST", "/oauth2/token",
                  form={"grant_type": "authorization_code", "code": "{{code}}", "client_id": "{{clientId}}", "client_secret": "{{clientSecret}}"},
                  asserts=["status == 400", "$.error == invalid_request", "$.error_description == Invalid code"]),
             step("Scope mở rộng hơn lúc đăng nhập → 400 Invalid scope", "POST", "/auth/scope",
                  body={"login": "{{loginId}}", "scope": "openid profile email offline_access"}, auth="none",
                  asserts=["status == 400", "$.issue[0].details.text == Invalid scope"]),
             step("Thiếu scope → 400 Missing scope", "POST", "/auth/scope", body={"login": "{{loginId}}"}, auth="none",
                  asserts=["status == 400", "$.issue[0].details.text == Missing scope"]),
             step("Thu hẹp scope → code mới", "POST", "/auth/scope", body={"login": "{{loginId}}", "scope": "openid profile"}, auth="none",
                  asserts=["status == 200", "$.code exists"], extracts=[("code3", "$.code")]),
             step("Thiếu profile → 400 Missing profile", "POST", "/auth/profile", body={"login": "{{loginId}}"}, auth="none",
                  asserts=["status == 400", "$.issue[0].details.text == Missing profile"]),
             step("Đổi code mới nhất lấy token", "POST", "/oauth2/token",
                  form={"grant_type": "authorization_code", "code": "{{code3}}", "client_id": "{{clientId}}", "client_secret": "{{clientSecret}}"},
                  asserts=["status == 200", "$.scope == openid profile"]),
             step("Login đã cấp token: GET /auth/login → 400 Login already granted", "GET", "/auth/login/{login}", path="/auth/login/{{loginId}}", auth="none",
                  asserts=["status == 400", "$.issue[0].details.text == Login already granted"]),
             step("GET /auth/login/abc → 400 Login ID is required", "GET", "/auth/login/{login}", path="/auth/login/abc", auth="none",
                  asserts=["status == 400", "$.issue[0].details.text == Login ID is required"]),
             step("GET /auth/login/<uuid lạ> → 404", "GET", "/auth/login/{login}", path=f"/auth/login/{ZERO_UUID}", auth="none",
                  asserts=["status == 404", "$.issue[0].code == not-found"]),
         ])

# ================================================================= S-07 token negative (data)
C = "{{clientId}}"
S = "{{clientSecret}}"
write_csv("auth-token-negative", [
    row("TK-01", "Positive", "client_credentials hợp lệ scope openid", 200, grant_type="client_credentials", tc_client_id=C, tc_client_secret=S, tc_scope="openid", tc_code=""),
    row("TK-02", "Boundary", "client_credentials với scope SMART system/ vẫn được", 200, grant_type="client_credentials", tc_client_id=C, tc_client_secret=S, tc_scope="openid system/Patient.rs", tc_code=""),
    row("TK-03", "Negative", "thiếu grant_type", 400, "invalid_request", "Missing grant_type", grant_type="", tc_client_id=C, tc_client_secret=S, tc_scope="", tc_code=""),
    row("TK-04", "Negative", "grant_type=password không hỗ trợ (mã invalid_request, không phải unsupported_grant_type)", 400, "invalid_request", "Unsupported grant_type", grant_type="password", tc_client_id=C, tc_client_secret=S, tc_scope="", tc_code=""),
    row("TK-05", "Negative", "cc thiếu client_id", 400, "invalid_request", "Missing client_id", grant_type="client_credentials", tc_client_id="", tc_client_secret=S, tc_scope="", tc_code=""),
    row("TK-06", "Negative", "cc thiếu client_secret", 400, "invalid_request", "Missing client_secret", grant_type="client_credentials", tc_client_id=C, tc_client_secret="", tc_scope="", tc_code=""),
    row("TK-07", "Negative", "cc client không tồn tại", 400, "invalid_request", "Invalid client", grant_type="client_credentials", tc_client_id=ZERO_UUID, tc_client_secret="some-secret-value", tc_scope="", tc_code=""),
    row("TK-08", "Negative", "cc secret sai", 400, "invalid_request", "Invalid secret", grant_type="client_credentials", tc_client_id=C, tc_client_secret="sai-secret-sai-secret-sai-secret-00", tc_scope="", tc_code=""),
    row("TK-09", "Security", "cc xin scope patient/ bị chặn", 400, "invalid_scope", "Cannot use client credentials with patient scope", grant_type="client_credentials", tc_client_id=C, tc_client_secret=S, tc_scope="patient/Patient.rs", tc_code=""),
    row("TK-10", "Security", "cc xin scope fhir.admin (nâng quyền) bị chặn", 400, "invalid_scope", "Scope fhir.admin is reserved", grant_type="client_credentials", tc_client_id=C, tc_client_secret=S, tc_scope="fhir.admin", tc_code=""),
    row("TK-11", "Security", "scope fhir.* không phân biệt hoa thường", 400, "invalid_scope", "Scope FHIR.SUPER is reserved", grant_type="client_credentials", tc_client_id=C, tc_client_secret=S, tc_scope="FHIR.SUPER", tc_code=""),
    row("TK-12", "Security", "fhir.write lẫn trong danh sách scope", 400, "invalid_scope", "Scope fhir.write is reserved", grant_type="client_credentials", tc_client_id=C, tc_client_secret=S, tc_scope="openid fhir.write", tc_code=""),
    row("TK-13", "Negative", "authorization_code thiếu code", 400, "invalid_request", "Missing code", grant_type="authorization_code", tc_client_id=C, tc_client_secret=S, tc_scope="", tc_code=""),
    row("TK-14", "Negative", "authorization_code với code không có thật", 400, "invalid_request", "Invalid code", grant_type="authorization_code", tc_client_id=C, tc_client_secret=S, tc_scope="", tc_code="not-a-real-code"),
    row("TK-15", "Security", "code chứa chuỗi tiêm SQL", 400, "invalid_request", "Invalid code", grant_type="authorization_code", tc_client_id=C, tc_client_secret=S, tc_scope="", tc_code="' OR '1'='1"),
    row("TK-16", "Security", "client_id chứa chuỗi tiêm SQL", 400, "invalid_request", "Invalid client", grant_type="client_credentials", tc_client_id="' OR '1'='1", tc_client_secret="x-x-x-x-x-x-x-x-x-x-x-x-x-x-x-x-x", tc_scope="", tc_code=""),
])
scenario("auth-token-negative", 1, "POST /oauth2/token: grant, client, scope (data-driven)",
         "Một bước POST /oauth2/token theo từng hàng dữ liệu: thiếu/sai grant_type, client, secret, scope bị cấm (patient/, fhir.*), code sai. Thông điệp error/error_description theo mã.",
         [step("POST /oauth2/token theo dữ liệu", "POST", "/oauth2/token",
               form={"grant_type": "{{grant_type}}", "client_id": "{{tc_client_id}}", "client_secret": "{{tc_client_secret}}", "scope": "{{tc_scope}}", "code": "{{tc_code}}"},
               asserts=EXP, scripts=[CHECK_OAUTH])])

# ================================================================= S-08 misc negatives (no data)
scenario("auth-negative-misc", 1, "Phản hồi lỗi chung: Content-Type, thiếu Bearer, introspect rỗng",
         "T1: /oauth2/token nhận JSON → 400 văn bản 'Unsupported content type'; thiếu Bearer → 401 + WWW-Authenticate; introspect không token → 400; token lạ → active:false; header bảo mật.",
         [
             step("Token với Content-Type JSON → 400 văn bản", "POST", "/oauth2/token", body={"grant_type": "client_credentials"}, headers={"Content-Type": "application/json"},
                  asserts=["status == 400"], scripts=["pm.test('thân là Unsupported content type', () => pm.expect(pm.response.text()).to.include('Unsupported content type'));"]),
             step("GET /auth/me không Authorization → 401 + WWW-Authenticate", "GET", "/auth/me", auth="none", asserts=["status == 401", "$.issue[0].code == login"],
                  scripts=["pm.test('WWW-Authenticate Bearer realm=auth', () => pm.expect(pm.response.headers.get('WWW-Authenticate')).to.include('Bearer realm=\"auth\"'));",
                           "pm.test('Cache-Control no-store', () => pm.expect(pm.response.headers.get('Cache-Control')).to.include('no-store'));",
                           "pm.test('X-Content-Type-Options nosniff', () => pm.expect(pm.response.headers.get('X-Content-Type-Options')).to.eql('nosniff'));",
                           "pm.test('có X-Request-Id', () => pm.expect(pm.response.headers.get('X-Request-Id')).to.be.a('string').and.not.empty);"]),
             cc_step(),
             step("Introspect không có token → 400 văn bản", "POST", "/oauth2/introspect", form={"token": ""}, auth="Bearer {{ccToken}}", asserts=["status == 400"],
                  scripts=["pm.test('Token to introspect must be provided', () => pm.expect(pm.response.text()).to.include('Token to introspect must be provided'));"]),
             step("Introspect token lạ → active:false", "POST", "/oauth2/introspect", form={"token": "khong-phai-token"}, auth="Bearer {{ccToken}}",
                  asserts=["status == 200", "$.active == false"]),
             step("Introspect id_token/rác không lộ thông tin: một khóa duy nhất", "POST", "/oauth2/introspect", form={"token": "a.b.c"}, auth="Bearer {{ccToken}}",
                  asserts=["status == 200", "$.active == false", "$.iss not exists"]),
             step("GET /oauth2/logout thu hồi login", "GET", "/oauth2/logout", auth="Bearer {{ccToken}}", asserts=["status == 200"]),
             step("Gọi lại logout bằng token đã thu hồi → 401 (L4)", "GET", "/oauth2/logout", auth="Bearer {{ccToken}}", asserts=["status == 401"]),
         ])

# ================================================================= S-09 authz: bearer xấu trên mọi route cần Bearer (data)
BAD = [
    ("AZ-01", "Security", "Bearer rỗng", ""),
    ("AZ-02", "Security", "Bearer là chuỗi rác", "garbage"),
    ("AZ-03", "Security", "JWT alg=none, payload rỗng", "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.e30."),
    ("AZ-04", "Security", "ba đoạn a.b.c không phải JWT", "a.b.c"),
    ("AZ-05", "Security", "chuỗi tiêm SQL trong token", "' OR '1'='1"),
    ("AZ-06", "Boundary", "token dài 4096 ký tự", "A" * 4096),
    ("AZ-07", "Security", "token chứa chữ 'Bearer' lặp", "Bearer Bearer x"),
]
write_csv("auth-authz-bad-bearer", [row(i, g, d, 401, "login", accessToken=t) for i, g, d, t in BAD])
BT = "Bearer {{accessToken}}"
scenario("auth-authz-bad-bearer", 1, "Authz: Bearer sai trên mọi route bảo vệ → 401 (data-driven)",
         "Với mỗi token xấu, gọi mọi route cần Bearer (me, revoke, changepassword, mfa/status, introspect, userinfo, logout, admin) và đòi 401 + OperationOutcome code=login, không lộ thông tin.",
         [
             step("GET /auth/me", "GET", "/auth/me", auth=BT, asserts=EXP, scripts=[CHECK_OO]),
             step("POST /auth/revoke", "POST", "/auth/revoke", body={"loginId": ZERO_UUID}, auth=BT, asserts=EXP, scripts=[CHECK_OO]),
             step("POST /auth/changepassword", "POST", "/auth/changepassword", body={"oldPassword": "x", "newPassword": "Aa-123456-xx"}, auth=BT, asserts=EXP, scripts=[CHECK_OO]),
             step("GET /auth/mfa/status", "GET", "/auth/mfa/status", auth=BT, asserts=EXP, scripts=[CHECK_OO]),
             step("POST /oauth2/introspect", "POST", "/oauth2/introspect", form={"token": "x"}, auth=BT, asserts=EXP, scripts=[CHECK_OO]),
             step("GET /oauth2/userinfo", "GET", "/oauth2/userinfo", auth=BT, asserts=EXP, scripts=[CHECK_OO]),
             step("POST /oauth2/logout", "POST", "/oauth2/logout", auth=BT, asserts=EXP, scripts=[CHECK_OO]),
             step("GET /admin/projects/{id}", "GET", "/admin/projects/{projectId}", path="/admin/projects/{{projectId}}", auth=BT, asserts=EXP, scripts=[CHECK_OO]),
             step("POST /admin/projects/{id}/settings", "POST", "/admin/projects/{projectId}/settings", path="/admin/projects/{{projectId}}/settings", body=[], auth=BT, asserts=EXP, scripts=[CHECK_OO]),
         ])

# ================================================================= S-10 signup lifecycle
scenario("auth-signup-lifecycle", 1, "Đăng ký → tạo project → token → đổi mật khẩu",
         "newuser (projectId=new, PKCE) → newproject → đổi code (code_verifier) → me/admin của project mới; admin project mới không đọc được project seed (403); changepassword sai/đúng; đổi mật khẩu thu hồi mọi phiên.",
         [
             step("Đăng ký tài khoản mới", "POST", "/auth/newuser",
                  body={"firstName": "QA", "lastName": "Signup", "email": "qa.signup.{{$timestamp}}{{$randomInt}}@example.org", "password": "Qa-Signup-2026-xK9", "projectId": "new",
                        "codeChallenge": CHALLENGE, "codeChallengeMethod": "S256"},
                  auth="none", asserts=["status == 200", "$.login exists", "$.emailVerificationRequired == false"], extracts=[("suLogin", "$.login")]),
             step("Tạo project mới", "POST", "/auth/newproject", body={"login": "{{suLogin}}", "projectName": "QA Project {{$timestamp}}"}, auth="none",
                  asserts=["status == 200", "$.code exists"], extracts=[("suCode", "$.code")]),
             step("Đổi code (PKCE) lấy token", "POST", "/oauth2/token",
                  form={"grant_type": "authorization_code", "code": "{{suCode}}", "code_verifier": VERIFIER},
                  asserts=["status == 200", "$.access_token exists", "$.project.reference exists"], extracts=[("suToken", "$.access_token")],
                  scripts=["const ref = pm.response.json().project.reference; pm.variables.set('suProjectId', ref.split('/')[1]);"]),
             me("suToken", extra=["$.membership.admin == true", "$.project.id == {{suProjectId}}"], name="GET /auth/me: chủ project là admin"),
             step("Admin project mới đọc project của mình", "GET", "/admin/projects/{projectId}", path="/admin/projects/{{suProjectId}}", auth="Bearer {{suToken}}",
                  asserts=["status == 200", "$.project.id == {{suProjectId}}"]),
             step("Admin project mới đọc project seed → 403 chung", "GET", "/admin/projects/{projectId}", path="/admin/projects/{{projectId}}", auth="Bearer {{suToken}}",
                  asserts=["status == 403", "$.issue[0].code == forbidden", "$.issue[0].details.text == Forbidden"]),
             step("changepassword: mật khẩu cũ sai → 400", "POST", "/auth/changepassword", body={"oldPassword": "sai-mat-khau-cu", "newPassword": "Qa-Signup-2026-new1"}, auth="Bearer {{suToken}}",
                  asserts=["status == 400", "$.issue[0].details.text == Incorrect password"]),
             step("changepassword: mật khẩu mới quá ngắn → 400", "POST", "/auth/changepassword", body={"oldPassword": "Qa-Signup-2026-xK9", "newPassword": "short"}, auth="Bearer {{suToken}}",
                  asserts=["status == 400", "$.issue[0].details.text == Password must be at least 8 characters"]),
             step("changepassword đúng → 200", "POST", "/auth/changepassword", body={"oldPassword": "Qa-Signup-2026-xK9", "newPassword": "Qa-Signup-2026-new1"}, auth="Bearer {{suToken}}",
                  asserts=["status == 200"]),
             me("suToken", 401, "Đổi mật khẩu thu hồi mọi phiên → /auth/me 401"),
             step("newproject dùng lại login đã cấp code → 400", "POST", "/auth/newproject", body={"login": "{{suLogin}}", "projectName": "QA Project lan hai"}, auth="none",
                  asserts=["status == 400"]),
         ])

# ================================================================= S-11 signup validation (data, JSON)
G128, G129 = "G" * 128, "G" * 129
EM73 = "q" * 61 + "@example.org"  # 73 ký tự
rows = [
    row("NU-01", "Positive", "đăng ký hợp lệ", 200, firstName="QA", lastName="Valid", email="qa.nu01.{{$timestamp}}{{$randomInt}}@example.org", password="Qa-Valid-2026-xK9"),
    row("NU-02", "Boundary", "firstName đúng 128 ký tự", 200, firstName=G128, lastName="Valid", email="qa.nu02.{{$timestamp}}{{$randomInt}}@example.org", password="Qa-Valid-2026-xK9"),
    row("NU-03", "Boundary", "firstName 129 ký tự", 400, "invalid", "First name must be between 1 and 128 characters", firstName=G129, lastName="Valid", email="qa.nu03.{{$timestamp}}{{$randomInt}}@example.org", password="Qa-Valid-2026-xK9"),
    row("NU-04", "Boundary", "lastName đúng 128 ký tự", 200, firstName="QA", lastName=G128, email="qa.nu04.{{$timestamp}}{{$randomInt}}@example.org", password="Qa-Valid-2026-xK9"),
    row("NU-05", "Boundary", "lastName 129 ký tự", 400, "invalid", "Last name must be between 1 and 128 characters", firstName="QA", lastName=G129, email="qa.nu05.{{$timestamp}}{{$randomInt}}@example.org", password="Qa-Valid-2026-xK9"),
    row("NU-06", "Negative", "firstName rỗng", 400, "invalid", "First name must be between 1 and 128 characters", firstName="", lastName="Valid", email="qa.nu06.{{$timestamp}}{{$randomInt}}@example.org", password="Qa-Valid-2026-xK9"),
    row("NU-07", "Negative", "lastName rỗng", 400, "invalid", "Last name must be between 1 and 128 characters", firstName="QA", lastName="", email="qa.nu07.{{$timestamp}}{{$randomInt}}@example.org", password="Qa-Valid-2026-xK9"),
    row("NU-08", "Negative", "email không có @", 400, "invalid", "Valid email address between 3 and 72 characters is required", firstName="QA", lastName="Valid", email="khong-phai-email", password="Qa-Valid-2026-xK9"),
    row("NU-09", "Boundary", "email 73 ký tự (vượt 72)", 400, "invalid", "Valid email address between 3 and 72 characters is required", firstName="QA", lastName="Valid", email=EM73, password="Qa-Valid-2026-xK9"),
    row("NU-10", "Negative", "email rỗng", 400, "invalid", "Valid email address between 3 and 72 characters is required", firstName="QA", lastName="Valid", email="", password="Qa-Valid-2026-xK9"),
    row("NU-11", "Boundary", "password 7 ký tự", 400, "invalid", "Password must be at least 8 characters", firstName="QA", lastName="Valid", email="qa.nu11.{{$timestamp}}{{$randomInt}}@example.org", password="Aa1-xyz"),
    row("NU-12", "Boundary", "password đúng 8 ký tự", 200, firstName="QA", lastName="Valid", email="qa.nu12.{{$timestamp}}{{$randomInt}}@example.org", password="Aa1-xyzK"),
    row("NU-13", "Boundary", "password đúng 72 byte ASCII", 200, firstName="QA", lastName="Valid", email="qa.nu13.{{$timestamp}}{{$randomInt}}@example.org", password="Aa1-" + "k" * 68),
    row("NU-14", "Boundary", "password 73 byte", 400, "invalid", "Password must be no more than 72 characters", firstName="QA", lastName="Valid", email="qa.nu14.{{$timestamp}}{{$randomInt}}@example.org", password="Aa1-" + "k" * 69),
    row("NU-15", "Boundary", "password 36 ký tự é = 72 byte (giới hạn tính theo byte)", 200, firstName="QA", lastName="Valid", email="qa.nu15.{{$timestamp}}{{$randomInt}}@example.org", password="é" * 36),
    row("NU-16", "Boundary", "password 37 ký tự é = 74 byte", 400, "invalid", "Password must be no more than 72 characters", firstName="QA", lastName="Valid", email="qa.nu16.{{$timestamp}}{{$randomInt}}@example.org", password="é" * 37),
    row("NU-17", "Negative", "nhiều lỗi cùng lúc: validator gom issue đầu là firstName", 400, "invalid", "First name must be between 1 and 128 characters", firstName="", lastName="", email="x", password="1"),
    row("NU-18", "Negative", "email đã đăng ký (email admin seed)", 400, "invalid", "Email already registered", firstName="QA", lastName="Dup", email="{{adminEmail}}", password="Qa-Valid-2026-xK9"),
    row("NU-19", "Security", "firstName chứa HTML/script được lưu nguyên, không lỗi 500", 200, firstName="<script>alert(1)</script>", lastName="Valid", email="qa.nu19.{{$timestamp}}{{$randomInt}}@example.org", password="Qa-Valid-2026-xK9"),
    row("NU-20", "Security", "lastName chứa chuỗi tiêm SQL", 200, firstName="QA", lastName="'; DROP TABLE users;--", email="qa.nu20.{{$timestamp}}{{$randomInt}}@example.org", password="Qa-Valid-2026-xK9"),
    row("NU-21", "Security", "email chứa khoảng trắng và dấu nháy", 400, "invalid", "Valid email address between 3 and 72 characters is required", firstName="QA", lastName="Valid", email="a b'c@example.org", password="Qa-Valid-2026-xK9"),
]
write_json("auth-signup-validation", rows)
scenario("auth-signup-validation", 1, "POST /auth/newuser: kiểm tra đầu vào (data-driven)",
         "Phân vùng tương đương và biên của firstName/lastName (1-128), email (3-72), password (8 ký tự đến 72 byte), trùng email, chuỗi nguy hiểm. Hàng 200 tạo user qa.* (dọn bằng cách xóa DB test).",
         [step("POST /auth/newuser theo dữ liệu", "POST", "/auth/newuser",
               body='{"firstName": "{{firstName}}", "lastName": "{{lastName}}", "email": "{{email}}", "password": "{{password}}", "projectId": "new"}', auth="none",
               asserts=EXP, scripts=[CHECK_OO])])

# ================================================================= S-12 login validation (data, JSON)
LB = '{"email": "{{lg_email}}", "password": "{{lg_password}}", "clientId": "{{lg_client}}", "scope": "openid", "projectId": {{lg_project}}}'
rows = [
    row("LG-01", "Positive", "admin seed đăng nhập đúng", 200, lg_email="{{adminEmail}}", lg_password="{{adminPassword}}", lg_client="{{clientId}}", lg_project="null"),
    row("LG-02", "Positive", "projectId đúng project của client", 200, lg_email="{{adminEmail}}", lg_password="{{adminPassword}}", lg_client="{{clientId}}", lg_project='"{{projectId}}"'),
    row("LG-03", "Negative", "mật khẩu sai", 400, "invalid", "Email or password is invalid", lg_email="{{adminEmail}}", lg_password="Sai-mat-khau-123", lg_client="{{clientId}}", lg_project="null"),
    row("LG-04", "Negative", "email không tồn tại (cùng thông điệp, chống dò tài khoản)", 400, "invalid", "Email or password is invalid", lg_email="khong.ton.tai@example.org", lg_password="Sai-mat-khau-123", lg_client="{{clientId}}", lg_project="null"),
    row("LG-05", "Negative", "email sai định dạng", 400, "invalid", "Valid email address is required", lg_email="khong-phai-email", lg_password="Sai-mat-khau-123", lg_client="{{clientId}}", lg_project="null"),
    row("LG-06", "Boundary", "password 7 ký tự", 400, "invalid", "Password must be at least 8 characters", lg_email="{{adminEmail}}", lg_password="Aa1-xyz", lg_client="{{clientId}}", lg_project="null"),
    row("LG-07", "Boundary", "password 73 byte", 400, "invalid", "Password must be no more than 72 characters", lg_email="{{adminEmail}}", lg_password="Aa1-" + "k" * 69, lg_client="{{clientId}}", lg_project="null"),
    row("LG-08", "Negative", "projectId không phải UUID", 400, "invalid", "Invalid projectId", lg_email="{{adminEmail}}", lg_password="Sai-mat-khau-123", lg_client="{{clientId}}", lg_project='"abc"'),
    row("LG-09", "Negative", "clientId không tồn tại", 404, "not-found", "Not found", lg_email="{{adminEmail}}", lg_password="Sai-mat-khau-123", lg_client=ZERO_UUID, lg_project="null"),
    row("LG-10", "Negative", "projectId khác project của client", 400, "invalid", "Invalid projectId", lg_email="{{adminEmail}}", lg_password="Sai-mat-khau-123", lg_client="{{clientId}}", lg_project=f'"{OTHER_UUID}"'),
    row("LG-11", "Security", "email chứa chuỗi tiêm SQL", 400, "invalid", "Valid email address is required", lg_email="' OR '1'='1", lg_password="Sai-mat-khau-123", lg_client="{{clientId}}", lg_project="null"),
    row("LG-12", "Security", "mật khẩu là chuỗi tiêm SQL (vẫn là sai thông tin, không 500)", 400, "invalid", "Email or password is invalid", lg_email="{{adminEmail}}", lg_password="' OR 1=1 --", lg_client="{{clientId}}", lg_project="null"),
    row("LG-13", "Security", "mật khẩu 10000 ký tự bị từ chối ở validator", 400, "invalid", "Password must be no more than 72 characters", lg_email="{{adminEmail}}", lg_password="A" * 10000, lg_client="{{clientId}}", lg_project="null"),
]
write_json("auth-login-validation", rows)
scenario("auth-login-validation", 1, "POST /auth/login: đầu vào và thông tin sai (data-driven)",
         "Validator gom issue (email, projectId, password 8 ký tự..72 byte), sai thông tin không phân biệt email/mật khẩu, client/project lạ, chuỗi nguy hiểm.",
         [step("POST /auth/login theo dữ liệu", "POST", "/auth/login", body=LB, auth="none", asserts=EXP, scripts=[CHECK_OO])])

# ================================================================= S-13 userinfo theo scope (data)
write_csv("auth-userinfo-scope", [
    row("UI-01", "Positive", "scope chỉ openid → chỉ có sub", 200, ui_scope="openid", present="sub", absent="email,given_name,name"),
    row("UI-02", "Positive", "scope có email → có email, không có tên", 200, ui_scope="openid email", present="sub,email,email_verified", absent="given_name,family_name"),
    row("UI-03", "Positive", "scope có profile → có tên, không có email", 200, ui_scope="openid profile", present="sub,name,given_name,family_name,locale", absent="email"),
    row("UI-04", "Boundary", "khớp theo từ: 'emailx' không cho email", 200, ui_scope="openid emailx", present="sub", absent="email"),
])
scenario("auth-userinfo-scope", 1, "GET /oauth2/userinfo theo scope (data-driven)",
         "Đăng nhập với scope khác nhau rồi đọc userinfo: claim chỉ xuất hiện khi scope có đúng từ (openid/email/profile).",
         [
             step("Đăng nhập admin với scope theo dữ liệu", "POST", "/auth/login", body={"email": "{{adminEmail}}", "password": "{{adminPassword}}", "clientId": "{{clientId}}", "scope": "{{ui_scope}}"},
                  auth="none", asserts=["status == 200", "$.code exists"], extracts=[("code", "$.code")]),
             step("Đổi code lấy token", "POST", "/oauth2/token", form={"grant_type": "authorization_code", "code": "{{code}}", "client_id": "{{clientId}}", "client_secret": "{{clientSecret}}"},
                  asserts=["status == 200"], extracts=[("userToken", "$.access_token")]),
             step("GET /oauth2/userinfo kiểm claim theo scope", "GET", "/oauth2/userinfo", auth="Bearer {{userToken}}", asserts=EXP,
                  scripts=["const j = pm.response.json(); const has = (s) => (pm.iterationData.get(s) || '').split(',').filter(Boolean); "
                           "has('present').forEach(k => pm.test('có ' + k, () => pm.expect(j[k]).to.not.equal(undefined))); "
                           "has('absent').forEach(k => pm.test('không có ' + k, () => pm.expect(j[k]).to.equal(undefined)));"]),
         ])

# ================================================================= S-14 clientinfo + method + resetpassword (no data)
scenario("auth-public-lookups", 2, "Route công khai: clientinfo, method, resetpassword",
         "clientinfo: client seed 200, lạ 404, redirect_uri lạ 400. method: email hợp lệ 200 '{}', sai 400. resetpassword luôn 200 'All OK' cho email hợp lệ (có tài khoản hay không), email/projectId sai 400.",
         [
             step("clientinfo: client seed", "GET", "/auth/clientinfo/{clientId}", path="/auth/clientinfo/{{clientId}}", auth="none", asserts=["status == 200", "$.secret not exists"]),
             step("clientinfo: client không tồn tại → 404", "GET", "/auth/clientinfo/{clientId}", path=f"/auth/clientinfo/{ZERO_UUID}", auth="none", asserts=["status == 404", "$.issue[0].code == not-found"]),
             step("clientinfo: redirect_uri lạ → 400", "GET", "/auth/clientinfo/{clientId}", path="/auth/clientinfo/{{clientId}}", query={"redirect_uri": "https://evil.example/cb"}, auth="none",
                  asserts=["status == 400", "$.issue[0].details.text == Invalid redirect URI"]),
             step("method: email admin → mật khẩu ({})", "POST", "/auth/method", body={"email": "{{adminEmail}}"}, auth="none", asserts=["status == 200"]),
             step("method: email sai → 400", "POST", "/auth/method", body={"email": "khong-phai-email"}, auth="none",
                  asserts=["status == 400", "$.issue[0].details.text == Valid email address is required"]),
             step("resetpassword: có tài khoản → 200 All OK", "POST", "/auth/resetpassword", body={"email": "{{adminEmail}}", "sendEmail": False}, auth="none",
                  asserts=["status == 200", "$.issue[0].details.text == All OK"]),
             step("resetpassword: không có tài khoản → 200 giống hệt (chống dò)", "POST", "/auth/resetpassword", body={"email": "khong.ton.tai@example.org", "sendEmail": False}, auth="none",
                  asserts=["status == 200", "$.issue[0].details.text == All OK"]),
             step("resetpassword: email sai → 400", "POST", "/auth/resetpassword", body={"email": "x"}, auth="none",
                  asserts=["status == 400", "$.issue[0].details.text == Valid email address is required"]),
             step("resetpassword: projectId không phải UUID → 400", "POST", "/auth/resetpassword", body={"email": "{{adminEmail}}", "projectId": "abc"}, auth="none",
                  asserts=["status == 400", "$.issue[0].details.text == Invalid projectId"]),
         ])

# ================================================================= ADMIN
PREFIX = login_steps()
AP = "/admin/projects/{projectId}"
APP = "/admin/projects/{{projectId}}"
UT = "Bearer {{userToken}}"

scenario("auth-admin-lifecycle", 1, "Admin project: setting/secret/site/client/rotate-secret/invite/member/delete",
         "Admin seed quản trị chính project: đặt setting, secret (che), site, tạo client rồi xoay secret (retiring), mời user Practitioner, sửa/tắt/xóa membership. Cần AUTH_ADMIN_ENABLED=true.",
         PREFIX + [
             step("Đọc project; sinh email/tên duy nhất cho các bước sau", "GET", AP, path=APP, auth=UT, asserts=["status == 200", "$.project.id == {{projectId}}"],
                  scripts=["const t = Date.now(); pm.variables.set('invEmail', 'qa.inv.' + t + '@example.org'); pm.variables.set('invEmail2', 'qa.inv2.' + t + '@example.org');"]),
             step("Đặt setting appName", "POST", AP + "/settings", path=APP + "/settings", body=[{"name": "appName", "valueString": "QA App"}], auth=UT,
                  asserts=["status == 200", "$.project.setting[0].name == appName"]),
             step("Đặt secret qaSecret", "POST", AP + "/secrets", path=APP + "/secrets", body=[{"name": "qaSecret", "valueString": "s3cr3t-value-1"}], auth=UT, asserts=["status == 200"]),
             step("Đọc project: secret bị che", "GET", AP, path=APP, auth=UT, asserts=["status == 200", "$.project.secret[0].name == qaSecret", "$.project.secret[0].valueString == ********"]),
             step("Đặt site", "POST", AP + "/sites", path=APP + "/sites", body=[{"name": "qa-site", "domain": ["qa.example.org"]}], auth=UT, asserts=["status == 200"]),
             step("Tạo client client_credentials", "POST", AP + "/client", path=APP + "/client", body={"name": "QA lifecycle client {{$timestamp}}", "grantType": ["client_credentials"]}, auth=UT,
                  asserts=["status == 201", "$.secret exists", "$.id exists"], extracts=[("newClientId", "$.id"), ("newClientSecret", "$.secret")]),
             cc_step("newCcToken", "{{newClientId}}", "{{newClientSecret}}", name="Token của client mới"),
             step("Xoay secret client", "POST", AP + "/client/{clientId}/rotate-secret", path=APP + "/client/{{newClientId}}/rotate-secret", body={}, auth=UT,
                  asserts=["status == 200", "$.clientSecret exists", "$.retiring == true"], extracts=[("rotatedSecret", "$.clientSecret")]),
             cc_step("oldOk", "{{newClientId}}", "{{newClientSecret}}", name="Secret cũ còn dùng được khi retiring"),
             cc_step("newOk", "{{newClientId}}", "{{rotatedSecret}}", name="Secret mới dùng được"),
             step("Xoay secret trùng secret hiện tại → 400", "POST", AP + "/client/{clientId}/rotate-secret", path=APP + "/client/{{newClientId}}/rotate-secret",
                  body={"secret": "{{rotatedSecret}}"}, auth=UT, asserts=["status == 400", "$.issue[0].details.text == New secret must differ from the current secret"]),
             step("Bỏ secret cũ (retireOnly)", "POST", AP + "/client/{clientId}/rotate-secret", path=APP + "/client/{{newClientId}}/rotate-secret", body={"retireOnly": True}, auth=UT,
                  asserts=["status == 200", "$.retiring == false"]),
             step("Secret cũ hết hiệu lực → 400 Invalid secret", "POST", "/oauth2/token",
                  form={"grant_type": "client_credentials", "client_id": "{{newClientId}}", "client_secret": "{{newClientSecret}}"},
                  asserts=["status == 400", "$.error == invalid_request", "$.error_description == Invalid secret"]),
             step("Mời user Practitioner (không gửi thư)", "POST", AP + "/invite", path=APP + "/invite",
                  body={"resourceType": "Practitioner", "firstName": "QA", "lastName": "Invite", "email": "{{invEmail}}", "sendEmail": False, "scope": "project", "password": "Qa-Invite-2026-pw1"},
                  auth=UT, asserts=["status == 200", "$.resourceType == ProjectMembership", "$.id exists"], extracts=[("membershipId", "$.id")]),
             step("Mời lại cùng email → 409", "POST", AP + "/invite", path=APP + "/invite",
                  body={"resourceType": "Practitioner", "firstName": "QA", "lastName": "Invite", "email": "{{invEmail}}", "sendEmail": False, "scope": "project"},
                  auth=UT, asserts=["status == 409", "$.issue[0].details.text == User is already a member of this project"]),
             step("Đọc membership; dựng body tắt active", "GET", AP + "/members/{membershipId}", path=APP + "/members/{{membershipId}}", auth=UT,
                  asserts=["status == 200", "$.id == {{membershipId}}"],
                  scripts=["const m = pm.response.json(); m.active = false; pm.variables.set('memberJson', JSON.stringify(m));"]),
             step("Tắt membership (active=false)", "POST", AP + "/members/{membershipId}", path=APP + "/members/{{membershipId}}", body="{{memberJson}}", auth=UT,
                  asserts=["status == 200", "$.active == false"]),
             step("Admin đặt mật khẩu cho user của project", "POST", "/admin/projects/setpassword", body={"email": "{{invEmail}}", "password": "Qa-Invite-2026-pw2"}, auth=UT, asserts=["status == 200"]),
             step("Đổi email của member", "POST", AP + "/members/{membershipId}/update-email", path=APP + "/members/{{membershipId}}/update-email", body={"email": "{{invEmail2}}"}, auth=UT,
                  asserts=["status == 200"]),
             step("Đổi email lần nữa về chính email đó → 400", "POST", AP + "/members/{membershipId}/update-email", path=APP + "/members/{{membershipId}}/update-email", body={"email": "{{invEmail2}}"}, auth=UT,
                  asserts=["status == 400", "$.issue[0].details.text == New email is the same as the current email"]),
             step("Xóa membership", "DELETE", AP + "/members/{membershipId}", path=APP + "/members/{{membershipId}}", auth=UT, asserts=["status == 200"]),
             step("Đọc membership đã xóa → 403 chung", "GET", AP + "/members/{membershipId}", path=APP + "/members/{{membershipId}}", auth=UT,
                  asserts=["status == 403", "$.issue[0].details.text == Forbidden"]),
             step("Xóa lần hai → 403 chung", "DELETE", AP + "/members/{membershipId}", path=APP + "/members/{{membershipId}}", auth=UT, asserts=["status == 403"]),
             step("Trả setting/secret/site về rỗng", "POST", AP + "/settings", path=APP + "/settings", body=[], auth=UT, asserts=["status == 200"]),
             step("Xóa secret", "POST", AP + "/secrets", path=APP + "/secrets", body=[], auth=UT, asserts=["status == 200"]),
             step("Xóa site", "POST", AP + "/sites", path=APP + "/sites", body=[], auth=UT, asserts=["status == 200"]),
         ])

# --- admin authz
scenario("auth-admin-authz", 1, "Admin: phân quyền (client không admin, project khác, membership lạ)",
         "client_credentials chưa nâng admin → 403; admin gọi project khác hoặc membership không tồn tại → 403 chung (không cho dò id); Basic không được chấp nhận.",
         [cc_step()] + PREFIX + [
             step("Client chưa admin đọc project → 403", "GET", AP, path=APP, auth="Bearer {{ccToken}}", asserts=["status == 403", "$.issue[0].code == forbidden"]),
             step("Client chưa admin tạo client → 403", "POST", AP + "/client", path=APP + "/client", body={"name": "khong-duoc"}, auth="Bearer {{ccToken}}", asserts=["status == 403"]),
             step("Client chưa admin mời user → 403", "POST", AP + "/invite", path=APP + "/invite",
                  body={"resourceType": "Practitioner", "firstName": "A", "lastName": "B", "email": "qa.never@example.org", "sendEmail": False}, auth="Bearer {{ccToken}}", asserts=["status == 403"]),
             step("Admin đọc project khác → 403", "GET", AP, path=f"/admin/projects/{OTHER_UUID}", auth=UT, asserts=["status == 403", "$.issue[0].details.text == Forbidden"]),
             step("Admin đặt setting ở project khác → 403", "POST", AP + "/settings", path=f"/admin/projects/{OTHER_UUID}/settings", body=[], auth=UT, asserts=["status == 403"]),
             step("Admin đọc membership không tồn tại → 403 chung", "GET", AP + "/members/{membershipId}", path=APP + f"/members/{ZERO_UUID}", auth=UT, asserts=["status == 403"]),
             step("Admin xóa membership không tồn tại → 403 chung", "DELETE", AP + "/members/{membershipId}", path=APP + f"/members/{ZERO_UUID}", auth=UT, asserts=["status == 403"]),
             step("Admin xoay secret client không tồn tại → 403 chung", "POST", AP + "/client/{clientId}/rotate-secret", path=APP + f"/client/{ZERO_UUID}/rotate-secret", body={}, auth=UT, asserts=["status == 403"]),
             step("Body hỏng của người không admin vẫn nhận 400 trước 403 (thứ tự kiểm tra)", "POST", AP + "/client", path=APP + "/client", body="{", auth="Bearer {{ccToken}}",
                  asserts=["status == 400", "$.issue[0].details.text == Content could not be parsed"]),
         ])


# --- admin validation data sets
def admin_validation(slug, title, desc, ep, rows, extra_name):
    write_json(slug, rows)
    scenario(slug, 1, title, desc, login_steps() + [
        step(extra_name, "POST", AP + ep, path=APP + ep, body="{{admin_body}}", auth=UT, asserts=EXP, scripts=[CHECK_OO])])


def r(case, group, desc, status, body, msg="", code=""):
    return row(case, group, desc, status, code, msg, admin_body=body if isinstance(body, str) else J(body))


SEC32, SEC256, SEC257 = "a" * 32, "b" * 256, "c" * 257
client_rows = [
    r("AC-01", "Positive", "client_credentials hợp lệ", 201, {"name": "QA client {{$timestamp}}", "grantType": ["client_credentials"]}),
    r("AC-02", "Positive", "client authorization_code có redirectUris, pkceOptional", 201,
      {"name": "QA web {{$timestamp}}", "grantType": ["authorization_code"], "redirectUris": ["https://app.example.org/cb"], "pkceOptional": True}),
    r("AC-03", "Positive", "chỉ có name", 201, {"name": "QA minimal {{$timestamp}}"}),
    r("AC-04", "Boundary", "secret đúng 32 ký tự", 201, {"name": "QA s32 {{$timestamp}}", "secret": SEC32}),
    r("AC-05", "Boundary", "secret đúng 256 ký tự", 201, {"name": "QA s256 {{$timestamp}}", "secret": SEC256}),
    r("AC-06", "Boundary", "secret 31 ký tự", 400, {"name": "QA s31 {{$timestamp}}", "secret": "a" * 31}, "secret must be between 32 and 256 characters", "invalid"),
    r("AC-07", "Boundary", "secret 257 ký tự", 400, {"name": "QA s257 {{$timestamp}}", "secret": SEC257}, "secret must be between 32 and 256 characters", "invalid"),
    r("AC-08", "Negative", "name rỗng", 400, {"name": ""}, "Client name is required", "invalid"),
    r("AC-09", "Negative", "name chỉ khoảng trắng", 400, {"name": "   "}, "Client name is required", "invalid"),
    r("AC-10", "Negative", "thiếu name", 400, {"description": "khong co ten"}, "Client name is required", "invalid"),
    r("AC-11", "Negative", "trường lạ status", 400, {"name": "QA x", "status": "off"}, "Unsupported field: status", "invalid"),
    r("AC-12", "Negative", "grantType ngoài danh sách", 400, {"name": "QA x", "grantType": ["password"]}, "Unsupported grantType: password", "invalid"),
    r("AC-13", "Negative", "redirectUris không tuyệt đối", 400, {"name": "QA x", "redirectUris": ["not-a-url"]}, "Invalid redirect URI", "invalid"),
    r("AC-14", "Negative", "redirectUris scheme ftp", 400, {"name": "QA x", "redirectUris": ["ftp://x.example.org/cb"]}, "Invalid redirect URI", "invalid"),
    r("AC-15", "Negative", "redirectUris có fragment", 400, {"name": "QA x", "redirectUris": ["https://x.example.org/cb#frag"]}, "Invalid redirect URI", "invalid"),
    r("AC-16", "Negative", "launchUri scheme ftp", 400, {"name": "QA x", "launchUri": "ftp://x.example.org/launch"}, "Invalid launch URI", "invalid"),
    r("AC-17", "Negative", "accessPolicy không tồn tại", 400, {"name": "QA x", "accessPolicy": {"reference": "AccessPolicy/" + ZERO_UUID}}, "Invalid access policy", "invalid"),
    r("AC-18", "Negative", "redirectUris sai kiểu (chuỗi)", 400, {"name": "QA x", "redirectUris": "https://x.example.org/cb"}, "Invalid client body", "invalid"),
    r("AC-19", "Negative", "body là mảng, không phải object", 400, "[]", "Content could not be parsed", "invalid"),
    r("AC-20", "Negative", "JSON hỏng", 400, "{", "Content could not be parsed", "invalid"),
    r("AC-21", "Security", "name chứa HTML được lưu nguyên", 201, {"name": "<img src=x onerror=alert(1)> {{$timestamp}}"}),
    r("AC-22", "Security", "name rất dài 5000 ký tự không gây 500", 201, {"name": "n" * 5000}),
]
admin_validation("auth-admin-client-validation", "POST /admin/projects/{id}/client: đầu vào (data-driven)",
                 "Phân vùng/biên của name, secret (32..256), grantType, redirectUris, launchUri, accessPolicy, trường lạ, JSON hỏng. Hàng 201 tạo client qa.* trong project seed.",
                 "/client", client_rows, "Tạo client theo dữ liệu")

settings_rows = [
    r("AS-01", "Positive", "appName hợp lệ", 200, [{"name": "appName", "valueString": "QA App"}]),
    r("AS-02", "Boundary", "appName đúng 100 ký tự", 200, [{"name": "appName", "valueString": "a" * 100}]),
    r("AS-03", "Boundary", "appName 101 ký tự", 400, [{"name": "appName", "valueString": "a" * 101}], "appName must be a string of 1 to 100 characters", "invalid"),
    r("AS-04", "Negative", "appName rỗng", 400, [{"name": "appName", "valueString": ""}], "appName must be a string of 1 to 100 characters", "invalid"),
    r("AS-05", "Negative", "tên ngoài allowlist", 400, [{"name": "allow-dangerous-redirect", "valueString": "x"}], "Setting is not allowed: allow-dangerous-redirect", "invalid"),
    r("AS-06", "Negative", "trùng tên appName", 400, [{"name": "appName", "valueString": "a"}, {"name": "appName", "valueString": "b"}], "Duplicate setting: appName", "invalid"),
    r("AS-07", "Positive", "mfaRequired=false", 200, [{"name": "mfaRequired", "valueBoolean": False}]),
    r("AS-08", "Negative", "mfaRequired kiểu chuỗi hợp lệ cú pháp nhưng sai kiểu giá trị", 400, [{"name": "mfaRequired", "valueString": "true"}], "mfaRequired must be a boolean", "invalid"),
    r("AS-09", "Negative", "allowedMfaMethods ngoài totp/email", 400, [{"name": "allowedMfaMethods", "valueString": "sms"}], 'allowedMfaMethods must list "totp" and/or "email"', "invalid"),
    r("AS-10", "Positive", "allowedMfaMethods totp,email", 200, [{"name": "allowedMfaMethods", "valueString": "totp,email"}]),
    r("AS-11", "Negative", "allowedPractitionerEmailDomain có @", 400, [{"name": "allowedPractitionerEmailDomain", "valueString": "a@example.org"}], "allowedPractitionerEmailDomain must be a domain name such as example.org", "invalid"),
    r("AS-12", "Negative", "allowedPractitionerEmailDomain không có dấu chấm", 400, [{"name": "allowedPractitionerEmailDomain", "valueString": "localhost"}], "allowedPractitionerEmailDomain must be a domain name such as example.org", "invalid"),
    r("AS-13", "Positive", "allowedPractitionerEmailDomain nhiều mục (được phép)", 200, [{"name": "allowedPractitionerEmailDomain", "valueString": "example.org"}, {"name": "allowedPractitionerEmailDomain", "valueString": "example.com"}]),
    r("AS-14", "Negative", "body không phải mảng", 400, "{}", "Content could not be parsed", "invalid"),
    r("AS-15", "Negative", "body null", 400, "null", "Content could not be parsed", "invalid"),
    r("AS-16", "Negative", "valueBoolean là chuỗi (sai kiểu JSON)", 400, '[{"name":"mfaRequired","valueBoolean":"yes"}]', "Content could not be parsed", "invalid"),
    r("AS-99", "Positive", "đặt lại rỗng (dọn dẹp, chạy cuối)", 200, []),
]
admin_validation("auth-admin-settings-validation", "POST /admin/projects/{id}/settings: allowlist và giá trị (data-driven)",
                 "Allowlist 4 tên, biên appName 1..100, boolean, danh sách MFA, tên miền Practitioner, trùng tên, body sai. Hàng cuối đặt lại [] để không để lại setting.",
                 "/settings", settings_rows, "Đặt settings theo dữ liệu")

secret_items_51 = [{"name": f"s{i}", "valueString": "v"} for i in range(51)]
secret_items_50 = [{"name": f"s{i}", "valueString": "v"} for i in range(50)]
secrets_rows = [
    r("ST-01", "Positive", "một secret hợp lệ", 200, [{"name": "qaSecret", "valueString": "v1"}]),
    r("ST-02", "Positive", "secret kiểu boolean và integer", 200, [{"name": "flag", "valueBoolean": True}, {"name": "count", "valueInteger": 3}]),
    r("ST-03", "Boundary", "tên dài đúng 64 ký tự", 200, [{"name": "a" * 64, "valueString": "v"}]),
    r("ST-04", "Boundary", "tên 65 ký tự", 400, [{"name": "a" * 65, "valueString": "v"}], "Secret name must start with a letter and use only letters, digits, '_', '.' or '-' (up to 64 characters)", "invalid"),
    r("ST-05", "Negative", "tên bắt đầu bằng số", 400, [{"name": "1abc", "valueString": "v"}], "Secret name must start with a letter and use only letters, digits, '_', '.' or '-' (up to 64 characters)", "invalid"),
    r("ST-06", "Negative", "tên có khoảng trắng", 400, [{"name": "a b", "valueString": "v"}], "Secret name must start with a letter and use only letters, digits, '_', '.' or '-' (up to 64 characters)", "invalid"),
    r("ST-07", "Negative", "trùng tên", 400, [{"name": "dup", "valueString": "1"}, {"name": "dup", "valueString": "2"}], "Duplicate secret: dup", "invalid"),
    r("ST-08", "Negative", "không có giá trị", 400, [{"name": "novalue"}], "Secret novalue must have exactly one value", "invalid"),
    r("ST-09", "Negative", "hai giá trị cùng lúc", 400, [{"name": "twovalues", "valueString": "a", "valueBoolean": True}], "Secret twovalues must have exactly one value", "invalid"),
    r("ST-10", "Boundary", "valueString đúng 4096 ký tự", 200, [{"name": "big", "valueString": "x" * 4096}]),
    r("ST-11", "Boundary", "valueString 4097 ký tự", 400, [{"name": "big", "valueString": "x" * 4097}], "Secret value too long: big", "invalid"),
    r("ST-12", "Boundary", "đúng 50 secret", 200, secret_items_50),
    r("ST-13", "Boundary", "51 secret", 400, secret_items_51, "Too many secrets", "invalid"),
    r("ST-14", "Negative", "giữ '********' khi secret chưa tồn tại", 400, [{"name": "neverset", "valueString": "********"}], "Secret neverset has no existing value to keep", "invalid"),
    r("ST-15", "Negative", "tên bắt đầu bằng smtp khi máy chủ chưa bật SMTP riêng", 400, [{"name": "smtpPassword", "valueString": "x"}], "SMTP secrets are not enabled on this server: smtpPassword", "invalid"),
    r("ST-16", "Negative", "body không phải mảng", 400, "{}", "Content could not be parsed", "invalid"),
    r("ST-99", "Positive", "xóa hết secret (dọn dẹp, chạy cuối)", 200, []),
]
admin_validation("auth-admin-secrets-validation", "POST /admin/projects/{id}/secrets: tên, giá trị, số lượng (data-driven)",
                 "Biên tên 1..64 ký tự, valueString ≤ 4096, tối đa 50 mục, đúng một value[x], giữ '********', tiền tố smtp. Hàng cuối xóa hết secret.",
                 "/secrets", secrets_rows, "Đặt secrets theo dữ liệu")

sites_21 = [{"name": f"site{i}", "domain": ["a.example.org"]} for i in range(21)]
sites_20 = [{"name": f"site{i}", "domain": ["a.example.org"]} for i in range(20)]
sites_rows = [
    r("SI-01", "Positive", "một site hợp lệ", 200, [{"name": "web", "domain": ["app.example.org"]}]),
    r("SI-02", "Boundary", "tên site đúng 100 ký tự", 200, [{"name": "n" * 100, "domain": ["app.example.org"]}]),
    r("SI-03", "Boundary", "tên site 101 ký tự", 400, [{"name": "n" * 101, "domain": ["app.example.org"]}], "Site name is required (1 to 100 characters)", "invalid"),
    r("SI-04", "Negative", "tên site rỗng", 400, [{"name": "", "domain": ["app.example.org"]}], "Site name is required (1 to 100 characters)", "invalid"),
    r("SI-05", "Negative", "trùng tên site", 400, [{"name": "dup", "domain": ["a.example.org"]}, {"name": "dup", "domain": ["b.example.org"]}], "Duplicate site name: dup", "invalid"),
    r("SI-06", "Negative", "miền có khoảng trắng", 400, [{"name": "w", "domain": ["bad domain"]}], "Invalid site domain: bad domain", "invalid"),
    r("SI-07", "Negative", "miền có dấu @", 400, [{"name": "w", "domain": ["a@example.org"]}], "Invalid site domain: a@example.org", "invalid"),
    r("SI-08", "Negative", "miền thiếu dấu chấm", 400, [{"name": "w", "domain": ["localhost"]}], "Invalid site domain: localhost", "invalid"),
    r("SI-09", "Negative", "googleClientId không kèm secret", 400, [{"name": "w", "domain": ["a.example.org"], "googleClientId": "g1"}], "googleClientId and googleClientSecret must be set together", "invalid"),
    r("SI-10", "Negative", "recaptchaSiteKey không kèm secret", 400, [{"name": "w", "domain": ["a.example.org"], "recaptchaSiteKey": "k1"}], "recaptchaSiteKey and recaptchaSecretKey must be set together", "invalid"),
    r("SI-11", "Boundary", "đúng 20 site", 200, sites_20),
    r("SI-12", "Boundary", "21 site", 400, sites_21, "Too many sites", "invalid"),
    r("SI-13", "Negative", "giá trị vượt 512 ký tự", 400, [{"name": "w", "domain": ["a.example.org"], "googleClientId": "g" * 513, "googleClientSecret": "s"}], "Site value too long", "invalid"),
    r("SI-14", "Negative", "body không phải mảng", 400, "null", "Content could not be parsed", "invalid"),
    r("SI-99", "Positive", "xóa hết site (dọn dẹp, chạy cuối)", 200, []),
]
admin_validation("auth-admin-sites-validation", "POST /admin/projects/{id}/sites: tên, miền, cặp khóa (data-driven)",
                 "Biên tên 1..100, ≤ 20 site, định dạng miền, cặp Google/reCAPTCHA đi cùng nhau, giá trị ≤ 512. Hàng cuối xóa hết site.",
                 "/sites", sites_rows, "Đặt sites theo dữ liệu")

inv = lambda **kw: dict({"resourceType": "Practitioner", "firstName": "QA", "lastName": "Invite", "email": "qa.iv.{{$timestamp}}{{$randomInt}}@example.org", "sendEmail": False, "scope": "project"}, **kw)  # noqa: E731


def drop(d, *ks):
    return {k: v for k, v in d.items() if k not in ks}


invite_rows = [
    r("IV-01", "Positive", "mời Practitioner hợp lệ", 200, inv()),
    r("IV-02", "Positive", "mời Patient hợp lệ", 200, inv(resourceType="Patient")),
    r("IV-03", "Negative", "thiếu resourceType", 400, drop(inv(), "resourceType"), "Resource type is required", "invalid"),
    r("IV-04", "Negative", "thiếu firstName", 400, drop(inv(), "firstName"), "First name is required", "invalid"),
    r("IV-05", "Negative", "thiếu lastName", 400, drop(inv(), "lastName"), "Last name is required", "invalid"),
    r("IV-06", "Negative", "không có email lẫn externalId", 400, drop(inv(), "email"), "Either email or externalId is required", "invalid"),
    r("IV-07", "Negative", "email sai định dạng", 400, inv(email="khong-phai-email"), "Valid email address is required", "invalid"),
    r("IV-08", "Negative", "trường lạ accessPolicy ở gốc", 400, inv(accessPolicy={"reference": "AccessPolicy/" + ZERO_UUID}), "Unsupported field: accessPolicy", "invalid"),
    r("IV-09", "Negative", "membership.accessPolicy không tồn tại", 400, inv(membership={"accessPolicy": {"reference": "AccessPolicy/" + ZERO_UUID}}), "Invalid access policy", "invalid"),
    r("IV-10", "Negative", "scope không phải project/server", 400, inv(scope="global"), 'scope must be "project" or "server"', "invalid"),
    r("IV-11", "Negative", "body JSON hỏng", 400, "{", "Content could not be parsed", "invalid"),
    r("IV-12", "Security", "firstName chứa HTML được lưu nguyên", 200, inv(firstName="<b>x</b>")),
    r("IV-13", "Security", "email với ký tự điều khiển", 400, inv(email="a\\u0000b@example.org"), "Valid email address is required", "invalid"),
]
admin_validation("auth-admin-invite-validation", "POST /admin/projects/{id}/invite: đầu vào (data-driven)",
                 "Trường bắt buộc (resourceType, firstName, lastName, email|externalId), định dạng email, scope, trường lạ, policy lạ. Hàng 200 tạo user/membership qa.iv.*.",
                 "/invite", invite_rows, "Mời user theo dữ liệu")

# ================================================================= SCIM
UB = "Bearer {{userToken}}"
scenario("auth-scim-lifecycle", 2, "SCIM v2 Users: tạo, đọc, lọc, sửa, xóa",
         "Cần AUTH_SCIM_ENABLED=true và project có feature scim (AUTH_DEFAULT_PROJECT_FEATURES=scim lúc seed). Admin Bearer tạo/đọc/lọc/PATCH/PUT/xóa user SCIM; thiếu token 401 hai dòng WWW-Authenticate; client không admin 403.",
         [cc_step()] + login_steps() + [
             step("Sinh userName/email duy nhất", "GET", "/health/live", auth="none", asserts=["status == 200"],
                  scripts=["const t = Date.now(); pm.variables.set('scimEmail', 'qa.scim.' + t + '@example.org');"]),
             step("POST /scim/v2/Users", "POST", "/scim/v2/Users",
                  body={"schemas": [SCIM_CORE], "userName": "{{scimEmail}}", "name": {"givenName": "QA", "familyName": "Scim"}, "emails": [{"value": "{{scimEmail}}"}], "userType": "Practitioner", "active": True},
                  auth=UB, asserts=["status == 201", "$.id exists", "$.active == true"], extracts=[("scimId", "$.id")],
                  scripts=["pm.test('Location trỏ /scim/v2/Users/<id>', () => pm.expect(pm.response.headers.get('Location')).to.include('/scim/v2/Users/'));"]),
             step("GET /scim/v2/Users/{id}", "GET", "/scim/v2/Users/{id}", path="/scim/v2/Users/{{scimId}}", auth=UB, asserts=["status == 200", "$.id == {{scimId}}", "$.name.givenName == QA"]),
             step("GET list lọc userName eq", "GET", "/scim/v2/Users", query={"filter": "userName eq \"{{scimEmail}}\""}, auth=UB, asserts=["status == 200", "$.totalResults == 1", "$.Resources is array"]),
             step("PATCH replace name.givenName", "PATCH", "/scim/v2/Users/{id}", path="/scim/v2/Users/{{scimId}}",
                  body={"schemas": [SCIM_PATCH], "Operations": [{"op": "replace", "path": "name.givenName", "value": "Updated"}]}, auth=UB,
                  asserts=["status == 200"]),  # chỉ 200: scimApply chỉ ghi tên khi User thuộc project, user SCIM mới là User toàn cục (scim.go:304) nên tên giữ nguyên
             step("PUT thay thế user", "PUT", "/scim/v2/Users/{id}", path="/scim/v2/Users/{{scimId}}",
                  body={"schemas": [SCIM_CORE], "userName": "{{scimEmail}}", "name": {"givenName": "Put", "familyName": "Scim"}, "emails": [{"value": "{{scimEmail}}"}], "active": True}, auth=UB,
                  asserts=["status == 200"]),  # tên chỉ ghi khi User thuộc project (scim.go:304); user SCIM mới là User toàn cục
             step("PATCH thiếu schema PatchOp → 400", "PATCH", "/scim/v2/Users/{id}", path="/scim/v2/Users/{{scimId}}",
                  body={"schemas": ["x"], "Operations": []}, auth=UB, asserts=["status == 400", "$.scimType == invalidSyntax"]),
             step("PATCH remove active → 400 mutability", "PATCH", "/scim/v2/Users/{id}", path="/scim/v2/Users/{{scimId}}",
                  body={"schemas": [SCIM_PATCH], "Operations": [{"op": "remove", "path": "active"}]}, auth=UB, asserts=["status == 400", "$.scimType == mutability"]),
             step("DELETE user → 204", "DELETE", "/scim/v2/Users/{id}", path="/scim/v2/Users/{{scimId}}", auth=UB, asserts=["status == 204"]),
             step("GET sau khi xóa → 404 SCIM Error", "GET", "/scim/v2/Users/{id}", path="/scim/v2/Users/{{scimId}}", auth=UB, asserts=["status == 404", "$.detail == Not found"]),
             step("Không token → 401 + hai dòng WWW-Authenticate", "GET", "/scim/v2/Users", auth="none", asserts=["status == 401"],
                  scripts=["const h = pm.response.headers.get('WWW-Authenticate') || ''; pm.test('có Bearer realm=scim', () => pm.expect(h).to.include('Bearer realm=\"scim\"'));"]),
             step("Client không admin → 403", "GET", "/scim/v2/Users", auth="Bearer {{ccToken}}", asserts=["status == 403"]),
             step("DELETE lần hai id đã xóa → 404", "DELETE", "/scim/v2/Users/{id}", path="/scim/v2/Users/{{scimId}}", auth=UB, asserts=["status == 404"]),
         ])

scim_rows = [
    row("SC-01", "Positive", "tạo hợp lệ", 201, scim_body=J({"schemas": [SCIM_CORE], "userName": "qa.sc01.{{$timestamp}}{{$randomInt}}@example.org", "name": {"givenName": "QA", "familyName": "Scim"}, "emails": [{"value": "qa.sc01.{{$timestamp}}{{$randomInt}}@example.org"}], "userType": "Practitioner"})),
    row("SC-02", "Negative", "thiếu emails", 400, "invalidValue", "emails[0].value is required", scim_body=J({"schemas": [SCIM_CORE], "userName": "qa.sc02", "name": {"givenName": "QA", "familyName": "Scim"}})),
    row("SC-03", "Negative", "userName rỗng", 400, "invalidValue", "userName is required", scim_body=J({"schemas": [SCIM_CORE], "userName": "", "name": {"givenName": "QA", "familyName": "Scim"}, "emails": [{"value": "qa.sc03.{{$timestamp}}{{$randomInt}}@example.org"}]})),
    row("SC-04", "Negative", "email sai định dạng", 400, "invalidValue", "Valid email address is required", scim_body=J({"schemas": [SCIM_CORE], "userName": "qa.sc04", "name": {"givenName": "QA", "familyName": "Scim"}, "emails": [{"value": "khong-phai-email"}]})),
    row("SC-05", "Negative", "thiếu givenName", 400, "invalidValue", "First name is required", scim_body=J({"schemas": [SCIM_CORE], "userName": "qa.sc05", "name": {"familyName": "Scim"}, "emails": [{"value": "qa.sc05.{{$timestamp}}{{$randomInt}}@example.org"}]})),
    row("SC-06", "Negative", "thiếu familyName", 400, "invalidValue", "Last name is required", scim_body=J({"schemas": [SCIM_CORE], "userName": "qa.sc06", "name": {"givenName": "QA"}, "emails": [{"value": "qa.sc06.{{$timestamp}}{{$randomInt}}@example.org"}]})),
    row("SC-07", "Negative", "RelatedPerson không có patient", 400, "invalidValue", "Patient is required to create a RelatedPerson", scim_body=J({"schemas": [SCIM_CORE], "userName": "qa.sc07", "name": {"givenName": "QA", "familyName": "Scim"}, "emails": [{"value": "qa.sc07.{{$timestamp}}{{$randomInt}}@example.org"}], "userType": "RelatedPerson"})),
    row("SC-08", "Negative", "JSON hỏng", 400, "invalidSyntax", "Content could not be parsed", scim_body="{"),
    row("SC-09", "Security", "givenName chứa HTML được lưu nguyên", 201, scim_body=J({"schemas": [SCIM_CORE], "userName": "qa.sc09", "name": {"givenName": "<script>1</script>", "familyName": "Scim"}, "emails": [{"value": "qa.sc09.{{$timestamp}}{{$randomInt}}@example.org"}]})),
]
write_json("auth-scim-create-validation", scim_rows)
scenario("auth-scim-create-validation", 2, "POST /scim/v2/Users: đầu vào (data-driven)",
         "Trường bắt buộc (userName, emails[0].value, name), định dạng email, RelatedPerson thiếu patient, JSON hỏng. Cần SCIM bật.",
         login_steps() + [step("POST /scim/v2/Users theo dữ liệu", "POST", "/scim/v2/Users", body="{{scim_body}}", auth=UB, asserts=EXP, scripts=[CHECK_SCIM])])

list_rows = [
    row("SL-01", "Positive", "liệt kê mặc định", 200, sc_filter='userName eq "khong-co-ai"', sc_start="1", sc_count="10"),
    row("SL-02", "Boundary", "count=0", 200, sc_filter='userName eq "khong-co-ai"', sc_start="1", sc_count="0"),
    row("SL-03", "Boundary", "startIndex < 1 coi là 1", 200, sc_filter='userName eq "khong-co-ai"', sc_start="0", sc_count="10"),
    row("SL-04", "Negative", "filter dạng không hỗ trợ", 400, "invalidFilter", "Unsupported filter: only `userName eq \"value\"` is supported", sc_filter='userName co "x"', sc_start="1", sc_count="10"),
    row("SL-05", "Negative", "startIndex không phải số", 400, "invalidValue", "startIndex must be an integer", sc_filter='userName eq "x"', sc_start="x", sc_count="10"),
    row("SL-06", "Negative", "count không phải số", 400, "invalidValue", "count must be an integer", sc_filter='userName eq "x"', sc_start="1", sc_count="abc"),
    row("SL-07", "Security", "filter chứa chuỗi tiêm SQL", 400, "invalidFilter", "Unsupported filter: value must be a quoted string", sc_filter="userName eq 'x' OR 1=1", sc_start="1", sc_count="10"),
]
write_csv("auth-scim-list-validation", list_rows)
scenario("auth-scim-list-validation", 2, "GET /scim/v2/Users: filter, phân trang (data-driven)",
         "filter chỉ hỗ trợ userName eq; startIndex/count phải là số; count âm/0 và startIndex<1 được chuẩn hóa. Cần SCIM bật.",
         login_steps() + [step("GET list theo dữ liệu", "GET", "/scim/v2/Users", query={"filter": "{{sc_filter}}", "startIndex": "{{sc_start}}", "count": "{{sc_count}}"}, auth=UB,
                               asserts=EXP, scripts=[CHECK_SCIM])])


# ================================================================= tài liệu và quy trình
def main():
    envvars = [{"name": n, "value": ""} for n in
               ["authUrl", "clientId", "clientSecret", "projectId", "adminEmail", "adminPassword", "apiKey",
                "etag", "invEmail", "invEmail2", "memberJson", "scimEmail", "suProjectId"]]
    (ROOT / "spec" / "environment.json").write_text(J({"variables": envvars}) + "\n", encoding="utf-8")

    md = ["# Kịch bản kiểm thử Trivita Auth (module 1533158)", "",
          "Sinh bởi `generate.py`. Mức ưu tiên: P0 chạy luôn, P1 chức năng chính, P2 cần cờ bật thêm.", ""]
    n_steps = 0
    for i, (slug, pr, title, desc, steps) in enumerate(SCENARIOS, start=1):
        n_steps += len(steps)
        data = DATA.get(slug)
        md += [f"## S-{i:02d} {slug} — {title}", "", f"- Ưu tiên: P{pr}", f"- Dữ liệu: " + (f"`data/{slug}.{'json' if (ROOT/'data'/(slug+'.json')).exists() else 'csv'}` ({len(data)} hàng)" if data else "không (kịch bản tĩnh)"),
               f"- Mô tả: {desc}", "- Các bước:"]
        for k, s in enumerate(steps, start=1):
            md.append(f"  {k}. {s['name']} — `{s['method']} {s['path']}` (endpoint {s['endpointId']}); kiểm: {', '.join(s['assertions']) or '—'}")
        if data:
            ids = [r_["case_id"] for r_ in data]
            md.append(f"- Case: {ids[0]} … {ids[-1]}")
        md.append("")
    md += ["## Chỉ thiết kế (chưa dựng, kèm lý do)", "",
           "| Route / luồng | Lý do chưa dựng | Điều kiện để dựng |", "|---|---|---|",
           "| `GET/POST /oauth2/authorize` | trả 302 tới `AUTH_APP_BASE_URL`; bước Apidog có theo chuyển hướng hay không chưa kiểm chứng | xác nhận tùy chọn follow-redirect, client có redirectUris |",
           "| `POST /oauth2/register` | route tắt mặc định (`AUTH_OAUTH_REGISTER_ENABLED`) | bật cờ; client dựng sẵn theo redirect_uri |",
           "| grant `refresh_token`, `pre-authorized_code`, `token-exchange`, `private_key_jwt` | cần client có grantType tương ứng/JWKS/khóa ký của bên test | client riêng tạo qua admin/client + JWKS tĩnh (`AUTH_ALLOW_PRIVATE_JWKS`) |",
           "| HTTP Basic của client ở `/oauth2/token` (A2, A3) | builder Apidog chỉ dựng Basic qua security scheme, header `Authorization` không khai báo bị server bỏ | cần bước customHttp có auth basic thủ công |",
           "| `/auth/mfa/*`, `/admin/.../mfa/reset` | cần mã TOTP tính theo thời gian (script không có thư viện) và `AUTH_MFA_SECRET_KEYS`; thiếu cấu hình là 500 fail đóng | bật khóa MFA, bước script HMAC-SHA1 |",
           "| `/auth/setpassword`, `/auth/verifyemail` | secret chỉ nằm trong link thư (provider `log` in ra log container) | đọc log hoặc SMTP bắt thư |",
           "| `/auth/google`, `/auth/external*`, `/auth/exchange`, `/auth/preauthorize` | cần IdP ngoài/Google JWKS thật hoặc giả lập | IdP giả lập + `auth domain add` |",
           "| `POST /email/v1/send` | route tắt mặc định, cần SMTP + feature `email` | SMTP giả (mailpit) |",
           "| `POST /auth/newpatient` | cần project mở đăng ký (openPatientRegistration + chính sách Patient) mà seed không tạo | tạo project mở bằng admin/settings hoặc CLI trước |",
           "| `POST /admin/.../members/{id}/resetpassword` | cần mailer cấu hình (`Email sending is not configured` nếu không) | SMTP giả |",
           "| `/admin/super/*` | cần `AUTH_SUPER_ADMIN_ENABLED` và đánh dấu `auth project super` bằng CLI | chạy lệnh CLI trong container trước |",
           "| `POST /admin/projects/{id}/client/{clientId}/smart-launch` | cần client có `launchUri` và `AUTH_FHIR_BASE_URL` | tạo client có launchUri trong scenario admin |",
           "| Hạn mức IP (429) và khóa tài khoản | mặc định tắt ở local; bật làm mọi scenario khác bị 429 | môi trường riêng có `AUTH_RATE_LIMIT=2`, `AUTH_LOGIN_RATE_LIMIT=1`; lặp 5 lần bằng nhiều bước |",
           "| Boundary email đúng 72 ký tự ở newuser | email phải duy nhất mỗi lần chạy mà độ dài cố định; `{{$timestamp}}` độ dài không cố định | pre-script sinh email theo độ dài |",
           ""]
    (ROOT / "scenarios" / "scenarios.md").write_text("\n".join(md), encoding="utf-8")

    groups = {}
    for rows_ in DATA.values():
        for r_ in rows_:
            groups[r_["case_group"]] = groups.get(r_["case_group"], 0) + 1
    print(f"{len(SCENARIOS)} scenario, {n_steps} step, {sum(groups.values())} hàng dữ liệu {groups}")


main()
