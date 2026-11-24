#!/usr/bin/env python3
"""Smoke test của bộ service tách (docker-compose.split.yml): fhir-server chỉ phát sự kiện và tạo job, ba service nền làm việc thật.

Chạy bằng `make smoke-split` (dựng compose, chạy script này, dọn). Kiểm bằng HTTP thật qua cổng đã publish của fhir-server:
  - fhir-notifier  giao webhook của Subscription (máy nhận giả ở máy chạy test, container tới bằng host.docker.internal)
  - fhir-worker    chạy $export (API không chạy job), kết quả đọc được qua API từ kho Binary dùng chung
  - fhir-downloader tải attachment ngoài, url đổi thành URL ký của kho, nội dung đúng
Cần Docker (Docker Desktop hoặc OrbStack: host.docker.internal), python3 chuẩn, cổng 8005, 8009, 8010, 8011 trống.
"""
import json, threading, time, urllib.request, http.server, sys
import os
W=os.environ["SMOKE_WRITE_TOKEN"]; A=os.environ["SMOKE_ADMIN_TOKEN"]
API="http://localhost:8005/fhir/R4"
hits={"file":0,"hook":[]}
class F(http.server.BaseHTTPRequestHandler):
    def do_GET(s):
        hits["file"]+=1; b=b"%PDF-1.7 smoke compose"
        s.send_response(200); s.send_header("Content-Type","application/pdf"); s.send_header("Content-Length",str(len(b))); s.end_headers(); s.wfile.write(b)
    def log_message(s,*a): pass
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(s):
        n=int(s.headers.get("Content-Length",0)); body=s.rfile.read(n)
        hits["hook"].append((s.headers.get("X-Medplum-Subscription"), json.loads(body).get("resourceType")))
        s.send_response(200); s.end_headers()
    def log_message(s,*a): pass
def serve(h):
    srv=http.server.ThreadingHTTPServer(("0.0.0.0",0),h); threading.Thread(target=srv.serve_forever,daemon=True).start(); return srv.server_address[1]
fp, hp = serve(F), serve(H)
def call(m,p,tok,body=None,hdr=None):
    r=urllib.request.Request(p if p.startswith("http") else API+"/"+p,method=m,data=json.dumps(body).encode() if body is not None else None)
    r.add_header("Authorization","Bearer "+tok); r.add_header("Content-Type","application/fhir+json")
    for k,v in (hdr or {}).items(): r.add_header(k,v)
    try:
        x=urllib.request.urlopen(r,timeout=30); return x.status,x.headers,x.read()
    except urllib.error.HTTPError as e: return e.code,e.headers,e.read()
def ok(c,msg):
    print(("OK   " if c else "FAIL ")+msg); 
    if not c: ok.bad=True
ok.bad=False
for port,name in ((8005,"fhir-server"),(8009,"fhir-downloader"),(8010,"fhir-notifier"),(8011,"fhir-worker")):
    path="/readyz" if port==8005 else "/health/ready"
    r=urllib.request.urlopen(f"http://localhost:{port}{path}",timeout=10); ok(r.status==200,f"{name} ready ({r.read().decode().strip()})")
# 1. webhook qua fhir-notifier
st,_,b=call("POST","Subscription",W,{"resourceType":"Subscription","status":"active","reason":"smoke","criteria":"Patient?family=Smoke","channel":{"type":"rest-hook","endpoint":f"http://host.docker.internal:{hp}/hook"}})
ok(st==201,f"tạo Subscription {st}")
time.sleep(4)
st,_,b=call("POST","Patient",W,{"resourceType":"Patient","name":[{"family":"Smoke"}]}); ok(st==201,f"tạo Patient {st}")
for _ in range(100):
    if hits["hook"]: break
    time.sleep(0.2)
ok(len(hits["hook"])==1 and hits["hook"][0][1]=="Patient",f"webhook do fhir-notifier giao: {hits['hook']}")
# 2. export do fhir-worker
for i in range(3): call("POST","Patient",W,{"resourceType":"Patient","name":[{"family":"Exp"}]})
st,h,b=call("GET","Patient/$export",A,hdr={"Prefer":"respond-async"}); ok(st==202,f"$export 202 ({st})")
loc=h.get("Content-Location"); t0=time.time(); out=None
while time.time()-t0<60:
    st,_,b=call("GET",loc,A)
    if st==200: out=json.loads(b); break
    time.sleep(0.5)
ok(out is not None,f"export xong do fhir-worker sau {time.time()-t0:.1f}s")
if out:
    n=sum(o["count"] for o in out["output"]); ok(n==4,f"export có {n} Patient (muốn 4: 1 Smoke + 3 Exp)")
    url=out["output"][0]["url"]; st,_,b=call("GET",url,A,hdr={"Accept":"application/fhir+ndjson"}); ok(st==200 and b.count(b"\n")+1>=1 and b'"resourceType":"Patient"' in b,f"tải file ndjson từ kho {st}, {len(b)} byte")
# 3. tải attachment do fhir-downloader
st,_,b=call("POST","DocumentReference",W,{"resourceType":"DocumentReference","status":"current","content":[{"attachment":{"url":f"http://host.docker.internal:{fp}/f.pdf","title":"smoke"}}]}); ok(st==201,f"tạo DocumentReference {st}")
did=json.loads(b)["id"]; got=None
for _ in range(100):
    st,_,b=call("GET","DocumentReference/"+did,W); u=json.loads(b)["content"][0]["attachment"]["url"]
    if not u.startswith("http://host.docker.internal"): got=u; break
    time.sleep(0.3)
ok(got is not None,f"attachment được tải và đổi url: {got}")
ok(hits["file"]==1,f"máy chủ ngoài bị gọi {hits['file']} lần")
if got:
    st,_,b=call("GET",got if got.startswith("http") else got,W)
    ok(st==200 and b"%PDF-1.7 smoke compose" in b,f"đọc nội dung đã tải qua API/URL ký {st}")
print("RESULT", "FAIL" if ok.bad else "PASS")
sys.exit(1 if ok.bad else 0)
