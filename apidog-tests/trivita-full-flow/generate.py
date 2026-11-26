#!/usr/bin/env python3
"""Sinh data/*.csv + scenarios/*.design.json|create.json + spec/endpoint-details + spec/environment.json cho bộ test FULL-FLOW xuyên hệ thống
(auth, fhir-server, dicomweb, ws-gateway) của Trivita Platform.

Chạy: python3 -I apidog-tests/trivita-full-flow/generate.py apidog-tests/trivita-full-flow
Nguồn sự thật: KHÔNG sửa tay các tệp sinh ra, sửa tệp này rồi chạy lại.

Quy ước:
- Bộ này không thuộc module nào của Apidog: MỌI bước là customHttp với URL tuyệt đối ({{authUrl}}, {{fhirUrl}}, {{dicomUrl}}, {{wsUrl}}),
  nên "endpoint" của bước là endpoint tổng hợp (chỉ có ở máy: spec/endpoint-details/<id>.json, id >= 9100000) để builder lấy method + path.
- Giá trị bí mật chỉ là biến chạy (clientId, clientSecret), không có trong tệp nào.
- STOW-RS dùng body raw multipart/related (cần builder có hỗ trợ "bodyType": "raw": bản vá ở apidog-tests/trivita-dicomweb/tools/).
"""
import csv
import json
import sys
from pathlib import Path

ROOT = Path(sys.argv[1] if len(sys.argv) > 1 else "apidog-tests/trivita-full-flow")
for d in ("data", "scenarios", "spec/endpoint-details", "dryrun"):
    (ROOT / d).mkdir(parents=True, exist_ok=True)

A, F, D, W = "{{authUrl}}", "{{fhirUrl}}", "{{dicomUrl}}", "{{wsUrl}}"
FHIR = F + "/fhir/R4"
BEARER = "Bearer {{accessToken}}"

# ---------------------------------------------------------------- endpoint tổng hợp (mỗi bước một endpoint: path = đúng URL của bước, không có {param})
_next = [9100000]


def synth(name, method, path):
    eid = _next[0]
    _next[0] += 1
    doc = {"id": eid, "name": name, "method": method.lower(), "path": path,
           "parameters": {"path": [], "query": [], "header": [], "cookie": []},
           "auth": {}, "securityScheme": {}, "requestBody": None, "responses": []}
    (ROOT / "spec" / "endpoint-details" / f"{eid}.json").write_text(json.dumps(doc, ensure_ascii=False), encoding="utf-8")
    return eid


# ---------------------------------------------------------------- script
def t(name, body):
    return f"pm.test({json.dumps(name, ensure_ascii=False)}, () => {{ {body} }});"


J = "pm.response.json()"
WAIT3 = "const t0 = Date.now(); while (Date.now() - t0 < 3000) {}"


def step(name, method, path, *, query=None, headers=None, body=None, body_type=None, form=None, auth=BEARER, asserts=(), extracts=(), scripts=()):
    s = {"name": name, "endpointId": synth(name, method, path), "method": method.upper(), "path": path, "auth": auth, "custom": True}
    for k, v in (("query", query), ("headers", headers), ("body", body), ("bodyType", body_type), ("form", form)):
        if v:
            s[k] = v
    s["assertions"] = list(asserts)
    pp = [{"type": "extractor", "data": {"variableName": v, "expression": e, "subject": "responseJson"}} for v, e in extracts]
    pp += [{"type": "customScript", "data": {"script": js}} for js in scripts]
    if pp:
        s["postProcessors"] = pp
    return s


SCEN = {}


def scenario(slug, name, priority, description, steps, tags=()):
    SCEN[slug] = (name, priority, steps)
    (ROOT / "scenarios" / f"{slug}.design.json").write_text(
        json.dumps({"slug": slug, "name": name, "priority": priority, "steps": steps}, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    (ROOT / "scenarios" / f"{slug}.create.json").write_text(
        json.dumps({"name": name, "description": description, "folderId": 0, "priority": priority, "tags": ["full-flow", *tags]}, ensure_ascii=False, indent=1) + "\n",
        encoding="utf-8")


ROWS = {}


def write_csv(slug, columns, rows):
    with (ROOT / "data" / f"{slug}.csv").open("w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=columns, extrasaction="raise", lineterminator="\n")
        w.writeheader()
        for r in rows:
            w.writerow({c: r.get(c, "") for c in columns})
    ROWS[slug] = rows


def row(cid, group, desc, status, **cols):
    r = {"case_id": cid, "case_group": group, "description": desc, "expected_status": str(status)}
    r.update(cols)
    return r


# ---------------------------------------------------------------- DICOM JSON + multipart (rút gọn từ trivita-dicomweb/generate.py)
CRLF, BND = "\r\n", "qa-ff-bnd"
CT_META = f'multipart/related; type="application/dicom+json"; boundary={BND}'
CT_CLASS = "1.2.840.10008.5.1.4.1.1.2"
UIDBASE = "1.2.826.0.1.3680043.9.7777"


def el(vr, *vals):
    return {"vr": vr, "Value": list(vals)}


def dicom_meta():
    return {"00080016": el("UI", CT_CLASS), "00080018": el("UI", "{{sop}}"), "00080060": el("CS", "CT"), "00080020": el("DA", "20240102"),
            "00100010": el("PN", {"Alphabetic": "Qa^FullFlow"}), "00100020": el("LO", "{{pid}}"), "0020000D": el("UI", "{{studyUid}}"), "0020000E": el("UI", "{{seriesUid}}"),
            "00280002": el("US", 1), "00280004": el("CS", "MONOCHROME2"), "00280008": el("IS", "1"), "00280010": el("US", 2), "00280011": el("US", 2),
            "00280100": el("US", 8), "00280101": el("US", 8), "00280102": el("US", 7), "00280103": el("US", 0), "7FE00010": {"vr": "OB", "BulkDataURI": "urn:qa:ff-px"}}


def part(headers, body):
    return f"--{BND}{CRLF}" + "".join(f"{k}: {v}{CRLF}" for k, v in headers.items()) + CRLF + body + CRLF


PIXEL = "FFLW"  # 2x2, 8 bit, 1 frame: chuỗi ASCII để so khớp từng byte
STOW_BODY = (part({"Content-Type": "application/dicom+json"}, json.dumps([dicom_meta()], ensure_ascii=False, separators=(",", ":")))
             + part({"Content-Location": "urn:qa:ff-px", "Content-Type": "application/octet-stream"}, PIXEL) + f"--{BND}--{CRLF}")

# ---------------------------------------------------------------- bước dùng chung
TOKEN_SCRIPT = (
    "const base = '" + UIDBASE + ".' + Date.now() + (100 + Math.floor(Math.random() * 900));\n"
    "pm.variables.set('studyUid', base + '.1'); pm.variables.set('seriesUid', base + '.1.1'); pm.variables.set('sop', base + '.1.1.1');\n"
    "pm.variables.set('pid', 'QA-FF-' + Date.now());\n"
    "pm.variables.set('topic', 'qa-ff-' + Date.now() + '-' + Math.floor(Math.random() * 1000000));"
)


def token_step(var="accessToken", scope=None, name="Lấy token client_credentials (auth)", script=None):
    form = {"grant_type": "client_credentials", "client_id": "{{clientId}}", "client_secret": "{{clientSecret}}"}
    if scope:
        form["scope"] = scope
    return step(name, "POST", A + "/oauth2/token", form=form, auth="none", asserts=["status == 200", "$.access_token exists"],
                extracts=[(var, "$.access_token")], scripts=[script] if script else [])


def health_steps():
    return [step("auth: GET /health/live", "GET", A + "/health/live", auth="none", asserts=["status == 200"]),
            step("fhir-server: GET /healthz", "GET", F + "/healthz", auth="none", asserts=["status == 200"]),
            step("dicomweb: GET /healthz", "GET", D + "/healthz", auth="none", asserts=["status == 200"]),
            step("ws-gateway: GET /healthz", "GET", W + "/healthz", auth="none", asserts=["status == 200"])]


def patient_body(gender=None):
    p = {"resourceType": "Patient", "identifier": [{"system": "urn:trivita:qa-mrn", "value": "{{pid}}"}], "name": [{"family": "FullFlowQa", "given": ["Qa"]}]}
    if gender:
        p["gender"] = gender
    return json.dumps(p, ensure_ascii=False)


# =============================================================================== 1. full-flow-e2e
STOW_HDR = {"Content-Type": CT_META}
SE = D + "/dicomweb/studies/{{studyUid}}/series/{{seriesUid}}/instances/{{sop}}"
e2e = [
    *health_steps(),
    token_step(script=TOKEN_SCRIPT),
    # ---- auth
    step("auth: POST /oauth2/introspect token vừa cấp -> active", "POST", A + "/oauth2/introspect", form={"token": "{{accessToken}}"},
         asserts=["status == 200", "$.active == true", "$.client_id == {{clientId}}"]),
    step("auth: GET /auth/me của ClientApplication", "GET", A + "/auth/me", asserts=["status == 200", "$.project.id exists"]),
    # ---- fhir-server: CRUD, search, history, $everything
    step("fhir: POST Patient", "POST", FHIR + "/Patient", body=patient_body("male"), asserts=["status == 201", "$.resourceType == Patient", "$.id exists"],
         extracts=[("patient_id", "$.id"), ("v1", "$.meta.versionId")]),
    step("fhir: GET Patient/{id}", "GET", FHIR + "/Patient/{{patient_id}}", asserts=["status == 200", "$.id == {{patient_id}}", "$.meta.versionId == {{v1}}"]),
    step("fhir: tìm Patient theo identifier -> đúng 1", "GET", FHIR + "/Patient", query={"identifier": "urn:trivita:qa-mrn|{{pid}}"},
         asserts=["status == 200", "$.resourceType == Bundle"], scripts=[t("đúng 1 entry", f"pm.expect({J}.entry.length).to.eql(1);")]),
    step("fhir: PUT Patient/{id} đổi gender (phiên bản 2)", "PUT", FHIR + "/Patient/{{patient_id}}", body=json.dumps(
        {"resourceType": "Patient", "id": "{{patient_id}}", "identifier": [{"system": "urn:trivita:qa-mrn", "value": "{{pid}}"}], "name": [{"family": "FullFlowQa", "given": ["Qa"]}], "gender": "female"}),
         asserts=["status == 200", "$.gender == female"], scripts=[t("versionId đổi", f"pm.expect({J}.meta.versionId).to.not.eql(pm.variables.get('v1'));")]),
    step("fhir: GET Patient/{id}/_history -> 2 phiên bản", "GET", FHIR + "/Patient/{{patient_id}}/_history", asserts=["status == 200", "$.type == history"],
         scripts=[t("2 entry", f"pm.expect({J}.entry.length).to.eql(2);")]),
    step("fhir: POST Observation gắn Patient", "POST", FHIR + "/Observation", body=json.dumps(
        {"resourceType": "Observation", "status": "final", "code": {"text": "qa full flow"}, "subject": {"reference": "Patient/{{patient_id}}"}, "valueString": "ok"}),
         asserts=["status == 201", "$.id exists"], extracts=[("obs_id", "$.id")]),
    step("fhir: GET Patient/{id}/$everything có Patient + Observation", "GET", FHIR + "/Patient/{{patient_id}}/$everything", asserts=["status == 200", "$.resourceType == Bundle"],
         scripts=[t("có cả Patient và Observation", f"const ty = ({J}.entry || []).map(e => e.resource.resourceType); pm.expect(ty).to.include('Patient'); pm.expect(ty).to.include('Observation');")]),
    # ---- fhir-server + fhir-worker: $export bất đồng bộ
    step("fhir: GET $export (Prefer: respond-async) -> 202", "GET", FHIR + "/$export", query={"_type": "Patient", "_outputFormat": "ndjson"}, headers={"Prefer": "respond-async"},
         asserts=["status == 202"],
         scripts=[t("Content-Location có id job", "const m = (pm.response.headers.get('Content-Location') || '').match(/bulkdata\\/([^\\/]+)\\/status/); pm.expect(m).to.not.eql(null); pm.variables.set('job_id', m[1]);")]),
    *[step(f"fhir: GET bulkdata/{{id}}/status (lần {i})", "GET", FHIR + "/bulkdata/{{job_id}}/status",
           scripts=[t("202 hoặc 200", "pm.expect([200, 202]).to.include(pm.response.code);"), WAIT3]) for i in (1, 2, 3)],
    step("fhir: GET bulkdata/{id}/status -> 200 manifest (do fhir-worker chạy)", "GET", FHIR + "/bulkdata/{{job_id}}/status", asserts=["status == 200", "$.output is array"],
         scripts=[t("có file output", "const u = (pm.response.json().output || [])[0]; pm.expect(u).to.not.eql(undefined); pm.variables.set('bin_id', String(u.url).split('/').pop());")]),
    step("fhir: GET Binary/{id} (ndjson từ kho dùng chung)", "GET", FHIR + "/Binary/{{bin_id}}", headers={"Accept": "application/fhir+ndjson"}, asserts=["status == 200"],
         scripts=[t("ndjson chứa Patient", "pm.expect(pm.response.text()).to.include('\"resourceType\":\"Patient\"');")]),
    step("fhir: DELETE bulkdata/{id}/status (dọn job)", "DELETE", FHIR + "/bulkdata/{{job_id}}/status", asserts=["status == 202"]),
    # ---- dicomweb: STOW -> QIDO -> WADO, rồi fhir-server thấy DicomStudy
    step("dicomweb: STOW-RS dạng metadata 1 instance (200)", "POST", D + "/dicomweb/studies", headers=STOW_HDR, body=STOW_BODY, body_type="raw", asserts=["status == 200"],
         scripts=[t("ReferencedSOPSequence 1 phần tử, không Failed", f"const j = {J}; pm.expect(j['00081199'].Value.length).to.eql(1); pm.expect(j['00081198']).to.eql(undefined);")]),
    step("dicomweb: QIDO-RS study theo StudyInstanceUID -> 1", "GET", D + "/dicomweb/studies", query={"StudyInstanceUID": "{{studyUid}}"}, asserts=["status == 200"],
         scripts=[t("đúng 1 study", f"const j = {J}; pm.expect(j.length).to.eql(1); pm.expect(j[0]['0020000D'].Value[0]).to.eql(pm.variables.get('studyUid'));")]),
    step("dicomweb: WADO-RS metadata instance", "GET", SE + "/metadata", asserts=["status == 200"],
         scripts=[t("có SOPInstanceUID", "pm.expect(pm.response.text()).to.include(pm.variables.get('sop'));")]),
    step("dicomweb: WADO-RS tải instance (Part 10)", "GET", SE, asserts=["status == 200"],
         scripts=[t("multipart/related, có DICM", "pm.expect(pm.response.headers.get('Content-Type')).to.include('multipart/related'); pm.expect(pm.response.text()).to.include('DICM');")]),
    step("dicomweb: WADO-RS frame 1 đúng byte đã STOW", "GET", SE + "/frames/1", asserts=["status == 200"],
         scripts=[t(f"body chứa {PIXEL}", f"pm.expect(pm.response.text()).to.include('{PIXEL}');")]),
    step("fhir: GET DicomStudy thấy study do dicomweb ghi (schema dùng chung)", "GET", FHIR + "/DicomStudy", query={"_count": "50"}, asserts=["status == 200"],
         scripts=[t("study có trong kết quả", "pm.expect(pm.response.text()).to.include(pm.variables.get('studyUid'));")]),
    # ---- ws-gateway: hub FHIRcast (cần FHIRCAST bật + token scope fhircast/*)
    token_step("wsToken", scope="fhircast/*", name="auth: token client_credentials scope fhircast/* cho ws-gateway"),
    step("ws-gateway: GET .well-known/fhircast-configuration (công khai)", "GET", W + "/fhircast/STU3/.well-known/fhircast-configuration", auth="none", asserts=["status == 200"]),
    step("ws-gateway: subscribe websocket (STU3)", "POST", W + "/fhircast/STU3", auth="Bearer {{wsToken}}", asserts=["status == 202", "$['hub.channel.endpoint'] exists"],
         body=json.dumps({"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": "{{topic}}", "hub.events": "Patient-open,Patient-close"}),
         extracts=[("channel_endpoint", "$['hub.channel.endpoint']")]),
    step("ws-gateway: mở ngữ cảnh Patient-open theo Patient vừa tạo", "POST", W + "/fhircast/STU3/{{topic}}", auth="Bearer {{wsToken}}", asserts=["status == 202", "$.success == true"],
         body='{"id":"qa-ff-{{$timestamp}}","timestamp":"2026-11-25T10:00:00.000Z","event":{"hub.topic":"{{topic}}","hub.event":"Patient-open","context":[{"key":"patient","resource":{"resourceType":"Patient","id":"{{patient_id}}"}}]}}'),
    step("ws-gateway: GET ngữ cảnh hiện tại = Patient", "GET", W + "/fhircast/STU3/{{topic}}", auth="Bearer {{wsToken}}", asserts=["status == 200"],
         scripts=[t("context.type", f"pm.expect({J}['context.type']).to.eql('Patient');")]),
    step("ws-gateway: đóng ngữ cảnh Patient-close", "POST", W + "/fhircast/STU3/{{topic}}", auth="Bearer {{wsToken}}", asserts=["status == 202", "$.success == true"],
         body='{"id":"qa-ff-{{$timestamp}}","timestamp":"2026-11-25T10:00:00.000Z","event":{"hub.topic":"{{topic}}","hub.event":"Patient-close","context":[{"key":"patient","resource":{"resourceType":"Patient","id":"{{patient_id}}"}}]}}'),
    step("ws-gateway: unsubscribe", "POST", W + "/fhircast/STU3", auth="Bearer {{wsToken}}", asserts=["status == 202"],
         body='{"hub.channel.type":"websocket","hub.mode":"unsubscribe","hub.topic":"{{topic}}","hub.channel.endpoint":"{{channel_endpoint}}"}'),
    # ---- dọn dẹp + thu hồi
    step("fhir: DELETE Observation", "DELETE", FHIR + "/Observation/{{obs_id}}", asserts=["status == 204"]),
    step("fhir: DELETE Patient (xóa mềm)", "DELETE", FHIR + "/Patient/{{patient_id}}", asserts=["status == 204"]),
    step("fhir: GET Patient đã xóa -> 410", "GET", FHIR + "/Patient/{{patient_id}}", asserts=["status == 410", "$.issue[0].code == deleted"]),
    # $expunge cần vai trò admin: client seed chỉ có fhir.write -> đăng nhập admin project rồi đổi code (như module auth)
    step("auth: đăng nhập admin project (để $expunge)", "POST", A + "/auth/login", auth="none",
         body=json.dumps({"email": "{{adminEmail}}", "password": "{{adminPassword}}", "clientId": "{{clientId}}", "scope": "openid profile email"}),
         asserts=["status == 200", "$.code exists"], extracts=[("adm_code", "$.code")]),
    step("auth: đổi code lấy token admin", "POST", A + "/oauth2/token", auth="none",
         form={"grant_type": "authorization_code", "code": "{{adm_code}}", "client_id": "{{clientId}}", "client_secret": "{{clientSecret}}"},
         asserts=["status == 200", "$.access_token exists"], extracts=[("adminToken", "$.access_token")]),
    step("fhir: POST Patient/{id}/$expunge (xóa vĩnh viễn)", "POST", FHIR + "/Patient/{{patient_id}}/$expunge", auth="Bearer {{adminToken}}", asserts=["status == 200"]),
    step("fhir: POST Observation/{id}/$expunge", "POST", FHIR + "/Observation/{{obs_id}}/$expunge", auth="Bearer {{adminToken}}", asserts=["status == 200"]),
    step("auth: POST /oauth2/logout thu hồi phiên của token", "POST", A + "/oauth2/logout", asserts=["status == 200"]),
    step("auth: sau logout /auth/me -> 401", "GET", A + "/auth/me", asserts=["status == 401"]),
]
scenario("full-flow-e2e", "Full flow: auth → FHIR → $export → DICOM → FHIRcast → dọn dẹp", 0,
         "Luồng đầu-cuối qua 4 service bằng JWT thật: health 4 service; token client_credentials; introspect và /auth/me; FHIR tạo/đọc/tìm/sửa/history/$everything; "
         "$export bất đồng bộ do fhir-worker chạy và đọc file từ kho Binary dùng chung; dicomweb STOW → QIDO → WADO đúng byte và fhir-server thấy DicomStudy; "
         "ws-gateway hub FHIRcast subscribe → mở/đóng ngữ cảnh theo Patient vừa tạo → unsubscribe; dọn Patient/Observation ($expunge); logout ở auth. "
         "Cần FHIRCAST bật ở ws-gateway (file override) và client có scope fhircast/*.", e2e, tags=["e2e"])
write_csv("full-flow-e2e", ["case_id", "case_group", "description", "expected_status"],
          [row("FF-01", "Positive", "Luồng đầu-cuối xuyên 4 service, một lần chạy", 200)])

# =============================================================================== 2. full-flow-negative-no-token
PROTECTED = [("auth: GET /auth/me", A + "/auth/me", "401"), ("fhir: GET /fhir/R4/Patient", FHIR + "/Patient", "401"),
             ("dicomweb: GET /dicomweb/studies", D + "/dicomweb/studies", "401"), ("ws-gateway: GET /fhircast/STU3/{topic}", W + "/fhircast/STU3/qa-no-token", "401")]
PUBLIC = [("auth: GET /.well-known/jwks.json", A + "/.well-known/jwks.json"), ("fhir: GET /fhir/R4/metadata", FHIR + "/metadata"),
          ("ws-gateway: GET /fhircast/STU3/.well-known/fhircast-configuration", W + "/fhircast/STU3/.well-known/fhircast-configuration")]
no_token = [*health_steps(),
            *[step(f"{n} không token -> 401", "GET", u, auth="none", asserts=["status == 401"]) for n, u, _ in PROTECTED],
            *[step(f"{n} không token -> 200 (công khai)", "GET", u, auth="none", asserts=["status == 200"]) for n, u in PUBLIC],
            step("dicomweb: ngoài tiền tố GET /fhir/R4/Patient -> 404 (không qua xác thực)", "GET", D + "/fhir/R4/Patient", auth="none", asserts=["status == 404"]),
            step("auth: POST /oauth2/token thiếu client_secret -> 4xx", "POST", A + "/oauth2/token", form={"grant_type": "client_credentials", "client_id": "{{clientId}}"}, auth="none",
                 scripts=[t("không phải 2xx/5xx", "pm.expect(pm.response.code).to.be.within(400, 499);")])]
scenario("full-flow-negative-no-token", "Full flow negative: không token → 401 trên cả 4 service", 1,
         "Mỗi service từ chối yêu cầu thiếu Authorization bằng 401 trên route bảo vệ, các route công khai (jwks, metadata, fhircast-configuration) vẫn 200, "
         "dicomweb không xác thực route ngoài tiền tố (404), /oauth2/token thiếu client_secret bị từ chối. Không cần token nên chạy được kể cả khi chưa seed client.", no_token, tags=["negative", "authz"])
write_csv("full-flow-negative-no-token", ["case_id", "case_group", "description", "expected_status"],
          [row("FN-01", "Security", "Thiếu Authorization trên 4 service + route công khai", 401)])

# =============================================================================== 3. full-flow-negative-bad-token
BAD = [("garbage", "Negative", "Chuỗi rác (không phải JWT)"), ("three-parts", "Negative", "Ba đoạn không phải JWT"),
       ("alg-none", "Security", "JWT alg=none, bỏ chữ ký"), ("sqli", "Security", "Chuỗi SQL injection"),
       ("long", "Boundary", "Token dài 4096 ký tự"), ("bearer-word", "Negative", "Giá trị là chữ Bearer")]
bad_rows = [row(f"FB-{i:02d}", g, d, 401, bad_kind=k) for i, (k, g, d) in enumerate(BAD, 1)]
# token xấu dựng bằng script (không để chuỗi giống JWT trong tệp dữ liệu)
BAD_SCRIPT = (
    "const k = pm.iterationData.get('bad_kind'); const m = {garbage: 'not-a-jwt', 'three-parts': 'aaa.bbb.ccc', sqli: \"' OR '1'='1\", 'bearer-word': 'Bearer', long: 'A'.repeat(4096),\n"
    "  'alg-none': ['eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0', 'eyJzdWIiOiJxYSJ9', ''].join('.')};\n"
    "pm.variables.set('bad_token', m[k]);")
bad_steps = [token_step(name="auth: token hợp lệ (đối chứng)", script=BAD_SCRIPT),
             step("đối chứng: fhir GET Patient với token hợp lệ -> 200", "GET", FHIR + "/Patient", query={"_count": "1"}, asserts=["status == 200"]),
             *[step(f"{n} với token sai -> 401", "GET", u, auth="Bearer {{bad_token}}", asserts=["status == {{expected_status}}"]) for n, u, _ in PROTECTED]]
scenario("full-flow-negative-bad-token", "Full flow negative: token sai → 401 trên cả 4 service", 1,
         "Cùng một token sai (chuỗi rác, 3 đoạn, alg=none, SQLi, quá dài, chữ Bearer) bị 4 service cùng từ chối 401; có bước đối chứng với token hợp lệ để chứng minh hạ tầng đang chạy.",
         bad_steps, tags=["negative", "security"])
write_csv("full-flow-negative-bad-token", ["case_id", "case_group", "description", "expected_status", "bad_kind"], bad_rows)

# =============================================================================== 4. full-flow-cross-service
cross = [token_step(script=TOKEN_SCRIPT),
         step("fhir: token của auth dùng được ở fhir-server", "GET", FHIR + "/Patient", query={"_count": "1"}, asserts=["status == 200"]),
         step("dicomweb: cùng token dùng được ở dicomweb", "GET", D + "/dicomweb/studies", asserts=["status == 200"]),
         token_step("wsToken", scope="fhircast/*", name="auth: token scope fhircast/*"),
         step("ws-gateway: token scope fhircast/* dùng được ở hub", "GET", W + "/fhircast/STU3/{{topic}}", auth="Bearer {{wsToken}}", asserts=["status == 200"]),
         step("auth: POST /oauth2/logout thu hồi phiên của token", "POST", A + "/oauth2/logout", asserts=["status == 200"]),
         step("auth: sau logout /auth/me -> 401", "GET", A + "/auth/me", asserts=["status == 401"]),
         step("fhir: token đã logout ở auth khi dùng ở fhir-server (ghi nhận hành vi: 200 hoặc 401)", "GET", FHIR + "/Patient", query={"_count": "1"},
              scripts=[t("200 (JWT chỉ kiểm chữ ký) hoặc 401 (kiểm thu hồi): ghi nhận, không khẳng định", "pm.expect([200, 401]).to.include(pm.response.code);")])]
scenario("full-flow-cross-service", "Full flow: một JWT của auth dùng chung cho các service", 1,
         "Token client_credentials của auth được fhir-server và dicomweb chấp nhận (cùng issuer/audience), token có scope fhircast/* được ws-gateway chấp nhận; "
         "sau logout ở auth, /auth/me trả 401. Việc fhir-server có từ chối token đã thu hồi hay không (G3) chưa rõ nên chỉ ghi nhận.", cross, tags=["cross-service"])
write_csv("full-flow-cross-service", ["case_id", "case_group", "description", "expected_status"],
          [row("FX-01", "Positive", "Một JWT, nhiều service; logout ở auth", 200)])

# =============================================================================== environment + README + scenarios.md
VARS = [("authUrl", "Base URL auth, vd http://localhost:8006 (phải trùng AUTH_BASE_URL/issuer)"), ("fhirUrl", "Base URL fhir-server, vd http://localhost:8005"),
        ("dicomUrl", "Base URL dicomweb, vd http://localhost:8008 (không kèm tiền tố)"), ("wsUrl", "Base URL ws-gateway, vd http://localhost:8007"),
        ("clientId", "client_id của ClientApplication seed (nhập lúc chạy, --env-var)"), ("clientSecret", "client_secret tương ứng (nhập lúc chạy, KHÔNG lưu)"),
        ("accessToken", "do bước token tạo ra"), ("wsToken", "token scope fhircast/*, do bước token thứ hai tạo ra"), ("bad_token", "do script ở bước token của negative-bad-token dựng từ cột bad_kind"),
        ("studyUid", "UID sinh mỗi lần chạy"), ("seriesUid", "như trên"), ("sop", "như trên"), ("pid", "PatientID/identifier sinh mỗi lần chạy"), ("topic", "topic FHIRcast sinh mỗi lần chạy"),
        ("patient_id", "do bước tạo Patient"), ("v1", "versionId đầu"), ("obs_id", "do bước tạo Observation"), ("job_id", "do bước $export"), ("bin_id", "do manifest $export"),
        ("channel_endpoint", "do bước subscribe")]
(ROOT / "spec" / "environment.json").write_text(json.dumps({"variables": [{"name": n, "value": "", "description": d} for n, d in VARS]}, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")

md = ["# Scenario của bộ trivita-full-flow (sinh bởi generate.py)", "",
      "Bộ này xuyên 4 service nên mọi bước là customHttp với URL tuyệt đối; không bind endpoint của module nào. Dữ liệu: `data/<slug>.csv`.", ""]
for i, (slug, (name, prio, steps)) in enumerate(SCEN.items(), 1):
    md += [f"## S-{i:02d} {slug} — {name}", "", f"Ưu tiên P{prio}; data: `data/{slug}.csv` ({len(ROWS[slug])} dòng); {len(steps)} bước.", ""]
    md += [f"{j}. {s['name']} — `{s['method']} {s['path']}`" for j, s in enumerate(steps, 1)] + [""]
md += ["## Chỉ thiết kế (không dựng trong Apidog)", "",
       "| Mục | Lý do | Nằm ở |", "|---|---|---|",
       "| Webhook rest-hook do fhir-notifier giao | cần máy nhận HTTP ở máy chạy test (host.docker.internal) | tests/full_smoke/smoke.py (`make smoke-full`) |",
       "| Attachment ngoài do fhir-downloader tải | cần máy chủ file ở máy chạy test, tắt chống SSRF chỉ trong override của test | smoke.py |",
       "| Nhận event-notification qua WebSocket (Subscription) của ws-gateway | Apidog CLI không có bước WebSocket | smoke.py (helper Go) |",
       "| STOW dạng tệp nhị phân thật | không dựng được nhị phân từ data | smoke.py (pkg/dicomtest) |", ""]
(ROOT / "scenarios" / "scenarios.md").write_text("\n".join(md), encoding="utf-8")

(ROOT / "data" / "README.md").write_text(
    "# Test data của trivita-full-flow\n\n"
    "- `full-flow-e2e`, `full-flow-negative-no-token`, `full-flow-cross-service`: MỘT dòng mỗi tệp. Đây là luồng một mạch, không có trường ràng buộc để chia lớp tương đương; "
    "dữ liệu động (UID, PatientID, topic) sinh bằng script ở bước token mỗi lần chạy.\n"
    "- `full-flow-negative-bad-token`: 6 dòng (Negative 3: chuỗi rác, 3 đoạn, chữ Bearer; Security 2: alg=none, SQLi; Boundary 1: 4096 ký tự), mọi dòng kỳ vọng 401 ở cả 4 service. Token xấu dựng bằng script từ cột `bad_kind` (không để chuỗi giống JWT trong tệp).\n- Không có dòng Positive ngoài 3 scenario một dòng; không có dòng Boundary ở các scenario còn lại vì không có tham số đầu vào.\n"
    "- Không có dòng Boundary/Positive riêng cho từng service: các scenario theo module (trivita-auth, trivita-fhir-server, trivita-dicomweb, trivita-ws-gateway) đã phủ.\n"
    "- Không chứa bí mật: `clientId`/`clientSecret` chỉ truyền lúc chạy (`--env-var`). Dữ liệu tạo ra mang tiền tố `QA-FF-`/`FullFlowQa`/`qa-ff-` để dễ dọn.\n"
    "- Chỉ có thể import hàng vào app bằng tay (CLI không upload được); chạy bằng CLI dùng thẳng các tệp này (`run_scenarios.py`).\n", encoding="utf-8")

total = sum(len(s[2]) for s in SCEN.values())
print(f"{len(SCEN)} scenario, {total} step, {sum(len(r) for r in ROWS.values())} row")
