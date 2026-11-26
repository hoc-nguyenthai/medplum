#!/usr/bin/env python3
"""Sinh data/*, scenarios/*.design.json|.create.json, scenarios/scenarios.md, spec/environment.json và các endpoint tổng hợp cho module trivita-dicomweb.

Chạy: python3 -I apidog-tests/trivita-dicomweb/generate.py apidog-tests/trivita-dicomweb
Không sửa tay các tệp được sinh; sửa ở đây rồi chạy lại.

Ý tưởng chính
- STOW-RS dùng DẠNG METADATA (multipart/related; type="application/dicom+json"): phần đầu là DICOM JSON, bulkdata PixelData là CHUỖI ASCII (vd "ABCDEFGHIJKL" =
  3 frame 2x2 8 bit), nên dựng được bằng body văn bản (bodyType "raw"). Không cần tệp DICOM nhị phân (xem coverage.md, mục STOW).
- Mọi scenario mở đầu bằng token client_credentials của auth ({{authUrl}}/oauth2/token, {{clientId}}, {{clientSecret}} nhập lúc chạy).
- Endpoint tổng hợp (id 9000001+) chỉ có ở máy: bước gọi sang auth và đường dẫn ngoài swagger (tiền tố project, route lạ, đường dẫn do dữ liệu quyết định).
"""
import csv
import json
import sys
from pathlib import Path

ROOT = Path(sys.argv[1])
(ROOT / "data").mkdir(exist_ok=True)
(ROOT / "scenarios").mkdir(exist_ok=True)
(ROOT / "spec" / "endpoint-details").mkdir(parents=True, exist_ok=True)

# ---------------------------------------------------------------- endpoint thật (từ spec/endpoints.json) và endpoint tổng hợp
_eps = json.loads((ROOT / "spec" / "endpoints.json").read_text(encoding="utf-8"))["data"]
EP = {(e["method"].lower(), e["path"]): e["id"] for e in _eps}


def ep(method, path):
    return EP[(method, path)]


def synth(eid, name, method, path):
    """Endpoint chỉ có ở máy (không tồn tại trong Apidog): builder biến thành bước customHttp với URL tuyệt đối."""
    doc = {"id": eid, "name": name, "method": method, "path": path, "parameters": {"path": [], "query": [], "header": [], "cookie": []},
           "auth": {}, "securityScheme": {}, "requestBody": None, "responses": []}
    (ROOT / "spec" / "endpoint-details" / f"{eid}.json").write_text(json.dumps(doc, ensure_ascii=False), encoding="utf-8")
    return eid


D = "{{dicomUrl}}"
TOKEN_EP = synth(9000001, "auth: POST /oauth2/token", "post", "{{authUrl}}/oauth2/token")
GET_VAR = synth(9000002, "dicomweb: GET theo đường dẫn trong dữ liệu", "get", D + "{{req_path}}")
EP_PROJ_STUDIES = synth(9000003, "dicomweb: GET /projects/{projectId}/dicomweb/studies", "get", D + "/projects/{{projectId}}/dicomweb/studies")
EP_API_STUDIES = synth(9000004, "dicomweb: GET /api/dicomweb/studies", "get", D + "/api/dicomweb/studies")
EP_APIPROJ_STUDIES = synth(9000005, "dicomweb: GET /api/projects/{projectId}/dicomweb/studies", "get", D + "/api/projects/{{projectId}}/dicomweb/studies")
EP_OTHER_PROJ = synth(9000006, "dicomweb: GET /projects/khac/dicomweb/studies", "get", D + "/projects/qa-not-my-project/dicomweb/studies")
EP_UNKNOWN = synth(9000007, "dicomweb: GET /dicomweb/foo (route lạ)", "get", D + "/dicomweb/foo")
EP_DELETE_STUDY = synth(9000008, "dicomweb: DELETE /dicomweb/studies/{uid} (chưa cài)", "delete", D + "/dicomweb/studies/{{studyUid}}")
EP_OUTSIDE = synth(9000009, "ngoài tiền tố: GET /fhir/R4/Patient", "get", D + "/fhir/R4/Patient")
EP_POST_SERIES = synth(9000010, "dicomweb: POST /dicomweb/series (chưa cài)", "post", D + "/dicomweb/series")

# ---------------------------------------------------------------- DICOM JSON, multipart, UID
CT_CLASS = "1.2.840.10008.5.1.4.1.1.2"
SR_CLASS = "1.2.840.10008.5.1.4.1.1.88.11"
CRLF = "\r\n"
BND = "qa-bnd"
CT_META = f'multipart/related; type="application/dicom+json"; boundary={BND}'
CT_FILE = f'multipart/related; type="application/dicom"; boundary={BND}'
UIDBASE = "1.2.826.0.1.3680043.9.7777"


def el(vr, *vals):
    return {"vr": vr, "Value": list(vals)}


def meta(study, series, sop, *, cls=CT_CLASS, uri=None, frames=1, rows=2, pid="QA-PID", name="Qa^Test", date="20240102", modality="CT", acc=None):
    """DICOM JSON của một instance. uri=None: không có PixelData (kiểu báo cáo SR)."""
    d = {"00080016": el("UI", cls), "00080018": el("UI", sop), "00080060": el("CS", modality), "00080020": el("DA", date),
         "00100010": el("PN", {"Alphabetic": name}), "00100020": el("LO", pid), "0020000D": el("UI", study), "0020000E": el("UI", series)}
    if acc:
        d["00080050"] = el("SH", acc)
    if uri is not None:
        d.update({"00280002": el("US", 1), "00280004": el("CS", "MONOCHROME2"), "00280008": el("IS", str(frames)), "00280010": el("US", rows), "00280011": el("US", rows),
                  "00280100": el("US", 8), "00280101": el("US", 8), "00280102": el("US", 7), "00280103": el("US", 0), "7FE00010": {"vr": "OB", "BulkDataURI": uri}})
    return d


def dumps(x):
    s = json.dumps(x, ensure_ascii=False, separators=(",", ":"))
    return s.replace('"{{rows}}"', "{{rows}}")  # Rows/Columns là US: phải là số, không phải chuỗi


def part(headers, body):
    return f"--{BND}{CRLF}" + "".join(f"{k}: {v}{CRLF}" for k, v in headers.items()) + CRLF + body + CRLF


def stow_meta(metas, bulks):
    """metas: list dict; bulks: {uri: chuỗi byte}. Trả body multipart/related dạng metadata."""
    out = part({"Content-Type": "application/dicom+json"}, dumps(metas))
    for uri, data in bulks.items():
        out += part({"Content-Location": uri, "Content-Type": "application/octet-stream"}, data)
    return out + f"--{BND}--{CRLF}"


# ---------------------------------------------------------------- khung scenario
SCEN = {}


def step(name, endpoint, path, *, method=None, query=None, headers=None, body=None, body_type=None, form=None, auth="Bearer {{accessToken}}",
         asserts=None, scripts=None, extracts=None, custom=True):
    s = {"name": name, "endpointId": endpoint, "path": path, "auth": auth, "custom": custom}
    if method:
        s["method"] = method
    for k, v in (("query", query), ("headers", headers), ("body", body), ("bodyType", body_type), ("form", form)):
        if v:
            s[k] = v
    s["assertions"] = list(asserts or [])
    pp = [{"type": "extractor", "data": {"variableName": v, "expression": e, "subject": "responseJson"}} for v, e in (extracts or [])]
    pp += [{"type": "customScript", "data": {"script": js}} for js in (scripts or [])]
    if pp:
        s["postProcessors"] = pp
    return s


def scenario(slug, name, priority, description, steps, tags=()):
    SCEN[slug] = {"name": name, "priority": priority, "description": description, "steps": steps}
    (ROOT / "scenarios" / f"{slug}.design.json").write_text(json.dumps({"slug": slug, "name": name, "priority": priority, "steps": steps}, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    (ROOT / "scenarios" / f"{slug}.create.json").write_text(json.dumps({"name": name, "description": description, "folderId": 0, "priority": priority, "tags": ["dicomweb", *tags]}, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")


COLS = {}


def write_csv(slug, columns, rows):
    with (ROOT / "data" / f"{slug}.csv").open("w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=columns, extrasaction="raise", lineterminator="\n")
        w.writeheader()
        for r in rows:
            w.writerow({c: r.get(c, "") for c in columns})
    COLS[slug] = (len(rows), "csv")


def write_json(slug, rows):
    (ROOT / "data" / f"{slug}.json").write_text(json.dumps(rows, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    COLS[slug] = (len(rows), "json")


def row(cid, group, desc, status, **cols):
    r = {"case_id": cid, "case_group": group, "description": desc, "expected_status": str(status)}
    r.update(cols)
    return r


# ---------------------------------------------------------------- script dùng chung
def t(name, body):
    return f"pm.test({json.dumps(name, ensure_ascii=False)}, () => {{ {body} }});"


MSG_CHECK = ("const m = pm.iterationData.get('expected_message'); "
             "if (m) { pm.test('error = ' + m, () => pm.expect(pm.response.json().error).to.eql(m)); }")
CT_CHECK = ("const c = pm.iterationData.get('expected_ct'); "
            "if (c) { pm.test('Content-Type chứa ' + c, () => pm.expect(pm.response.headers.get('Content-Type') || '').to.include(c)); }")
BODY_CHECK = ("const b = pm.iterationData.get('expect_body'); "
              "if (b) { pm.test('body chứa ' + b, () => pm.expect(pm.response.text()).to.include(b)); }")
FAILED_CHECK = ("if (pm.iterationData.get('expect_failed') === '1') { pm.test('có FailedSOPSequence (00081198)', "
                "() => pm.expect(pm.response.json()['00081198']).to.be.an('object')); }")

TOKEN_SCRIPT = (
    "const tok = pm.response.json().access_token; pm.variables.set('accessToken', tok);\n"
    "try { const p = JSON.parse(atob(tok.split('.')[1].replace(/-/g, '+').replace(/_/g, '/'))); pm.variables.set('projectId', p.fhir_project || ''); } catch (e) { pm.variables.set('projectId', ''); }\n"
    "const base = '" + UIDBASE + ".' + Date.now() + (100 + Math.floor(Math.random() * 900));\n"
    "pm.variables.set('studyUid', base + '.1'); pm.variables.set('seriesUid', base + '.1.1'); pm.variables.set('sop1', base + '.1.1.1'); pm.variables.set('sop2', base + '.1.1.2');\n"
    "pm.variables.set('series2Uid', base + '.1.2'); pm.variables.set('sop3', base + '.1.2.1'); pm.variables.set('otherStudyUid', base + '.9');\n"
    "pm.variables.set('pid', 'QA-' + Date.now());"
)


def token_step(extra_script=""):
    return step("Lấy access token (client_credentials của auth)", TOKEN_EP, "{{authUrl}}/oauth2/token", auth="none",
                form={"grant_type": "client_credentials", "client_id": "{{clientId}}", "client_secret": "{{clientSecret}}"},
                asserts=["status == 200", "$.access_token exists"], scripts=[TOKEN_SCRIPT + extra_script])


STOW = ep("post", "/dicomweb/studies")
STOW_STUDY = ep("post", "/dicomweb/studies/{studyUid}")
QIDO_STUDIES, QIDO_SERIES, QIDO_INST = ep("get", "/dicomweb/studies"), ep("get", "/dicomweb/series"), ep("get", "/dicomweb/instances")
Q_ST_SERIES, Q_ST_INST = ep("get", "/dicomweb/studies/{studyUid}/series"), ep("get", "/dicomweb/studies/{studyUid}/instances")
Q_SE_INST = ep("get", "/dicomweb/studies/{studyUid}/series/{seriesUid}/instances")
W_STUDY, W_SERIES = ep("get", "/dicomweb/studies/{studyUid}"), ep("get", "/dicomweb/studies/{studyUid}/series/{seriesUid}")
W_INST = ep("get", "/dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}")
W_META_ST, W_META_SE = ep("get", "/dicomweb/studies/{studyUid}/metadata"), ep("get", "/dicomweb/studies/{studyUid}/series/{seriesUid}/metadata")
W_META_INST = ep("get", "/dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/metadata")
W_FRAMES = ep("get", "/dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame}")
W_BULK = ep("get", "/dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/bulkdata/{tag}")
W_REND = ep("get", "/dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/rendered")
W_FREND = ep("get", "/dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame}/rendered")
WADO_URI = ep("get", "/dicomweb/wado")
INST = "/instances/{{sop1}}"
SE = "/dicomweb/studies/{{studyUid}}/series/{{seriesUid}}"

# =============================================================================== 1. health
scenario("dicomweb-health", "DICOMweb: health smoke", 0,
         "Bốn probe không cần token: /health/live, /health/ready, /health/startup, /healthz (200, thân JSON, không bị hạn mức IP).",
         [step(n, ep("get", p), p, auth="none", custom=False, asserts=["status == 200"])
          for n, p in (("GET /health/live", "/health/live"), ("GET /health/ready", "/health/ready"), ("GET /health/startup", "/health/startup"), ("GET /healthz", "/healthz"))])
write_csv("dicomweb-health", ["case_id", "case_group", "description", "expected_status"],
          [row("HL-01", "Positive", "Bốn probe trả 200 không cần Authorization", 200)])

# =============================================================================== 2. lifecycle
HDR_META = {"Content-Type": CT_META}
life_meta = [meta("{{studyUid}}", "{{seriesUid}}", "{{sop1}}", uri="urn:qa:px1", frames="{{frames}}", rows="{{rows}}", pid="{{pid}}", name="Qa^Lifecycle", acc="ACC-LIFE"),
             meta("{{studyUid}}", "{{seriesUid}}", "{{sop2}}", uri="urn:qa:px2", frames=1, rows="{{rows}}", pid="{{pid}}", name="Qa^Lifecycle")]
STOW_LIFE = stow_meta(life_meta, {"urn:qa:px1": "{{px}}", "urn:qa:px2": "{{f1}}"})
# một phần: một instance SR hợp lệ (series 2, không PixelData) + một instance UID sai -> 202
bad_meta = meta("{{studyUid}}", "{{series2Uid}}", "abc", pid="{{pid}}")
STOW_PARTIAL = stow_meta([meta("{{studyUid}}", "{{series2Uid}}", "{{sop3}}", cls=SR_CLASS, modality="SR", pid="{{pid}}", name="Qa^Lifecycle"), bad_meta], {})
# vào study khác (A900): cùng instance sop1 nhưng URL là study khác
STOW_BODY_OTHER = stow_meta([meta("{{studyUid}}", "{{seriesUid}}", "{{sop2}}", uri="urn:qa:px2", frames=1, rows="{{rows}}", pid="{{pid}}")], {"urn:qa:px2": "{{f1}}"})

J = "pm.response.json()"
life = [
    token_step(),
    step("STOW-RS dạng metadata: 2 instance cùng study/series (200)", STOW, "/dicomweb/studies", method="post", headers=HDR_META, body=STOW_LIFE, body_type="raw",
         asserts=["status == 200"],
         scripts=[t("Content-Type dicom+json", "pm.expect(pm.response.headers.get('Content-Type')).to.include('application/dicom+json');"),
                  t("ReferencedSOPSequence có 2 phần tử, không có FailedSOPSequence", f"const j = {J}; pm.expect(j['00081199'].Value.length).to.eql(2); pm.expect(j['00081198']).to.eql(undefined);"),
                  t("RetrieveURL cấp study trỏ đúng studyUid", f"pm.expect({J}['00081190'].Value[0]).to.include(pm.variables.get('studyUid'));")]),
    step("STOW-RS gửi lại cùng instance: idempotent (200)", STOW, "/dicomweb/studies", method="post", headers=HDR_META, body=STOW_LIFE, body_type="raw",
         asserts=["status == 200"], scripts=[t("vẫn 2 instance tham chiếu", f"pm.expect({J}['00081199'].Value.length).to.eql(2);")]),
    step("STOW-RS vào study đúng của URL (200)", STOW_STUDY, "/dicomweb/studies/{{studyUid}}", method="post", headers=HDR_META, body=STOW_BODY_OTHER, body_type="raw",
         asserts=["status == 200"]),
    step("STOW-RS vào study KHÁC của URL: A900 (409)", STOW_STUDY, "/dicomweb/studies/{{otherStudyUid}}", method="post", headers=HDR_META, body=STOW_BODY_OTHER, body_type="raw",
         asserts=["status == 409"],
         scripts=[t("FailureReason 43264 (0xA900) khi instance chưa có... hoặc đã có thì idempotent",
                    f"const f = {J}['00081198']; if (f) {{ pm.expect(f.Value[0]['00081197'].Value[0]).to.eql(43264); }}")]),
    step("STOW-RS một phần: 1 SR hợp lệ + 1 UID sai (202)", STOW, "/dicomweb/studies", method="post", headers=HDR_META, body=STOW_PARTIAL, body_type="raw",
         asserts=["status == 202"],
         scripts=[t("có cả Referenced (1) và Failed (1)", f"const j = {J}; pm.expect(j['00081199'].Value.length).to.eql(1); pm.expect(j['00081198'].Value.length).to.eql(1);")]),
    step("QIDO study theo StudyInstanceUID", QIDO_STUDIES, "/dicomweb/studies", query={"StudyInstanceUID": "{{studyUid}}"}, asserts=["status == 200"],
         scripts=[t("đúng 1 study, đúng UID", f"const j = {J}; pm.expect(j.length).to.eql(1); pm.expect(j[0]['0020000D'].Value[0]).to.eql(pm.variables.get('studyUid'));"),
                  t("Content-Type dicom+json", "pm.expect(pm.response.headers.get('Content-Type')).to.include('application/dicom+json');")]),
    step("QIDO study theo PatientID + tham số không hỗ trợ (Warning 299)", QIDO_STUDIES, "/dicomweb/studies", query={"PatientID": "{{pid}}", "foo": "1"}, asserts=["status == 200"],
         scripts=[t("Warning nêu tham số bị bỏ qua", "pm.expect(pm.response.headers.get('Warning') || '').to.include('Ignored unsupported query parameters: foo');"),
                  t("1 study", f"pm.expect({J}.length).to.eql(1);")]),
    step("QIDO series của study (2 series)", Q_ST_SERIES, "/dicomweb/studies/{{studyUid}}/series", asserts=["status == 200"],
         scripts=[t("2 series", f"pm.expect({J}.length).to.eql(2);")]),
    step("QIDO series toàn project theo SeriesInstanceUID", QIDO_SERIES, "/dicomweb/series", query={"SeriesInstanceUID": "{{seriesUid}}"}, asserts=["status == 200"],
         scripts=[t("1 series", f"pm.expect({J}.length).to.eql(1);")]),
    step("QIDO instance của series (2 instance)", Q_SE_INST, SE + "/instances", asserts=["status == 200"], scripts=[t("2 instance", f"pm.expect({J}.length).to.eql(2);")]),
    step("QIDO instance của study (3 instance)", Q_ST_INST, "/dicomweb/studies/{{studyUid}}/instances", asserts=["status == 200"], scripts=[t("3 instance", f"pm.expect({J}.length).to.eql(3);")]),
    step("QIDO instance theo SOPInstanceUID", QIDO_INST, "/dicomweb/instances", query={"SOPInstanceUID": "{{sop1}}"}, asserts=["status == 200"],
         scripts=[t("1 instance", f"pm.expect({J}.length).to.eql(1);")]),
    step("QIDO phân trang: limit=1 còn trang sau (Warning 299)", Q_SE_INST, SE + "/instances", query={"limit": "1"}, asserts=["status == 200"],
         scripts=[t("1 phần tử + Warning có trang sau", f"pm.expect({J}.length).to.eql(1); pm.expect(pm.response.headers.get('Warning') || '').to.include('additional results');")]),
    step("WADO-RS metadata của study", W_META_ST, "/dicomweb/studies/{{studyUid}}/metadata", asserts=["status == 200"], scripts=[t("có StudyInstanceUID", "pm.expect(pm.response.text()).to.include('0020000D');")]),
    step("WADO-RS metadata của series", W_META_SE, SE + "/metadata", asserts=["status == 200"]),
    step("WADO-RS metadata của instance", W_META_INST, SE + INST + "/metadata", asserts=["status == 200"],
         scripts=[t("có SOPInstanceUID", "pm.expect(pm.response.text()).to.include(pm.variables.get('sop1'));")]),
    step("WADO-RS tải instance (Part 10 trong multipart)", W_INST, SE + INST, asserts=["status == 200"],
         scripts=[t("multipart/related, có DICM", "pm.expect(pm.response.headers.get('Content-Type')).to.include('multipart/related'); pm.expect(pm.response.text()).to.include('DICM');")]),
    step("WADO-RS tải series", W_SERIES, SE, asserts=["status == 200"], scripts=[t("multipart/related", "pm.expect(pm.response.headers.get('Content-Type')).to.include('multipart/related');")]),
    step("WADO-RS tải study", W_STUDY, "/dicomweb/studies/{{studyUid}}", asserts=["status == 200"], scripts=[t("multipart/related", "pm.expect(pm.response.headers.get('Content-Type')).to.include('multipart/related');")]),
    step("WADO-RS frame 2: đúng byte đã STOW", W_FRAMES, SE + INST + "/frames/2", asserts=["status == 200"],
         scripts=[t("body chứa frame 2", "pm.expect(pm.response.text()).to.include(pm.iterationData.get('f2'));"),
                  t("multipart/related", "pm.expect(pm.response.headers.get('Content-Type')).to.include('multipart/related');")]),
    step("WADO-RS frames 2,1: đúng thứ tự yêu cầu", W_FRAMES, SE + INST + "/frames/2,1", asserts=["status == 200"],
         scripts=[t("frame 2 đứng trước frame 1", "const b = pm.response.text(); pm.expect(b.indexOf(pm.iterationData.get('f2'))).to.be.above(-1); pm.expect(b.indexOf(pm.iterationData.get('f2'))).to.be.below(b.indexOf(pm.iterationData.get('f1')));")]),
    step("WADO-RS bulkdata PixelData: đúng toàn bộ byte", W_BULK, SE + INST + "/bulkdata/7FE00010", asserts=["status == 200"],
         scripts=[t("body chứa pixel đã gửi", "pm.expect(pm.response.text()).to.include(pm.iterationData.get('px'));")]),
    step("WADO-RS rendered (mặc định JPEG)", W_REND, SE + INST + "/rendered", asserts=["status == 200"],
         scripts=[t("image/jpeg", "pm.expect(pm.response.headers.get('Content-Type')).to.include('image/jpeg');")]),
    step("WADO-RS rendered PNG (Accept image/png)", W_REND, SE + INST + "/rendered", headers={"Accept": "image/png"}, asserts=["status == 200"],
         scripts=[t("image/png", "pm.expect(pm.response.headers.get('Content-Type')).to.include('image/png');")]),
    step("WADO-RS frame 2 rendered PNG", W_FREND, SE + INST + "/frames/2/rendered", headers={"Accept": "image/png"}, asserts=["status == 200"]),
    step("WADO-URI tệp nguyên văn (application/dicom)", WADO_URI, "/dicomweb/wado",
         query={"requestType": "WADO", "studyUID": "{{studyUid}}", "seriesUID": "{{seriesUid}}", "objectUID": "{{sop1}}", "contentType": "application/dicom"}, asserts=["status == 200"],
         scripts=[t("application/dicom, có DICM", "pm.expect(pm.response.headers.get('Content-Type')).to.include('application/dicom'); pm.expect(pm.response.text()).to.include('DICM');")]),
    step("WADO-URI ảnh PNG frame 2", WADO_URI, "/dicomweb/wado",
         query={"requestType": "WADO", "studyUID": "{{studyUid}}", "seriesUID": "{{seriesUid}}", "objectUID": "{{sop1}}", "contentType": "image/png", "frameNumber": "2"}, asserts=["status == 200"],
         scripts=[t("image/png", "pm.expect(pm.response.headers.get('Content-Type')).to.include('image/png');")]),
    step("Instance SR (không ảnh): frame -> 404 Pixel data not found", W_FRAMES, "/dicomweb/studies/{{studyUid}}/series/{{series2Uid}}/instances/{{sop3}}/frames/1", asserts=["status == 404"],
         scripts=[t("thông điệp", f"pm.expect({J}.error).to.eql('Pixel data not found for instance');")]),
]
scenario("dicomweb-lifecycle", "DICOMweb: lifecycle STOW → QIDO → WADO", 0,
         "Luồng chính: token → STOW-RS dạng metadata (200, idempotent, study sai 409, một phần 202) → QIDO 4 cấp + phân trang + Warning → WADO-RS metadata/tải/frame/bulkdata/rendered → WADO-URI. "
         "Pixel là chuỗi ASCII nên so khớp từng byte. Mỗi dòng dữ liệu = một hình dạng ảnh (số frame, kích thước).", life, tags=["lifecycle"])
write_csv("dicomweb-lifecycle", ["case_id", "case_group", "description", "expected_status", "frames", "rows", "px", "f1", "f2"], [
    row("LC-01", "Positive", "3 frame 2x2 8 bit (12 byte)", 200, frames=3, rows=2, px="ABCDEFGHIJKL", f1="ABCD", f2="EFGH"),
    row("LC-02", "Boundary", "2 frame 3x3 8 bit (18 byte), kích thước ảnh khác", 200, frames=2, rows=3, px="abcdefghiJKLMNOPQR", f1="abcdefghi", f2="JKLMNOPQR"),
])

# =============================================================================== 3. STOW validation (dữ liệu quyết định cả Content-Type lẫn body)
OK1 = lambda sop="{{sop}}", uri="urn:qa:v": meta("{{studyUid}}", "{{seriesUid}}", sop, uri=uri, frames=1, rows=2, pid="QA-STOWVAL")  # noqa: E731
GOOD_UID_BASE = UIDBASE + ".400"


def vbody(metas, bulks=None, raw_first=None, ct_first="application/dicom+json", extra=""):
    first = raw_first if raw_first is not None else dumps(metas)
    return part({"Content-Type": ct_first}, first) + extra + f"--{BND}--{CRLF}"


def m1(sop, study=GOOD_UID_BASE + ".1", series=GOOD_UID_BASE + ".1.1", uri="urn:qa:v", cls=CT_CLASS, px=True):
    d = meta(study, series, sop, uri=uri if px else None, frames=1, rows=2, pid="QA-STOWVAL", cls=cls)
    return d


bulk_ok = part({"Content-Location": "urn:qa:v", "Content-Type": "application/octet-stream"}, "ABCD")
no_cls = m1(GOOD_UID_BASE + ".1.1.7")
del no_cls["00080016"]
sop64 = GOOD_UID_BASE + ".1.1." + "9" * (64 - len(GOOD_UID_BASE + ".1.1."))
sop65 = sop64 + "9"
assert len(sop64) == 64 and len(sop65) == 65
sv = []


def vrow(cid, group, desc, status, ct, body, msg="", failed=""):
    sv.append(row(cid, group, desc, status, ct=ct, raw_body=body, expected_message=msg, expect_failed=failed))


vrow("SV-01", "Negative", "Content-Type không phải multipart/related (application/json)", 415, "application/json", "{}", "Cần Content-Type multipart/related")
vrow("SV-02", "Negative", "multipart/related thiếu boundary", 400, "multipart/related", "x", "Thiếu boundary")
vrow("SV-03", "Negative", "multipart/related nhưng thân không phải multipart", 400, CT_META, "đây không phải multipart")
vrow("SV-04", "Negative", "multipart hợp lệ nhưng không có phần nào (chỉ boundary đóng)", 400, CT_META, f"--{BND}--{CRLF}", "Yêu cầu không có instance nào")
vrow("SV-05", "Negative", "Metadata JSON hỏng ({oops)", 400, CT_META, vbody(None, raw_first='[{"oops"'))
vrow("SV-06", "Negative", "Phần metadata rỗng", 400, CT_META, vbody(None, raw_first=""))
vrow("SV-07", "Negative", "Metadata là mảng rỗng []", 400, CT_META, vbody(None, raw_first="[]"))
vrow("SV-08", "Negative", "Phần bulkdata thiếu Content-Location và Content-ID", 400, CT_META,
     vbody([m1(GOOD_UID_BASE + ".1.1.1")], extra=part({"Content-Type": "application/octet-stream"}, "ABCD")), "Phần bulkdata thiếu Content-Location hoặc Content-ID")
vrow("SV-09", "Negative", "SOPInstanceUID không phải UID (abc) -> 409 FailedSOPSequence", 409, CT_META, vbody([m1("abc")], extra=bulk_ok), failed="1")
vrow("SV-10", "Negative", "Thiếu SOPClassUID (0008,0016) -> 409", 409, CT_META, vbody([no_cls], extra=bulk_ok), failed="1")
vrow("SV-11", "Negative", "PixelData tham chiếu bulkdata không có trong yêu cầu -> 409", 409, CT_META, vbody([m1(GOOD_UID_BASE + ".1.1.2", uri="urn:qa:thieu")], extra=bulk_ok), failed="1")
vrow("SV-12", "Negative", "UID có chữ cái giữa chừng (1.2.826.abc) -> 409", 409, CT_META, vbody([m1("1.2.826.abc")], extra=bulk_ok), failed="1")
vrow("SV-13", "Boundary", "SOPInstanceUID dài 65 ký tự (vượt trần 64) -> 409", 409, CT_META, vbody([m1(sop65)], extra=bulk_ok), failed="1")
vrow("SV-14", "Boundary", "SOPInstanceUID dài đúng 64 ký tự -> 200 (cố định nên chạy lại vẫn 200)", 200, CT_META, vbody([m1(sop64)], extra=bulk_ok))
vrow("SV-15", "Negative", "Dạng tệp: phần đầu Content-Type text/plain -> 409 (0xC000)", 409, CT_FILE, part({"Content-Type": "text/plain"}, "xin chào"), failed="1")
vrow("SV-16", "Negative", "Dạng tệp: application/dicom nhưng nội dung không phải DICOM -> 409", 409, CT_FILE, part({"Content-Type": "application/dicom"}, "đây không phải tệp DICOM"), failed="1")
vrow("SV-17", "Security", "Content-Type chèn thêm tham số lạ + boundary dài 70 ký tự, không có phần -> 400 không 5xx", 400,
     'multipart/related; type="application/dicom+json"; boundary=' + "b" * 70, f"--{'b' * 70}--{CRLF}", "Yêu cầu không có instance nào")
vrow("SV-18", "Security", "Tên Content-Location chứa chuỗi giống lệnh ('; DROP TABLE) -> xử lý như khóa thường, không 5xx", 200, CT_META,
     vbody([m1(GOOD_UID_BASE + ".1.1.3", uri="x'; DROP TABLE--")], extra=part({"Content-Location": "x'; DROP TABLE--", "Content-Type": "application/octet-stream"}, "ABCD")))
write_json("dicomweb-stow-validation", sv)
scenario("dicomweb-stow-validation", "DICOMweb: STOW-RS validation (data-driven)", 1,
         "Một bước STOW-RS theo từng dòng: Content-Type, boundary, thân multipart, metadata JSON, bulkdata, UID (rỗng/sai/65 ký tự/đúng 64), dạng tệp không phải DICOM. "
         "Lỗi cả yêu cầu: 415/400; lỗi từng instance: 409 + FailedSOPSequence.",
         [token_step(),
          step("STOW-RS theo dữ liệu", STOW, "/dicomweb/studies", method="post", headers={"Content-Type": "{{ct}}"}, body="{{raw_body}}", body_type="raw",
               asserts=["status == {{expected_status}}"], scripts=[MSG_CHECK, FAILED_CHECK])], tags=["validation"])

# =============================================================================== 4. QIDO matrix
QS1 = UIDBASE + ".300.1"
QS2 = UIDBASE + ".300.2"
qm = [meta(QS1, QS1 + ".1", QS1 + ".1.1", uri="urn:qa:q1", rows=2, pid="QA-QIDO-1", name="Qa^Alpha", date="20240102", modality="CT", acc="ACC-1"),
      meta(QS2, QS2 + ".1", QS2 + ".1.1", uri="urn:qa:q2", rows=2, pid="QA-QIDO-2", name="Qa^Beta", date="20240215", modality="MR", acc="ACC-2")]
QIDO_SEED = stow_meta(qm, {"urn:qa:q1": "ABCD", "urn:qa:q2": "EFGH"})
BOTH = "QA-QIDO-1\\QA-QIDO-2"
qcols = ["case_id", "case_group", "description", "expected_status", "PatientID", "PatientName", "StudyDate", "ModalitiesInStudy", "AccessionNumber", "limit", "offset", "tag_hex", "expected_count", "expect_warning", "expected_message"]
qr = [
    row("QD-01", "Positive", "PatientID khớp chính xác 1 study", 200, PatientID="QA-QIDO-1", expected_count=1),
    row("QD-02", "Positive", "Nhiều PatientID ngăn bằng \\ là OR", 200, PatientID=BOTH, expected_count=2),
    row("QD-03", "Positive", "Nhiều PatientID ngăn bằng dấu phẩy là OR", 200, PatientID="QA-QIDO-1,QA-QIDO-2", expected_count=2),
    row("QD-04", "Positive", "PatientName tiền tố có * (AND với PatientID)", 200, PatientID=BOTH, PatientName="Qa*", expected_count=2),
    row("QD-05", "Positive", "PatientName tiền tố không phân biệt hoa thường", 200, PatientID=BOTH, PatientName="qa^al", expected_count=1),
    row("QD-06", "Positive", "StudyDate một ngày", 200, PatientID=BOTH, StudyDate="20240102", expected_count=1),
    row("QD-07", "Positive", "StudyDate khoảng 20240101-20240131", 200, PatientID=BOTH, StudyDate="20240101-20240131", expected_count=1),
    row("QD-08", "Positive", "StudyDate mở cuối 20240101-", 200, PatientID=BOTH, StudyDate="20240101-", expected_count=2),
    row("QD-09", "Positive", "StudyDate mở đầu -20240131", 200, PatientID=BOTH, StudyDate="-20240131", expected_count=1),
    row("QD-10", "Positive", "ModalitiesInStudy=MR", 200, PatientID=BOTH, ModalitiesInStudy="MR", expected_count=1),
    row("QD-11", "Positive", "AccessionNumber chính xác", 200, AccessionNumber="ACC-2", PatientID=BOTH, expected_count=1),
    row("QD-12", "Positive", "Thuộc tính viết bằng tag 00100020 thay cho từ khóa", 200, tag_hex="QA-QIDO-1", expected_count=1),
    row("QD-13", "Positive", "Không khớp: 200 với mảng rỗng (không 204)", 200, PatientID="QA-QIDO-KHONG-CO", expected_count=0),
    row("QD-14", "Positive", "offset=1 limit=1 lấy phần tử thứ 2", 200, PatientID=BOTH, limit=1, offset=1, expected_count=1),
    row("QD-15", "Boundary", "limit=1 còn trang sau: Warning 299", 200, PatientID=BOTH, limit=1, expected_count=1, expect_warning="more"),
    row("QD-16", "Boundary", "limit=2 đúng bằng số còn lại: không Warning trang sau", 200, PatientID=BOTH, limit=2, expected_count=2, expect_warning="none"),
    row("QD-17", "Boundary", "limit=0 dùng mặc định (không 400)", 200, PatientID=BOTH, limit=0, expected_count=2),
    row("QD-18", "Boundary", "limit=1001 bị kẹp 1000 (không 400)", 200, PatientID=BOTH, limit=1001, expected_count=2),
    row("QD-19", "Boundary", "offset vượt tổng số: mảng rỗng", 200, PatientID=BOTH, offset=5, expected_count=0),
    row("QD-20", "Boundary", "limit không phải số (abc) dùng mặc định", 200, PatientID=BOTH, limit="abc", expected_count=2),
    row("QD-21", "Boundary", "offset âm (-3) dùng 0", 200, PatientID=BOTH, offset="-3", expected_count=2),
    row("QD-22", "Negative", "StudyDate có gạch ngang kiểu ISO (2024-01-02)", 400, StudyDate="2024-01-02", expected_message="StudyDate: ngày phải là YYYYMMDD hợp lệ"),
    row("QD-23", "Negative", "StudyDate không có thật (20240230)", 400, StudyDate="20240230", expected_message="StudyDate: ngày phải là YYYYMMDD hợp lệ"),
    row("QD-24", "Negative", "StudyDate chỉ là dấu -", 400, StudyDate="-", expected_message="StudyDate: khoảng ngày rỗng"),
    row("QD-25", "Negative", "StudyDate nhiều giá trị", 400, StudyDate="20240101-20240102\\20240201-", expected_message="StudyDate: chỉ nhận một ngày hoặc một khoảng ngày"),
    row("QD-26", "Negative", "PatientName có * ở giữa", 400, PatientName="ng*en", expected_message="PatientName: ký tự đại diện chỉ được dùng ở cuối giá trị"),
    row("QD-27", "Negative", "PatientName có ? ở giữa", 400, PatientName="n?uyen", expected_message="PatientName: ký tự đại diện chỉ được dùng ở cuối giá trị"),
    row("QD-28", "Security", "PatientID chứa chuỗi SQL: coi như giá trị thường, không khớp, không 5xx", 200, PatientID="x' OR '1'='1' --", expected_count=0),
    row("QD-29", "Security", "PatientID chứa thẻ script: không khớp, không 5xx", 200, PatientID="<script>alert(1)</script>", expected_count=0),
    row("QD-30", "Security", "PatientID rất dài (2000 ký tự): không 5xx", 200, PatientID="Z" * 2000, expected_count=0),
]
write_csv("dicomweb-qido-matrix", qcols, qr)
QWARN = ("const w = pm.iterationData.get('expect_warning'); const h = pm.response.headers.get('Warning') || ''; "
         "if (w === 'more') { pm.test('Warning: còn trang sau', () => pm.expect(h).to.include('additional results')); } "
         "if (w === 'none') { pm.test('không Warning trang sau', () => pm.expect(h).to.not.include('additional results')); }")
QCOUNT = ("const n = pm.iterationData.get('expected_count'); if (n !== '' && n !== undefined && pm.response.code === 200) "
          "{ pm.test('số kết quả = ' + n, () => pm.expect(pm.response.json().length).to.eql(parseInt(n))); }")
scenario("dicomweb-qido-matrix", "DICOMweb: QIDO-RS /studies (data-driven)", 1,
         "Seed 2 study cố định bằng STOW-RS (idempotent) rồi tìm study theo từng tổ hợp: khớp chính xác, OR, tiền tố, khoảng ngày, modality, tag hex, phân trang, "
         "giá trị sai (400), chuỗi tấn công. Mỗi dòng chạy lại bước seed (200 vì idempotent).",
         [token_step(),
          step("Seed: STOW 2 study cố định", STOW, "/dicomweb/studies", method="post", headers=HDR_META, body=QIDO_SEED, body_type="raw", asserts=["status == 200"]),
          step("QIDO /studies theo dữ liệu", QIDO_STUDIES, "/dicomweb/studies",
               query={"PatientID": "{{PatientID}}", "PatientName": "{{PatientName}}", "StudyDate": "{{StudyDate}}", "ModalitiesInStudy": "{{ModalitiesInStudy}}",
                      "AccessionNumber": "{{AccessionNumber}}", "limit": "{{limit}}", "offset": "{{offset}}", "00100020": "{{tag_hex}}"},
               asserts=["status == {{expected_status}}"], scripts=[MSG_CHECK, QCOUNT, QWARN])], tags=["qido"])

# =============================================================================== 5. retrieve matrix (WADO-RS, WADO-URI, 404/400/416/406/501)
FS = UIDBASE + ".200.1"
FSE, FI = FS + ".1", FS + ".1.1"
SRSE, SRI = FS + ".2", FS + ".2.1"
RM_SEED = stow_meta([meta(FS, FSE, FI, uri="urn:qa:rm", frames=3, rows=2, pid="QA-RETRIEVE", name="Qa^Retrieve"),
                     meta(FS, SRSE, SRI, cls=SR_CLASS, modality="SR", pid="QA-RETRIEVE", name="Qa^Retrieve")], {"urn:qa:rm": "ABCDEFGHIJKL"})
S_ = f"/dicomweb/studies/{FS}"
I_ = f"{S_}/series/{FSE}/instances/{FI}"
SR_ = f"{S_}/series/{SRSE}/instances/{SRI}"
WURI = f"/dicomweb/wado?requestType=WADO&studyUID={FS}&seriesUID={FSE}&objectUID={FI}"
WURI_SR = f"/dicomweb/wado?requestType=WADO&studyUID={FS}&seriesUID={SRSE}&objectUID={SRI}"
rcols = ["case_id", "case_group", "description", "expected_status", "req_path", "accept", "expected_ct", "expect_body", "expected_message"]
R = []


def rm(cid, g, desc, status, path, accept="", ct="", body="", msg=""):
    R.append(row(cid, g, desc, status, req_path=path, accept=accept, expected_ct=ct, expect_body=body, expected_message=msg))


rm("RM-01", "Positive", "Frame 1 (multipart/related, đúng byte ABCD)", 200, I_ + "/frames/1", ct="multipart/related", body="ABCD")
rm("RM-02", "Positive", "Frames 2,1 -> có EFGH", 200, I_ + "/frames/2,1", ct="multipart/related", body="EFGH")
rm("RM-03", "Boundary", "50 frame trong một yêu cầu (giới hạn trên)", 200, I_ + "/frames/" + ",".join(["1"] * 50), ct="multipart/related", body="ABCD")
rm("RM-04", "Boundary", "Số frame 9 chữ số (000000001) vẫn hợp lệ", 200, I_ + "/frames/000000001", ct="multipart/related", body="ABCD")
rm("RM-05", "Positive", "Bulkdata PixelData: toàn bộ 12 byte", 200, I_ + "/bulkdata/7FE00010", ct="multipart/related", body="ABCDEFGHIJKL")
rm("RM-06", "Positive", "Bulkdata tag viết thường 7fe00010", 200, I_ + "/bulkdata/7fe00010", ct="multipart/related", body="ABCDEFGHIJKL")
rm("RM-07", "Positive", "Rendered không Accept -> image/jpeg", 200, I_ + "/rendered", ct="image/jpeg")
rm("RM-08", "Positive", "Rendered Accept image/png", 200, I_ + "/rendered", accept="image/png", ct="image/png")
rm("RM-09", "Positive", "Accept có q: image/png;q=0.5, image/jpeg -> JPEG", 200, I_ + "/rendered", accept="image/png;q=0.5, image/jpeg", ct="image/jpeg")
rm("RM-10", "Positive", "Accept */*;q=0.1, image/png -> PNG", 200, I_ + "/rendered", accept="*/*;q=0.1, image/png", ct="image/png")
rm("RM-11", "Positive", "Rendered với window=128,256", 200, I_ + "/rendered?window=128,256", accept="image/png", ct="image/png")
rm("RM-12", "Boundary", "Rendered quality=1 (biên dưới)", 200, I_ + "/rendered?quality=1", ct="image/jpeg")
rm("RM-13", "Boundary", "Rendered quality=100 (biên trên)", 200, I_ + "/rendered?quality=100", ct="image/jpeg")
rm("RM-14", "Positive", "Frame 2 rendered PNG", 200, I_ + "/frames/2/rendered", accept="image/png", ct="image/png")
rm("RM-15", "Positive", "WADO-URI tệp nguyên văn", 200, WURI + "&contentType=application/dicom", ct="application/dicom", body="DICM")
rm("RM-16", "Positive", "WADO-URI mặc định -> image/jpeg", 200, WURI, ct="image/jpeg")
rm("RM-17", "Positive", "WADO-URI PNG frame 2 + cửa sổ", 200, WURI + "&contentType=image/png&frameNumber=2&windowCenter=128&windowWidth=256", ct="image/png")
rm("RM-18", "Positive", "WADO-URI contentType=image/jpeg,application/dicom;q=0.5 chọn JPEG", 200, WURI + "&contentType=image/jpeg,application/dicom;q=0.5", ct="image/jpeg")
rm("RM-19", "Positive", "Tải instance (Part 10 trong multipart)", 200, I_, ct="multipart/related", body="DICM")
rm("RM-20", "Positive", "Tải series", 200, f"{S_}/series/{FSE}", ct="multipart/related")
rm("RM-21", "Positive", "Tải study", 200, S_, ct="multipart/related")
rm("RM-22", "Positive", "Metadata study", 200, S_ + "/metadata", body="0020000D")
rm("RM-23", "Positive", "Metadata series", 200, f"{S_}/series/{FSE}/metadata", body="0020000E")
rm("RM-24", "Positive", "Metadata instance", 200, I_ + "/metadata", body=FI)
rm("RM-25", "Positive", "QIDO series của study (2 series)", 200, S_ + "/series", body=SRSE)
rm("RM-26", "Positive", "QIDO instance của study", 200, S_ + "/instances", body=SRI)
rm("RM-27", "Positive", "QIDO instance của series", 200, f"{S_}/series/{FSE}/instances", body=FI)
rm("RM-28", "Positive", "WADO-URI tệp của instance SR (không ảnh) vẫn tải được", 200, WURI_SR + "&contentType=application/dicom", ct="application/dicom", body="DICM")
# ---- 404
rm("RM-30", "Negative", "Metadata study không có", 404, "/dicomweb/studies/1.2.3.4/metadata", msg="Study not found")
rm("RM-31", "Negative", "Metadata series không thuộc study", 404, f"{S_}/series/1.2.3.4/metadata", msg="Series not found")
rm("RM-32", "Negative", "Metadata instance không thuộc series", 404, f"{S_}/series/{FSE}/instances/1.2.3.4/metadata", msg="Instance not found")
rm("RM-33", "Negative", "Tải study không có", 404, "/dicomweb/studies/1.2.3.4", msg="Study not found")
rm("RM-34", "Negative", "Frame của study không có", 404, "/dicomweb/studies/1.2.3.4/series/1.2/instances/1.2.3/frames/1", msg="Study not found")
rm("RM-35", "Negative", "QIDO series của study không có", 404, "/dicomweb/studies/1.2.3.4/series", msg="Study not found")
rm("RM-36", "Negative", "QIDO instance của series không có", 404, f"{S_}/series/1.2.3.4/instances", msg="Series not found")
rm("RM-37", "Negative", "Frame của instance SR (không ảnh)", 404, SR_ + "/frames/1", msg="Pixel data not found for instance")
rm("RM-38", "Negative", "Bulkdata PixelData của instance SR", 404, SR_ + "/bulkdata/7FE00010", msg="Pixel data not found for instance")
rm("RM-39", "Negative", "Bulkdata tag khác PixelData (00080018)", 404, I_ + "/bulkdata/00080018", msg="Bulk data not found")
rm("RM-40", "Negative", "Rendered của instance SR", 404, SR_ + "/rendered", msg="Pixel data not found for instance")
rm("RM-41", "Negative", "WADO-URI object không có", 404, f"/dicomweb/wado?requestType=WADO&studyUID={FS}&seriesUID={FSE}&objectUID=1.2.3.4", msg="Instance not found")
rm("RM-42", "Negative", "WADO-URI ảnh của instance SR", 404, WURI_SR + "&contentType=image/png", msg="Pixel data not found for instance")
# ---- 400 / 416 / 406 / 501
for cid, fr, why in (("RM-50", "0", "frame 0"), ("RM-51", "abc", "frame chữ"), ("RM-52", "1,,2", "danh sách có phần tử rỗng"), ("RM-53", "-1", "frame âm"), ("RM-54", "01x", "chữ số lẫn chữ cái"),
                     ("RM-55", "1000000000", "10 chữ số (vượt 9 ký tự)")):
    rm(cid, "Negative" if cid != "RM-55" else "Boundary", f"Số frame sai: {why}", 400, I_ + "/frames/" + fr, msg="Invalid frame number")
rm("RM-56", "Boundary", "51 frame trong một yêu cầu (vượt 50)", 400, I_ + "/frames/" + ",".join(["1"] * 51), msg="Too many frames in one request")
rm("RM-57", "Boundary", "Frame 4 trên instance 3 frame", 416, I_ + "/frames/4", msg="Requested frame number exceeds total frames available")
rm("RM-58", "Boundary", "Frame 999999999 (9 chữ số) vượt số frame", 416, I_ + "/frames/999999999", msg="Requested frame number exceeds total frames available")
rm("RM-59", "Negative", "Rendered frame 4 vượt số frame", 416, I_ + "/frames/4/rendered", accept="image/png", msg="Requested frame number exceeds total frames available")
rm("RM-60", "Negative", "Rendered Accept application/pdf", 406, I_ + "/rendered", accept="application/pdf", msg="Chỉ hỗ trợ image/jpeg và image/png")
for cid, q in (("RM-61", "0"), ("RM-62", "999"), ("RM-63", "abc")):
    rm(cid, "Boundary" if q != "abc" else "Negative", f"Rendered quality={q}", 400, I_ + f"/rendered?quality={q}", msg="quality phải từ 1 đến 100")
rm("RM-64", "Negative", "Rendered window=5,0 (width <= 0)", 400, I_ + "/rendered?window=5,0", msg="window: center và width phải là số, width > 0")
rm("RM-65", "Negative", "Rendered window chỉ một phần (1)", 400, I_ + "/rendered?window=1", msg="window phải có dạng center,width[,linear]")
rm("RM-66", "Negative", "Rendered cấp series chưa cài", 501, f"{S_}/series/{FSE}/rendered", msg="Chưa cài đặt")
rm("RM-67", "Negative", "Rendered cấp study chưa cài", 501, S_ + "/rendered", msg="Chưa cài đặt")
rm("RM-68", "Negative", "WADO-URI requestType sai (FOO)", 400, f"/dicomweb/wado?requestType=FOO&studyUID={FS}&seriesUID={FSE}&objectUID={FI}", msg="requestType phải là WADO")
rm("RM-69", "Negative", "WADO-URI thiếu objectUID", 400, f"/dicomweb/wado?requestType=WADO&studyUID={FS}&seriesUID={FSE}", msg="Thiếu studyUID, seriesUID hoặc objectUID")
rm("RM-70", "Negative", "WADO-URI anonymize=yes", 400, WURI + "&anonymize=yes", msg="Không hỗ trợ anonymize")
rm("RM-71", "Negative", "WADO-URI contentType=text/html", 406, WURI + "&contentType=text/html", msg="Chỉ hỗ trợ application/dicom, image/jpeg và image/png")
rm("RM-72", "Negative", "WADO-URI frameNumber=x (kiểu ảnh)", 400, WURI + "&contentType=image/png&frameNumber=x", msg="Invalid frame number")
rm("RM-73", "Boundary", "WADO-URI imageQuality=999", 400, WURI + "&contentType=image/jpeg&imageQuality=999", msg="quality phải từ 1 đến 100")
rm("RM-74", "Negative", "WADO-URI windowCenter thiếu windowWidth", 400, WURI + "&contentType=image/png&windowCenter=5", msg="window: center và width phải là số, width > 0")
rm("RM-75", "Negative", "WADO-URI frameNumber=9 vượt số frame", 416, WURI + "&contentType=image/png&frameNumber=9", msg="Requested frame number exceeds total frames available")
# ---- Security (kỳ vọng suy ra từ tài liệu: UID trong đường dẫn chỉ là khóa tra cứu -> 404; xác nhận ở lần chạy đầu, xem coverage.md G3)
rm("RM-80", "Security", "UID trong đường dẫn chứa chuỗi SQL (đã mã hóa %27): không khớp, không 5xx", 404, "/dicomweb/studies/1%27%20OR%20%271%27=%271/metadata", msg="Study not found")
rm("RM-81", "Security", "UID rất dài (500 ký tự): không khớp, không 5xx", 404, "/dicomweb/studies/" + "9" * 500 + "/metadata", msg="Study not found")
write_csv("dicomweb-retrieve-matrix", rcols, R)
scenario("dicomweb-retrieve-matrix", "DICOMweb: WADO-RS / WADO-URI / QIDO cấp con (data-driven)", 1,
         "Seed 1 study cố định (CT 3 frame 2x2 'ABCDEFGHIJKL' + 1 SR không ảnh) rồi gọi GET theo đường dẫn trong dữ liệu: frame/bulkdata/rendered/WADO-URI (200 và so byte), "
         "404 phân biệt, 400 tham số, 416, 406, 501 và biên (50/51 frame, quality 1/100/999).",
         [token_step(),
          step("Seed: STOW study cố định", STOW, "/dicomweb/studies", method="post", headers=HDR_META, body=RM_SEED, body_type="raw", asserts=["status == 200"]),
          step("GET theo dữ liệu", GET_VAR, "{{dicomUrl}}{{req_path}}", headers={"Accept": "{{accept}}"}, asserts=["status == {{expected_status}}"], scripts=[MSG_CHECK, CT_CHECK, BODY_CHECK])],
         tags=["wado"])

# =============================================================================== 6. authz: token sai (data-driven)
tamper = ("const tok0 = pm.response.json().access_token; const k = pm.iterationData.get('bad_kind'); let bad = ''; "
          "if (k === 'garbage') bad = 'khong-phai-jwt'; else if (k === 'three-parts') bad = 'aaa.bbb.ccc'; "
          "else if (k === 'alg-none') bad = btoa('{\"alg\":\"none\",\"typ\":\"JWT\"}').replace(/=/g, '') + '.' + tok0.split('.')[1] + '.'; "
          "else if (k === 'tampered') { const p = tok0.split('.'); bad = p[0] + '.' + p[1] + '.' + p[2].slice(0, -2) + (p[2].slice(-2) === 'AA' ? 'BB' : 'AA'); } "
          "else if (k === 'no-signature') bad = tok0.split('.').slice(0, 2).join('.'); pm.variables.set('callBad', bad);")
WWW = "pm.expect(pm.response.headers.get('WWW-Authenticate') || '').to.include('Bearer realm=\"dicomweb\"');"
AUTH_CHECK = ("const m = pm.iterationData.get('expected_message'); if (m) { pm.test('error = ' + m, () => pm.expect(pm.response.json().error).to.eql(m)); } " + t("WWW-Authenticate", WWW))
BADB = "Bearer {{callBad}}"
az = [row("AZ-01", "Security", "Chuỗi rác (không phải JWT)", 401, bad_kind="garbage", expected_message="Token không hợp lệ"),
      row("AZ-02", "Security", "Ba đoạn nhưng không phải base64 JWT", 401, bad_kind="three-parts", expected_message="Token không hợp lệ"),
      row("AZ-03", "Security", "JWT hợp lệ nhưng alg=none (bỏ chữ ký)", 401, bad_kind="alg-none", expected_message="Token không hợp lệ"),
      row("AZ-04", "Security", "JWT thật bị sửa chữ ký (hai ký tự cuối)", 401, bad_kind="tampered", expected_message="Token không hợp lệ"),
      row("AZ-05", "Security", "JWT thật bị cắt mất phần chữ ký", 401, bad_kind="no-signature", expected_message="Token không hợp lệ"),
      row("AZ-06", "Security", "Authorization: Bearer rỗng", 401, bad_kind="empty")]
write_csv("dicomweb-authz-invalid-token", ["case_id", "case_group", "description", "expected_status", "bad_kind", "expected_message"], az)
scenario("dicomweb-authz-invalid-token", "DICOMweb: authz — token sai (data-driven)", 1,
         "Lấy token thật rồi biến thành token xấu theo từng dòng (rác, alg=none, sửa chữ ký, cắt chữ ký, rỗng) và gọi QIDO, STOW, WADO-URI, frame: mọi route DICOMweb phải 401 kèm WWW-Authenticate.",
         [token_step(tamper),
          step("GET /dicomweb/studies với token xấu", QIDO_STUDIES, "/dicomweb/studies", auth=BADB, asserts=["status == {{expected_status}}"], scripts=[AUTH_CHECK]),
          step("POST /dicomweb/studies với token xấu", STOW, "/dicomweb/studies", method="post", auth=BADB, headers=HDR_META, body=f"--{BND}--{CRLF}", body_type="raw", asserts=["status == {{expected_status}}"], scripts=[AUTH_CHECK]),
          step("GET /dicomweb/wado với token xấu", WADO_URI, "/dicomweb/wado", auth=BADB, query={"requestType": "WADO", "studyUID": "1.2", "seriesUID": "1.2", "objectUID": "1.2"},
               asserts=["status == {{expected_status}}"], scripts=[AUTH_CHECK]),
          step("GET frame với token xấu", W_FRAMES, "/dicomweb/studies/1.2/series/1.2/instances/1.2/frames/1", auth=BADB, asserts=["status == {{expected_status}}"], scripts=[AUTH_CHECK])],
         tags=["authz", "security"])

# =============================================================================== 7. routing + cô lập project (1 dòng)
rt = [
    token_step(),
    step("Không token -> 401 Thiếu bearer token", QIDO_STUDIES, "/dicomweb/studies", auth="none", asserts=["status == 401"],
         scripts=[t("thông điệp + WWW-Authenticate", f"pm.expect({J}.error).to.eql('Thiếu bearer token'); {WWW}")]),
    step("Tiền tố /projects/{projectId}/dicomweb khớp project của token -> 200", EP_PROJ_STUDIES, "{{dicomUrl}}/projects/{{projectId}}/dicomweb/studies", asserts=["status == 200"], scripts=[t("mảng", f"pm.expect(Array.isArray({J})).to.eql(true);")]),
    step("Tiền tố /api/dicomweb -> 200", EP_API_STUDIES, "{{dicomUrl}}/api/dicomweb/studies", asserts=["status == 200"]),
    step("Tiền tố /api/projects/{projectId}/dicomweb -> 200", EP_APIPROJ_STUDIES, "{{dicomUrl}}/api/projects/{{projectId}}/dicomweb/studies", asserts=["status == 200"]),
    step("Project khác trong URL -> 403", EP_OTHER_PROJ, "{{dicomUrl}}/projects/qa-not-my-project/dicomweb/studies", asserts=["status == 403"],
         scripts=[t("thông điệp", f"pm.expect({J}.error).to.eql('Không được dùng project khác');")]),
    step("Header X-Fhir-Project khác project của token -> 403", QIDO_STUDIES, "/dicomweb/studies", headers={"X-Fhir-Project": "qa-not-my-project"}, asserts=["status == 403"],
         scripts=[t("thông điệp", f"pm.expect({J}.error).to.eql('Không được dùng project khác');")]),
    step("Route lạ dưới tiền tố: GET /dicomweb/foo -> 501", EP_UNKNOWN, "{{dicomUrl}}/dicomweb/foo", asserts=["status == 501"], scripts=[t("thông điệp", f"pm.expect({J}.error).to.eql('Chưa cài đặt');")]),
    step("Xóa study chưa cài: DELETE /dicomweb/studies/{uid} -> 501", EP_DELETE_STUDY, "{{dicomUrl}}/dicomweb/studies/{{studyUid}}", asserts=["status == 501"], scripts=[t("thông điệp", f"pm.expect({J}.error).to.eql('Chưa cài đặt');")]),
    step("POST /dicomweb/series chưa cài -> 501", EP_POST_SERIES, "{{dicomUrl}}/dicomweb/series", asserts=["status == 501"], scripts=[t("thông điệp", f"pm.expect({J}.error).to.eql('Chưa cài đặt');")]),
    step("Ngoài tiền tố: GET /fhir/R4/Patient -> 404 (không qua xác thực)", EP_OUTSIDE, "{{dicomUrl}}/fhir/R4/Patient", auth="none", asserts=["status == 404"],
         scripts=[t("thông điệp", f"pm.expect({J}.error).to.eql('Không có route cho GET /fhir/R4/Patient');")]),
    step("Header bảo mật có mặt ở mọi phản hồi", QIDO_STUDIES, "/dicomweb/studies", asserts=["status == 200"],
         scripts=[t("header bảo mật", "const h = pm.response.headers; pm.expect(h.get('Cache-Control')).to.include('no-store'); pm.expect(h.get('X-Content-Type-Options')).to.eql('nosniff'); pm.expect(h.get('X-Frame-Options')).to.eql('DENY'); pm.expect(h.get('X-Request-Id')).to.be.ok;")]),
]
scenario("dicomweb-routing-isolation", "DICOMweb: bốn tiền tố, cô lập project, route chưa cài", 1,
         "Không token (401), bốn tiền tố tương đương (200), projectId/X-Fhir-Project khác project của token (403), route lạ/DELETE/POST /series (501), ngoài tiền tố (404), header bảo mật.", rt, tags=["authz", "routing"])
write_csv("dicomweb-routing-isolation", ["case_id", "case_group", "description", "expected_status"], [row("RT-01", "Security", "Token thường: tiền tố, cô lập project, 501, 404", 200)])

# ---------------------------------------------------------------- environment.json (chỉ tên biến)
VARS = [("apiKey", "khai báo bởi security scheme BearerAuth2 của module (header Authorization); các step dùng bearer {{accessToken}} nên KHÔNG cần nhập"), ("dicomUrl", "Base URL dicomweb, vd http://localhost:8008 (không kèm tiền tố)"), ("authUrl", "Base URL auth, vd http://localhost:8006"),
        ("clientId", "client_id của ClientApplication seed (nhập lúc chạy, --env-var)"), ("clientSecret", "client_secret tương ứng (nhập lúc chạy, KHÔNG lưu)"),
        ("accessToken", "do bước token tạo ra"), ("projectId", "do bước token tạo ra (claim fhir_project)"), ("callBad", "token xấu do script tạo (authz)"),
        ("studyUid", "UID sinh mỗi lần chạy (lifecycle)"), ("seriesUid", "như trên"), ("series2Uid", "như trên"), ("sop1", "như trên"), ("sop2", "như trên"), ("sop3", "như trên"),
        ("otherStudyUid", "như trên"), ("pid", "PatientID sinh mỗi lần chạy")]
(ROOT / "spec" / "environment.json").write_text(json.dumps({"variables": [{"name": n, "value": "", "description": d} for n, d in VARS]}, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")

# ---------------------------------------------------------------- scenarios.md
L = ["# Scenario của module trivita-dicomweb (sinh bởi generate.py)", "", "Mọi scenario bắt đầu bằng bước token (auth). Dữ liệu: `data/<slug>.csv|json`. Bảng tổng kết nằm cuối.", ""]
for i, (slug, s) in enumerate(SCEN.items(), 1):
    n, ext = COLS[slug]
    L += [f"## S-{i:02d} {slug} — {s['name']}", "", f"Ưu tiên P{s['priority']} · dữ liệu `data/{slug}.{ext}` ({n} dòng)", "", s["description"], ""]
    for j, st in enumerate(s["steps"], 1):
        L.append(f"{j}. {st['name']} — `{(st.get('method') or '').upper() or 'GET'} {st['path']}` (endpoint {st['endpointId']}){' · bound' if not st.get('custom') else ''}")
    L.append("")
L += ["## Chỉ thiết kế (chưa dựng) và lý do", "",
      "- STOW-RS dạng **tệp** (`application/dicom` nhị phân, JPEG baseline, nhiều frame nén): Apidog step không dựng được byte nhị phân từ dữ liệu. Dạng metadata đã phủ cùng đường lưu; còn lại dùng `tests/full_smoke` (helper Go).",
      "- Rendered JPEG baseline trả nguyên byte, JPEG2000/RLE 406, frame > 64 MiB 413: cần tệp nén thật.",
      "- 413 `Body quá lớn` (trần 4 GiB), 429 hạn mức IP (mặc định tắt ở local): không an toàn/không có sẵn khi chạy local.",
      "- 403 token chỉ đọc gửi STOW, thiếu quyền `Binary`, super-admin (`X-Fhir-Project`): cần token có scope khác (client_credentials bị chặn scope `fhir.*`); xem coverage.md G1.", "",
      "## Tổng kết độ phủ (dòng dữ liệu theo nhóm)", "", "| Scenario | Positive | Negative | Boundary | Security | Other | Tổng |", "|---|---|---|---|---|---|---|"]
tot = {}
for slug, (n, ext) in COLS.items():
    rows_ = json.loads((ROOT / "data" / f"{slug}.json").read_text(encoding="utf-8")) if ext == "json" else list(csv.DictReader((ROOT / "data" / f"{slug}.csv").open(encoding="utf-8")))
    c = {g: sum(1 for r in rows_ if r["case_group"] == g) for g in ("Positive", "Negative", "Boundary", "Security", "Other")}
    for g in c:
        tot[g] = tot.get(g, 0) + c[g]
    L.append(f"| {slug} | " + " | ".join(str(c[g]) for g in c) + f" | {n} |")
L.append("| **Tổng** | " + " | ".join(str(tot[g]) for g in tot) + f" | {sum(tot.values())} |")
(ROOT / "scenarios" / "scenarios.md").write_text("\n".join(L) + "\n", encoding="utf-8")
print({k: v[0] for k, v in COLS.items()}, "scenarios:", len(SCEN), "steps:", sum(len(s["steps"]) for s in SCEN.values()))
