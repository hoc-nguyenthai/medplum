#!/usr/bin/env python3
"""Smoke test toàn bộ hệ thống (docker-compose.full.yml), bằng JWT thật của auth.

Chạy bằng `make smoke-full` (run.sh dựng compose, seed client, chạy script này, dọn). Kiểm bằng HTTP thật qua cổng đã publish:
  - auth           cấp token client_credentials; fhir-server từ chối khi không có token
  - fhir-server    tạo và tìm Patient
  - fhir-worker    chạy $export (API không chạy job), file kết quả đọc được qua API từ kho Binary dùng chung
  - fhir-notifier  giao webhook của Subscription rest-hook đúng một lần (máy nhận giả ở máy chạy test, container tới bằng host.docker.internal)
  - fhir-downloader tải attachment ngoài, url đổi thành URL ký của kho, nội dung đúng
  - dicomweb       STOW-RS, QIDO-RS, WADO-RS trả lại đúng từng byte; fhir-server thấy DicomStudy (schema fhir dùng chung)
  - ws-gateway     websocket của Subscription nhận event-notification (helper ws, Go)
Đối số: thư mục làm việc của run.sh (seed.txt, smoke.dcm, helper). Cần Docker (Docker Desktop hoặc OrbStack: host.docker.internal), python3 chuẩn.
"""
import http.server, json, subprocess, sys, threading, time, urllib.error, urllib.parse, urllib.request

WORK = sys.argv[1]
seed = dict(l.strip().split(": ", 1) for l in open(WORK + "/seed.txt") if ": " in l)
AUTH_URL = "http://localhost:8006"; API = "http://localhost:8005/fhir/R4"; DW = "http://localhost:8008/dicomweb"
PDF = b"%PDF-1.7 full smoke"
hits = {"file": 0, "hook": []}


class FileServer(http.server.BaseHTTPRequestHandler):
    def do_GET(s):
        hits["file"] += 1
        s.send_response(200); s.send_header("Content-Type", "application/pdf"); s.send_header("Content-Length", str(len(PDF))); s.end_headers(); s.wfile.write(PDF)

    def log_message(s, *a): pass


class HookServer(http.server.BaseHTTPRequestHandler):
    def do_POST(s):
        body = s.rfile.read(int(s.headers.get("Content-Length", 0)))
        hits["hook"].append((s.headers.get("X-Medplum-Subscription"), json.loads(body).get("resourceType") if body else None))
        s.send_response(200); s.end_headers()

    def log_message(s, *a): pass


def serve(h):
    srv = http.server.ThreadingHTTPServer(("0.0.0.0", 0), h)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv.server_address[1]


def req(method, url, data=None, hdr=None):
    r = urllib.request.Request(url, method=method, data=data)
    for k, v in (hdr or {}).items():
        r.add_header(k, v)
    try:
        x = urllib.request.urlopen(r, timeout=30)
        return x.status, x.headers, x.read()
    except urllib.error.HTTPError as e:
        return e.code, e.headers, e.read()


bad = False


def ok(cond, msg):
    global bad
    print(("OK   " if cond else "FAIL ") + msg)
    bad |= not cond


fp, hp = serve(FileServer), serve(HookServer)

print("== auth")
st, _, b = req("POST", AUTH_URL + "/oauth2/token",
               urllib.parse.urlencode({"grant_type": "client_credentials", "client_id": seed["clientId"], "client_secret": seed["clientSecret"]}).encode(),
               {"Content-Type": "application/x-www-form-urlencoded"})
ok(st == 200, f"token client_credentials {st}")
if st != 200:
    sys.exit(1)
TOK = json.loads(b)["access_token"]
AUTHZ = {"Authorization": "Bearer " + TOK}


def fhir(method, path, body=None, hdr=None):
    url = path if path.startswith("http") else API + "/" + path
    return req(method, url, json.dumps(body).encode() if body is not None else None, {**AUTHZ, "Content-Type": "application/fhir+json", **(hdr or {})})


print("== fhir-server")
st, _, _ = req("GET", API + "/Patient")
ok(st == 401, f"không token bị từ chối {st}")
st, _, _ = fhir("POST", "Patient", {"resourceType": "Patient", "name": [{"family": "FullSmoke"}]})
ok(st == 201, f"tạo Patient {st}")
st, _, b = fhir("GET", "Patient?family=FullSmoke")
ok(st == 200 and len(json.loads(b).get("entry", [])) == 1, f"tìm thấy Patient {st}")

print("== fhir-notifier (rest-hook)")
st, _, b = fhir("POST", "Subscription", {"resourceType": "Subscription", "status": "active", "reason": "full smoke", "criteria": "Patient?family=HookSmoke",
                                         "channel": {"type": "rest-hook", "endpoint": f"http://host.docker.internal:{hp}/hook", "payload": "application/fhir+json"}})
ok(st == 201, f"tạo Subscription rest-hook {st}")
sid = json.loads(b).get("id")
time.sleep(3)  # notifier nạp Subscription mới
st, _, _ = fhir("POST", "Patient", {"resourceType": "Patient", "name": [{"family": "HookSmoke"}]})
ok(st == 201, f"tạo Patient khớp tiêu chí {st}")
fhir("POST", "Patient", {"resourceType": "Patient", "name": [{"family": "KhongKhop"}]})
for _ in range(100):
    if hits["hook"]:
        break
    time.sleep(0.2)
time.sleep(1.5)  # chờ thêm để bắt giao thừa
ok(len(hits["hook"]) == 1 and hits["hook"][0][0] == sid, f"webhook đúng 1 lần, đúng Subscription: {hits['hook']}")

print("== fhir-worker ($export)")
st, h, _ = fhir("GET", "Patient/$export", hdr={"Prefer": "respond-async", "Accept": "application/fhir+json"})
ok(st == 202, f"$export {st}")
out = None
if st == 202:
    t0 = time.time()
    while time.time() - t0 < 60:
        st, _, b = fhir("GET", h.get("Content-Location"))
        if st == 200:
            out = json.loads(b)
            break
        time.sleep(0.5)
    ok(out is not None, f"export xong do fhir-worker sau {time.time() - t0:.1f}s")
if out:
    n = sum(o["count"] for o in out["output"])
    ok(n >= 3, f"export có {n} Patient (ít nhất 3)")
    st, _, b = fhir("GET", out["output"][0]["url"], hdr={"Accept": "application/fhir+ndjson"})
    ok(st == 200 and b'"resourceType":"Patient"' in b, f"tải file ndjson từ kho {st}, {len(b)} byte")

print("== fhir-downloader")
st, _, b = fhir("POST", "DocumentReference", {"resourceType": "DocumentReference", "status": "current",
                                              "content": [{"attachment": {"url": f"http://host.docker.internal:{fp}/f.pdf", "title": "smoke"}}]})
ok(st == 201, f"tạo DocumentReference trỏ URL ngoài {st}")
got = None
if st == 201:
    did = json.loads(b)["id"]
    for _ in range(100):
        st, _, b = fhir("GET", "DocumentReference/" + did)
        u = json.loads(b)["content"][0]["attachment"]["url"]
        if not u.startswith("http://host.docker.internal"):
            got = u
            break
        time.sleep(0.3)
ok(got is not None, "url attachment được đổi sang kho Binary")
ok(hits["file"] == 1, f"máy chủ ngoài bị gọi {hits['file']} lần (muốn 1)")
if got:
    st, _, b = fhir("GET", got if got.startswith("http") else API + "/" + got)
    ok(st == 200 and PDF in b, f"đọc lại nội dung đã tải {st}, {len(b)} byte")

print("== dicomweb")
dcm = open(WORK + "/smoke.dcm", "rb").read()
study = "1.2.826.0.1.3680043.8.498.9001"; series = study + ".1"; sop = series + ".1"  # khớp helper dicom
body = b"--b\r\nContent-Type: application/dicom\r\n\r\n" + dcm + b"\r\n--b--\r\n"
st, _, b = req("POST", DW + "/studies", body, {**AUTHZ, "Content-Type": 'multipart/related; type="application/dicom"; boundary=b', "Accept": "application/dicom+json"})
ok(st == 200, f"STOW-RS {st}" + ("" if st == 200 else f" {b[:200]!r}"))
st, _, b = req("GET", DW + "/studies?StudyInstanceUID=" + study, None, {**AUTHZ, "Accept": "application/dicom+json"})
ok(st == 200 and study.encode() in b, f"QIDO-RS tìm thấy study {st}")
st, _, b = req("GET", f"{DW}/studies/{study}/series/{series}/instances/{sop}", None, {**AUTHZ, "Accept": 'multipart/related; type="application/dicom"'})
ok(st == 200 and dcm in b, f"WADO-RS trả lại đúng từng byte {st}, {len(b)} byte")
st, _, b = fhir("GET", "DicomStudy?_count=10")
ok(st == 200 and study.encode() in b, f"fhir-server thấy DicomStudy do dicomweb ghi {st}")

print("== ws-gateway (websocket)")
r = subprocess.run([WORK + "/helper", "ws", API, TOK], capture_output=True, text=True, timeout=60)
print(r.stdout.rstrip())
ok(r.returncode == 0, "websocket qua ws-gateway" + ("" if r.returncode == 0 else f" {r.stderr.strip()}"))

print("RESULT", "FAIL" if bad else "PASS")
sys.exit(1 if bad else 0)
