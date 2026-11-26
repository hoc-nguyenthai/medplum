#!/usr/bin/env python3
"""Sinh data/*.json|csv + scenarios/*.design.json|create.json cho module Apidog "Trivita FHIR Server" (apps/fhir-server).

Chạy: python3 -I apidog-tests/trivita-fhir-server/generate.py apidog-tests/trivita-fhir-server
Nguồn sự thật của bộ test: KHÔNG sửa tay các tệp sinh ra, sửa tệp này rồi chạy lại.

Quy ước:
- Mọi scenario mở đầu bằng bước lấy token client_credentials ở auth ({{authUrl}}/oauth2/token), extract vào bearerToken.
- Giá trị bí mật chỉ là biến chạy (clientId, clientSecret, ...), không có trong tệp nào.
- Cột dữ liệu chung: case_id, case_group, description, expected_status, expected_code (issue.code của OperationOutcome),
  expected_message (chuỗi con của issue[0].details.text hoặc errors[0].message). Rỗng = không kiểm.
- Bước nào có header/query/form hoặc kỳ vọng khác 200 đi theo customHttp (builder tự chọn), để không dính kiểm phản hồi tích hợp.
"""
import csv
import json
import sys
from pathlib import Path

ROOT = Path(sys.argv[1] if len(sys.argv) > 1 else "apidog-tests/trivita-fhir-server")
(ROOT / "data").mkdir(parents=True, exist_ok=True)
(ROOT / "scenarios").mkdir(parents=True, exist_ok=True)
(ROOT / "spec" / "endpoint-details").mkdir(parents=True, exist_ok=True)

# ---------------------------------------------------------------- endpoint ids (nhánh AI ai/20261125-from-main-trivita-platform-tests)
EP = {
    "batch": 47548491, "export": 47548504, "cs_import_system": 47548507, "lookup": 47548519, "healthz": 47548521,
    "graphql": 47548492, "bulk_get": 47548505, "cs_import": 47548508, "expand": 47548520, "readyz": 47548522,
    "metadata": 47548493, "bulk_del": 47548506, "cm_import_url": 47548509, "search": 47548494, "cm_import": 47548510,
    "cond_put": 47548495, "pkg_install": 47548511, "create": 47548496, "everything": 47548512, "search_post": 47548497,
    "rate": 47548513, "read": 47548498, "job": 47548514, "update": 47548499, "reindex_type": 47548515,
    "delete": 47548500, "validate": 47548516, "patch": 47548501, "expunge": 47548517, "history": 47548502,
    "reindex": 47548518, "vread": 47548503,
    "auth-token": "auth-token", "auth-login": "auth-login",  # endpoint của module Auth, dùng làm customHttp (xem write_auth_token_detail)
}
AUTH = "Bearer {{bearerToken}}"
FHIR = "/fhir/R4"
JSON_PATCH = "application/json-patch+json"
FHIR_JSON = "application/fhir+json"
ZERO_UUID = "00000000-0000-4000-8000-000000000000"
# system của identifier: không chứa "/" để đường dẫn trong design khớp mẫu endpoint (builder tách theo "/")
IDENT_SYS = "urn:trivita:qa-mrn"


def write_auth_token_detail():
    """Endpoint giả cho bước token: builder cần endpoint-details/<id>.json (method + path); path tuyệt đối {{authUrl}}."""
    d = {"id": "auth-token", "name": "Token client_credentials (module Trivita Auth)", "method": "post",
         "path": "{{authUrl}}/oauth2/token", "parameters": {"path": [], "query": [], "header": [], "cookie": []},
         "auth": {"type": "noauth"}, "securityScheme": {}, "requestBody": {}, "responses": []}
    (ROOT / "spec" / "endpoint-details" / "auth-token.json").write_text(json.dumps(d, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    d2 = dict(d, id="auth-login", name="Đăng nhập email + mật khẩu (module Trivita Auth)", path="{{authUrl}}/auth/login")
    (ROOT / "spec" / "endpoint-details" / "auth-login.json").write_text(json.dumps(d2, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")


# ---------------------------------------------------------------- builders
def js(code):
    return {"type": "customScript", "data": {"script": code}}


def ext(var, expr, subject="responseJson"):
    return {"type": "extractor", "data": {"variableName": var, "expression": expr, "subject": subject}}


def step(name, ep, method, path, query=None, headers=None, body=None, form=None, auth=AUTH, asserts=(), post=(), custom=None):
    s = {"name": name, "endpointId": EP[ep], "method": method, "path": path}
    if query:
        s["query"] = query
    if headers:
        s["headers"] = headers
    if body is not None:
        s["body"] = body if isinstance(body, str) else json.dumps(body, ensure_ascii=False)
    if form is not None:
        s["form"] = form
    if auth:
        s["auth"] = auth
    if asserts:
        s["assertions"] = list(asserts)
    if post:
        s["postProcessors"] = list(post)
    if custom is None:  # bound chỉ khi kỳ vọng đúng 200, không header/query/form
        only200 = list(asserts) and all(a == "status == 200" for a in asserts if a.startswith("status"))
        custom = not (only200 and not headers and not query and form is None)
    if custom:
        s["custom"] = True
    return s


def token(var="bearerToken", client="clientId", secret="clientSecret", scope_name=None, scope_value=None, name=None):
    """Bước token client_credentials (customHttp tới auth). scope_name/scope_value: tên và giá trị trường scope (có thể là biến)."""
    if scope_name is None and client == "clientId":
        return {"_admin_token": True, "var": var}  # scenario() bung thành đăng nhập admin + đổi code (client seed chỉ có fhir.write, không phải admin)
    form = {"grant_type": "client_credentials", "client_id": "{{%s}}" % client, "client_secret": "{{%s}}" % secret}
    if scope_name:
        form[scope_name] = scope_value
    return step(name or "Lấy token client_credentials (auth)", "auth-token", "POST", "{{authUrl}}/oauth2/token", form=form, auth="none",
                asserts=["status == 200", "$.access_token exists"], post=[ext(var, "$.access_token")])


def patient(family, gender=None, birth=None, ident=None, extra=None, pid=None):
    p = {"resourceType": "Patient"}
    if pid:
        p["id"] = pid
    if ident:
        p["identifier"] = [{"system": IDENT_SYS, "value": ident}]
    p["name"] = [{"family": family, "given": ["Qa"]}]
    if gender:
        p["gender"] = gender
    if birth:
        p["birthDate"] = birth
    if extra:
        p.update(extra)
    return json.dumps(p, ensure_ascii=False)


def create_patient(name, family, var_id="patient_id", extract_family=None, **kw):
    posts = [ext(var_id, "$.id")]
    if extract_family:
        posts.append(ext(extract_family, "$.name[0].family"))
    return step(name, "create", "POST", f"{FHIR}/Patient", body=patient(family, **kw),
                asserts=["status == 201", "$.id exists"], post=posts)


STATUS_JS = ("const e = pm.iterationData.get('%s'); "
             "if (e) { pm.test('status == ' + e, () => pm.expect(String(pm.response.code)).to.eql(String(e))); }")
ERR_JS = (
    "const code = pm.iterationData.get('expected_code'); const msg = pm.iterationData.get('expected_message'); "
    "let b = {}; try { b = pm.response.json(); } catch (x) {} "
    "const issue = (b.issue && b.issue[0]) || {}; "
    "const text = (issue.details && issue.details.text) || (b.errors && b.errors[0] && b.errors[0].message) || ''; "
    "if (code) { pm.test('issue.code == ' + code, () => pm.expect(issue.code).to.eql(code)); } "
    "if (msg) { pm.test('thông điệp chứa: ' + msg, () => pm.expect(text).to.include(msg)); }"
)
SEARCH_JS = (
    "const g = k => pm.iterationData.get(k); const b = pm.response.json(); const n = (b.entry || []).length; "
    "const has = k => g(k) !== undefined && g(k) !== ''; "
    "if (has('expected_entries')) { pm.test('số entry == ' + g('expected_entries'), () => pm.expect(n).to.eql(Number(g('expected_entries')))); } "
    "if (has('expected_total')) { pm.test('total == ' + g('expected_total'), () => pm.expect(b.total).to.eql(Number(g('expected_total')))); } "
    "const rel = r => (b.link || []).some(l => l.relation === r); "
    "if (has('expected_next')) { pm.test('link next == ' + g('expected_next'), () => pm.expect(rel('next')).to.eql(g('expected_next') === 'true')); } "
    "if (has('expected_prev')) { pm.test('link previous == ' + g('expected_prev'), () => pm.expect(rel('previous')).to.eql(g('expected_prev') === 'true')); } "
    "if (has('expected_first_birthdate')) { pm.test('entry[0].birthDate == ' + g('expected_first_birthdate'), "
    "() => pm.expect(b.entry[0].resource.birthDate).to.eql(g('expected_first_birthdate'))); }"
)
HEADER_HAS_JS = "pm.test('header %(h)s chứa %(v)s', () => pm.expect(pm.response.headers.get('%(h)s') || '').to.include('%(v)s'));"
VAR_SET_FROM_HEADER_JS = (
    "const loc = pm.response.headers.get('Content-Location') || ''; const m = loc.match(/bulkdata\\/([^\\/]+)\\/status/); "
    "pm.test('Content-Location có id job', () => pm.expect(m).to.not.eql(null)); if (m) { pm.variables.set('job_id', m[1]); }"
)
SLEEP_JS = "const t0 = Date.now(); while (Date.now() - t0 < %d) {}"


def admin_token_steps(var="bearerToken"):
    return [
        step("Đăng nhập admin project (auth /auth/login)", "auth-login", "POST", "{{authUrl}}/auth/login", auth="none",
             body={"email": "{{adminEmail}}", "password": "{{adminPassword}}", "clientId": "{{clientId}}", "scope": "openid profile email"},
             asserts=["status == 200", "$.code exists"], post=[ext("adm_code", "$.code")]),
        step("Đổi code lấy token admin (authorization_code)", "auth-token", "POST", "{{authUrl}}/oauth2/token", auth="none",
             form={"grant_type": "authorization_code", "code": "{{adm_code}}", "client_id": "{{clientId}}", "client_secret": "{{clientSecret}}"},
             asserts=["status == 200", "$.access_token exists"], post=[ext(var, "$.access_token")]),
    ]


def scenario(slug, name, priority, description, steps, tags=None):
    steps = [x for st in steps for x in (admin_token_steps(st["var"]) if st.get("_admin_token") else [st])]
    design = {"slug": slug, "name": name, "priority": priority, "steps": steps}
    (ROOT / "scenarios" / f"{slug}.design.json").write_text(json.dumps(design, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    create = {"name": name, "description": description, "priority": priority, "folderId": 0}
    if tags:
        create["tags"] = tags
    (ROOT / "scenarios" / f"{slug}.create.json").write_text(json.dumps(create, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")


COUNTS = {}


def write_json(slug, rows):
    cols = []
    for r in rows:
        cols += [k for k in r if k not in cols]
    rows = [{c: str(r.get(c, "")) for c in cols} for r in rows]
    (ROOT / "data" / f"{slug}.json").write_text(json.dumps(rows, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    COUNTS[slug] = rows


def write_csv(slug, columns, rows):
    with (ROOT / "data" / f"{slug}.csv").open("w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=columns)
        w.writeheader()
        for r in rows:
            w.writerow({c: r.get(c, "") for c in columns})
    COUNTS[slug] = rows


def row(cid, group, desc, status, code="", msg="", **cols):
    return {"case_id": cid, "case_group": group, "description": desc, "expected_status": status,
            "expected_code": code, "expected_message": msg, **cols}


write_auth_token_detail()

# ================================================================= S-01 smoke
write_csv("smoke", ["case_id", "case_group", "description", "expected_status"],
          [row("TC-SM-01", "Positive", "Probe sống/sẵn sàng, metadata công khai, 401 khi thiếu token, tìm kiếm có token", "200")])
scenario("smoke", "Smoke fhir-server", 0, "Healthz, readyz, metadata công khai, 401 khi thiếu token, tìm kiếm Patient có token.", [
    token(),
    step("GET /healthz", "healthz", "GET", "/healthz", auth="none", asserts=["status == 200", "$.status == ok"]),
    step("GET /readyz", "readyz", "GET", "/readyz", auth="none", asserts=["status == 200", "$.ready == true"]),
    step("GET /fhir/R4/metadata (công khai)", "metadata", "GET", f"{FHIR}/metadata", auth="none",
         asserts=["status == 200", "$.resourceType == CapabilityStatement", "$.fhirVersion == 4.0.1"]),
    step("GET Patient không token → 401", "search", "GET", f"{FHIR}/Patient", auth="none",
         asserts=["status == 401", "$.resourceType == OperationOutcome", "$.issue[0].code == login"],
         post=[js(HEADER_HAS_JS % {"h": "WWW-Authenticate", "v": 'Bearer realm="fhir"'})]),
    step("GET Patient?_count=1 có token", "search", "GET", f"{FHIR}/Patient", query={"_count": "1"},
         asserts=["status == 200", "$.resourceType == Bundle", "$.type == searchset"]),
], ["smoke"])

# ================================================================= S-02 crud-lifecycle
cr = []
for i, (grp, desc, g, b, fam, given) in enumerate([
    ("Positive", "Patient nam, ngày sinh đầy đủ", "male", "1980-01-01", "Nguyen", "Van A"),
    ("Positive", "Patient nữ", "female", "1999-12-31", "Tran", "Thi B"),
    ("Positive", "gender other", "other", "1975-05-05", "Le", "C"),
    ("Boundary", "ngày sinh tối thiểu hợp lý 1900-01-01", "male", "1900-01-01", "Pham", "Cu"),
    ("Boundary", "ngày sinh chỉ có năm (FHIR date một phần)", "female", "2000", "Hoang", "D"),
    ("Positive", "tên tiếng Việt có dấu", "male", "1990-02-03", "Nguyễn", "Văn Á"),
    ("Security", "chuỗi giống XSS trong tên được lưu nguyên văn, không thực thi", "female", "1991-01-01", "<script>alert(1)</script>", "x"),
    ("Security", "chuỗi giống SQLi trong tên được lưu nguyên văn", "male", "1992-01-01", "Robert'); DROP TABLE fhir_resource;--", "x"),
], start=1):
    cr.append(row(f"TC-CR-{i:02d}", grp, desc, "201", family=fam, given=given, gender=g, birth_date=b))
write_json("crud-lifecycle", cr)


def cr_body(pid=None, gender="{{gender}}"):
    p = {"resourceType": "Patient", "name": [{"family": "{{family}}-{{$timestamp}}", "given": ["{{given}}"]}],
         "gender": gender, "birthDate": "{{birth_date}}"}
    if pid:
        p["id"] = pid
    return json.dumps(p, ensure_ascii=False)


scenario("crud-lifecycle", "Vòng đời Patient (CRUD, history, vread, patch, expunge)", 0,
         "Tạo → đọc → sửa → history → vread → PATCH → xóa mềm → 410 → expunge → 404.", [
    token(),
    step("POST Patient", "create", "POST", f"{FHIR}/Patient", body=cr_body(),
         asserts=["status == 201", "$.resourceType == Patient", "$.id exists", "$.meta.versionId exists"],
         post=[ext("patient_id", "$.id"), ext("v1", "$.meta.versionId"),
               js(HEADER_HAS_JS % {"h": "Location", "v": "/_history/"}), js(HEADER_HAS_JS % {"h": "ETag", "v": 'W/"'})]),
    step("GET Patient/{id}", "read", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}",
         asserts=["status == 200", "$.id == {{patient_id}}", "$.meta.versionId == {{v1}}"]),
    step("PUT Patient/{id} (đổi gender)", "update", "PUT", f"{FHIR}/Patient/{{{{patient_id}}}}", body=cr_body(pid="{{patient_id}}", gender="unknown"),
         asserts=["status == 200", "$.gender == unknown"],
         post=[ext("v2", "$.meta.versionId"),
               js("pm.test('versionId đổi sau PUT', () => pm.expect(pm.response.json().meta.versionId).to.not.eql(pm.variables.get('v1')));")]),
    step("GET _history (2 phiên bản)", "history", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}/_history",
         asserts=["status == 200", "$.resourceType == Bundle", "$.type == history"],
         post=[js("pm.test('2 entry', () => pm.expect(pm.response.json().entry.length).to.eql(2));")]),
    step("GET _history/{v1} (vread bản đầu)", "vread", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}/_history/{{{{v1}}}}",
         asserts=["status == 200", "$.meta.versionId == {{v1}}", "$.gender == {{gender}}"]),
    step("PATCH Patient/{id} (JSON Patch)", "patch", "PATCH", f"{FHIR}/Patient/{{{{patient_id}}}}",
         headers={"Content-Type": JSON_PATCH}, body=[{"op": "replace", "path": "/gender", "value": "other"}],
         asserts=["status == 200", "$.gender == other"]),
    step("DELETE Patient/{id} → 204", "delete", "DELETE", f"{FHIR}/Patient/{{{{patient_id}}}}", asserts=["status == 204"]),
    step("GET Patient đã xóa → 410", "read", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}",
         asserts=["status == 410", "$.issue[0].code == deleted"]),
    step("DELETE lần hai → 204 (không sinh phiên bản)", "delete", "DELETE", f"{FHIR}/Patient/{{{{patient_id}}}}", asserts=["status == 204"]),
    step("GET _history sau xóa (4 phiên bản)", "history", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}/_history",
         asserts=["status == 200", "$.type == history"],
         post=[js("pm.test('4 entry: create, put, patch, delete', () => pm.expect(pm.response.json().entry.length).to.eql(4));")]),
    step("POST $expunge → 200", "expunge", "POST", f"{FHIR}/Patient/{{{{patient_id}}}}/$expunge",
         asserts=["status == 200", "$.resourceType == OperationOutcome"]),
    step("GET sau expunge → 404", "read", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}", asserts=["status == 404", "$.issue[0].code == not-found"]),
    step("POST $expunge lần hai → 404", "expunge", "POST", f"{FHIR}/Patient/{{{{patient_id}}}}/$expunge", asserts=["status == 404"]),
], ["crud", "p0"])

# ================================================================= S-03 write-validation
OBS_MIN = '{"resourceType":"Observation"}'
wv = [
    row("TC-WV-01", "Positive", "Patient tối thiểu hợp lệ", "201", resource_type="Patient", body='{"resourceType":"Patient"}'),
    row("TC-WV-02", "Negative", "thân là mảng", "400", "invalid", "Body phải là một resource JSON", resource_type="Patient", body="[]"),
    row("TC-WV-03", "Negative", "thân là chuỗi JSON", "400", "invalid", "Body phải là một resource JSON", resource_type="Patient", body='"x"'),
    row("TC-WV-04", "Negative", "thân là số", "400", "invalid", "Body phải là một resource JSON", resource_type="Patient", body="1"),
    row("TC-WV-05", "Negative", "JSON hỏng", "400", "invalid", "JSON không hợp lệ", resource_type="Patient", body="{"),
    row("TC-WV-06", "Negative", "thiếu resourceType", "400", "invalid", 'resourceType không khớp: đường dẫn là Patient, body là ""', resource_type="Patient", body="{}"),
    row("TC-WV-07", "Negative", "resourceType lệch đường dẫn", "400", "invalid", 'resourceType không khớp: đường dẫn là Patient, body là "Observation"',
        resource_type="Patient", body=OBS_MIN),
    row("TC-WV-08", "Negative", "thuộc tính lạ", "400", "structure", 'Thuộc tính không hợp lệ "foo"', resource_type="Patient",
        body='{"resourceType":"Patient","foo":1}'),
    row("TC-WV-09", "Negative", "sai kiểu: active là chuỗi", "400", "value", "", resource_type="Patient", body='{"resourceType":"Patient","active":"yes"}'),
    row("TC-WV-10", "Negative", "thiếu thuộc tính bắt buộc của Observation", "400", "required", "Thiếu thuộc tính bắt buộc", resource_type="Observation", body=OBS_MIN),
    row("TC-WV-11", "Negative", "loại resource lạ", "404", "not-found", "Không có route cho POST Foo", resource_type="Foo", body='{"resourceType":"Foo"}'),
    row("TC-WV-12", "Negative", "Content-Type text/plain", "415", "not-supported", "Content-Type không hỗ trợ: text/plain", resource_type="Patient",
        body='{"resourceType":"Patient"}', content_type="text/plain"),
    row("TC-WV-13", "Boundary", "id chứa gạch dưới (sai định dạng id)", "400", "", "", resource_type="Patient", body='{"resourceType":"Patient","id":"a_b"}'),
    row("TC-WV-14", "Boundary", "id do client gửi bị server ghi đè", "201", resource_type="Patient", body='{"resourceType":"Patient","id":"mine"}', forbidden_id="mine"),
    row("TC-WV-15", "Security", "meta.project giả mạo bị ghi đè bằng project của token", "201", resource_type="Patient",
        body='{"resourceType":"Patient","meta":{"project":"project-khac"}}', forbidden_project="project-khac"),
    row("TC-WV-16", "Security", "SQLi trong family được lưu nguyên văn", "201", resource_type="Patient",
        body='{"resourceType":"Patient","name":[{"family":"x\' OR 1=1 --"}]}'),
]
for r in wv:
    r.setdefault("content_type", FHIR_JSON)
    r.setdefault("forbidden_id", "")
    r.setdefault("forbidden_project", "")
write_json("write-validation", wv)
scenario("write-validation", "POST resource: validate thân và lỗi", 1,
         "POST /fhir/R4/{resourceType} theo bảng ca: thân hỏng, sai kiểu, thiếu bắt buộc, loại lạ, Content-Type, id/meta do server ghi đè.", [
    token(),
    step("POST /{resourceType} theo data", "create", "POST", f"{FHIR}/{{{{resource_type}}}}", headers={"Content-Type": "{{content_type}}"}, body="{{body}}",
         asserts=["status == {{expected_status}}"],
         post=[js(ERR_JS),
               js("const f = pm.iterationData.get('forbidden_id'); if (f) { pm.test('id client gửi bị ghi đè', () => pm.expect(pm.response.json().id).to.not.eql(f)); } "
                  "const p = pm.iterationData.get('forbidden_project'); if (p) { pm.test('meta.project không phải giá trị client gửi', "
                  "() => pm.expect((pm.response.json().meta || {}).project).to.not.eql(p)); }")]),
], ["negative", "validation"])

# ================================================================= S-04 conditional-write-flow
cw = [row("TC-CW-01", "Positive", "create/update có điều kiện: 0 khớp, 1 khớp, nhiều khớp, If-Match", "201")]
write_json("conditional-write-flow", cw)
scenario("conditional-write-flow", "Ghi có điều kiện (If-None-Exist, PUT theo query)", 1,
         "POST If-None-Exist: 0/1/nhiều khớp; PUT ?identifier=: 0 khớp tạo mới, 1 khớp cập nhật, nhiều khớp 412, If-Match lệch, id lệch.", [
    token(),
    step("POST Patient (ident mới)", "create", "POST", f"{FHIR}/Patient",
         body=patient("Cond-{{$timestamp}}", ident="cw-{{case_id}}-{{$timestamp}}"),
         asserts=["status == 201"], post=[ext("patient_id", "$.id"), ext("ident", "$.identifier[0].value"), ext("v1", "$.meta.versionId")]),
    step("POST If-None-Exist (1 khớp) → 200, không tạo mới", "create", "POST", f"{FHIR}/Patient",
         headers={"If-None-Exist": f"identifier={IDENT_SYS}|{{{{ident}}}}"}, body=patient("Cond-khac", ident="{{ident}}"),
         asserts=["status == 200", "$.id == {{patient_id}}"],
         post=[js("pm.test('không có Location (không tạo mới)', () => pm.expect(pm.response.headers.get('Location')).to.eql(undefined));")]),
    step("POST cùng ident không điều kiện → 201 (tạo trùng)", "create", "POST", f"{FHIR}/Patient", body=patient("Cond-trung", ident="{{ident}}"),
         asserts=["status == 201"]),
    step("POST If-None-Exist (2 khớp) → 412", "create", "POST", f"{FHIR}/Patient",
         headers={"If-None-Exist": f"identifier={IDENT_SYS}|{{{{ident}}}}"}, body=patient("Cond-khac", ident="{{ident}}"),
         asserts=["status == 412", "$.issue[0].code == conflict"], post=[js("pm.test('thông điệp', () => pm.expect(pm.response.json().issue[0].details.text).to.include('Nhiều resource khớp If-None-Exist'));")]),
    step("PUT ?identifier (2 khớp) → 412", "cond_put", "PUT", f"{FHIR}/Patient?identifier={IDENT_SYS}|{{{{ident}}}}", body=patient("Cond-put", ident="{{ident}}"),
         asserts=["status == 412"], post=[js("pm.test('thông điệp', () => pm.expect(pm.response.json().issue[0].details.text).to.include('Nhiều resource khớp tiêu chí'));")]),
    step("PUT ?identifier (0 khớp) → 201 tạo mới", "cond_put", "PUT", f"{FHIR}/Patient?identifier={IDENT_SYS}|{{{{ident}}}}-moi",
         body=patient("Cond-moi", ident="{{ident}}-moi"), asserts=["status == 201"],
         post=[ext("patient2_id", "$.id"), ext("v_new", "$.meta.versionId")]),
    step("PUT ?identifier (1 khớp) → 200 cập nhật cùng id", "cond_put", "PUT", f"{FHIR}/Patient?identifier={IDENT_SYS}|{{{{ident}}}}-moi",
         body=patient("Cond-moi", gender="female", ident="{{ident}}-moi"), asserts=["status == 200", "$.id == {{patient2_id}}", "$.gender == female"]),
    step("PUT ?identifier (1 khớp) + If-Match cũ → 412", "cond_put", "PUT", f"{FHIR}/Patient?identifier={IDENT_SYS}|{{{{ident}}}}-moi",
         headers={"If-Match": 'W/"{{v_new}}"'}, body=patient("Cond-moi", ident="{{ident}}-moi"),
         asserts=["status == 412"], post=[js("pm.test('thông điệp', () => pm.expect(pm.response.json().issue[0].details.text).to.include('Phiên bản không khớp'));")]),
    step("PUT ?identifier (1 khớp) + id lệch → 400", "cond_put", "PUT", f"{FHIR}/Patient?identifier={IDENT_SYS}|{{{{ident}}}}-moi",
         body=patient("Cond-moi", ident="{{ident}}-moi", pid=ZERO_UUID), asserts=["status == 400"],
         post=[js("pm.test('thông điệp', () => pm.expect(pm.response.json().issue[0].details.text).to.include('id trong body không khớp với resource tìm thấy'));")]),
], ["conditional"])

# ================================================================= S-05 conditional-create-negative
cc = [
    row("TC-CC-01", "Positive", "If-None-Exist không khớp resource nào → tạo mới", "201", cond="identifier=urn:trivita:qa-none|{{case_id}}-{{$timestamp}}"),
    row("TC-CC-02", "Negative", "If-None-Exist chỉ có _count (không tiêu chí)", "400", "invalid", "Thao tác có điều kiện cần tiêu chí search", cond="_count=1"),
    row("TC-CC-03", "Negative", "If-None-Exist tham số lạ", "400", "invalid", "tham số search không hợp lệ: zzz", cond="zzz=1"),
    row("TC-CC-04", "Negative", "If-None-Exist mã hóa hỏng", "400", "invalid", "If-None-Exist không hợp lệ", cond="a=%zz"),
    row("TC-CC-05", "Security", "If-None-Exist chứa SQLi → không lỗi 5xx", "201", cond="identifier=x'%20OR%201=1--"),
]
write_json("conditional-create-negative", cc)
scenario("conditional-create-negative", "POST If-None-Exist: điều kiện sai", 1, "Header If-None-Exist thiếu tiêu chí, tham số lạ, mã hóa hỏng.", [
    token(),
    step("POST Patient + If-None-Exist theo data", "create", "POST", f"{FHIR}/Patient", headers={"If-None-Exist": "{{cond}}"}, body=patient("CCN-{{$timestamp}}"),
         asserts=["status == {{expected_status}}"], post=[js(ERR_JS)]),
], ["conditional", "negative"])

# ================================================================= S-06 conditional-update-negative
cu = [
    row("TC-CU-01", "Negative", "PUT không query", "400", "invalid", "Thao tác có điều kiện cần tiêu chí search", cond="", put_body='{"resourceType":"Patient"}'),
    row("TC-CU-02", "Negative", "PUT chỉ _count", "400", "invalid", "Thao tác có điều kiện cần tiêu chí search", cond="_count=1", put_body='{"resourceType":"Patient"}'),
    row("TC-CU-03", "Negative", "PUT tham số lạ", "400", "invalid", "tham số search không hợp lệ: zzz", cond="zzz=1", put_body='{"resourceType":"Patient"}'),
    row("TC-CU-04", "Negative", "0 khớp nhưng thân có id", "400", "invalid", "PUT có điều kiện không khớp resource nào thì không được kèm id",
        cond="identifier=urn:trivita:qa-none|{{case_id}}-{{$timestamp}}", put_body='{"resourceType":"Patient","id":"abc"}'),
    row("TC-CU-05", "Negative", "thân hỏng", "400", "invalid", "JSON không hợp lệ", cond="identifier=urn:trivita:qa-none|x", put_body="{"),
    row("TC-CU-06", "Positive", "0 khớp, thân không id → tạo mới", "201", cond="identifier=urn:trivita:qa-none|{{case_id}}-{{$timestamp}}",
        put_body='{"resourceType":"Patient","identifier":[{"system":"urn:trivita:qa-none","value":"cu-06"}]}'),
]
write_json("conditional-update-negative", cu)
scenario("conditional-update-negative", "PUT /{resourceType}?query: điều kiện sai", 1, "PUT có điều kiện: thiếu tiêu chí, tham số lạ, id sai, thân hỏng.", [
    token(),
    step("PUT /Patient?{cond} theo data", "cond_put", "PUT", f"{FHIR}/Patient?{{{{cond}}}}", body="{{put_body}}",
         asserts=["status == {{expected_status}}"], post=[js(ERR_JS)]),
], ["conditional", "negative"])

# ================================================================= S-07 update-rules
scenario("update-rules", "PUT/GET: If-Match, If-None-Match, Prefer, id lệch", 1,
         "Quy tắc phiên bản của PUT và đọc có điều kiện; Prefer return=minimal/OperationOutcome.", [
    token(),
    create_patient("POST Patient", "UR-{{$timestamp}}", gender="male"),
    step("(setup) đọc v1", "read", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}", asserts=["status == 200"], post=[ext("v1", "$.meta.versionId")]),
    step("PUT If-Match sai → 412", "update", "PUT", f"{FHIR}/Patient/{{{{patient_id}}}}", headers={"If-Match": 'W/"khong-co"'},
         body=patient("UR", pid="{{patient_id}}"), asserts=["status == 412", "$.issue[0].code == conflict"]),
    step("PUT If-Match đúng (W/\"v1\") → 200", "update", "PUT", f"{FHIR}/Patient/{{{{patient_id}}}}", headers={"If-Match": 'W/"{{v1}}"'},
         body=patient("UR", gender="female", pid="{{patient_id}}"), asserts=["status == 200", "$.gender == female"], post=[ext("v2", "$.meta.versionId")]),
    step("PUT If-Match v1 (đã cũ) → 412 nêu phiên bản hiện tại", "update", "PUT", f"{FHIR}/Patient/{{{{patient_id}}}}", headers={"If-Match": "{{v1}}"},
         body=patient("UR", pid="{{patient_id}}"), asserts=["status == 412"],
         post=[js("pm.test('nêu versionId hiện tại', () => pm.expect(pm.response.json().issue[0].details.text).to.include(pm.variables.get('v2')));")]),
    step("PUT If-Match * → 412", "update", "PUT", f"{FHIR}/Patient/{{{{patient_id}}}}", headers={"If-Match": "*"},
         body=patient("UR", pid="{{patient_id}}"), asserts=["status == 412"]),
    step("PUT id thân lệch đường dẫn → 400", "update", "PUT", f"{FHIR}/Patient/{{{{patient_id}}}}", body=patient("UR", pid=ZERO_UUID),
         asserts=["status == 400"], post=[js("pm.test('thông điệp', () => pm.expect(pm.response.json().issue[0].details.text).to.include('id trong body không khớp với đường dẫn'));")]),
    step("PUT id chưa tồn tại → 404 (update-as-create tắt)", "update", "PUT", f"{FHIR}/Patient/{ZERO_UUID}", body=patient("UR", pid=ZERO_UUID),
         asserts=["status == 404", "$.issue[0].code == not-found"]),
    step("GET If-None-Match khớp → 304", "read", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}", headers={"If-None-Match": 'W/"{{v2}}"'}, asserts=["status == 304"]),
    step("GET If-None-Match khác → 200", "read", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}", headers={"If-None-Match": 'W/"khac"'}, asserts=["status == 200"]),
    step("PUT y hệt → 200 và ETag mới (vẫn sinh phiên bản)", "update", "PUT", f"{FHIR}/Patient/{{{{patient_id}}}}",
         body=patient("UR", gender="female", pid="{{patient_id}}"), asserts=["status == 200"],
         post=[js("pm.test('versionId mới', () => pm.expect(pm.response.json().meta.versionId).to.not.eql(pm.variables.get('v2')));")]),
    step("PUT Prefer return=minimal → 200 thân rỗng", "update", "PUT", f"{FHIR}/Patient/{{{{patient_id}}}}", headers={"Prefer": "return=minimal"},
         body=patient("UR", pid="{{patient_id}}"), asserts=["status == 200"],
         post=[js("pm.test('thân rỗng', () => pm.expect(pm.response.text()).to.eql('')); " + HEADER_HAS_JS % {"h": "ETag", "v": 'W/"'})]),
    step("PUT Prefer return=OperationOutcome → thân OperationOutcome", "update", "PUT", f"{FHIR}/Patient/{{{{patient_id}}}}",
         headers={"Prefer": "return=OperationOutcome"}, body=patient("UR", pid="{{patient_id}}"),
         asserts=["status == 200", "$.resourceType == OperationOutcome"]),
], ["update"])
write_csv("update-rules", ["case_id", "case_group", "description", "expected_status"],
          [row("TC-UR-01", "Positive", "chuỗi quy tắc phiên bản/điều kiện/Prefer của PUT", "200")])

# ================================================================= S-08 patch-negative
J = json.dumps
pn = [
    row("TC-PN-01", "Positive", "replace gender", "200", patch_ct=JSON_PATCH, patch_body=J([{"op": "replace", "path": "/gender", "value": "female"}])),
    row("TC-PN-02", "Positive", "add birthDate", "200", patch_ct=JSON_PATCH, patch_body=J([{"op": "add", "path": "/birthDate", "value": "1985-05-05"}])),
    row("TC-PN-03", "Positive", "remove gender", "200", patch_ct=JSON_PATCH, patch_body=J([{"op": "remove", "path": "/gender"}])),
    row("TC-PN-04", "Boundary", "mảng rỗng vẫn sinh phiên bản mới", "200", patch_ct=JSON_PATCH, patch_body="[]"),
    row("TC-PN-05", "Positive", "test thành công rồi replace", "200", patch_ct=JSON_PATCH,
        patch_body=J([{"op": "test", "path": "/gender", "value": "male"}, {"op": "replace", "path": "/gender", "value": "other"}])),
    row("TC-PN-06", "Negative", "Content-Type application/json (không phải json-patch)", "415", "not-supported", "PATCH cần Content-Type application/json-patch+json",
        patch_ct="application/json", patch_body=J([{"op": "replace", "path": "/gender", "value": "female"}])),
    row("TC-PN-07", "Negative", "FHIRPath Patch (fhir+json) chưa hỗ trợ", "415", "not-supported", "PATCH cần Content-Type application/json-patch+json",
        patch_ct=FHIR_JSON, patch_body='{"resourceType":"Parameters","parameter":[]}'),
    row("TC-PN-08", "Negative", "thân là object, không phải mảng", "400", "invalid", "Body của PATCH phải là một mảng JSON Patch",
        patch_ct=JSON_PATCH, patch_body='{"op":"replace"}'),
    row("TC-PN-09", "Negative", "op không tồn tại", "400", "invalid", "JSON Patch không áp dụng được", patch_ct=JSON_PATCH, patch_body=J([{"op": "bogus", "path": "/gender"}])),
    row("TC-PN-10", "Negative", "test thất bại", "400", "invalid", "JSON Patch không áp dụng được",
        patch_ct=JSON_PATCH, patch_body=J([{"op": "test", "path": "/gender", "value": "zzz"}])),
    row("TC-PN-11", "Negative", "đổi resourceType", "400", "invalid", "Không được đổi resourceType bằng PATCH",
        patch_ct=JSON_PATCH, patch_body=J([{"op": "replace", "path": "/resourceType", "value": "Observation"}])),
    row("TC-PN-12", "Negative", "đổi id", "400", "invalid", "Không được đổi id bằng PATCH", patch_ct=JSON_PATCH, patch_body=J([{"op": "replace", "path": "/id", "value": "x"}])),
    row("TC-PN-13", "Negative", "kết quả không hợp lệ (thêm thuộc tính lạ)", "400", "", "", patch_ct=JSON_PATCH, patch_body=J([{"op": "add", "path": "/foo", "value": 1}])),
    row("TC-PN-14", "Negative", "xóa gốc tài liệu", "400", "invalid", "JSON Patch không áp dụng được", patch_ct=JSON_PATCH, patch_body=J([{"op": "remove", "path": ""}])),
    row("TC-PN-15", "Security", "sửa meta.versionId: giá trị gửi bị bỏ, server cấp mới", "200", patch_ct=JSON_PATCH,
        patch_body=J([{"op": "add", "path": "/meta/versionId", "value": "gia-mao"}]), forbidden_version="gia-mao"),
]
for r in pn:
    r.setdefault("forbidden_version", "")
write_json("patch-negative", pn)
scenario("patch-negative", "PATCH Patient: JSON Patch hợp lệ và lỗi", 1, "Mỗi dòng: tạo Patient mới rồi PATCH theo data.", [
    token(),
    create_patient("(setup) POST Patient", "PN-{{$timestamp}}", gender="male"),
    step("PATCH Patient/{id} theo data", "patch", "PATCH", f"{FHIR}/Patient/{{{{patient_id}}}}", headers={"Content-Type": "{{patch_ct}}"}, body="{{patch_body}}",
         asserts=["status == {{expected_status}}"],
         post=[js(ERR_JS), js("const f = pm.iterationData.get('forbidden_version'); if (f) { pm.test('versionId do server cấp', () => pm.expect(pm.response.json().meta.versionId).to.not.eql(f)); }")]),
], ["patch", "negative"])

# ================================================================= S-09 search
SE = [
    ("Positive", "chỉ lọc family: 3 kết quả, không next", "", "200", dict(expected_entries="3", expected_next="false")),
    ("Positive", "_count=2: trang đầu có next", "&_count=2", "200", dict(expected_entries="2", expected_next="true", expected_prev="false")),
    ("Positive", "_count=2&_offset=2: trang cuối có previous", "&_count=2&_offset=2", "200", dict(expected_entries="1", expected_next="false", expected_prev="true")),
    ("Positive", "_summary=count chỉ trả total", "&_summary=count", "200", dict(expected_entries="0", expected_total="3")),
    ("Positive", "_total=accurate", "&_total=accurate", "200", dict(expected_entries="3", expected_total="3")),
    ("Positive", "_total=estimate (đếm chính xác như accurate)", "&_total=estimate", "200", dict(expected_total="3")),
    ("Positive", "gender=female", "&gender=female", "200", dict(expected_entries="2")),
    ("Positive", "gender:not=female", "&gender:not=female", "200", dict(expected_entries="1")),
    ("Positive", "gender=male,female (OR)", "&gender=male,female", "200", dict(expected_entries="3")),
    ("Positive", "birthdate=ge1990-01-01", "&birthdate=ge1990-01-01", "200", dict(expected_entries="2")),
    ("Positive", "birthdate=lt1990-01-01", "&birthdate=lt1990-01-01", "200", dict(expected_entries="1")),
    ("Positive", "_sort=birthdate tăng dần", "&_sort=birthdate&_count=1", "200", dict(expected_entries="1", expected_first_birthdate="1980-01-01")),
    ("Positive", "_sort=-birthdate giảm dần", "&_sort=-birthdate&_count=1", "200", dict(expected_entries="1", expected_first_birthdate="2000-12-31")),
    ("Positive", "_elements=gender", "&_elements=gender", "200", dict(expected_entries="3")),
    ("Positive", "_lastUpdated=gt2000-01-01", "&_lastUpdated=gt2000-01-01", "200", dict(expected_entries="3")),
    ("Positive", "_id không tồn tại", "&_id=khong-co", "200", dict(expected_entries="0")),
    ("Boundary", "_count=0: không entry, không next", "&_count=0", "200", dict(expected_entries="0", expected_next="false")),
    ("Boundary", "_count=1: một entry, có next", "&_count=1", "200", dict(expected_entries="1", expected_next="true")),
    ("Boundary", "_count=1000 (trần)", "&_count=1000", "200", dict(expected_entries="3")),
    ("Boundary", "_count=1001 bị cắt im lặng về 1000", "&_count=1001", "200", dict(expected_entries="3")),
    ("Boundary", "_offset bằng tổng", "&_offset=3", "200", dict(expected_entries="0", expected_prev="true")),
    ("Boundary", "_offset rất lớn", "&_offset=1000000", "200", dict(expected_entries="0")),
    ("Negative", "_count không phải số", "&_count=abc", "400", dict(code="invalid", msg='_count không hợp lệ: "abc"')),
    ("Negative", "_count âm", "&_count=-1", "400", dict(code="invalid", msg="_count không hợp lệ")),
    ("Negative", "_offset âm", "&_offset=-1", "400", dict(code="invalid", msg='_offset không hợp lệ: "-1"')),
    ("Negative", "_total sai", "&_total=x", "400", dict(code="invalid", msg="_total không hợp lệ")),
    ("Negative", "tham số lạ", "&zzz=1", "400", dict(code="invalid", msg="tham số search không hợp lệ: zzz")),
    ("Negative", "_sort tham số lạ", "&_sort=zzz", "400", dict(code="invalid", msg="tham số sắp xếp không hợp lệ: zzz")),
    ("Negative", "_query chưa hỗ trợ", "&_query=x", "400", dict(code="invalid", msg="tham số _query chưa được hỗ trợ")),
    ("Negative", "birthdate sai cú pháp", "&birthdate=xyz", "400", dict(code="invalid", msg='ngày không hợp lệ: "xyz"')),
    ("Negative", "modifier lạ", "&gender:foo=a", "400", dict(code="invalid", msg="modifier không hợp lệ: foo")),
    ("Negative", "_include sai dạng", "&_include=x", "400", dict(code="invalid", msg="giá trị _include không hợp lệ")),
    ("Negative", "_has thiếu phần", "&_has:Observation=1", "400", dict(code="invalid", msg="_has không hợp lệ")),
    ("Negative", "_compartment nhiều giá trị", "&_compartment=Patient/a,Patient/b", "400", dict(code="invalid", msg="_compartment phải có dạng")),
    ("Security", "SQLi trong giá trị gender: không lỗi, không kết quả", "&gender=%27%20OR%201%3D1--", "200", dict(expected_entries="0")),
    ("Security", "SQLi trong _sort bị từ chối", "&_sort=gender%3BDROP%20TABLE%20x", "400", dict(code="invalid", msg="tham số sắp xếp không hợp lệ")),
    ("Security", "SQLi trong _count bị từ chối", "&_count=1%3BSELECT%201", "400", dict(code="invalid", msg="_count không hợp lệ")),
    ("Security", "XSS trong family AND: không kết quả", "&family:contains=%3Cscript%3E", "200", dict(expected_entries="0")),
]
se_rows = []
for i, (grp, desc, q, st, extra) in enumerate(SE, start=1):
    code, msg = extra.pop("code", ""), extra.pop("msg", "")
    r = row(f"TC-SE-{i:02d}", grp, desc, st, code, msg, query=q)
    for k in ("expected_entries", "expected_total", "expected_next", "expected_prev", "expected_first_birthdate"):
        r[k] = extra.get(k, "")
    se_rows.append(r)
write_json("search", se_rows)
scenario("search", "Tìm kiếm Patient: lọc, sắp xếp, phân trang, lỗi", 1,
         "Mỗi dòng: tạo 3 Patient cùng family duy nhất (nam 1980-01-01, nữ 1990-06-15, nữ 2000-12-31) rồi GET /Patient?family=…{query}.", [
    token(),
    create_patient("(setup) Patient 1 (male, 1980-01-01)", "Qa-{{case_id}}-{{$timestamp}}", extract_family="marker", gender="male", birth="1980-01-01"),
    create_patient("(setup) Patient 2 (female, 1990-06-15)", "{{marker}}", var_id="p2_id", gender="female", birth="1990-06-15"),
    create_patient("(setup) Patient 3 (female, 2000-12-31)", "{{marker}}", var_id="p3_id", gender="female", birth="2000-12-31"),
    step("GET /Patient?family={marker}{query}", "search", "GET", f"{FHIR}/Patient?family={{{{marker}}}}{{{{query}}}}",
         asserts=["status == {{expected_status}}"], post=[js(ERR_JS), js(SEARCH_JS)], custom=True),
], ["search"])

# ================================================================= S-10 search-post
sp = [
    row("TC-SP-01", "Positive", "form hợp lệ, _count=2", "200", extra_name="_format", extra_value="json", count="2", expected_entries="2"),
    row("TC-SP-02", "Positive", "_count=0 không entry", "200", extra_name="_format", extra_value="json", count="0", expected_entries="0"),
    row("TC-SP-03", "Positive", "_count=100 trả đủ 2", "200", extra_name="_format", extra_value="json", count="100", expected_entries="2"),
    row("TC-SP-04", "Negative", "_count không phải số", "400", "invalid", "_count không hợp lệ", extra_name="_format", extra_value="json", count="abc", expected_entries=""),
    row("TC-SP-05", "Negative", "tham số lạ trong form", "400", "invalid", "tham số search không hợp lệ: zzz", extra_name="zzz", extra_value="1", count="2", expected_entries=""),
    row("TC-SP-06", "Negative", "_total sai trong form", "400", "invalid", "_total không hợp lệ", extra_name="_total", extra_value="x", count="2", expected_entries=""),
]
write_json("search-post", sp)
scenario("search-post", "POST /{resourceType}/_search (form)", 1, "Tìm kiếm dạng form; mỗi dòng tạo 2 Patient cùng family.", [
    token(),
    create_patient("(setup) Patient 1", "Qa-{{case_id}}-{{$timestamp}}", extract_family="marker", gender="male"),
    create_patient("(setup) Patient 2", "{{marker}}", var_id="p2_id", gender="female"),
    step("POST Patient/_search (form)", "search_post", "POST", f"{FHIR}/Patient/_search",
         form={"family": "{{marker}}", "_count": "{{count}}", "{{extra_name}}": "{{extra_value}}"},
         asserts=["status == {{expected_status}}"], post=[js(ERR_JS), js(SEARCH_JS)]),
], ["search", "negative"])

# ================================================================= S-11 batch-flow
BP = "{{$timestamp}}"
batch_body = {"resourceType": "Bundle", "type": "batch", "entry": [
    {"request": {"method": "POST", "url": "Patient"}, "resource": json.loads(patient("Batch-" + BP, gender="male"))},
    {"request": {"method": "GET", "url": "Patient?family=khong-co-" + BP}},
    {"request": {"method": "GET", "url": "Patient/" + ZERO_UUID}},
]}
txn_body = {"resourceType": "Bundle", "type": "transaction", "entry": [
    {"fullUrl": "urn:uuid:11111111-1111-4111-8111-111111111111", "request": {"method": "POST", "url": "Patient"},
     "resource": json.loads(patient("Txn-" + BP, gender="female"))},
    {"fullUrl": "urn:uuid:22222222-2222-4222-8222-222222222222", "request": {"method": "POST", "url": "Observation"},
     "resource": {"resourceType": "Observation", "status": "final", "code": {"text": "qa"},
                  "subject": {"reference": "urn:uuid:11111111-1111-4111-8111-111111111111"}}},
]}
txn_fail = {"resourceType": "Bundle", "type": "transaction", "entry": [
    {"request": {"method": "POST", "url": "Patient", "ifNoneExist": "identifier=" + IDENT_SYS + "|tx-{{case_id}}-" + BP},
     "resource": json.loads(patient("TxnFail-" + BP, ident="tx-{{case_id}}-" + BP))},
    {"request": {"method": "POST", "url": "Observation"}, "resource": {"resourceType": "Observation"}},
]}
write_json("batch-transaction-flow", [row("TC-BF-01", "Positive", "batch (lỗi từng entry), transaction urn:uuid, rollback khi 1 entry lỗi", "200")])
scenario("batch-transaction-flow", "Batch và transaction (luồng)", 1,
         "batch: entry lỗi không rollback; transaction: urn:uuid được viết lại, entry lỗi rollback cả giao dịch.", [
    token(),
    step("POST / batch (3 entry)", "batch", "POST", FHIR, body=batch_body, headers={"Content-Type": FHIR_JSON},
         asserts=["status == 200", "$.resourceType == Bundle", "$.type == batch-response", "$.entry[0].response.status == 201 Created"],
         post=[js("const e = pm.response.json().entry; pm.test('entry[1] search 200', () => pm.expect(e[1].response.status).to.include('200')); "
                  "pm.test('entry[2] 404 (lỗi riêng entry, Bundle vẫn 200)', () => pm.expect(e[2].response.status).to.include('404'));")]),
    step("POST / transaction (urn:uuid)", "batch", "POST", FHIR, body=txn_body, headers={"Content-Type": FHIR_JSON},
         asserts=["status == 200", "$.type == transaction-response", "$.entry[0].response.status == 201 Created", "$.entry[1].response.status == 201 Created"],
         post=[ext("obs_id", "$.entry[1].resource.id")]),
    step("GET Observation: subject đã được viết lại thành Patient/{id}", "read", "GET", f"{FHIR}/Observation/{{{{obs_id}}}}",
         asserts=["status == 200"],
         post=[js("pm.test('subject không còn urn:uuid', () => pm.expect(pm.response.json().subject.reference).to.match(/^Patient\\//));")]),
    step("POST / transaction có entry lỗi → lỗi, rollback", "batch", "POST", FHIR, body=txn_fail, headers={"Content-Type": FHIR_JSON},
         asserts=["status == 400", "$.resourceType == OperationOutcome"]),
    step("GET Patient?identifier (rollback: không có)", "search", "GET", f"{FHIR}/Patient?identifier={IDENT_SYS}|tx-{{{{case_id}}}}-{BP}&_total=accurate",
         asserts=["status == 200", "$.total == 0"], custom=True),
], ["batch"])

# ================================================================= S-12 bundle-negative
def bundle(**kw):
    return json.dumps(kw, ensure_ascii=False)


bn_default = dict(content_type=FHIR_JSON, prefer="handling=lenient", expect_empty_body="")
bn = [
    row("TC-BN-01", "Negative", "thân là Patient, không phải Bundle", "400", "invalid", "Body phải là Bundle", body='{"resourceType":"Patient"}'),
    row("TC-BN-02", "Negative", "thân là mảng", "400", "invalid", "Body phải là Bundle", body="[1]"),
    row("TC-BN-03", "Negative", "Bundle.type collection", "400", "invalid", "Bundle.type phải là batch hoặc transaction", body='{"resourceType":"Bundle","type":"collection"}'),
    row("TC-BN-04", "Negative", "entry không phải object", "400", "invalid", "entry[0] không hợp lệ", body='{"resourceType":"Bundle","type":"batch","entry":[1]}'),
    row("TC-BN-05", "Negative", "entry thiếu request", "400", "invalid", "entry[0] thiếu request", body='{"resourceType":"Bundle","type":"batch","entry":[{}]}'),
    row("TC-BN-06", "Negative", "request thiếu url", "400", "invalid", "entry[0].request cần method và url",
        body='{"resourceType":"Bundle","type":"batch","entry":[{"request":{"method":"GET"}}]}'),
    row("TC-BN-07", "Negative", "JSON hỏng", "400", "invalid", "JSON không hợp lệ", body="{"),
    row("TC-BN-08", "Boundary", "batch rỗng", "200", body='{"resourceType":"Bundle","type":"batch"}'),
    row("TC-BN-09", "Boundary", "transaction rỗng", "200", body='{"resourceType":"Bundle","type":"transaction"}'),
    row("TC-BN-10", "Negative", "transaction với method HEAD", "400", "invalid", "method không hỗ trợ trong Bundle: HEAD",
        body='{"resourceType":"Bundle","type":"transaction","entry":[{"request":{"method":"HEAD","url":"Patient/x"}}]}'),
    row("TC-BN-11", "Negative", "transaction: GET id không có → cả Bundle 404", "404", "not-found", "",
        body='{"resourceType":"Bundle","type":"transaction","entry":[{"request":{"method":"GET","url":"Patient/%s"}}]}' % ZERO_UUID),
    row("TC-BN-12", "Boundary", "Prefer return=minimal → thân rỗng", "200", body='{"resourceType":"Bundle","type":"batch"}',
        prefer="return=minimal", expect_empty_body="true"),
    row("TC-BN-13", "Negative", "Content-Type fhir+xml", "415", "not-supported", "Content-Type không hỗ trợ: application/fhir+xml",
        body='{"resourceType":"Bundle","type":"batch"}', content_type="application/fhir+xml"),
    row("TC-BN-14", "Security", "transaction: PATCH entry không thực thi được (thân phải là mảng)", "400", "", "",
        body='{"resourceType":"Bundle","type":"transaction","entry":[{"request":{"method":"PATCH","url":"Patient/%s"},"resource":{"resourceType":"Patient"}}]}' % ZERO_UUID),
]
for r in bn:
    for k, v in bn_default.items():
        r.setdefault(k, v)
write_json("bundle-negative", bn)
scenario("bundle-negative", "POST / (batch/transaction): Bundle sai", 1, "Bundle không hợp lệ, rỗng, method lạ, Prefer, Content-Type.", [
    token(),
    step("POST / theo data", "batch", "POST", FHIR, headers={"Content-Type": "{{content_type}}", "Prefer": "{{prefer}}"}, body="{{body}}",
         asserts=["status == {{expected_status}}"],
         post=[js(ERR_JS), js("if (pm.iterationData.get('expect_empty_body') === 'true') { pm.test('thân rỗng', () => pm.expect(pm.response.text()).to.eql('')); }")]),
], ["batch", "negative"])

# ================================================================= S-13 validate-operation
va = [
    row("TC-VA-01", "Positive", "Patient hợp lệ (cảnh báo thiếu narrative vẫn 200)", "200", resource_type="Patient", body='{"resourceType":"Patient","gender":"male"}'),
    row("TC-VA-02", "Positive", "Parameters bọc resource", "200", resource_type="Patient",
        body='{"resourceType":"Parameters","parameter":[{"name":"resource","resource":{"resourceType":"Patient"}}]}'),
    row("TC-VA-03", "Negative", "thuộc tính lạ", "400", "structure", 'Thuộc tính không hợp lệ "bogus"', resource_type="Patient", body='{"resourceType":"Patient","bogus":1}'),
    row("TC-VA-04", "Negative", "thiếu bắt buộc của Observation", "400", "required", "Thiếu thuộc tính bắt buộc", resource_type="Observation", body='{"resourceType":"Observation"}'),
    row("TC-VA-05", "Negative", "thân rỗng", "400", "invalid", "Thiếu resource cần kiểm tra", resource_type="Patient", body=""),
    row("TC-VA-06", "Negative", "resourceType lệch", "400", "invalid", 'resourceType không khớp: đường dẫn là Patient, resource là "Observation"',
        resource_type="Patient", body='{"resourceType":"Observation"}'),
    row("TC-VA-07", "Negative", "thân là mảng", "400", "invalid", "Body phải là một resource hoặc Parameters", resource_type="Patient", body="[]"),
    row("TC-VA-08", "Negative", "Parameters thiếu name", "400", "invalid", "Parameters.parameter[0] thiếu name", resource_type="Patient",
        body='{"resourceType":"Parameters","parameter":[{}]}'),
    row("TC-VA-09", "Negative", "JSON hỏng", "400", "invalid", "JSON không hợp lệ", resource_type="Patient", body="{"),
    row("TC-VA-10", "Negative", "Content-Type text/plain", "415", "not-supported", "Content-Type không hỗ trợ: text/plain", resource_type="Patient",
        body='{"resourceType":"Patient"}', content_type="text/plain"),
    row("TC-VA-11", "Negative", "loại resource lạ", "404", "not-found", "Không có route cho POST Foo/$validate", resource_type="Foo", body='{"resourceType":"Foo"}'),
]
for r in va:
    r.setdefault("content_type", FHIR_JSON)
write_json("validate-operation", va)
scenario("validate-operation", "POST /{resourceType}/$validate", 1, "$validate: hợp lệ, cảnh báo, lỗi cấu trúc, thân sai, Content-Type, loại lạ. Không ghi dữ liệu.", [
    token(),
    step("POST /{resourceType}/$validate theo data", "validate", "POST", f"{FHIR}/{{{{resource_type}}}}/$validate",
         headers={"Content-Type": "{{content_type}}"}, body="{{body}}", asserts=["status == {{expected_status}}"],
         post=[js(ERR_JS), js("if (pm.iterationData.get('expected_status') === '200') { pm.test('OperationOutcome', () => pm.expect(pm.response.json().resourceType).to.eql('OperationOutcome')); }")]),
], ["operation", "validation"])

# ================================================================= S-14 everything
ev = [
    row("TC-EV-01", "Positive", "đủ hồ sơ: Patient + Observation + Condition", "200", ev_count="100", ev_type="Patient,Observation,Condition", ev_since="1970-01-01T00:00:00Z", expected_entries="3", expected_total="3"),
    row("TC-EV-02", "Positive", "_type=Observation chỉ trả Observation", "200", ev_count="100", ev_type="Observation", ev_since="1970-01-01T00:00:00Z", expected_entries="1", expected_total="1"),
    row("TC-EV-03", "Boundary", "_count=1 bị cắt: 1 match + entry outcome too-costly", "200", ev_count="1", ev_type="Patient,Observation,Condition", ev_since="1970-01-01T00:00:00Z", expected_entries="2", expected_total="1"),
    row("TC-EV-04", "Boundary", "_since ở tương lai: không entry", "200", ev_count="100", ev_type="Patient,Observation,Condition", ev_since="2099-01-01T00:00:00Z", expected_entries="0", expected_total="0"),
    row("TC-EV-05", "Boundary", "_count=10000 (trần)", "200", ev_count="10000", ev_type="Patient,Observation,Condition", ev_since="1970-01-01T00:00:00Z", expected_entries="3", expected_total="3"),
    row("TC-EV-06", "Negative", "_count=0", "400", "invalid", "_count phải trong khoảng 1 đến 10000", ev_count="0", ev_type="Patient", ev_since="1970-01-01T00:00:00Z"),
    row("TC-EV-07", "Negative", "_count=10001", "400", "invalid", "_count phải trong khoảng 1 đến 10000", ev_count="10001", ev_type="Patient", ev_since="1970-01-01T00:00:00Z"),
    row("TC-EV-08", "Negative", "_count không phải số", "400", "invalid", "Tham số _count phải là số nguyên", ev_count="x", ev_type="Patient", ev_since="1970-01-01T00:00:00Z"),
    row("TC-EV-09", "Negative", "_since chỉ có ngày", "400", "invalid", "_since phải là thời điểm RFC 3339", ev_count="100", ev_type="Patient", ev_since="2026-01-01"),
    row("TC-EV-10", "Negative", "_type lạ", "400", "invalid", "_type không hợp lệ: Foo", ev_count="100", ev_type="Foo", ev_since="1970-01-01T00:00:00Z"),
]
for r in ev:
    for k in ("expected_entries", "expected_total"):
        r.setdefault(k, "")
write_json("everything", ev)
scenario("everything", "Patient/{id}/$everything", 1,
         "Mỗi dòng: Patient + Observation + Condition rồi $everything theo data; cuối: xóa Patient → 410, id lạ → 404.", [
    token(),
    create_patient("(setup) POST Patient", "Ev-{{case_id}}-{{$timestamp}}"),
    step("(setup) POST Observation", "create", "POST", f"{FHIR}/Observation",
         body={"resourceType": "Observation", "status": "final", "code": {"text": "qa"}, "subject": {"reference": "Patient/{{patient_id}}"}}, asserts=["status == 201"]),
    step("(setup) POST Condition", "create", "POST", f"{FHIR}/Condition",
         body={"resourceType": "Condition", "subject": {"reference": "Patient/{{patient_id}}"}}, asserts=["status == 201"]),
    step("GET Patient/{id}/$everything theo data", "everything", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}/$everything",
         query={"_count": "{{ev_count}}", "_type": "{{ev_type}}", "_since": "{{ev_since}}"}, asserts=["status == {{expected_status}}"],
         post=[js(ERR_JS), js(SEARCH_JS)]),
    step("DELETE Patient (xóa mềm)", "delete", "DELETE", f"{FHIR}/Patient/{{{{patient_id}}}}", asserts=["status == 204"]),
    step("$everything của Patient đã xóa → 410", "everything", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}/$everything",
         query={"_count": "10", "_type": "Patient", "_since": "1970-01-01T00:00:00Z"}, asserts=["status == 410", "$.issue[0].code == deleted"]),
    step("$everything của id lạ → 404", "everything", "GET", f"{FHIR}/Patient/{ZERO_UUID}/$everything",
         query={"_count": "10", "_type": "Patient", "_since": "1970-01-01T00:00:00Z"}, asserts=["status == 404", "$.issue[0].code == not-found"]),
], ["operation"])

# ================================================================= S-15 bulk-export-flow
write_json("bulk-export-flow", [row("TC-BX-01", "Positive", "$export async → poll → manifest → tải ndjson → DELETE job", "202")])
poll = lambda i: step(f"GET bulkdata/{{id}}/status (lần {i})", "bulk_get", "GET", f"{FHIR}/bulkdata/{{{{job_id}}}}/status",
                      asserts=[], post=[js("pm.test('202 hoặc 200', () => pm.expect([200, 202]).to.include(pm.response.code));"), js(SLEEP_JS % 3000)], custom=True)
scenario("bulk-export-flow", "Bulk export $export (async) đầu-cuối", 1,
         "Cần fhir-worker chạy: 202 + Content-Location → poll status → manifest 200 → tải ndjson từ Binary → DELETE → 404/410.", [
    token(),
    create_patient("(setup) POST Patient (đảm bảo có dữ liệu)", "Bx-{{$timestamp}}"),
    step("GET $export (Prefer: respond-async)", "export", "GET", f"{FHIR}/$export", query={"_type": "Patient", "_outputFormat": "ndjson"},
         headers={"Prefer": "respond-async"}, asserts=["status == 202", "$.resourceType == OperationOutcome"], post=[js(VAR_SET_FROM_HEADER_JS)]),
    poll(1), poll(2), poll(3),
    step("GET bulkdata/{id}/status → 200 manifest", "bulk_get", "GET", f"{FHIR}/bulkdata/{{{{job_id}}}}/status",
         asserts=["status == 200", "$.requiresAccessToken == true", "$.output is array"],
         post=[js("const u = (pm.response.json().output || [])[0]; pm.test('có file output', () => pm.expect(u).to.not.eql(undefined)); "
                  "if (u) { pm.variables.set('bin_id', String(u.url).split('/').pop()); }")]),
    step("GET Binary/{id} (ndjson)", "read", "GET", f"{FHIR}/Binary/{{{{bin_id}}}}", headers={"Accept": "application/fhir+ndjson"},
         asserts=["status == 200"], post=[js("pm.test('ndjson chứa Patient', () => pm.expect(pm.response.text()).to.include('\"resourceType\":\"Patient\"'));")]),
    step("DELETE bulkdata/{id}/status → 202", "bulk_del", "DELETE", f"{FHIR}/bulkdata/{{{{job_id}}}}/status", asserts=["status == 202"]),
    step("GET status sau xóa → 404/410", "bulk_get", "GET", f"{FHIR}/bulkdata/{{{{job_id}}}}/status", asserts=[],
         post=[js("pm.test('404 hoặc 410', () => pm.expect([404, 410]).to.include(pm.response.code));")], custom=True),
], ["bulk", "async"])

# ================================================================= S-16 bulk-export-negative
bx = [
    row("TC-BXN-01", "Negative", "thiếu Prefer respond-async", "400", "invalid", "Bulk export cần header Prefer: respond-async", prefer="handling=lenient", out_fmt="ndjson", etype="Patient", since="2020-01-01T00:00:00Z"),
    row("TC-BXN-02", "Negative", "_outputFormat=csv", "400", "invalid", "_outputFormat chỉ hỗ trợ ndjson, nhận csv", prefer="respond-async", out_fmt="csv", etype="Patient", since="2020-01-01T00:00:00Z"),
    row("TC-BXN-03", "Negative", "_type có loại lạ", "400", "invalid", "_type không hợp lệ: Foo", prefer="respond-async", out_fmt="ndjson", etype="Patient,Foo", since="2020-01-01T00:00:00Z"),
    row("TC-BXN-04", "Negative", "_since chỉ có ngày", "400", "invalid", "_since phải là instant RFC3339", prefer="respond-async", out_fmt="ndjson", etype="Patient", since="2026-01-01"),
    row("TC-BXN-05", "Security", "_type chứa SQLi", "400", "invalid", "_type không hợp lệ", prefer="respond-async", out_fmt="ndjson", etype="Patient%3BDROP%20TABLE%20x", since="2020-01-01T00:00:00Z"),
]
write_json("bulk-export-negative", bx)
scenario("bulk-export-negative", "$export: tham số sai", 1, "Mọi ca đều bị từ chối trước khi tạo job (không có tác dụng phụ).", [
    token(),
    step("GET $export theo data", "export", "GET", f"{FHIR}/$export", query={"_type": "{{etype}}", "_outputFormat": "{{out_fmt}}", "_since": "{{since}}"},
         headers={"Prefer": "{{prefer}}"}, asserts=["status == {{expected_status}}"], post=[js(ERR_JS)]),
], ["bulk", "negative"])

# ================================================================= S-17 status-unknown-id
su = [
    row("TC-ST-01", "Negative", "id dạng chuỗi lạ", "404", "not-found", "", job_id="khong-co"),
    row("TC-ST-02", "Negative", "UUID chưa có", "404", "not-found", "", job_id=ZERO_UUID),
    row("TC-ST-03", "Security", "dấu hiệu path traversal đã mã hóa", "404", "not-found", "", job_id="..%2F..%2Fetc%2Fpasswd"),
    row("TC-ST-04", "Security", "SQLi trong id", "404", "not-found", "", job_id="x'%20OR%201=1--"),
    row("TC-ST-05", "Boundary", "id rất dài (512 ký tự)", "404", "not-found", "", job_id="a" * 512),
]
write_json("status-unknown-id", su)
scenario("status-unknown-id", "bulkdata/job status: id không tồn tại", 2, "GET/DELETE bulkdata/{id}/status và GET job/{id}/status với id lạ đều 404.", [
    token(),
    step("GET bulkdata/{id}/status", "bulk_get", "GET", f"{FHIR}/bulkdata/{{{{job_id}}}}/status", asserts=["status == {{expected_status}}"], post=[js(ERR_JS)]),
    step("DELETE bulkdata/{id}/status", "bulk_del", "DELETE", f"{FHIR}/bulkdata/{{{{job_id}}}}/status", asserts=["status == {{expected_status}}"], post=[js(ERR_JS)]),
    step("GET job/{id}/status", "job", "GET", f"{FHIR}/job/{{{{job_id}}}}/status", asserts=["status == {{expected_status}}"], post=[js(ERR_JS)]),
], ["bulk", "negative"])

# ================================================================= S-18/19 terminology
def cs_body():
    return {"resourceType": "CodeSystem", "status": "active", "content": "complete", "url": "http://qa.trivita.test/cs/{{case_id}}-{{$timestamp}}",
            "concept": [{"code": "A", "display": "Alpha", "concept": [{"code": "A1", "display": "Alpha one"}]}, {"code": "B", "display": "Beta"}]}


setup_cs = step("(setup) POST CodeSystem (A > A1, B)", "create", "POST", f"{FHIR}/CodeSystem", body=cs_body(), asserts=["status == 201"],
                post=[ext("cs_url", "$.url"), ext("cs_id", "$.id")])
lk = [
    row("TC-LK-01", "Positive", "mã gốc A", "200", code_value="A", expected_display="Alpha"),
    row("TC-LK-02", "Positive", "mã con A1", "200", code_value="A1", expected_display="Alpha one"),
    row("TC-LK-03", "Positive", "mã B", "200", code_value="B", expected_display="Beta"),
    row("TC-LK-04", "Positive", "lọc property=child", "200", code_value="A", prop_name="property", prop_value="child", expected_display="Alpha"),
    row("TC-LK-05", "Negative", "thiếu code", "400", "invalid", "Thiếu tham số code (hoặc coding)", code_name="_c", code_value="A"),
    row("TC-LK-06", "Negative", "thiếu system", "400", "invalid", "Thiếu tham số system", sys_name="_s", code_value="A"),
    row("TC-LK-07", "Negative", "CodeSystem không tồn tại", "404", "not-found", "CodeSystem không tồn tại", code_value="A", sys_suffix="-khong-co"),
    row("TC-LK-08", "Negative", "mã không có trong CodeSystem", "404", "not-found", "Không có mã ZZ", code_value="ZZ"),
    row("TC-LK-09", "Security", "code chứa SQLi", "404", "not-found", "", code_value="' OR 1=1--"),
    row("TC-LK-10", "Boundary", "code dài 1000 ký tự", "404", "not-found", "", code_value="a" * 1000),
]
for r in lk:
    r.setdefault("sys_name", "system"); r.setdefault("code_name", "code"); r.setdefault("prop_name", "_p")
    r.setdefault("prop_value", "x"); r.setdefault("sys_suffix", ""); r.setdefault("expected_display", "")
write_json("terminology-lookup", lk)
scenario("terminology-lookup", "CodeSystem/$lookup", 1, "Mỗi dòng: tạo CodeSystem rồi $lookup theo data (tên tham số cũng là biến để bỏ tham số bằng tham số đệm _s/_c/_p).", [
    token(), setup_cs,
    step("GET CodeSystem/$lookup theo data", "lookup", "GET", f"{FHIR}/CodeSystem/$lookup",
         query={"{{sys_name}}": "{{cs_url}}{{sys_suffix}}", "{{code_name}}": "{{code_value}}", "{{prop_name}}": "{{prop_value}}"},
         asserts=["status == {{expected_status}}"],
         post=[js(ERR_JS), js("const d = pm.iterationData.get('expected_display'); if (d) { const p = pm.response.json().parameter.find(x => x.name === 'display'); "
                              "pm.test('display == ' + d, () => pm.expect(p && p.valueString).to.eql(d)); }")]),
], ["terminology"])

ex = [
    row("TC-EX-01", "Positive", "mở rộng toàn bộ", "200", expected_contains="3", expected_total="3"),
    row("TC-EX-02", "Positive", "count=2", "200", count_name="count", count_value="2", expected_contains="2", expected_total="3"),
    row("TC-EX-03", "Positive", "count=2&offset=2", "200", count_name="count", count_value="2", offset_name="offset", offset_value="2", expected_contains="1", expected_total="3"),
    row("TC-EX-04", "Boundary", "offset bằng tổng: rỗng", "200", offset_name="offset", offset_value="3", expected_contains="0", expected_total="3"),
    row("TC-EX-05", "Negative", "thiếu url", "400", "invalid", "Thiếu tham số url hoặc valueSet", url_name="_u"),
    row("TC-EX-06", "Negative", "count không phải số", "400", "invalid", "Tham số count phải là số nguyên", count_name="count", count_value="abc"),
    row("TC-EX-07", "Negative", "count âm", "400", "invalid", "count và offset phải >= 0", count_name="count", count_value="-1"),
    row("TC-EX-08", "Negative", "offset âm", "400", "invalid", "count và offset phải >= 0", offset_name="offset", offset_value="-1"),
    row("TC-EX-09", "Negative", "ValueSet không tồn tại", "404", "not-found", "ValueSet không tồn tại", url_suffix="-khong-co"),
    row("TC-EX-10", "Security", "url chứa SQLi", "404", "not-found", "", url_suffix="' OR 1=1--"),
]
for r in ex:
    r.setdefault("url_name", "url"); r.setdefault("url_suffix", ""); r.setdefault("count_name", "_c"); r.setdefault("count_value", "1")
    r.setdefault("offset_name", "_o"); r.setdefault("offset_value", "0"); r.setdefault("expected_contains", ""); r.setdefault("expected_total", "")
write_json("terminology-expand", ex)
scenario("terminology-expand", "ValueSet/$expand", 1, "Mỗi dòng: tạo CodeSystem + ValueSet include rồi $expand theo data.", [
    token(), setup_cs,
    step("(setup) POST ValueSet include CodeSystem", "create", "POST", f"{FHIR}/ValueSet",
         body={"resourceType": "ValueSet", "status": "active", "url": "http://qa.trivita.test/vs/{{case_id}}-{{$timestamp}}", "compose": {"include": [{"system": "{{cs_url}}"}]}},
         asserts=["status == 201"], post=[ext("vs_url", "$.url")]),
    step("GET ValueSet/$expand theo data", "expand", "GET", f"{FHIR}/ValueSet/$expand",
         query={"{{url_name}}": "{{vs_url}}{{url_suffix}}", "{{count_name}}": "{{count_value}}", "{{offset_name}}": "{{offset_value}}"},
         asserts=["status == {{expected_status}}"],
         post=[js(ERR_JS), js("const g = k => pm.iterationData.get(k); const b = pm.response.json(); "
                              "if (g('expected_total')) { pm.test('expansion.total == ' + g('expected_total'), () => pm.expect(b.expansion.total).to.eql(Number(g('expected_total')))); } "
                              "if (g('expected_contains')) { pm.test('contains == ' + g('expected_contains'), () => pm.expect((b.expansion.contains || []).length).to.eql(Number(g('expected_contains')))); }")]),
], ["terminology"])

# ================================================================= S-20 code-system-import
P = lambda parts: {"resourceType": "Parameters", "parameter": parts}
concept = lambda c, d: {"name": "concept", "valueCoding": {"code": c, "display": d}}
ci = [
    row("TC-CI-01", "Positive", "nạp 2 concept", "200", import_body=json.dumps(P([concept("A", "Alpha"), concept("B", "Beta")]))),
    row("TC-CI-02", "Positive", "gọi lại cùng dữ liệu (idempotent)", "200", import_body=json.dumps(P([concept("A", "Alpha")]))),
    row("TC-CI-03", "Negative", "thân không phải Parameters", "400", "invalid", "Body phải là Parameters", import_body='{"resourceType":"Patient"}'),
    row("TC-CI-04", "Negative", "concept thiếu code", "400", "invalid", "concept thiếu code", import_body=json.dumps(P([{"name": "concept", "valueCoding": {"display": "x"}}]))),
    row("TC-CI-05", "Negative", "designation tham chiếu concept chưa có", "400", "invalid", "Concept chưa có: ZZ",
        import_body=json.dumps(P([{"name": "designation", "part": [{"name": "code", "valueCode": "ZZ"}, {"name": "language", "valueCode": "vi"}, {"name": "value", "valueString": "x"}]}]))),
    row("TC-CI-06", "Negative", "property chưa khai báo ở CodeSystem.property", "400", "invalid", "Property không xác định: zz",
        import_body=json.dumps(P([concept("A", "Alpha"), {"name": "property", "part": [{"name": "code", "valueCode": "A"}, {"name": "property", "valueCode": "zz"}, {"name": "value", "valueString": "x"}]}]))),
]
write_json("code-system-import", ci)
scenario("code-system-import", "CodeSystem/{id}/$import (lô nhỏ)", 2, "Nạp concept cho CodeSystem not-present (kho concept riêng); lô lớn xem 'Chỉ thiết kế'.", [
    token(),
    step("(setup) POST CodeSystem content=not-present", "create", "POST", f"{FHIR}/CodeSystem",
         body={"resourceType": "CodeSystem", "status": "active", "content": "not-present", "url": "http://qa.trivita.test/cs-import/{{case_id}}-{{$timestamp}}"},
         asserts=["status == 201"], post=[ext("cs_id", "$.id")]),
    step("POST CodeSystem/{id}/$import theo data", "cs_import", "POST", f"{FHIR}/CodeSystem/{{{{cs_id}}}}/$import", body="{{import_body}}",
         asserts=["status == {{expected_status}}"], post=[js(ERR_JS)]),
    step("POST $import lần hai cùng dữ liệu (idempotent với ca hợp lệ, cùng lỗi với ca sai)", "cs_import", "POST", f"{FHIR}/CodeSystem/{{{{cs_id}}}}/$import", body="{{import_body}}",
         asserts=["status == {{expected_status}}"], post=[js(ERR_JS)]),
], ["terminology", "admin"])

# ================================================================= S-21 graphql
Q = lambda s: json.dumps(s, ensure_ascii=False)
gq = [
    row("TC-GQ-01", "Positive", "đọc một resource theo id", "200", gql_query=Q('query Q($id: ID!) { Patient(id: $id) { id gender } }'), expected_path="data.Patient.id", expected_value="PATIENT_ID"),
    row("TC-GQ-02", "Positive", "danh sách theo family", "200", gql_query=Q('query Q($f: String) { PatientList(family: $f) { id } }'), expected_path="data.PatientList.length", expected_value="1"),
    row("TC-GQ-03", "Positive", "connection có count", "200", gql_query=Q('query Q($f: String) { PatientConnection(family: $f, _count: 1) { count } }'), expected_path="data.PatientConnection.count", expected_value="1"),
    row("TC-GQ-04", "Positive", "id không tồn tại → data null, không errors", "200", gql_query=Q('{ Patient(id: "khong-co") { id } }'), expected_path="data.Patient", expected_value="null"),
    row("TC-GQ-05", "Negative", "field gốc lạ → lỗi trong errors, HTTP 200", "200", "", "field gốc không hợp lệ: Foo", gql_query=Q('{ Foo(id: "x") { id } }')),
    row("TC-GQ-06", "Negative", "tham số search lạ → errors, HTTP 200", "200", "", "tham số search không hợp lệ: zzz", gql_query=Q('{ PatientList(zzz: "a") { id } }')),
    row("TC-GQ-07", "Negative", "cú pháp sai", "400", "invalid", "lỗi cú pháp GraphQL", gql_query=Q('{ Patient(id: ')),
    row("TC-GQ-08", "Negative", "mutation chưa hỗ trợ", "400", "invalid", "mutation chưa được hỗ trợ", gql_query=Q('mutation { Patient { id } }')),
    row("TC-GQ-09", "Negative", "query rỗng", "400", "invalid", "thiếu query", gql_query=Q("")),
    row("TC-GQ-10", "Boundary", "truy vấn lồng quá 12 cấp", "400", "invalid", "selection lồng quá sâu", gql_query=Q("{ " + "a { " * 13 + "id" + " }" * 13 + " }")),
    row("TC-GQ-11", "Boundary", "truy vấn dài hơn 64 KiB", "400", "invalid", "truy vấn quá dài", gql_query=Q("{ " + "a" * 66000 + " }")),
]
for r in gq:
    r.setdefault("expected_path", ""); r.setdefault("expected_value", "")
write_json("graphql", gq)
scenario("graphql", "POST $graphql (chỉ đọc)", 1, "Đọc, danh sách, connection; lỗi cấp field trả 200 + errors; lỗi cú pháp 400.", [
    token(),
    create_patient("(setup) POST Patient", "Gq-{{case_id}}-{{$timestamp}}", extract_family="marker", gender="male"),
    step("POST $graphql theo data", "graphql", "POST", f"{FHIR}/$graphql", headers={"Content-Type": "application/json"},
         body='{"query": {{gql_query}}, "variables": {"id": "{{patient_id}}", "f": "{{marker}}"}}', asserts=["status == {{expected_status}}"],
         post=[js(ERR_JS),
               js("const p = pm.iterationData.get('expected_path'); let v = pm.iterationData.get('expected_value'); if (p) { "
                  "if (v === 'PATIENT_ID') { v = pm.variables.get('patient_id'); } const b = pm.response.json(); "
                  "let cur = b; p.split('.').forEach(k => { cur = (cur === null || cur === undefined) ? cur : cur[k]; }); "
                  "pm.test(p + ' == ' + v, () => pm.expect(String(cur)).to.eql(String(v))); }")]),
], ["graphql"])

# ================================================================= S-22 authz-matrix (RBAC theo scope SMART)
def st_script(var):
    return js(STATUS_JS % var)


def msg_script(var):
    return js("const m = pm.iterationData.get('%s'); if (m) { pm.test('thông điệp chứa: ' + m, () => pm.expect(pm.response.json().issue[0].details.text).to.include(m)); }" % var)


NOADM = "Token không đủ quyền cho thao tác này"
az = [
    row("TC-AZ-01", "Positive", "client seed không xin scope (fhir.write, KHÔNG phải admin): ghi được, $reindex bị chặn", "403", scope_name="x_no_scope", token_scope="1", st_get="200", st_post="201", st_put="200", st_reindex="403", st_delete="204", msg_reindex=NOADM),
    row("TC-AZ-02", "Security", "chỉ scope đọc system/Patient.rs: ghi bị chặn ở Guard", "403", scope_name="scope", token_scope="system/Patient.rs",
        st_get="200", st_post="403", st_put="403", st_reindex="403", st_delete="403", msg_post=NOADM, msg_reindex=NOADM),
    row("TC-AZ-03", "Security", "system/Patient.cruds: ghi được, không có quyền admin ($reindex)", "403", scope_name="scope", token_scope="system/Patient.cruds",
        st_get="200", st_post="201", st_put="200", st_reindex="403", st_delete="204", msg_reindex=NOADM),
    row("TC-AZ-04", "Security", "chỉ quyền Observation: Patient bị chặn theo SMART", "403", scope_name="scope", token_scope="system/Observation.cruds",
        st_get="403", st_post="403", st_put="403", st_reindex="403", st_delete="403", msg_get="Token thiếu quyền r (SMART) trên Patient", msg_post="Token thiếu quyền c (SMART) trên Patient"),
    row("TC-AZ-05", "Security", "system/Patient.r: chỉ đọc theo id", "403", scope_name="scope", token_scope="system/Patient.r",
        st_get="200", st_post="403", st_put="403", st_reindex="403", st_delete="403", msg_post=NOADM),
    row("TC-AZ-06", "Security", "system/Patient.ru: đọc và sửa, không tạo/xóa", "403", scope_name="scope", token_scope="system/Patient.ru",
        st_get="200", st_post="403", st_put="200", st_reindex="403", st_delete="403", msg_post="Token thiếu quyền c (SMART) trên Patient"),
]
for r in az:
    for k in ("msg_get", "msg_post", "msg_reindex"):
        r.setdefault(k, "")
write_json("authz-matrix", az)
scenario("authz-matrix", "Phân quyền theo scope token (RBAC + SMART)", 1,
         "Mỗi dòng xin token với scope khác nhau rồi thử đọc, tạo, sửa, $reindex (admin), xóa trên Patient. Cần client được phép xin các scope system/*.", [
    token(var="adminToken", name="Token admin (không scope) để chuẩn bị/dọn"),
    step("(setup) POST Patient bằng token admin", "create", "POST", f"{FHIR}/Patient", auth="Bearer {{adminToken}}", body=patient("Az-{{case_id}}-{{$timestamp}}", gender="male"),
         asserts=["status == 201"], post=[ext("patient_id", "$.id")]),
    token(scope_name="{{scope_name}}", scope_value="{{token_scope}}", name="Token theo scope của dòng dữ liệu"),
    step("GET Patient/{id}", "read", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}", asserts=[], post=[st_script("st_get"), msg_script("msg_get")], custom=True),
    step("POST Patient", "create", "POST", f"{FHIR}/Patient", body=patient("Az2-{{case_id}}-{{$timestamp}}"), asserts=[], post=[st_script("st_post"), msg_script("msg_post")], custom=True),
    step("PUT Patient/{id}", "update", "PUT", f"{FHIR}/Patient/{{{{patient_id}}}}", body=patient("Az-{{case_id}}", gender="female", pid="{{patient_id}}"),
         asserts=[], post=[st_script("st_put")], custom=True),
    step("POST Patient/{id}/$reindex (cần admin)", "reindex", "POST", f"{FHIR}/Patient/{{{{patient_id}}}}/$reindex", asserts=[], post=[st_script("st_reindex"), msg_script("msg_reindex")], custom=True),
    step("DELETE Patient/{id}", "delete", "DELETE", f"{FHIR}/Patient/{{{{patient_id}}}}", asserts=[], post=[st_script("st_delete")], custom=True),
    step("(dọn) $expunge bằng token admin", "expunge", "POST", f"{FHIR}/Patient/{{{{patient_id}}}}/$expunge", auth="Bearer {{adminToken}}", asserts=["status == 200"]),
], ["authz", "security"])

# ================================================================= S-23 authn-negative
an = [
    row("TC-AN-01", "Security", "token không phải JWT", "401", "login", "Token không hợp lệ", bad_token="not-a-jwt"),
    row("TC-AN-02", "Security", "chuỗi ba đoạn nhưng không phải JWT", "401", "login", "Token không hợp lệ", bad_token="aaa.bbb.ccc"),
    row("TC-AN-03", "Security", "token SQLi", "401", "login", "", bad_token="' OR '1'='1"),
    row("TC-AN-04", "Boundary", "token rất dài (4096 ký tự)", "401", "login", "", bad_token="a" * 4096),
]
write_json("authn-negative", an)
scenario("authn-negative", "Xác thực: token sai hoặc thiếu", 1, "Token giả → 401 invalid_token; không Authorization → 401; metadata vẫn công khai kể cả khi kèm token sai.", [
    token(),
    step("GET Patient với token hợp lệ (đối chứng)", "search", "GET", f"{FHIR}/Patient", query={"_count": "1"}, asserts=["status == 200"]),
    step("GET Patient với token sai → 401", "search", "GET", f"{FHIR}/Patient", query={"_count": "1"}, auth="Bearer {{bad_token}}",
         asserts=["status == {{expected_status}}"],
         post=[js(ERR_JS), js(HEADER_HAS_JS % {"h": "WWW-Authenticate", "v": "invalid_token"})]),
    step("GET Patient không Authorization → 401", "search", "GET", f"{FHIR}/Patient", auth="none", asserts=["status == 401", "$.issue[0].code == login"],
         post=[js("pm.test('thông điệp thiếu token', () => pm.expect(pm.response.json().issue[0].details.text).to.include('Thiếu')); " + HEADER_HAS_JS % {"h": "WWW-Authenticate", "v": 'Bearer realm="fhir"'})]),
    step("GET metadata kèm token sai → 200 (công khai)", "metadata", "GET", f"{FHIR}/metadata", auth="Bearer {{bad_token}}", asserts=["status == 200"]),
], ["authn", "security"])

# ================================================================= S-24 project-isolation
write_json("project-isolation", [row("TC-PI-01", "Security", "project B không thấy dữ liệu của project A", "404")])
scenario("project-isolation", "Cô lập dữ liệu giữa hai project", 1,
         "Cần client của project thứ hai (otherClientId/otherClientSecret). Project B đọc/sửa/xóa/history/vread/$everything dữ liệu của A đều 404; tìm kiếm không thấy.", [
    token(name="Token project A"),
    create_patient("(A) POST Patient", "Iso-{{case_id}}-{{$timestamp}}", extract_family="marker", gender="male"),
    token(var="bearerTokenB", client="otherClientId", secret="otherClientSecret", name="Token project B"),
    step("(B) GET Patient/{id} → 404", "read", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}", auth="Bearer {{bearerTokenB}}", asserts=["status == 404", "$.issue[0].code == not-found"]),
    step("(B) PUT Patient/{id} → 404", "update", "PUT", f"{FHIR}/Patient/{{{{patient_id}}}}", auth="Bearer {{bearerTokenB}}", body=patient("Hack", pid="{{patient_id}}"), asserts=["status == 404"]),
    step("(B) DELETE Patient/{id} → 404", "delete", "DELETE", f"{FHIR}/Patient/{{{{patient_id}}}}", auth="Bearer {{bearerTokenB}}", asserts=["status == 404"]),
    step("(B) GET _history → 404", "history", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}/_history", auth="Bearer {{bearerTokenB}}", asserts=["status == 404"]),
    step("(B) GET $everything → 404", "everything", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}/$everything", auth="Bearer {{bearerTokenB}}",
         query={"_count": "10", "_type": "Patient", "_since": "1970-01-01T00:00:00Z"}, asserts=["status == 404"]),
    step("(B) tìm theo family của A → 0 kết quả", "search", "GET", f"{FHIR}/Patient?family={{{{marker}}}}&_total=accurate", auth="Bearer {{bearerTokenB}}",
         asserts=["status == 200", "$.total == 0"], custom=True),
    step("(A) vẫn đọc được → 200", "read", "GET", f"{FHIR}/Patient/{{{{patient_id}}}}", asserts=["status == 200", "$.id == {{patient_id}}"]),
], ["authz", "security", "isolation"])

# ================================================================= S-25 authz-project-header
ph = [
    row("TC-PH-01", "Security", "X-Fhir-Project khác project của token", "403", "forbidden", "Token không được chọn project khác", x_project="project-khac"),
    row("TC-PH-02", "Boundary", "X-Fhir-Project dài 129 ký tự", "403", "forbidden", "", x_project="a" * 129),
    row("TC-PH-03", "Security", "X-Fhir-Project chứa SQLi", "403", "forbidden", "", x_project="x'; DROP TABLE y;--"),
]
write_json("authz-project-header", ph)
scenario("authz-project-header", "Header X-Fhir-Project với token thường", 2, "Chỉ super-admin được chọn project bằng header; token thường bị 403.", [
    token(),
    step("GET Patient + X-Fhir-Project", "search", "GET", f"{FHIR}/Patient", query={"_count": "1"}, headers={"X-Fhir-Project": "{{x_project}}"},
         asserts=["status == {{expected_status}}"], post=[js(ERR_JS)]),
], ["authz", "security"])

# ================================================================= S-26 rate-limits-flow
write_json("rate-limits-flow", [row("TC-RL-01", "Positive", "xem hạn mức điểm của chính project; project khác 403; không token 401", "200")])
scenario("rate-limits-flow", "Project/{id}/$rate-limits", 2, "Admin xem hạn mức của chính project (enabled true/false tùy cấu hình).", [
    token(),
    step("GET Project/{projectId}/$rate-limits", "rate", "GET", f"{FHIR}/Project/{{{{projectId}}}}/$rate-limits",
         asserts=["status == 200", "$.resourceType == Parameters"],
         post=[js("const p = pm.response.json().parameter.find(x => x.name === 'enabled'); pm.test('có tham số enabled', () => pm.expect(p).to.not.eql(undefined));")]),
    step("GET Project/khac/$rate-limits → 403", "rate", "GET", f"{FHIR}/Project/project-khac/$rate-limits", asserts=["status == 403"],
         post=[js("pm.test('thông điệp', () => pm.expect(pm.response.json().issue[0].details.text).to.include('Chỉ quản trị của chính project này được xem hạn mức'));")]),
    step("GET $rate-limits không token → 401", "rate", "GET", f"{FHIR}/Project/{{{{projectId}}}}/$rate-limits", auth="none", asserts=["status == 401"]),
], ["rate-limit", "admin"])

# ---------------------------------------------------------------- scenarios.md (sinh từ design)
DESIGN_ONLY = [
    ("CodeSystem/$import và ConceptMap/$import lô lớn (>10000 phần tử, nhiều lô)", "nặng, ghi nhiều dòng kho concept; chỉ lô nhỏ ở code-system-import. ConceptMap/$import chưa có scenario (cùng khuôn: Parameters.mapping, lỗi M3-M9 ở cases/operation.md)"),
    ("PackageRelease/{id}/$install", "ghi hàng loạt resource từ gói Binary 64 MiB, không idempotent (cài lại tạo bản sao); cần gói mẫu riêng"),
    ("{resourceType}/$reindex cả loại (202 + job nền)", "tạo AsyncJob quét toàn loại của project, chạy ở fhir-worker; chỉ chạy được khi chủ project đồng ý; $reindex một resource đã phủ ở authz-matrix"),
    ("POST / batch async (Prefer: respond-async)", "chỉ có tác dụng khi FHIR_ASYNC_BATCH=true (mặc định tắt) và cần fhir-worker"),
    ("PUT update-as-create (201 với id client chọn)", "chỉ khi FHIR_UPDATE_AS_CREATE=true (mặc định tắt: ca 404 đã có ở update-rules)"),
    ("413 thân quá 16 MiB; 429 hạn mức IP/điểm; Project/$rate-limits enabled=true", "cần tạo thân 16 MiB hoặc đổi cấu hình FHIR_RATE_LIMIT/FHIR_QUOTA_*; làm bằng test Go (pkg/ratelimit) thay vì Apidog"),
    ("AccessPolicy (criteria, readonly, hiddenFields, interaction) và compartment patient/", "AccessPolicy không có API tạo ở auth (chỉ seed 6 policy mặc định), nên không dựng được client có policy riêng bằng Apidog; đã phủ ở test Go e2e/bootstrap"),
    ("Binary thô (RawBinary: POST/GET byte, ký URL) và $export tải qua /storage", "phụ thuộc FHIR_BINARY_STORAGE và URL ký; luồng đọc ndjson từ Binary đã nằm ở bulk-export-flow"),
    ("Subscription rest-hook / websocket", "cần máy nhận webhook và ws-gateway; thuộc scenario full-flow xuyên hệ thống, không thuộc module này"),
    ("Super-admin: X-Fhir-Project hợp lệ và audit", "cần project bật superAdmin (auth project super); chỉ ca token thường bị 403 ở authz-project-header"),
]


def write_scenarios_md():
    out = ["# Scenarios: Trivita FHIR Server (module 1533159)\n",
           "Sinh tự động từ `generate.py`. Mỗi scenario mở đầu bằng bước token `client_credentials` (customHttp tới `{{authUrl}}/oauth2/token`).\n"]
    n = 0
    for p in sorted((ROOT / "scenarios").glob("*.design.json")):
        d = json.loads(p.read_text(encoding="utf-8"))
        n += 1
        slug = d["slug"]
        rows = COUNTS.get(slug, [])
        cid = f"{rows[0]['case_id']} … {rows[-1]['case_id']}" if rows else "-"
        out.append(f"## S-{n:02d} {slug} — {d['name']}\n")
        out.append(f"- Ưu tiên: P{d['priority']}; dữ liệu: `data/{slug}.json|csv` ({len(rows)} dòng, {cid})")
        out.append("- Các bước:")
        for i, s in enumerate(d["steps"], start=1):
            out.append(f"  {i}. {s['name']} — `{s['method']} {s['path']}`")
        out.append("")
    out.append("## Chỉ thiết kế (không dựng, kèm lý do)\n")
    for t, why in DESIGN_ONLY:
        out.append(f"- **{t}**: {why}")
    out.append("")
    (ROOT / "scenarios" / "scenarios.md").write_text("\n".join(out), encoding="utf-8")


write_scenarios_md()

# ---------------------------------------------------------------- tóm tắt
if __name__ == "__main__":
    total_rows = sum(len(r) for r in COUNTS.values())
    designs = sorted((ROOT / "scenarios").glob("*.design.json"))
    nsteps = sum(len(json.loads(p.read_text(encoding="utf-8"))["steps"]) for p in designs)
    print(f"{len(designs)} scenario, {nsteps} step, {total_rows} dòng dữ liệu")
    groups = {}
    for rows in COUNTS.values():
        for r in rows:
            groups[r["case_group"]] = groups.get(r["case_group"], 0) + 1
    print(groups)
