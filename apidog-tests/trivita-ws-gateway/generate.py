#!/usr/bin/env python3
"""Sinh data/ + scenarios/ + spec/environment.json cho module Trivita WS Gateway (Apidog module 1533161).

Chạy: python3 -I apidog-tests/trivita-ws-gateway/generate.py apidog-tests/trivita-ws-gateway
Nguồn sự thật của test data và thiết kế scenario; KHÔNG sửa tay các tệp sinh ra.
Nguồn luật: apps/ws-gateway/docs/api-contract/cases/fhircast.md (+ mã pkg/fhircast, internal/presentation/httpapi).
"""
import csv
import json
import sys
from pathlib import Path

ROOT = Path(sys.argv[1] if len(sys.argv) > 1 else ".")
(ROOT / "data").mkdir(parents=True, exist_ok=True)
(ROOT / "scenarios").mkdir(parents=True, exist_ok=True)

EP = {}
for e in json.loads((ROOT / "spec" / "endpoints.json").read_text(encoding="utf-8"))["data"]:
    EP[(e["method"].lower(), e["path"])] = e["id"]


def ep(method, path):
    return EP[(method, path)]


# ---------------------------------------------------------------- helpers
TS = "2026-11-25T10:00:00.000Z"
EVENTS = ["syncerror", "heartbeat", "userlogout", "userhibernate", "Patient-open", "Patient-close", "ImagingStudy-open",
          "ImagingStudy-close", "Encounter-open", "Encounter-close", "DiagnosticReport-open", "DiagnosticReport-close",
          "DiagnosticReport-select", "DiagnosticReport-update"]
ALL_EVENTS = ",".join(EVENTS)
ENV_NAMES = ["authUrl", "wsUrl", "clientId", "clientSecret",
             # đặt bởi script ở bước chuẩn bị / extractor, khai báo để check_test_assets biết
             "access_token", "topic", "channel_endpoint", "ctx_version", "ctx_version_2",
             # tên biến của security scheme apiKey trong spec import; builder thay bằng bearer {{access_token}}, không dùng
             "apiKey"]

_ids_seen = set()


def row(case_id, group, desc, status, code="", **cols):
    assert case_id not in _ids_seen, case_id
    _ids_seen.add(case_id)
    return dict(case_id=case_id, case_group=group, description=desc, expected_status=str(status), expected_code=code, **cols)


def write_json(slug, rows):
    (ROOT / "data" / f"{slug}.json").write_text(json.dumps(rows, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")


def js(o):
    return json.dumps(o, ensure_ascii=True, separators=(",", ":"))


def step(name, method, tpl, path=None, **kw):
    s = {"name": name, "endpointId": ep(method, tpl), "method": method.upper(), "path": path or tpl}
    s.update({k: v for k, v in kw.items() if v is not None})
    return s


def script(code):
    return {"type": "customScript", "data": {"script": code}}


def extract(var, expr):
    return {"type": "extractor", "data": {"variableName": var, "expression": expr, "subject": "responseJson"}}


SCENARIOS = []


def scenario(slug, name, priority, description, steps, data=None):
    SCENARIOS.append((slug, name, priority, description, steps, data))
    (ROOT / "scenarios" / f"{slug}.design.json").write_text(
        json.dumps({"slug": slug, "name": name, "priority": priority, "steps": steps}, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    (ROOT / "scenarios" / f"{slug}.create.json").write_text(
        json.dumps({"name": name, "description": description, "folderId": 0, "priority": priority, "tags": ["ws-gateway", "fhircast"]},
                   ensure_ascii=False, indent=1) + "\n", encoding="utf-8")


# Bước chuẩn bị: gọi /healthz (công khai) và dùng script lấy token client_credentials từ auth, đặt topic duy nhất.
PREP_JS = r"""
const scope = pm.iterationData.get('oauth_scope') || 'fhircast/*';
const authUrl = pm.variables.get('authUrl') || pm.environment.get('authUrl');
const set = (k, v) => { try { pm.variables.set(k, v); } catch (e) {} try { pm.environment.set(k, v); } catch (e) {} };
set('topic', 'qa-' + Date.now() + '-' + Math.floor(Math.random() * 1000000));
pm.sendRequest({
  url: authUrl + '/oauth2/token', method: 'POST',
  header: { 'Content-Type': 'application/x-www-form-urlencoded' },
  body: { mode: 'urlencoded', urlencoded: [
    { key: 'grant_type', value: 'client_credentials' },
    { key: 'client_id', value: pm.variables.get('clientId') || pm.environment.get('clientId') },
    { key: 'client_secret', value: pm.variables.get('clientSecret') || pm.environment.get('clientSecret') },
    { key: 'scope', value: scope } ] }
}, (err, res) => {
  pm.test('token client_credentials 200', () => { pm.expect(err).to.eql(null); pm.expect(res.code).to.eql(200); });
  if (!err && res.code === 200) { set('access_token', res.json().access_token); }
});
"""


def prep(name="Chuẩn bị: lấy token client_credentials từ auth và đặt topic"):
    return step(name, "get", "/healthz", assertions=["status == 200"], postProcessors=[script(PREP_JS)], auth="none")


AUTH = "Bearer {{access_token}}"
MSG_JS = ("const m = pm.iterationData.get('expected_message'); "
          "if (m) { pm.test('thông điệp lỗi', () => pm.expect(pm.response.json().issue[0].details.text).to.eql(m)); }")
EP_JS = ("if (pm.response.code === 202) { pm.test('hub.channel.endpoint', () => "
         "pm.expect(pm.response.json()['hub.channel.endpoint']).to.match(/\\/ws\\/fhircast\\/[0-9a-f-]{36}$/)); }")
FHIR_JSON_JS = ("if (pm.response.code >= 400) { pm.test('lỗi là OperationOutcome', () => { "
                "pm.expect(pm.response.headers.get('Content-Type')).to.include('application/fhir+json'); "
                "pm.expect(pm.response.json().resourceType).to.eql('OperationOutcome'); }); }")


def event_body(topic="{{topic}}", hub_event="{{open_event}}", context="{{context_json}}", extra=""):
    return ('{"id":"qa-{{$timestamp}}","timestamp":"' + TS + '","event":{"hub.topic":"' + topic + '","hub.event":"' + hub_event +
            '","context":' + context + extra + '}}')


def sub_body(events=ALL_EVENTS, topic="{{topic}}"):
    return js({"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": "TOPIC", "hub.events": events}).replace("TOPIC", topic)


def unsub_body():
    return '{"hub.channel.type":"websocket","hub.mode":"unsubscribe","hub.topic":"{{topic}}","hub.channel.endpoint":"{{channel_endpoint}}"}'


CTX = {
    "Patient": [{"key": "patient", "resource": {"resourceType": "Patient", "id": "qa-patient-1"}}],
    "ImagingStudy": [{"key": "study", "resource": {"resourceType": "ImagingStudy", "id": "qa-study-1", "status": "available"}}],
    "Encounter": [{"key": "encounter", "resource": {"resourceType": "Encounter", "id": "qa-enc-1", "status": "in-progress"}}],
}

# ---------------------------------------------------------------- S-01 health smoke (không token, không data)
scenario("health-smoke", "Health smoke", 0, "4 probe công khai: live/startup/healthz luôn 200, ready kiểm Redis.", [
    step("GET /health/live", "get", "/health/live", assertions=["status == 200", "$.status == ok"], auth="none"),
    step("GET /health/startup", "get", "/health/startup", assertions=["status == 200", "$.status == ok"], auth="none"),
    step("GET /healthz", "get", "/healthz", assertions=["status == 200", "$.status == ok"], auth="none"),
    step("GET /health/ready (Redis ping được)", "get", "/health/ready",
         assertions=["status == 200", "$.ready == true", "$.checks.redis == true", "$.checks.accepting_traffic == true"], auth="none"),
])

# ---------------------------------------------------------------- S-02 discovery (công khai, không data)
DISC_JS = """
const j = pm.response.json();
const ev = %s;
pm.test('eventsSupported đủ 14 sự kiện theo thứ tự công bố', () => pm.expect(j.eventsSupported).to.eql(ev));
pm.test('websocketSupport true, webhookSupport false', () => { pm.expect(j.websocketSupport).to.eql(true); pm.expect(j.webhookSupport).to.eql(false); });
pm.test('mọi khóa nằm trong tập đã công bố', () => { const ks = Object.keys(j).sort(); pm.expect(ks).to.eql(%s); });
pm.test('Content-Type application/json', () => pm.expect(pm.response.headers.get('Content-Type')).to.include('application/json'));
"""
KEYS_STU3 = json.dumps(sorted(["eventsSupported", "fhircastVersion", "getCurrentSupport", "webhookSupport", "websocketSupport"]))
KEYS_STU2 = json.dumps(sorted(["eventsSupported", "fhircastVersion", "webhookSupport", "websocketSupport"]))
scenario("discovery", "Discovery well-known", 0, "Cấu hình Hub công khai: STU2 không có getCurrentSupport, STU3 và /hub có; không cần token.", [
    step("STU2 well-known", "get", "/fhircast/STU2/.well-known/fhircast-configuration", auth="none",
         assertions=["status == 200", "$.fhircastVersion == STU2", "$.getCurrentSupport not exists"],
         postProcessors=[script(DISC_JS % (json.dumps(EVENTS), KEYS_STU2))]),
    step("STU3 well-known", "get", "/fhircast/STU3/.well-known/fhircast-configuration", auth="none",
         assertions=["status == 200", "$.fhircastVersion == STU3", "$.getCurrentSupport == true"],
         postProcessors=[script(DISC_JS % (json.dumps(EVENTS), KEYS_STU3))]),
    step("/hub well-known (bí danh STU3: fhircastVersion vẫn STU3)", "get", "/hub/.well-known/fhircast-configuration", auth="none",
         assertions=["status == 200", "$.fhircastVersion == STU3", "$.getCurrentSupport == true"],
         postProcessors=[script(DISC_JS % (json.dumps(EVENTS), KEYS_STU3))]),
])

# ---------------------------------------------------------------- S-03..05 lifecycle theo phiên bản
LIFE_ROWS = [
    row("LC-%s-01", "Positive", "Patient-open → GET → close → hủy", 202, open_event="Patient-open", close_event="Patient-close",
        open_type="Patient", context_json=js(CTX["Patient"])),
    row("LC-%s-02", "Positive", "ImagingStudy-open → GET → close → hủy", 202, open_event="ImagingStudy-open",
        close_event="ImagingStudy-close", open_type="ImagingStudy", context_json=js(CTX["ImagingStudy"])),
    row("LC-%s-03", "Positive", "Encounter-open → GET → close → hủy", 202, open_event="Encounter-open", close_event="Encounter-close",
        open_type="Encounter", context_json=js(CTX["Encounter"])),
]


def lifecycle(slug, label, base, wk, pri):
    rows = []
    for r in LIFE_ROWS:
        r = dict(r)
        r["case_id"] = r["case_id"] % label
        assert r["case_id"] not in _ids_seen
        _ids_seen.add(r["case_id"])
        rows.append(r)
    write_json(slug, rows)
    stu2 = base == "/fhircast/STU2"
    get_asserts = ["status == 200"] if stu2 else ["status == 200", "$['context.type'] == {{open_type}}", "$['context.versionId'] == {{ctx_version}}"]
    get_js = ("const j = pm.response.json(); pm.test('STU2: mảng context có resource đúng loại', () => { pm.expect(Array.isArray(j)).to.eql(true); "
              "pm.expect(j[0].resource.resourceType).to.eql(pm.iterationData.get('open_type')); });") if stu2 else \
             "pm.test('STU3: có mảng context', () => pm.expect(Array.isArray(pm.response.json().context)).to.eql(true));"
    empty_js = ("pm.test('chưa có/đã đóng: []', () => pm.expect(pm.response.json()).to.eql([]));" if stu2 else
                "pm.test('chưa có/đã đóng: context.type rỗng, context []', () => { const j = pm.response.json(); "
                "pm.expect(j['context.type']).to.eql(''); pm.expect(j.context).to.eql([]); });")
    steps = [
        prep(),
        step("well-known công khai", "get", f"{base}/.well-known/fhircast-configuration", auth="none", assertions=["status == 200"]),
        step("Đăng ký kênh websocket (JSON)", "post", base, auth=AUTH, body=sub_body(),
             assertions=["status == 202", "$['hub.channel.endpoint'] exists"],
             postProcessors=[extract("channel_endpoint", "$['hub.channel.endpoint']"), script(EP_JS)]),
        step("GET ngữ cảnh khi chưa có", "get", f"{base}/{{topic}}", f"{base}/{{{{topic}}}}", auth=AUTH, assertions=["status == 200"],
             postProcessors=[script(empty_js)]),
        step("Mở ngữ cảnh qua POST topic", "post", f"{base}/{{topic}}", f"{base}/{{{{topic}}}}", auth=AUTH, body=event_body(),
             assertions=["status == 202", "$.success == true", "$.event.event['context.versionId'] exists"],
             postProcessors=[extract("ctx_version", "$.event.event['context.versionId']")]),
        step("GET ngữ cảnh hiện tại ({{open_type}})", "get", f"{base}/{{topic}}", f"{base}/{{{{topic}}}}", auth=AUTH, assertions=get_asserts,
             postProcessors=[script(get_js)]),
        step("POST {v} (không topic ở đường dẫn): heartbeat không đổi ngữ cảnh", "post", base, auth=AUTH,
             body=event_body(hub_event="heartbeat", context="[]"), assertions=["status == 202", "$.success == true"]),
        step("Đóng ngữ cảnh", "post", f"{base}/{{topic}}", f"{base}/{{{{topic}}}}", auth=AUTH,
             body=event_body(hub_event="{{close_event}}", context="{{context_json}}"),
             assertions=["status == 202", "$.success == true", "$.event.event['context.versionId'] not exists"]),
        step("GET sau khi đóng", "get", f"{base}/{{topic}}", f"{base}/{{{{topic}}}}", auth=AUTH, assertions=["status == 200"],
             postProcessors=[script(empty_js)]),
        step("Hủy đăng ký", "post", base, auth=AUTH, body=unsub_body(),
             assertions=["status == 202", "$['hub.channel.endpoint'] exists"]),
        step("Hủy lần hai: endpoint không còn → 400 Invalid endpoint", "post", base, auth=AUTH, body=unsub_body(),
             assertions=["status == 400"],
             postProcessors=[script("pm.test('Invalid endpoint', () => pm.expect(pm.response.json().issue[0].details.text).to.eql('Invalid endpoint'));")]),
    ]
    scenario(slug, f"Hub lifecycle {label}", pri,
             f"Đăng ký → mở ngữ cảnh → GET → heartbeat → đóng → hủy trên {base} (data: Patient/ImagingStudy/Encounter).", steps)


lifecycle("hub-lifecycle-stu3", "STU3", "/fhircast/STU3", None, 0)
lifecycle("hub-lifecycle-stu2", "STU2", "/fhircast/STU2", None, 1)
lifecycle("hub-lifecycle-hub-alias", "HUB", "/hub", None, 1)

# ---------------------------------------------------------------- S-06 DiagnosticReport: open / update / stale / close
DR = [{"key": "report", "resource": {"resourceType": "DiagnosticReport", "id": "qa-dr-1", "status": "preliminary"}}]


def dr_update(version, entries=None, bundle=True):
    ctx = list(DR)
    if bundle:
        ctx.append({"key": "updates", "resource": {"resourceType": "Bundle", "type": "transaction", "entry": entries or [
            {"fullUrl": "urn:uuid:qa-obs-1", "request": {"method": "PUT", "url": "Observation/qa-obs-1"},
             "resource": {"resourceType": "Observation", "id": "qa-obs-1", "status": "final", "code": {"text": "qa"}}}]}})
    ev = {"hub.topic": "TOPIC", "hub.event": "DiagnosticReport-update", "context": ctx, "context.versionId": version}
    return '{"id":"qa-{{$timestamp}}","timestamp":"' + TS + '","event":' + js(ev).replace('"TOPIC"', '"{{topic}}"') + "}"


def P(name, body, status, extra_asserts=(), pp=(), tpl="/fhircast/STU3/{topic}"):
    return step(name, "post", tpl, "/fhircast/STU3/{{topic}}", auth=AUTH, body=body,
                assertions=[f"status == {status}", *extra_asserts], postProcessors=list(pp))


def msg(text):
    return script(f"pm.test('thông điệp lỗi', () => pm.expect(pm.response.json().issue[0].details.text).to.eql({json.dumps(text, ensure_ascii=False)}));")


scenario("hub-diagnosticreport-flow", "Hub DiagnosticReport open/update/close", 1,
         "DiagnosticReport-open → update đúng phiên bản → update phiên bản cũ (409/400) → close. Phiên bản context.versionId theo FHIRcast STU3.", [
    prep(),
    P("Mở DiagnosticReport", event_body(hub_event="DiagnosticReport-open", context=js(DR)), 202,
      ["$.event.event['context.versionId'] exists"], [extract("ctx_version", "$.event.event['context.versionId']")]),
    step("GET: context.type DiagnosticReport", "get", "/fhircast/STU3/{topic}", "/fhircast/STU3/{{topic}}", auth=AUTH,
         assertions=["status == 200", "$['context.type'] == DiagnosticReport", "$['context.versionId'] == {{ctx_version}}"]),
    P("Update sai phiên bản → 400 Expected event.context.versionId", dr_update("khong-phai-phien-ban-hien-tai"), 400, [],
      [script("pm.test('thông điệp Expected ...', () => pm.expect(pm.response.json().issue[0].details.text).to.include(\"Expected event.context.versionId to be '\" + pm.variables.get('ctx_version') + \"', received 'khong-phai-phien-ban-hien-tai'\"));")]),
    P("Update thiếu bundle updates → 400", dr_update("{{ctx_version}}", bundle=False), 400, [],
      [msg("Update event requires an update bundle in the context")]),
    P("Update có entry POST → 400 (chỉ PUT/DELETE)", dr_update("{{ctx_version}}", entries=[
        {"fullUrl": "urn:uuid:x", "request": {"method": "POST", "url": "Observation"}, "resource": {"resourceType": "Observation", "status": "final"}}]), 400,
      [], [msg("Update bundle contains bad entry with request.method = 'POST', only 'PUT' and 'DELETE' are supported by an update event")]),
    P("Update hợp lệ → 202, có priorVersionId và versionId mới", dr_update("{{ctx_version}}"), 202,
      ["$.event.event['context.priorVersionId'] == {{ctx_version}}", "$.event.event['context.versionId'] exists"],
      [extract("ctx_version_2", "$.event.event['context.versionId']")]),
    P("Update lại bằng phiên bản CŨ → 400 (không còn khớp)", dr_update("{{ctx_version}}"), 400, [],
      [script("pm.test('báo phiên bản hiện tại', () => pm.expect(pm.response.json().issue[0].details.text).to.include(\"Expected event.context.versionId to be '\" + pm.variables.get('ctx_version_2') + \"'\"));")]),
    P("Đóng DiagnosticReport", event_body(hub_event="DiagnosticReport-close", context=js(DR)), 202),
    P("Update khi đã đóng → 400 No DiagnosticReport currently open", dr_update("{{ctx_version_2}}"), 400, [],
      [msg("No DiagnosticReport currently open for this topic")]),
])

# ---------------------------------------------------------------- S-07 validation đăng ký/hủy (POST /fhircast/STU3)
A256 = "a" * 256
E128 = "é" * 128  # 256 byte UTF-8
E129 = "é" * 129  # 258 byte
EVT = "Patient-open,Patient-close"
OK = {"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": "qa-val-topic", "hub.events": EVT}


def w(**kv):
    d = dict(OK)
    for k, v in kv.items():
        k = k.replace("__", ".")
        if v is None:
            d.pop(k, None)
        else:
            d[k] = v
    return js(d)


def B(case, group, desc, status, body, msg_=""):
    return row(case, group, desc, status, body_json=body, expected_message=msg_)


SV = [
    B("SV-01", "Negative", "Thân rỗng {} → thiếu channel.type", 400, "{}", "Missing hub.channel.type"),
    B("SV-02", "Negative", "Thiếu hub.channel.type", 400, w(hub__channel__type=None), "Missing hub.channel.type"),
    B("SV-03", "Negative", "Thiếu hub.mode", 400, w(hub__mode=None), "Missing hub.mode"),
    B("SV-04", "Negative", "Thiếu hub.topic", 400, w(hub__topic=None), "Missing hub.topic"),
    B("SV-05", "Negative", "hub.topic không phải chuỗi (số 5) coi như thiếu", 400, w(hub__topic=5), "Missing hub.topic"),
    B("SV-06", "Negative", "hub.channel.type=webhook", 400, w(hub__channel__type="webhook"), "Invalid hub.channel.type"),
    B("SV-07", "Negative", "hub.channel.type=rest-hook kèm hub.callback (Hub không gọi ra ngoài)", 400,
      w(hub__channel__type="rest-hook", hub__callback="http://127.0.0.1:9/hook"), "Invalid hub.channel.type"),
    B("SV-08", "Boundary", "hub.channel.type=WebSocket (phân biệt hoa thường)", 400, w(hub__channel__type="WebSocket"), "Invalid hub.channel.type"),
    B("SV-09", "Negative", "hub.mode lạ", 400, w(hub__mode="refresh"), "Invalid hub.mode"),
    B("SV-10", "Boundary", "hub.mode=Subscribe (hoa) không hợp lệ", 400, w(hub__mode="Subscribe"), "Invalid hub.mode"),
    B("SV-11", "Negative", "Thứ tự kiểm: channel.type sai + thiếu mode → báo Missing hub.mode", 400,
      w(hub__channel__type="webhook", hub__mode=None), "Missing hub.mode"),
    B("SV-12", "Negative", "Thứ tự kiểm: mode lạ + thiếu topic → báo Missing hub.topic", 400,
      w(hub__mode="refresh", hub__topic=None), "Missing hub.topic"),
    B("SV-13", "Boundary", "hub.topic 257 byte", 400, w(hub__topic="a" * 257), "Invalid hub.topic"),
    B("SV-14", "Boundary", "hub.topic 129 ký tự é = 258 byte (đếm theo byte)", 400, w(hub__topic=E129), "Invalid hub.topic"),
    B("SV-15", "Negative", "hub.topic có ký tự điều khiển \\u0001", 400, w(hub__topic="qa\u0001x"), "Invalid hub.topic"),
    B("SV-16", "Negative", "hub.topic có DEL \\u007f", 400, w(hub__topic="qa\u007fx"), "Invalid hub.topic"),
    B("SV-17", "Negative", "Đăng ký thiếu hub.events", 400, w(hub__events=None), "Missing hub.events"),
    B("SV-18", "Negative", "hub.events chỉ dấu phẩy/khoảng trắng", 400, w(hub__events=" , "), "Invalid hub.events"),
    B("SV-19", "Negative", "hub.events có tên lạ", 400, w(hub__events="Patient-open,Foo"), "Invalid hub.events"),
    B("SV-20", "Negative", "Hủy thiếu endpoint", 400, w(hub__mode="unsubscribe", hub__events=None), "Missing endpoint"),
    B("SV-21", "Negative", "Hủy endpoint không tồn tại", 400,
      w(hub__mode="unsubscribe", hub__events=None, hub__channel__endpoint="ws://gw.test/ws/fhircast/00000000-0000-4000-8000-000000000000"),
      "Invalid endpoint"),
    B("SV-22", "Negative", "Hủy bằng trường cũ `endpoint` không tồn tại", 400,
      w(hub__mode="unsubscribe", hub__events=None, endpoint="00000000-0000-4000-8000-000000000001"), "Invalid endpoint"),
    B("SV-23", "Negative", "Thân JSON không phải đối tượng ([1,2])", 400, "[1,2]", "Invalid request body"),
    B("SV-24", "Negative", "Thân JSON là null", 400, "null", "Invalid request body"),
    B("SV-25", "Positive", "Đăng ký hợp lệ", 202, w()),
    B("SV-26", "Positive", "hub.events không phân biệt hoa thường", 202, w(hub__events="patient-OPEN, PATIENT-close")),
    B("SV-27", "Positive", "hub.events đủ 14 sự kiện", 202, w(hub__events=ALL_EVENTS)),
    B("SV-28", "Boundary", "hub.topic đúng 256 byte (a×256)", 202, w(hub__topic=A256)),
    B("SV-29", "Boundary", "hub.topic đúng 256 byte UTF-8 (é×128)", 202, w(hub__topic=E128)),
    B("SV-30", "Boundary", "hub.topic 1 ký tự", 202, w(hub__topic="x")),
    B("SV-31", "Positive", "hub.callback/hub.secret/hub.lease_seconds bị bỏ qua (kênh websocket)", 202,
      w(hub__callback="http://127.0.0.1:9/hook", hub__secret="s3cret-value", hub__lease_seconds="60")),
    B("SV-32", "Security", "hub.topic chứa chuỗi tiêm SQL — chỉ là chuỗi, không lỗi", 202, w(hub__topic="x'; DROP TABLE subscriptions;--")),
    B("SV-33", "Security", "hub.topic chứa thẻ script — không phản chiếu thành HTML", 202, w(hub__topic="<script>alert(1)</script>")),
    B("SV-34", "Security", "hub.topic kiểu path traversal", 202, w(hub__topic="../../etc/passwd")),
]
write_json("hub-validation-subscribe", SV)
scenario("hub-validation-subscribe", "Hub validation đăng ký/hủy", 1,
         "POST /fhircast/STU3 dạng đăng ký/hủy theo data: thứ tự kiểm, biên topic 256 byte, events, bí danh endpoint, bơm chuỗi.", [
    prep(),
    step("POST /fhircast/STU3 theo data", "post", "/fhircast/STU3", auth=AUTH, body="{{body_json}}",
         assertions=["status == {{expected_status}}"], postProcessors=[script(MSG_JS), script(EP_JS), script(FHIR_JSON_JS)]),
])

# ---------------------------------------------------------------- S-08 validation đổi ngữ cảnh (POST /fhircast/STU3 có id)
def ev(**over):
    d = {"id": "qa-ev-1", "timestamp": TS, "event": {"hub.topic": "qa-ev-topic", "hub.event": "heartbeat", "context": []}}
    for k, v in over.items():
        if k.startswith("ev_"):
            k = k[3:].replace("__", ".")
            if v is None:
                d["event"].pop(k, None)
            else:
                d["event"][k] = v
        elif v is None:
            d.pop(k, None)
        else:
            d[k] = v
    return js(d)


SE = [
    B("SE-01", "Negative", "id rỗng", 400, ev(id=""), "Missing event ID"),
    B("SE-02", "Negative", "chỉ có event, thiếu id", 400, ev(id=None), "Missing event ID"),
    B("SE-03", "Negative", "id không phải chuỗi (số 5)", 400, ev(id=5), "Missing event ID"),
    B("SE-04", "Negative", "thiếu timestamp", 400, ev(timestamp=None), "Missing event timestamp"),
    B("SE-05", "Negative", "event là chuỗi, không phải đối tượng", 400, ev(event="x"), "Missing event payload"),
    B("SE-06", "Negative", "event.hub.topic thiếu", 400, ev(ev_hub__topic=None), 'Missing event["hub.topic"]'),
    B("SE-07", "Negative", "event.hub.event thiếu", 400, ev(ev_hub__event=None), 'Missing event["hub.event"]'),
    B("SE-08", "Negative", "event.context thiếu", 400, ev(ev_context=None), "Missing event.context"),
    B("SE-09", "Negative", "event.context null", 400, ev(ev_context=None).replace('"hub.event":"heartbeat"}', '"hub.event":"heartbeat","context":null}'),
      "Missing event.context"),
    B("SE-10", "Negative", "event.context là chuỗi", 400, ev(ev_context="abc"), "Invalid event.context"),
    B("SE-11", "Negative", "event.context là mảng số [1,2]", 400, ev(ev_context=[1, 2]), "Invalid event.context"),
    B("SE-12", "Boundary", "event.hub.topic 257 byte", 400, ev(ev_hub__topic="a" * 257), 'Invalid event["hub.topic"]'),
    B("SE-13", "Negative", "event.hub.event lạ", 400, ev(ev_hub__event="Foo-open"), 'Invalid event["hub.event"]'),
    B("SE-14", "Negative", "DiagnosticReport-update khi chưa mở báo cáo", 400,
      ev(ev_hub__event="DiagnosticReport-update", ev_hub__topic="qa-ev-topic-nodr", ev_context=[]), "No DiagnosticReport currently open for this topic"),
    B("SE-15", "Positive", "heartbeat với context []", 202, ev()),
    B("SE-16", "Positive", "userlogout", 202, ev(ev_hub__event="userlogout")),
    B("SE-17", "Positive", "DiagnosticReport-select không đổi ngữ cảnh", 202, ev(ev_hub__event="DiagnosticReport-select")),
    B("SE-18", "Positive", "Patient-open", 202, ev(ev_hub__event="Patient-open", ev_context=CTX["Patient"])),
    B("SE-19", "Boundary", "hub.event không phân biệt hoa thường (patient-OPEN)", 202, ev(ev_hub__event="patient-OPEN", ev_context=CTX["Patient"])),
    B("SE-20", "Boundary", "topic đúng 256 byte", 202, ev(ev_hub__topic=A256)),
    B("SE-21", "Security", "topic chứa chuỗi tiêm / path traversal", 202, ev(ev_hub__topic="../../etc/passwd';--")),
]
write_json("hub-validation-event", SE)
scenario("hub-validation-event", "Hub validation đổi ngữ cảnh", 1,
         "POST /fhircast/STU3 dạng thông điệp (có id/event) theo data: thứ tự ValidateEvent, biên topic, sự kiện không đổi ngữ cảnh.", [
    prep(),
    step("POST /fhircast/STU3 thông điệp theo data", "post", "/fhircast/STU3", auth=AUTH, body="{{body_json}}",
         assertions=["status == {{expected_status}}"],
         postProcessors=[script(MSG_JS), script(FHIR_JSON_JS),
                         script("if (pm.response.code === 202) { pm.test('success + event', () => { const j = pm.response.json(); pm.expect(j.success).to.eql(true); pm.expect(j.event.id).to.be.a('string'); }); }")]),
])

# ---------------------------------------------------------------- S-09 POST {v}/{topic}: khớp topic
VALID_PATH_EVENT = lambda t: ev(ev_hub__topic=t)  # noqa: E731
TP = [
    row("TP-01", "Positive", "topic đường dẫn khớp event[hub.topic]", 202, topic_path="qa-path-match", body_json=VALID_PATH_EVENT("qa-path-match"), expected_message=""),
    row("TP-02", "Negative", "lệch topic (cờ ALLOW_TOPIC_MISMATCH tắt mặc định) → Topic mismatch", 400, topic_path="qa-path-a",
        body_json=VALID_PATH_EVENT("qa-path-b"), expected_message="Topic mismatch"),
    row("TP-03", "Negative", "thân {} → Missing event ID", 400, topic_path="qa-path-match", body_json="{}", expected_message="Missing event ID"),
    row("TP-04", "Negative", "thân dạng đăng ký gửi nhầm vào /{topic} → Missing event ID", 400, topic_path="qa-path-match",
        body_json=w(), expected_message="Missing event ID"),
    row("TP-05", "Negative", "thông điệp thiếu context + lệch topic: thông điệp ValidateEvent đi trước", 400, topic_path="qa-path-a",
        body_json=ev(ev_hub__topic="qa-path-b", ev_context=None), expected_message="Missing event.context"),
    row("TP-06", "Boundary", "topic đường dẫn mã hóa %2F khớp event có '/'", 202, topic_path="a%2Fb", body_json=VALID_PATH_EVENT("a/b"), expected_message=""),
]
write_json("hub-topic-path", TP)
scenario("hub-topic-path", "Hub POST {v}/{topic} khớp topic", 1,
         "POST /fhircast/STU3/{topic}: kiểm khớp topic (400 Topic mismatch), thân sai dạng, topic mã hóa.", [
    prep(),
    step("POST /fhircast/STU3/{topic} theo data", "post", "/fhircast/STU3/{topic}", "/fhircast/STU3/{{topic_path}}", auth=AUTH,
         body="{{body_json}}", assertions=["status == {{expected_status}}"], postProcessors=[script(MSG_JS), script(FHIR_JSON_JS)]),
])

# ---------------------------------------------------------------- S-10 GET topic với topic đặc biệt
GT = [
    row("GT-01", "Positive", "topic chưa từng dùng: STU3 trả context rỗng", 200, topic_enc="qa-never-used-0001", expected_message=""),
    row("GT-02", "Boundary", "topic có '/' mã hóa %2F", 200, topic_enc="a%2Fb", expected_message=""),
    row("GT-03", "Boundary", "topic có '%' mã hóa %25", 200, topic_enc="100%25", expected_message=""),
    row("GT-04", "Boundary", "topic có khoảng trắng %20", 200, topic_enc="a%20b", expected_message=""),
    row("GT-05", "Boundary", "topic Unicode (bác sĩ)", 200, topic_enc="b%C3%A1c-s%C4%A9", expected_message=""),
    row("GT-06", "Boundary", "topic đúng 256 byte", 200, topic_enc="a" * 256, expected_message=""),
    row("GT-07", "Boundary", "topic 257 byte", 400, topic_enc="a" * 257, expected_message="Invalid hub.topic"),
    row("GT-08", "Negative", "topic có ký tự điều khiển %00", 400, topic_enc="a%00b", expected_message="Invalid hub.topic"),
    row("GT-09", "Security", "topic kiểu path traversal mã hóa", 200, topic_enc="..%2F..%2Fetc%2Fpasswd", expected_message=""),
]
write_json("hub-get-topic", GT)
scenario("hub-get-topic", "Hub GET topic (STU3)", 1,
         "GET /fhircast/STU3/{topic} với topic đặc biệt: mã hóa đúng một lần, biên 256 byte, ký tự điều khiển.", [
    prep(),
    step("GET /fhircast/STU3/{topic} theo data", "get", "/fhircast/STU3/{topic}", "/fhircast/STU3/{{topic_enc}}", auth=AUTH,
         assertions=["status == {{expected_status}}"],
         postProcessors=[script(MSG_JS),
                         script("if (pm.response.code === 200) { pm.test('STU3: context.type + mảng context', () => { const j = pm.response.json(); "
                                "pm.expect(j).to.have.property('context.type'); pm.expect(Array.isArray(j.context)).to.eql(true); }); }")]),
])

# ---------------------------------------------------------------- S-11 content types (form)
scenario("hub-content-types", "Hub content-type form", 2,
         "Đăng ký/hủy dạng application/x-www-form-urlencoded; thân form hỏng; dạng đổi ngữ cảnh gửi bằng form.", [
    prep(),
    step("Đăng ký bằng form (events có khoảng trắng)", "post", "/fhircast/STU3", auth=AUTH,
         form={"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": "{{topic}}", "hub.events": "Patient-open, Patient-close"},
         assertions=["status == 202", "$['hub.channel.endpoint'] exists"], postProcessors=[extract("channel_endpoint", "$['hub.channel.endpoint']"), script(EP_JS)]),
    step("Form channel.type=webhook → 400", "post", "/fhircast/STU3", auth=AUTH,
         form={"hub.channel.type": "webhook", "hub.mode": "subscribe", "hub.topic": "{{topic}}", "hub.events": "Patient-open"},
         assertions=["status == 400"], postProcessors=[msg("Invalid hub.channel.type")]),
    step("Form có id+timestamp → rẽ sang đổi ngữ cảnh: Missing event payload", "post", "/fhircast/STU3", auth=AUTH,
         form={"id": "1", "timestamp": "x"}, assertions=["status == 400"], postProcessors=[msg("Missing event payload")]),
    step("Hủy bằng form", "post", "/fhircast/STU3", auth=AUTH,
         form={"hub.channel.type": "websocket", "hub.mode": "unsubscribe", "hub.topic": "{{topic}}", "hub.channel.endpoint": "{{channel_endpoint}}"},
         assertions=["status == 202"]),
    step("Hủy lại bằng form → 400 Invalid endpoint", "post", "/fhircast/STU3", auth=AUTH,
         form={"hub.channel.type": "websocket", "hub.mode": "unsubscribe", "hub.topic": "{{topic}}", "hub.channel.endpoint": "{{channel_endpoint}}"},
         assertions=["status == 400"], postProcessors=[msg("Invalid endpoint")]),
])

# ---------------------------------------------------------------- S-12 mọi route Hub cần Bearer (không data)
routes = []
for base in ("/fhircast/STU2", "/fhircast/STU3", "/hub"):
    routes += [("post", base, base, '{"hub.channel.type":"websocket","hub.mode":"subscribe","hub.topic":"qa-noauth","hub.events":"Patient-open"}'),
               ("get", base + "/{topic}", base + "/qa-noauth", None),
               ("post", base + "/{topic}", base + "/qa-noauth", event_body(topic="qa-noauth", hub_event="heartbeat", context="[]"))]
WWW_JS = "pm.test('WWW-Authenticate: Bearer realm=\"fhircast\"', () => pm.expect(pm.response.headers.get('WWW-Authenticate')).to.eql('Bearer realm=\"fhircast\"'));"
scenario("hub-requires-auth-all-routes", "Hub từ chối khi không có token (9 route)", 1,
         "Mỗi route Hub (STU2, STU3, /hub × POST đăng ký, GET topic, POST topic) trả 401 + WWW-Authenticate khi không có Authorization.", [
    step(f"{m.upper()} {p} không token → 401", m, t, p, auth="none", body=b, assertions=["status == 401"],
         postProcessors=[script(WWW_JS), script(FHIR_JSON_JS)]) for m, t, p, b in routes
])

# ---------------------------------------------------------------- S-13 token sai (Security)
BAD = [
    row("AZ-01", "Security", "Authorization: Bearer rỗng", 401, credential=""),
    row("AZ-02", "Security", "Token không phải JWT", 401, credential="invalid"),
    row("AZ-03", "Security", "Chuỗi giống JWT nhưng sai chữ ký", 401, credential="not.a.jwt"),
    row("AZ-04", "Security", "Hai tiền tố Bearer", 401, credential="Bearer invalid"),
    row("AZ-05", "Security", "Token dài bất thường (4096 ký tự)", 401, credential="a" * 4096),
    row("AZ-06", "Security", "Token chứa chuỗi tiêm SQL", 401, credential="x' OR '1'='1"),
]
write_json("hub-authz-bad-token", BAD)
bad_ss = lambda m, t, p, b: step(f"{m.upper()} {p} token sai", m, t, p, auth="Bearer {{credential}}", body=b,  # noqa: E731
                                 assertions=["status == {{expected_status}}"], postProcessors=[script(WWW_JS), script(FHIR_JSON_JS)])
scenario("hub-authz-bad-token", "Hub authz: token sai", 1,
         "Token sai/giả/quá dài trên POST đăng ký, GET topic và POST /hub: luôn 401 + WWW-Authenticate, không lộ chi tiết.", [
    bad_ss("post", "/fhircast/STU3", "/fhircast/STU3", routes[3][3]),
    bad_ss("get", "/fhircast/STU3/{topic}", "/fhircast/STU3/qa-noauth", None),
    bad_ss("post", "/hub", "/hub", routes[6][3]),
])

# ---------------------------------------------------------------- S-14 scope
SC = [
    row("SC-01", "Positive", "scope fhircast/* được chấp nhận (đối chứng)", 202, oauth_scope="fhircast/*"),
    row("SC-02", "Security", "token không có scope fhircast/ → 403 Forbidden (FHIRCAST_REQUIRE_SCOPE=true)", 403, oauth_scope="openid"),
    row("SC-03", "Security", "token chỉ có scope SMART khác → 403", 403, oauth_scope="system/Patient.rs"),
]
write_json("hub-authz-scope", SC)
scenario("hub-authz-scope", "Hub authz: scope", 1,
         "Token client_credentials xin scope khác nhau: có fhircast/* → 202, không có → 403 (cần auth cấp được token với các scope đó, xem G2).", [
    prep("Lấy token với scope {{oauth_scope}}"),
    step("POST /fhircast/STU3 đăng ký theo scope", "post", "/fhircast/STU3", auth=AUTH, body=sub_body(events="Patient-open"),
         assertions=["status == {{expected_status}}"],
         postProcessors=[script("if (pm.response.code === 403) { pm.test('Forbidden', () => pm.expect(pm.response.json().issue[0].details.text).to.eql('Forbidden')); }")]),
])

# ---------------------------------------------------------------- env
env = {"name": "Trivita WS Gateway (tên biến, không giá trị)", "variables": [{"name": n, "value": ""} for n in ENV_NAMES]}
(ROOT / "spec" / "environment.json").write_text(json.dumps(env, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")

# ---------------------------------------------------------------- scenarios.md
GROUPS = {}
for p in (ROOT / "data").glob("*.json"):
    for r in json.loads(p.read_text(encoding="utf-8")):
        GROUPS.setdefault(r["case_group"], []).append((p.stem, r["case_id"]))
md = ["# Scenarios — Trivita WS Gateway", "", "Mọi scenario có xác thực mở đầu bằng bước **Chuẩn bị** (GET /healthz + script `pm.sendRequest` lấy token `client_credentials` từ `{{authUrl}}/oauth2/token` bằng `{{clientId}}`/`{{clientSecret}}`, đặt `access_token` và `topic` duy nhất). Base URL của module là `{{wsUrl}}`.", ""]
for i, (slug, name, pri, desc, steps, _) in enumerate(SCENARIOS, 1):
    md.append(f"## S-{i:02d} {slug} — {name}")
    md.append(f"P{pri}. {desc}")
    dfile = ROOT / "data" / f"{slug}.json"
    md.append(f"Data: {'`data/' + slug + '.json` (' + str(len(json.loads(dfile.read_text(encoding='utf-8')))) + ' dòng)' if dfile.exists() else 'không (bước cố định)'}")
    for j, s in enumerate(steps, 1):
        md.append(f"{j}. {s['name']} — `{s['method']} {s['path']}` (ep {s['endpointId']}); " + ", ".join(s.get("assertions", [])))
    md.append("")
md += ["## Chỉ thiết kế (không build)", "",
       "| Flow | Lý do |", "|---|---|",
       "| `GET /ws/fhircast/{endpoint}` và `GET /ws/subscriptions-r4` (nâng cấp WebSocket, khung handshake/event-notification/heartbeat, đóng 1001/1009) | Test Scenario của Apidog CLI chạy HTTP, không có bước WebSocket. Đã có tests/integration và tests/e2e của ws-gateway (gorilla/coder websocket) và `make smoke-full` (helper Go) phủ phần này. Vẫn nên khai 2 endpoint trong module để tài liệu đủ. |",
       "| Thông điệp đổi ngữ cảnh được phát tới socket đang nối sau `POST` (E1) | cần client WebSocket đồng thời; xem trên |",
       "| Cô lập theo project (project khác không đọc/đổi/hủy được) và người dùng khác cùng project hủy đăng ký của bạn → `Invalid endpoint` | cần token của project thứ hai / user thứ hai (seed thêm một project và một client); chưa có dữ liệu seed |",
       "| 413 khi thân > `FHIRCAST_MAX_BODY_BYTES` (1 MiB) | cần cell dữ liệu > 1 MiB; để cho test Go (`TestFhircastBodyTooLarge...`). Có thể thêm 1 dòng nếu muốn |",
       "| 429 hạn mức kết nối WS, 503 `/health/ready` khi Redis chết hoặc đang tắt, 500 do Redis lỗi | cần phá hạ tầng (dừng Redis) — không làm trên stack chung; đã phủ ở tests/integration |",
       "| `FHIRCAST_ALLOW_TOPIC_MISMATCH=true` (T3), `FHIRCAST_REQUIRE_SCOPE=false`, FHIRCAST tắt (404) | cần khởi động lại gateway với cấu hình khác |",
       "| 409 xung đột update đồng thời (E14) | cần 2 yêu cầu song song; chỉ test ở tầng Hub (`TestHubConcurrentUpdatesExactlyOneWins`) |",
       "| Token alg=none / hết hạn / sai issuer-audience | cần token tự ký; `check_test_assets.py` chặn chuỗi giống JWT trong data; đã có test `fhirauth` |",
       "", "## Phủ data theo nhóm", "", "| Nhóm | Số dòng | Scenario |", "|---|---|---|"]
for g in ("Positive", "Negative", "Boundary", "Security", "Other"):
    items = GROUPS.get(g, [])
    md.append(f"| {g} | {len(items)} | {', '.join(sorted({s for s, _ in items})) or '—'} |")
(ROOT / "scenarios" / "scenarios.md").write_text("\n".join(md) + "\n", encoding="utf-8")
print(f"{len(SCENARIOS)} scenario, {len(_ids_seen)} dòng data")
